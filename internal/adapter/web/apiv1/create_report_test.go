package apiv1

import (
	"context"
	"encoding/json"
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

// newFullEnv 在 newEnv 的基础上多装配手填与财报（真库、真 usecase）。
func newFullEnv(t *testing.T) *env {
	t.Helper()
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
	spRepo := sqlite.NewSpecialProjectRepo(db)
	if err := spRepo.Upsert(context.Background(), &domain.SpecialProject{ID: "sp-1", Name: "装修", StartedOn: fixedNow}); err != nil {
		t.Fatal(err)
	}
	nav := usecase.PeriodNav{Now: func() time.Time { return fixedNow }}
	q := usecase.NewTxQuery(txRepo, catRepo, catRepo).WithSpecialRepo(spRepo).WithNav(nav)
	rep := usecase.NewQueryReport(txRepo, catRepo).WithSpecialRepo(spRepo)
	api := New(Deps{
		Categories: catRepo, Specials: spRepo, SpecialCheck: usecase.NewSpecialView(spRepo),
		TxQuery: q, Tx: txRepo, TxInsert: txRepo, Report: rep, Nav: nav,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &env{h: api.Routes(), tx: txRepo, cats: catRepo}
}

func (e *env) post(path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(b)))
	e.h.ServeHTTP(rec, req)
	return rec
}

func validCreate() map[string]any {
	return map[string]any{
		"occurred_at": "2025-07-03 14:22", "account": "husband", "direction": "expense",
		"amount_fen": 1234, "category_id": leafExpense, "counterparty": "星巴克", "note": "拿铁",
	}
}

func TestCreateTransactionStoresFen(t *testing.T) {
	e := newFullEnv(t)
	rec := e.post("/transactions", validCreate())
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200（%s）", rec.Code, rec.Body)
	}
	var resp struct {
		Data struct{ ID string } `json:"data"`
	}
	decode(t, rec, &resp)
	if resp.Data.ID == "" {
		t.Fatalf("data.id 为空：%s", rec.Body)
	}
	got, err := e.tx.Get(context.Background(), resp.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"库里的金额（分原样落库，1234 分 = 12.34 元）", got.Amount, int64(1234)},
		{"source", got.Source, domain.SourceManual},
		{"status（给了分类 → confirmed）", got.Status, domain.TxStatusConfirmed},
		{"category_id", got.CategoryID, leafExpense},
		{"account", got.Account, domain.AccountHusband},
		{"counterparty", got.Counterparty, "星巴克"},
		{"note", got.Note, "拿铁"},
		{"occurred_at", got.OccurredAt.Format("2006-01-02 15:04"), "2025-07-03 14:22"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v; want %v", c.name, c.got, c.want)
		}
	}
}

func TestCreateTransactionErrors(t *testing.T) {
	e := newFullEnv(t)
	tests := []struct {
		name    string
		mod     func(map[string]any)
		raw     string // 非空时直接作为 body
		wantMsg string
	}{
		{"缺金额", func(m map[string]any) { delete(m, "amount_fen") }, "", "请提供金额（amount_fen 或 amount_yuan）"},
		{"金额为 0", func(m map[string]any) { m["amount_fen"] = 0 }, "", "金额必须为正数"},
		{"金额为负", func(m map[string]any) { m["amount_fen"] = -5 }, "", "金额必须为正数"},
		{"金额带小数（客户端传了元）", nil, `{"amount_fen":12.5}`, "请求体不是合法的 JSON（amount_fen 必须是整数分）"},
		{"金额是字符串", nil, `{"amount_fen":"12"}`, "请求体不是合法的 JSON（amount_fen 必须是整数分）"},
		{"账户非法", func(m map[string]any) { m["account"] = "family" }, "", "请选择账户归属（男主/女主）"},
		{"方向非法", func(m map[string]any) { m["direction"] = "x" }, "", "请选择收支方向"},
		{"日期非法", func(m map[string]any) { m["occurred_at"] = "x" }, "", "日期时间格式不正确"},
		{"科目不存在", func(m map[string]any) { m["category_id"] = "nope" }, "", "请选择有效的二级分类"},
		{"科目是一级分组", func(m map[string]any) { m["category_id"] = "expense.discretion" }, "", "请选择有效的二级分类"},
		{"不是 JSON", nil, `not json`, "请求体不是合法的 JSON（amount_fen 必须是整数分）"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if tt.raw != "" {
				rec = httptest.NewRecorder()
				e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/transactions", strings.NewReader(tt.raw)))
			} else {
				m := validCreate()
				tt.mod(m)
				rec = e.post("/transactions", m)
			}
			if rec.Code != 400 {
				t.Fatalf("status = %d; want 400（%s）", rec.Code, rec.Body)
			}
			code, msg := errCode(t, rec)
			if code != "bad_request" || msg != tt.wantMsg {
				t.Errorf("error = %q / %q; want bad_request / %q", code, msg, tt.wantMsg)
			}
		})
	}
	// 校验失败一条都不能落库
	page, err := e.tx.List(context.Background(), mustPeriod(t, "2025-07"), domain.AccountFamily)
	if err != nil || len(page) != 0 {
		t.Errorf("校验失败后库里有 %d 条流水（err=%v）；want 0", len(page), err)
	}
}

func mustPeriod(t *testing.T, label string) domain.Period {
	t.Helper()
	p, err := domain.ParsePeriod(label)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// POST /transactions 与 PATCH /transactions/{id} 各就各位：
// 同路径不同 method，互不吞噬；未注册的 method 不能落到 {id}。
func TestCreateTransactionRouteOwnership(t *testing.T) {
	e := newFullEnv(t)
	rec := e.post("/transactions", validCreate())
	var resp struct {
		Data struct{ ID string } `json:"data"`
	}
	decode(t, rec, &resp)
	id := resp.Data.ID

	if rec := e.do("PATCH", "/transactions/"+id, `{"note":"改过"}`); rec.Code != 200 {
		t.Errorf("PATCH /transactions/{id} = %d; want 200（%s）", rec.Code, rec.Body)
	}
	got, _ := e.tx.Get(context.Background(), id)
	if got.Note != "改过" {
		t.Errorf("PATCH 后 note = %q; want 改过", got.Note)
	}
	// PATCH 不带 id 的集合路径不存在
	if rec := e.do("PATCH", "/transactions", `{"note":"x"}`); rec.Code != 405 && rec.Code != 404 {
		t.Errorf("PATCH /transactions = %d; want 404/405", rec.Code)
	}
	// POST 到 {id} 路径不存在，且不得被 POST /transactions 的处理器接走
	rec = e.post("/transactions/"+id, validCreate())
	if rec.Code != 404 && rec.Code != 405 {
		t.Errorf("POST /transactions/{id} = %d; want 404/405", rec.Code)
	}
	// POST /transactions/batch 同理（batch 只挂 PATCH）
	rec = e.post("/transactions/batch", map[string]any{})
	if rec.Code != 404 && rec.Code != 405 {
		t.Errorf("POST /transactions/batch = %d; want 404/405", rec.Code)
	}
}

// ----- 财报 -----

type reportResp struct {
	Data usecase.ReportDTO `json:"data"`
}

func TestReportEndpointInvariantAndWarning(t *testing.T) {
	e := newFullEnv(t)
	at := time.Date(2025, 5, 10, 10, 0, 0, 0, time.Local) // 2025Q2
	add := func(id, cat string, dir domain.Direction, fen int64, special string) {
		e.insert(t, id, func(x *domain.Transaction) {
			x.OccurredAt, x.CategoryID, x.Direction, x.Amount = at, cat, dir, fen
			x.Status, x.SpecialID = domain.TxStatusConfirmed, special
		})
	}
	add("in", leafIncome, domain.DirectionIncome, 1_000_000, "")
	add("d1", leafExpense, domain.DirectionExpense, 50_000, "")                     // 日常自由裁量 500 元
	add("d2", "expense.fixed.housing", domain.DirectionExpense, 50_000, "")         // 日常其它 500 元
	add("sp", "expense.fixed.housing", domain.DirectionExpense, 20_000_000, "sp-1") // 装修 20 万

	rec := e.do("GET", "/report", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200（%s）", rec.Code, rec.Body)
	}
	var r reportResp
	decode(t, rec, &r)
	if r.Data.Period.Key != "2025Q2" || r.Data.Period.Type != "quarterly" {
		t.Errorf("缺省周期 = %s/%s; want quarterly/2025Q2（财报缺省是上一个完整季度，不是月）", r.Data.Period.Type, r.Data.Period.Key)
	}
	d, s, a := r.Data.Cashflow[0], r.Data.Cashflow[1], r.Data.Cashflow[2]
	if d.ExpenseFen != 100_000 || s.ExpenseFen != 20_000_000 || a.ExpenseFen != 20_100_000 {
		t.Errorf("支出 daily/special/all = %d/%d/%d; want 100000/20000000/20100000", d.ExpenseFen, s.ExpenseFen, a.ExpenseFen)
	}
	if d.ExpenseFen+s.ExpenseFen != a.ExpenseFen || d.IncomeFen+s.IncomeFen != a.IncomeFen || d.SurplusFen+s.SurplusFen != a.SurplusFen {
		t.Errorf("daily + special != all：%+v", r.Data.Cashflow)
	}
	// 日常自由裁量 50%：分母若误用全口径 20.1 万，占比只有 0.2%，告警会被静默
	k := r.Data.KPI
	if !k.DiscretionWarning || k.DiscretionRatioText != "50.0%" {
		t.Errorf("warning=%v ratio=%q; want true / 50.0%%（分母必须是日常支出 1000 元，不是全口径）", k.DiscretionWarning, k.DiscretionRatioText)
	}
	if len(r.Data.SpecialByProject) != 1 || r.Data.SpecialByProject[0].AmountText != "200,000.00" {
		t.Errorf("special_by_project = %+v; want 装修 200,000.00", r.Data.SpecialByProject)
	}

	// 原始 JSON 里键名与成对字段都在（契约）
	var raw struct {
		Data struct {
			KPI map[string]any `json:"kpi"`
		} `json:"data"`
	}
	decode(t, rec, &raw)
	for _, key := range []string{"total_income_fen", "total_income_text", "discretion_ratio", "discretion_ratio_text",
		"discretion_warning", "discretion_note", "surplus_rate", "surplus_rate_text", "daily_surplus_text"} {
		if _, ok := raw.Data.KPI[key]; !ok {
			t.Errorf("kpi 缺少 %q", key)
		}
	}
}

// 日常自由裁量 30%：同一笔 20 万专项既不能让告警亮起，也不能改变占比。
func TestReportWarningNotAffectedBySpecial(t *testing.T) {
	e := newFullEnv(t)
	at := time.Date(2025, 5, 10, 10, 0, 0, 0, time.Local)
	add := func(id, cat string, dir domain.Direction, fen int64, special string) {
		e.insert(t, id, func(x *domain.Transaction) {
			x.OccurredAt, x.CategoryID, x.Direction, x.Amount = at, cat, dir, fen
			x.Status, x.SpecialID = domain.TxStatusConfirmed, special
		})
	}
	add("d1", leafExpense, domain.DirectionExpense, 30_000, "")
	add("d2", "expense.fixed.housing", domain.DirectionExpense, 70_000, "")
	add("sp", leafExpense, domain.DirectionExpense, 20_000_000, "sp-1") // 专项里的购物也不计入日常占比
	var r reportResp
	decode(t, e.do("GET", "/report?type=quarterly&period=2025Q2", ""), &r)
	if r.Data.KPI.DiscretionWarning || r.Data.KPI.DiscretionRatioText != "30.0%" {
		t.Errorf("warning=%v ratio=%q; want false / 30.0%%（专项里的自由裁量支出不进日常分子）",
			r.Data.KPI.DiscretionWarning, r.Data.KPI.DiscretionRatioText)
	}
}

func TestReportEndpointErrors(t *testing.T) {
	e := newFullEnv(t)
	rec := e.do("GET", "/report?type=quarterly&period=garbage", "")
	if rec.Code != 400 {
		t.Fatalf("status = %d; want 400（%s）", rec.Code, rec.Body)
	}
	if code, _ := errCode(t, rec); code != "bad_request" {
		t.Errorf("code = %q; want bad_request", code)
	}
	rec = e.do("GET", "/report?type=annual&period=2024&account=wife", "")
	var r reportResp
	decode(t, rec, &r)
	if rec.Code != 200 || r.Data.Period.Key != "2024" || r.Data.AccountText != "女主" {
		t.Errorf("annual/2024/wife → %d %s %q; want 200 / 2024 / 女主", rec.Code, r.Data.Period.Key, r.Data.AccountText)
	}
}

// amount_fen 与 amount_yuan 二选一；"12.34" 与 1234 落库完全相同。
func TestCreateTransactionAmountYuanOrFen(t *testing.T) {
	tests := []struct {
		name    string
		mod     func(map[string]any)
		wantMsg string // 空 = 期望 200
	}{
		{"只给 yuan", func(m map[string]any) { delete(m, "amount_fen"); m["amount_yuan"] = "12.34" }, ""},
		{"只给 fen", func(m map[string]any) {}, ""},
		{"两个都给 → 400，不猜优先级", func(m map[string]any) { m["amount_yuan"] = "12.34" }, "金额只能给 amount_fen 或 amount_yuan 其中一个"},
		{"都不给 → 400", func(m map[string]any) { delete(m, "amount_fen") }, "请提供金额（amount_fen 或 amount_yuan）"},
		{"yuan 非法 → 400 复用 ParseYuanToFen 的报错", func(m map[string]any) { delete(m, "amount_fen"); m["amount_yuan"] = "abc" }, "金额格式不正确"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newFullEnv(t)
			m := validCreate()
			tt.mod(m)
			rec := e.post("/transactions", m)
			if tt.wantMsg == "" {
				if rec.Code != 200 {
					t.Fatalf("status = %d; want 200（%s）", rec.Code, rec.Body)
				}
				return
			}
			if rec.Code != 400 {
				t.Fatalf("status = %d; want 400（%s）", rec.Code, rec.Body)
			}
			if _, msg := errCode(t, rec); msg != tt.wantMsg {
				t.Errorf("message = %q; want %q", msg, tt.wantMsg)
			}
		})
	}

	// 两条路径落库的流水逐字段相同（除 id）
	e := newFullEnv(t)
	create := func(mod func(map[string]any)) domain.Transaction {
		m := validCreate()
		mod(m)
		rec := e.post("/transactions", m)
		var resp struct {
			Data struct{ ID string } `json:"data"`
		}
		decode(t, rec, &resp)
		got, err := e.tx.Get(context.Background(), resp.Data.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	a := create(func(m map[string]any) { delete(m, "amount_fen"); m["amount_yuan"] = "12.34" })
	b := create(func(m map[string]any) {})
	a.ID, b.ID, a.CreatedAt, b.CreatedAt, a.UpdatedAt, b.UpdatedAt = "", "", time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if a != b {
		t.Errorf("yuan=\"12.34\" 落库 %+v; fen=1234 落库 %+v; want 完全相同（换算只有 ParseYuanToFen 一处）", a, b)
	}
}

// occurred_at 缺省取服务器时间（这里是注入的 fixedNow）；给了非法值仍是 400。
func TestCreateTransactionOccurredAtOptional(t *testing.T) {
	e := newFullEnv(t)
	m := validCreate()
	delete(m, "occurred_at")
	rec := e.post("/transactions", m)
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200（%s）", rec.Code, rec.Body)
	}
	var resp struct {
		Data struct{ ID string } `json:"data"`
	}
	decode(t, rec, &resp)
	got, err := e.tx.Get(context.Background(), resp.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OccurredAt.Equal(fixedNow) {
		t.Errorf("OccurredAt = %v; want 服务器时钟 %v（缺省不由客户端拼）", got.OccurredAt, fixedNow)
	}
}

func TestCreateTransactionMemberLimit(t *testing.T) {
	e := newFullEnv(t)
	m := validCreate()
	m["member"] = strings.Repeat("字", 21)
	rec := e.post("/transactions", m)
	if rec.Code != 400 {
		t.Fatalf("status = %d; want 400（member 21 字，与 PATCH 同一上限 20）", rec.Code)
	}
}
