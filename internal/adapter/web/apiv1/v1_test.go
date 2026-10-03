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
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/infrastructure/sqlite"
	"family-finances/internal/usecase"
)

// 这组测试用真库（Open+Migrate）+ 真 usecase + 真路由，断言 JSON 结构与状态码。

const (
	leafExpense = "expense.discretion.shopping"
	leafIncome  = "income.salary.husband"
)

// fixedNow 固定时钟：2025-08-15，默认周期 = 上月 2025-07 / 上季 2025Q2 / 去年 2024
var fixedNow = time.Date(2025, 8, 15, 12, 0, 0, 0, time.Local)

type env struct {
	h    http.Handler
	tx   *sqlite.TransactionRepo
	cats *sqlite.CategoryRepo
}

func newEnv(t *testing.T) *env {
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
		TxQuery: q, Tx: txRepo, Nav: nav,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &env{h: api.Routes(), tx: txRepo, cats: catRepo}
}

func (e *env) insert(t *testing.T, id string, mod func(*domain.Transaction)) {
	t.Helper()
	at := time.Date(2025, 7, 10, 10, 0, 0, 0, time.Local)
	tx := domain.Transaction{
		ID: id, Source: domain.SourceManual, Account: domain.AccountHusband,
		OccurredAt: at, Amount: 100, Direction: domain.DirectionExpense,
		Status: domain.TxStatusPendingReview, CreatedAt: at, UpdatedAt: at,
	}
	if mod != nil {
		mod(&tx)
	}
	if err := e.tx.Insert(context.Background(), tx); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func (e *env) do(method, path, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) (code, msg string) {
	t.Helper()
	var r struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	decode(t, rec, &r)
	return r.Error.Code, r.Error.Message
}

func TestListTransactionsEnvelope(t *testing.T) {
	e := newEnv(t)
	e.insert(t, "a", func(x *domain.Transaction) { x.Member = "张三"; x.Amount = 12345 })
	rec := e.do("GET", "/transactions?type=monthly&period=2025-07", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200（%s）", rec.Code, rec.Body)
	}
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	decode(t, rec, &resp)
	for _, k := range []string{"rows", "period", "page", "totals", "facets"} {
		if _, ok := resp.Data[k]; !ok {
			t.Errorf("data 缺少 %q；want rows/period/page/totals/facets 都在（信封结构是客户端契约）", k)
		}
	}
	if _, ok := resp.Data["rule"]; ok {
		t.Errorf("没传 rule_id 时不应输出 rule")
	}
	if !strings.Contains(rec.Body.String(), `"amount_text":"123.45"`) || !strings.Contains(rec.Body.String(), `"key":"2025-07"`) {
		t.Errorf("body = %s; want 行内 amount_text 已格式化且 period.key 回显", rec.Body)
	}
}

func TestListTransactionsErrors(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name     string
		path     string
		wantCode int
		wantErr  string
		why      string
	}{
		{"非法 period", "/transactions?period=garbage", 400, "bad_request", "周期标签解析失败是客户端参数错，不是 500"},
		{"季度标签越界", "/transactions?period=2025Q9", 400, "bad_request", "形状对但值非法同样是 400"},
		{"规则不存在", "/transactions?rule_id=nope", 404, "not_found", "sqlite GetRule 返回 sql.ErrNoRows，也必须映射成 404 而不是 500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("GET", tt.path, "")
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d; want %d（%s）", rec.Code, tt.wantCode, tt.why)
			}
			if code, msg := errCode(t, rec); code != tt.wantErr || msg == "" {
				t.Fatalf("error = (%q,%q); want code=%s 且 message 非空", code, msg, tt.wantErr)
			}
		})
	}
}

func TestListTransactionsPagination(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 25; i++ {
		i := i
		// 按金额递增，sort=amount&order=asc 时第 n 条的金额恰为 n 元
		e.insert(t, fmt.Sprintf("t%02d", i), func(x *domain.Transaction) { x.Amount = int64(i) * 100 })
	}
	tests := []struct {
		name        string
		query       string
		wantFirst   string
		wantRows    int
		wantPage    int
		wantSize    int
		wantTotal   int
		wantPages   int
		wantTotalFn int64
	}{
		{"第 2 页每页 10", "page=2&page_size=10", "t11", 10, 2, 10, 25, 3, 0},
		{"最后一页只有 5 条", "page=3&page_size=10", "t21", 5, 3, 10, 25, 3, 0},
		{"page_size 超上限钳到 200", "page_size=9999", "t01", 25, 1, 200, 25, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("GET", "/transactions?type=monthly&period=2025-07&sort=amount&order=asc&"+tt.query, "")
			var resp struct {
				Data struct {
					Rows []struct{ ID string } `json:"rows"`
					Page struct {
						Page       int `json:"page"`
						PageSize   int `json:"page_size"`
						Total      int `json:"total"`
						TotalPages int `json:"total_pages"`
					} `json:"page"`
					Totals struct {
						ExpenseFen int64 `json:"expense_fen"`
					} `json:"totals"`
				} `json:"data"`
			}
			decode(t, rec, &resp)
			d := resp.Data
			if len(d.Rows) != tt.wantRows || d.Rows[0].ID != tt.wantFirst {
				t.Fatalf("rows = %d 条、首条 %v; want %d 条、首条 %s（offset=(page-1)*size 是否透传正确）", len(d.Rows), d.Rows, tt.wantRows, tt.wantFirst)
			}
			if d.Page.Page != tt.wantPage || d.Page.PageSize != tt.wantSize || d.Page.Total != tt.wantTotal || d.Page.TotalPages != tt.wantPages {
				t.Errorf("page = %+v; want page=%d size=%d total=%d pages=%d", d.Page, tt.wantPage, tt.wantSize, tt.wantTotal, tt.wantPages)
			}
			if want := int64(25*26/2) * 100; d.Totals.ExpenseFen != want {
				t.Errorf("totals.expense_fen = %d; want %d（合计是整个筛选结果集的，不是当页的）", d.Totals.ExpenseFen, want)
			}
		})
	}
}

func TestPatchRouting(t *testing.T) {
	e := newEnv(t)
	e.insert(t, "tx-1", nil)
	e.insert(t, "batch-me", nil)

	// 批量：落到批量处理器 → 信封里是 updated
	rec := e.do("PATCH", "/transactions/batch", `{"ids":["tx-1","batch-me","ghost"],"special_id":"sp-1"}`)
	var b struct {
		Data map[string]int `json:"data"`
	}
	decode(t, rec, &b)
	if rec.Code != 200 || b.Data["updated"] != 2 {
		t.Fatalf("PATCH /transactions/batch = %d %s; want 200 且 updated=2（不存在的 id 静默跳过）", rec.Code, rec.Body)
	}
	got, _ := e.tx.Get(context.Background(), "tx-1")
	if got.SpecialID != "sp-1" {
		t.Errorf("special_id = %q; want sp-1", got.SpecialID)
	}

	// 单条：/tx-1 落到 {id} 处理器 → 信封里是 ok
	rec = e.do("PATCH", "/transactions/tx-1", `{"note":"hi"}`)
	var s struct {
		Data map[string]bool `json:"data"`
	}
	decode(t, rec, &s)
	if rec.Code != 200 || !s.Data["ok"] {
		t.Fatalf("PATCH /transactions/tx-1 = %d %s; want 200 且 ok=true", rec.Code, rec.Body)
	}
	// 名字长得像 batch 的 id 仍是单条
	rec = e.do("PATCH", "/transactions/batchx", `{"note":"x"}`)
	if rec.Code != 404 {
		t.Errorf("PATCH /transactions/batchx = %d; want 404（落到 {id}，该流水不存在）", rec.Code)
	}
	// 同路径上其它 method 不得悄悄落到占位段
	if rec = e.do("POST", "/transactions/batch", `{}`); rec.Code != 405 {
		t.Errorf("POST /transactions/batch = %d; want 405", rec.Code)
	}
}

func TestPatchSingleSemantics(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCode   int
		wantErr    string
		wantStatus domain.TxStatus
		wantCat    string
		why        string
	}{
		{"非空分类自动 confirmed", `{"category_id":"` + leafExpense + `"}`, 200, "", domain.TxStatusConfirmed, leafExpense, "与 handler.UpdateTransaction 一致"},
		{"清空分类不改状态", `{"category_id":""}`, 200, "", domain.TxStatusPendingReview, "", "空串=清空，不触发 confirmed"},
		{"一级分类被拒", `{"category_id":"expense.discretion"}`, 400, "bad_request", domain.TxStatusPendingReview, "", "只能选二级科目"},
		{"非法状态", `{"status":"weird"}`, 400, "bad_request", domain.TxStatusPendingReview, "", ""},
		{"family 不是存储账户", `{"account":"family"}`, 400, "bad_request", domain.TxStatusPendingReview, "", "family 只是查询视图"},
		{"专项不存在", `{"special_id":"nope"}`, 400, "bad_request", domain.TxStatusPendingReview, "", ""},
		{"坏 JSON", `{`, 400, "bad_request", domain.TxStatusPendingReview, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.insert(t, "tx-1", func(x *domain.Transaction) { x.CategoryID = ""; x.Status = domain.TxStatusPendingReview })
			rec := e.do("PATCH", "/transactions/tx-1", tt.body)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d; want %d（%s）body=%s", rec.Code, tt.wantCode, tt.why, rec.Body)
			}
			if tt.wantErr != "" {
				if code, _ := errCode(t, rec); code != tt.wantErr {
					t.Errorf("code = %q; want %q", code, tt.wantErr)
				}
			}
			got, _ := e.tx.Get(context.Background(), "tx-1")
			if got.Status != tt.wantStatus || got.CategoryID != tt.wantCat {
				t.Errorf("落库 (status=%s, category=%q); want (%s, %q)（%s）", got.Status, got.CategoryID, tt.wantStatus, tt.wantCat, tt.why)
			}
		})
	}
	// 不存在的流水 → 404
	e := newEnv(t)
	rec := e.do("PATCH", "/transactions/ghost", `{"note":"x"}`)
	if code, _ := errCode(t, rec); rec.Code != 404 || code != "not_found" {
		t.Errorf("不存在的流水 = %d %s; want 404 not_found", rec.Code, code)
	}
}

func TestPatchBatchValidation(t *testing.T) {
	e := newEnv(t)
	e.insert(t, "tx-1", func(x *domain.Transaction) { x.SpecialID = "sp-1" })
	tests := []struct {
		name string
		body string
		want int
	}{
		{"空 ids", `{"ids":[],"special_id":"sp-1"}`, 400},
		{"缺 special_id", `{"ids":["tx-1"]}`, 400},
		{"专项不存在", `{"ids":["tx-1"],"special_id":"nope"}`, 400},
		{"超过上限", `{"ids":[` + strings.TrimSuffix(strings.Repeat(`"x",`, maxBatchTxIDs+1), ",") + `],"special_id":""}`, 400},
		{"空串归回日常", `{"ids":["tx-1"],"special_id":""}`, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := e.do("PATCH", "/transactions/batch", tt.body); rec.Code != tt.want {
				t.Fatalf("status = %d; want %d（%s）", rec.Code, tt.want, rec.Body)
			}
		})
	}
	if got, _ := e.tx.Get(context.Background(), "tx-1"); got.SpecialID != "" {
		t.Errorf("special_id = %q; want 空（归回日常）", got.SpecialID)
	}
}

func TestApplyRule(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, r := range []domain.CategoryRule{
		{ID: "r-ok", Pattern: "星巴克", PatternType: "contains", Field: "counterparty", CategoryID: leafExpense, IsActive: true, CreatedAt: fixedNow},
		{ID: "r-dead", Pattern: "星巴克", PatternType: "contains", Field: "counterparty", CategoryID: leafIncome, IsActive: true, CreatedAt: fixedNow},
		{ID: "r-skip", Pattern: "转账", PatternType: "contains", Field: "counterparty", CategoryID: "", IsActive: true, CreatedAt: fixedNow},
	} {
		if err := e.cats.InsertRule(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	e.insert(t, "hit", func(x *domain.Transaction) { x.Counterparty = "星巴克" })
	e.insert(t, "miss", func(x *domain.Transaction) { x.Counterparty = "别家" })

	tests := []struct {
		name     string
		path     string
		body     string
		wantCode int
		wantErr  string
		wantMsg  string // 非空则 message 须含该片段（面向用户的原话）
	}{
		{"成功", "/rules/r-ok/apply", `{"type":"monthly","period":"2025-07","account":"family"}`, 200, "", ""},
		{"规则不存在", "/rules/nope/apply", `{"period":"2025-07"}`, 404, "not_found", ""},
		{"死规则", "/rules/r-dead/apply", `{"period":"2025-07"}`, 400, "bad_request", "永远不会命中的死规则"},
		{"无目标科目", "/rules/r-skip/apply", `{"period":"2025-07"}`, 400, "bad_request", "没有目标科目"},
		{"非法周期", "/rules/r-ok/apply", `{"period":"garbage"}`, 400, "bad_request", ""},
		{"坏 JSON", "/rules/r-ok/apply", `{`, 400, "bad_request", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("POST", tt.path, tt.body)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d; want %d（%s）", rec.Code, tt.wantCode, rec.Body)
			}
			if tt.wantErr == "" {
				var r struct {
					Data map[string]int `json:"data"`
				}
				decode(t, rec, &r)
				if r.Data["updated"] != 1 {
					t.Errorf("updated = %d; want 1（只有 hit 命中且为支出）", r.Data["updated"])
				}
				return
			}
			code, msg := errCode(t, rec)
			if code != tt.wantErr || !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("error = (%q,%q); want code=%s、message 含 %q（usecase 的中文原样回显）", code, msg, tt.wantErr, tt.wantMsg)
			}
		})
	}
	hit, _ := e.tx.Get(ctx, "hit")
	miss, _ := e.tx.Get(ctx, "miss")
	if hit.CategoryID != leafExpense || hit.Status != domain.TxStatusConfirmed || miss.CategoryID != "" {
		t.Errorf("落库 hit=(%s,%s) miss=%q; want hit 被分类并 confirmed、miss 不动", hit.CategoryID, hit.Status, miss.CategoryID)
	}
	// GET 不得进入 apply
	if rec := e.do("GET", "/rules/r-ok/apply", ""); rec.Code != 405 {
		t.Errorf("GET apply = %d; want 405", rec.Code)
	}
}

func TestPeriodsNav(t *testing.T) {
	e := newEnv(t)
	type view struct {
		Type, Key, Label, Prev, Next string
		HasNext                      bool `json:"has_next"`
		IsCurrent                    bool `json:"is_current"`
	}
	tests := []struct {
		name string
		path string
		want view
		why  string
	}{
		{"跨年进位_月", "/periods/nav?type=monthly&period=2025-01", view{"monthly", "2025-01", "", "2024-12", "2025-02", true, false}, "2025-01 的 prev 要跨年到 2024-12"},
		{"跨年进位_季", "/periods/nav?period=2025Q1", view{"quarterly", "2025Q1", "", "2024Q4", "2025Q2", true, false}, "period 的形状决定 type"},
		{"当期无下一期", "/periods/nav?type=quarterly&period=2025Q3", view{"quarterly", "2025Q3", "", "2025Q2", "2025Q4", false, true}, "now=2025-08-15 落在 Q3：has_next=false，is_current=true"},
		{"缺省 type 取季度默认", "/periods/nav", view{"quarterly", "2025Q2", "", "2025Q1", "2025Q3", true, false}, "缺省=上一个完整季度"},
		{"非法 type 回落季度", "/periods/nav?type=bogus", view{"quarterly", "2025Q2", "", "2025Q1", "2025Q3", true, false}, "非法 type 不报错"},
		{"年度默认", "/periods/nav?type=annual", view{"annual", "2024", "", "2023", "2025", true, false}, "年度默认=去年"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("GET", tt.path, "")
			if rec.Code != 200 {
				t.Fatalf("status = %d; want 200（%s）", rec.Code, rec.Body)
			}
			var resp struct {
				Data view `json:"data"`
			}
			decode(t, rec, &resp)
			got := resp.Data
			got.Label = "" // label 文案不是本用例的关注点
			if got != tt.want {
				t.Errorf("got %+v; want %+v（%s）", got, tt.want, tt.why)
			}
		})
	}
	rec := e.do("GET", "/periods/nav?period=garbage", "")
	if code, _ := errCode(t, rec); rec.Code != 400 || code != "bad_request" {
		t.Errorf("非法 period = %d %s; want 400 bad_request", rec.Code, code)
	}
}

func TestMetaDefaultPeriod(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/meta", "")
	var resp struct {
		Data struct {
			DefaultPeriod map[string]struct {
				Type, Key, Label string
				HasNext          bool `json:"has_next"`
			} `json:"default_period"`
		} `json:"data"`
	}
	decode(t, rec, &resp)
	want := map[string]string{"monthly": "2025-07", "quarterly": "2025Q2", "annual": "2024"}
	for typ, key := range want {
		got, ok := resp.Data.DefaultPeriod[typ]
		if !ok {
			t.Errorf("default_period 缺少 %s；want 三个粒度都在", typ)
			continue
		}
		if got.Key != key || got.Type != typ || got.Label == "" {
			t.Errorf("default_period[%s] = %+v; want key=%s（上一个完整周期）、type 同名、label 非空", typ, got, key)
		}
	}
}
