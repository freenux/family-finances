package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"family-finances/internal/adapter/web"
	"family-finances/internal/adapter/web/apiv1"
	"family-finances/internal/domain"
	"family-finances/internal/infrastructure/sqlite"
)

// 手填的两条入口（SSR 表单 / apiv1 JSON）同一组输入必须产出完全相同的流水：
// 校验与装配只在 usecase.CreateTransaction 一份，这里钉住两个适配器没有各自偷偷加工。
func TestManualEntryFormAndAPIProduceSameTransaction(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatal(err)
	}
	txRepo := sqlite.NewTransactionRepo(db)
	catRepo := sqlite.NewCategoryRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{txRepo: txRepo, catRepo: catRepo, flash: newFlashStore(), log: log}
	api := apiv1.New(apiv1.Deps{Categories: catRepo, Tx: txRepo, TxInsert: txRepo, Log: log}).Routes()

	const cat = "expense.discretion.shopping"
	// 表单收元（"12.34"），API 收分（1234）：同一笔钱
	form := url.Values{
		"occurred_at": {"2025-07-03T14:22"}, "account": {"wife"}, "direction": {"expense"},
		"amount": {"12.34"}, "category_id": {cat}, "member": {" 孩子 "},
		"counterparty": {"星巴克"}, "description": {"咖啡"}, "note": {"拿铁"},
	}
	req := httptest.NewRequest(http.MethodPost, "/imports/manual", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ManualEntrySubmit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("表单 status = %d; want 303（%s）", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/transactions?") || !strings.Contains(loc, "period=2025-07") {
		t.Errorf("表单跳转 = %q; want /transactions?...period=2025-07...", loc)
	}

	body := `{"occurred_at":"2025-07-03 14:22","account":"wife","direction":"expense","amount_fen":1234,` +
		`"category_id":"` + cat + `","member":" 孩子 ","counterparty":"星巴克","description":"咖啡","note":"拿铁"}`
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/transactions", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("API status = %d; want 200（%s）", rec.Code, rec.Body)
	}

	p, _ := domain.ParsePeriod("2025-07")
	got, err := txRepo.List(context.Background(), p, domain.AccountFamily)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("库里 %d 条流水; want 2（表单一条 + API 一条）", len(got))
	}
	a, b := got[0], got[1]
	// ID / 创建与更新时间天然不同，其余字段必须逐一相等
	b.ID, b.CreatedAt, b.UpdatedAt = a.ID, a.CreatedAt, a.UpdatedAt
	if !a.OccurredAt.Equal(b.OccurredAt) {
		t.Errorf("OccurredAt 不同：%v vs %v", a.OccurredAt, b.OccurredAt)
	}
	b.OccurredAt = a.OccurredAt
	if a != b {
		t.Errorf("两条入口产出的流水不等价：\n表单/API = %+v\n          %+v", a, b)
	}
	if a.Amount != 1234 || a.Source != domain.SourceManual || a.Status != domain.TxStatusConfirmed || a.Member != "孩子" {
		t.Errorf("流水 = %+v; want 金额 1234 分、来源 manual、confirmed、member 已 trim", a)
	}
}

// 表单路径的校验失败文案保持原样（400 + 导入页错误）：对外行为不变。
func TestManualEntryFormKeepsErrorMessages(t *testing.T) {
	tests := []struct {
		name    string
		mod     func(url.Values)
		wantMsg string
	}{
		{"日期", func(v url.Values) { v.Set("occurred_at", "x") }, "日期时间格式不正确"},
		{"账户", func(v url.Values) { v.Set("account", "") }, "请选择账户归属（男主/女主）"},
		{"方向", func(v url.Values) { v.Set("direction", "") }, "请选择收支方向"},
		{"金额格式", func(v url.Values) { v.Set("amount", "abc") }, "金额格式不正确"},
		{"金额非正", func(v url.Values) { v.Set("amount", "0") }, "金额必须为正数"},
		{"金额过大", func(v url.Values) { v.Set("amount", "1e30") }, "金额格式不正确"},
		{"科目", func(v url.Values) { v.Set("category_id", "nope") }, "请选择有效的二级分类"},
	}
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{
				render: renderer, txRepo: newStubTxRepo(), catRepo: stubCatRepo{}, flash: newFlashStore(),
				log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			v := url.Values{"occurred_at": {"2025-07-03T14:22"}, "account": {"husband"}, "direction": {"expense"}, "amount": {"1"}}
			tt.mod(v)
			req := httptest.NewRequest(http.MethodPost, "/imports/manual", strings.NewReader(v.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			h.ManualEntrySubmit(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.wantMsg) {
				t.Errorf("页面里没有错误文案 %q", tt.wantMsg)
			}
		})
	}
}

// 手填不给科目：SSR 表单与 /api/v1 都必须落成 pending_review（给了科目则 confirmed），
// 并且真的能被 ListPendingCategory 捞到——这正是 LLM 兜底（ClassifyPending）的入口。
// 若落成 confirmed，这笔流水既不进聚合又没人来分类，录进去就消失。
func TestManualEntryWithoutCategoryIsPickedUpByClassifier(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatal(err)
	}
	txRepo := sqlite.NewTransactionRepo(db)
	catRepo := sqlite.NewCategoryRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{txRepo: txRepo, catRepo: catRepo, flash: newFlashStore(), log: log}
	api := apiv1.New(apiv1.Deps{Categories: catRepo, Tx: txRepo, TxInsert: txRepo, Log: log}).Routes()

	const cat = "expense.discretion.shopping"
	postForm := func(counterparty, category string) {
		form := url.Values{
			"occurred_at": {"2025-07-03T14:22"}, "account": {"wife"}, "direction": {"expense"},
			"amount": {"12.34"}, "category_id": {category}, "counterparty": {counterparty},
		}
		req := httptest.NewRequest(http.MethodPost, "/imports/manual", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ManualEntrySubmit(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("表单 status = %d; want 303（%s）", rec.Code, rec.Body)
		}
	}
	postAPI := func(counterparty, category string) {
		body := `{"occurred_at":"2025-07-03 14:22","account":"wife","direction":"expense","amount_fen":1234,` +
			`"category_id":"` + category + `","counterparty":"` + counterparty + `"}`
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/transactions", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("API status = %d; want 200（%s）", rec.Code, rec.Body)
		}
	}
	postForm("表单无科目", "")
	postForm("表单有科目", cat)
	postAPI("API无科目", "")
	postAPI("API有科目", cat)

	p, _ := domain.ParsePeriod("2025-07")
	all, err := txRepo.List(context.Background(), p, domain.AccountFamily)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]domain.TxStatus{
		"表单无科目": domain.TxStatusPendingReview, "API无科目": domain.TxStatusPendingReview,
		"表单有科目": domain.TxStatusConfirmed, "API有科目": domain.TxStatusConfirmed,
	}
	if len(all) != len(want) {
		t.Fatalf("库里 %d 条; want %d", len(all), len(want))
	}
	for _, tx := range all {
		if tx.Status != want[tx.Counterparty] {
			t.Errorf("%s: status = %v; want %v（无科目必须待核对，有科目才算已确认）", tx.Counterparty, tx.Status, want[tx.Counterparty])
		}
	}

	pending, err := txRepo.ListPendingCategory(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tx := range pending {
		got[tx.Counterparty] = true
	}
	if len(pending) != 2 || !got["表单无科目"] || !got["API无科目"] {
		t.Errorf("ListPendingCategory = %+v; want 恰为两条无科目手填（LLM 兜底只能看见这一类）", got)
	}
}
