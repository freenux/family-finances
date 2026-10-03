package apiv1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/infrastructure/sqlite"
	"family-finances/internal/usecase"
)

// newBulkEnv 同 newEnv，但注入了 by-filter 需要的 TxBulk。
func newBulkEnv(t *testing.T) *env {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	txRepo := sqlite.NewTransactionRepo(db)
	catRepo := sqlite.NewCategoryRepo(db)
	spRepo := sqlite.NewSpecialProjectRepo(db)
	if err := spRepo.Upsert(context.Background(), &domain.SpecialProject{ID: "sp-1", Name: "装修", StartedOn: fixedNow}); err != nil {
		t.Fatal(err)
	}
	nav := usecase.PeriodNav{Now: func() time.Time { return fixedNow }}
	q := usecase.NewTxQuery(txRepo, catRepo, catRepo).WithSpecialRepo(spRepo).WithNav(nav)
	api := New(Deps{
		Categories: catRepo, Specials: spRepo, SpecialCheck: usecase.NewSpecialView(spRepo),
		TxQuery: q, Tx: txRepo, TxBulk: txRepo, Nav: nav,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &env{h: api.Routes(), tx: txRepo, cats: catRepo}
}

func (e *env) patchJSON(path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(b)))
	return rec
}

// 不带任何参数：流水视图的缺省窗口是上月（monthly），不是上季度。
// 网页 /transactions 的同一条断言在 handler 包（TestTransactionsDefaultPeriodSameAsAPI）。
func TestListTransactionsDefaultPeriodIsLastMonth(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name, path, wantType, wantKey string
	}{
		{"无参数", "/transactions", "monthly", "2025-07"},
		{"只给 period：沿用 monthly 比对", "/transactions?period=2025-03", "monthly", "2025-03"},
		{"显式 quarterly 不受影响", "/transactions?type=quarterly", "quarterly", "2025Q2"},
		{"type 与 period 对不上：退回 type 的默认", "/transactions?type=annual&period=2025-03", "annual", "2024"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("GET", tt.path, "")
			if rec.Code != 200 {
				t.Fatalf("status = %d（%s）", rec.Code, rec.Body)
			}
			var resp struct {
				Data struct {
					Period struct{ Type, Key string } `json:"period"`
				} `json:"data"`
			}
			decode(t, rec, &resp)
			if resp.Data.Period.Type != tt.wantType || resp.Data.Period.Key != tt.wantKey {
				t.Errorf("period = %+v; want %s/%s", resp.Data.Period, tt.wantType, tt.wantKey)
			}
		})
	}
}

// 路由归属：/by-filter、/batch 与 /{id} 同挂 PATCH，静态段优先、互不吞噬。
func TestByFilterRouteOwnership(t *testing.T) {
	e := newBulkEnv(t)
	e.insert(t, "tx-1", nil)
	tests := []struct {
		name     string
		path     string
		body     any
		wantCode int
		wantMsg  string // 非空时断言 error.message，用来区分到底是哪个处理器接的
	}{
		{"by-filter 落静态处理器（缺 special_id）", "/transactions/by-filter", map[string]any{}, 400, "缺少 special_id"},
		{"batch 落静态处理器（缺 ids）", "/transactions/batch", map[string]any{"special_id": ""}, 400, "请选择要归类的流水"},
		{"by-filterx 落到 {id}（流水不存在）", "/transactions/by-filterx", map[string]any{"note": "x"}, 404, "流水不存在"},
		{"普通 id 仍是单条更新", "/transactions/tx-1", map[string]any{"note": "x"}, 200, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.patchJSON(tt.path, tt.body)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d; want %d（%s）", rec.Code, tt.wantCode, rec.Body)
			}
			if tt.wantMsg != "" {
				if _, msg := errCode(t, rec); msg != tt.wantMsg {
					t.Errorf("message = %q; want %q（说明被别的处理器接走了）", msg, tt.wantMsg)
				}
			}
		})
	}
	// by-filter 只有 PATCH：别的 method 不能被 {id} 吞掉后返回 200
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest(m, "/transactions/by-filter", nil))
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Errorf("%s /transactions/by-filter = %d; want 404/405", m, rec.Code)
		}
	}
}

// 跨多页：归类一次，所有命中行都改（不只是第一页），不命中的行不动。
func TestByFilterAssignsAcrossPages(t *testing.T) {
	e := newBulkEnv(t)
	for i := 0; i < 120; i++ {
		i := i
		e.insert(t, fmt.Sprintf("jul-%03d", i), func(x *domain.Transaction) {
			x.OccurredAt = time.Date(2025, 7, 1+i%28, 8, i%60, 0, 0, time.Local)
			if i%4 == 0 { // 30 笔已分类：不在「未分类」筛选内
				x.CategoryID = leafExpense
			}
		})
	}
	e.insert(t, "aug-1", func(x *domain.Transaction) { x.OccurredAt = time.Date(2025, 8, 2, 8, 0, 0, 0, time.Local) })
	e.insert(t, "wife-1", func(x *domain.Transaction) { x.Account = domain.AccountWife })

	listTotal := func(q string) int {
		var resp struct {
			Data struct {
				Page struct{ Total int } `json:"page"`
			} `json:"data"`
		}
		decode(t, e.do("GET", "/transactions?"+q, ""), &resp)
		return resp.Data.Page.Total
	}
	// 列表上的筛选：2025-07 未分类、husband，每页 50 → 90+... 以列表 total 为准
	const filter = "type=monthly&period=2025-07&account=husband&category=__none__"
	want := listTotal(filter + "&page_size=50")
	if want <= 50 {
		t.Fatalf("前置：命中 %d 条，需要跨页（>50）", want)
	}

	// 请求里夹带 page / page_size / sort：必须被忽略
	rec := e.patchJSON("/transactions/by-filter?"+filter+"&page=2&page_size=50&sort=amount", map[string]string{"special_id": "sp-1"})
	if rec.Code != 200 {
		t.Fatalf("status = %d（%s）", rec.Code, rec.Body)
	}
	var resp struct {
		Data struct{ Updated int } `json:"data"`
	}
	decode(t, rec, &resp)
	if resp.Data.Updated != want {
		t.Errorf("updated = %d; want %d（整个筛选结果）", resp.Data.Updated, want)
	}
	if got := listTotal("type=monthly&period=2025-07&special=sp-1"); got != want {
		t.Errorf("专项 sp-1 下共 %d 条; want %d（不止第一页）", got, want)
	}
	for _, id := range []string{"aug-1", "wife-1", "jul-000", "jul-004"} {
		got, err := e.tx.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.SpecialID != "" {
			t.Errorf("%s 不在筛选内却被改成 %q", id, got.SpecialID)
		}
	}

	// 空串归回日常
	rec = e.patchJSON("/transactions/by-filter?type=monthly&period=2025-07&special=sp-1", map[string]string{"special_id": ""})
	decode(t, rec, &resp)
	if rec.Code != 200 || resp.Data.Updated != want || listTotal("type=monthly&period=2025-07&special=sp-1") != 0 {
		t.Errorf("归回日常: code=%d updated=%d; want 200/%d 且专项下清空", rec.Code, resp.Data.Updated, want)
	}
}

func TestByFilterErrors(t *testing.T) {
	e := newBulkEnv(t)
	e.insert(t, "a", nil)
	tests := []struct {
		name     string
		path     string
		body     string
		wantCode int
	}{
		{"专项不存在", "/transactions/by-filter?period=2025-07", `{"special_id":"ghost"}`, 400},
		{"周期非法", "/transactions/by-filter?period=garbage", `{"special_id":"sp-1"}`, 400},
		{"规则不存在", "/transactions/by-filter?rule_id=nope", `{"special_id":"sp-1"}`, 404},
		{"body 不是 JSON", "/transactions/by-filter", `not json`, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, tt.path, bytes.NewBufferString(tt.body)))
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d; want %d（%s）", rec.Code, tt.wantCode, rec.Body)
			}
		})
	}
	got, _ := e.tx.Get(context.Background(), "a")
	if got.SpecialID != "" {
		t.Errorf("失败的请求改了数据: special=%q", got.SpecialID)
	}
}
