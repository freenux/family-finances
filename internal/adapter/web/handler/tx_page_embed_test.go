package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"family-finances/internal/adapter/web"
	"family-finances/internal/adapter/web/apiv1"
	"family-finances/internal/domain"
	"family-finances/internal/usecase"
)

func newTxPageHandler(t *testing.T, txRepo *stubTxRepo, specials *stubSpecialRepo) (*Handler, *usecase.TxQuery) {
	t.Helper()
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	q := usecase.NewTxQuery(txRepo, stubCatRepo{}, stubRuleRepo{})
	return &Handler{
		render:      renderer,
		txRepo:      txRepo,
		txQuery:     q,
		catRepo:     stubCatRepo{},
		ruleRepo:    stubRuleRepo{},
		specialView: usecase.NewSpecialView(specials),
		flash:       newFlashStore(),
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, q
}

// embeddedJSON 取出页面里 <script id=… type="application/json"> 的原文
func embeddedJSON(t *testing.T, body, id string) string {
	t.Helper()
	open := `<script id="` + id + `" type="application/json">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("页面里没有 #%s", id)
	}
	rest := body[i+len(open):]
	return rest[:strings.Index(rest, "</script>")]
}

// 同一个视图只能有一个缺省窗口：网页 /transactions 与 GET /api/v1/transactions
// 不带任何参数时，向仓库要的周期必须相同，且都是上月。
func TestTransactionsDefaultPeriodSameAsAPI(t *testing.T) {
	txRepo := newStubTxRepo()
	h, q := newTxPageHandler(t, txRepo, &stubSpecialRepo{})
	api := apiv1.New(apiv1.Deps{
		Categories: stubCatRepo{}, TxQuery: q, Tx: txRepo,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Routes()

	rec := httptest.NewRecorder()
	h.ListTransactions(rec, httptest.NewRequest(http.MethodGet, "/transactions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("页面 status = %d; want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/transactions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("API status = %d; want 200（%s）", rec.Code, rec.Body)
	}

	if len(txRepo.queries) != 2 {
		t.Fatalf("QueryTransactions 被调用 %d 次; want 2（页面一次、API 一次）", len(txRepo.queries))
	}
	page, apiQ := txRepo.queries[0].Period, txRepo.queries[1].Period
	want := domain.CurrentMonth(time.Now()).Previous().Label
	if page.Label != apiQ.Label || page.Type != apiQ.Type {
		t.Errorf("页面缺省周期 %s/%s != API 缺省周期 %s/%s; want 两者相同（同一视图不能有两个缺省窗口）",
			page.Type, page.Label, apiQ.Type, apiQ.Label)
	}
	if page.Label != want || page.Type != domain.PeriodMonthly {
		t.Errorf("缺省周期 = %s/%s; want monthly/%s（上月）", page.Type, page.Label, want)
	}
}

// 首屏把 meta（下拉选项）一并 rawJSON 嵌入：能被 JSON.parse，分组形状正确，
// 页面也就不用再发 /api/v1/meta。
func TestListTransactionsEmbedsMeta(t *testing.T) {
	txRepo := newStubTxRepo()
	specials := &stubSpecialRepo{projects: []domain.SpecialProject{{ID: "sp-1", Name: "装修", StartedOn: time.Now()}}}
	h, _ := newTxPageHandler(t, txRepo, specials)
	rec := httptest.NewRecorder()
	h.ListTransactions(rec, httptest.NewRequest(http.MethodGet, "/transactions?type=monthly&period=2026-03", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200（%s）", rec.Code, rec.Body)
	}
	raw := embeddedJSON(t, rec.Body.String(), "data-meta")
	var meta usecase.MetaView
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("嵌入的 meta 解析失败: %v（raw=%.80s）——多半是没用 rawJSON", err, raw)
	}
	if len(meta.Categories) != 1 || meta.Categories[0].GroupID != "expense.discretion" ||
		len(meta.Categories[0].Items) != 1 || meta.Categories[0].Items[0].ID != "expense.discretion.shopping" {
		t.Errorf("categories = %+v; want 一个分组 expense.discretion，下挂 shopping", meta.Categories)
	}
	if len(meta.Specials) != 1 || meta.Specials[0].ID != "sp-1" || meta.Specials[0].Name != "装修" {
		t.Errorf("specials = %+v; want [sp-1 装修]", meta.Specials)
	}
	if len(meta.AccountViews) != 3 || len(meta.Accounts) != 2 || len(meta.Statuses) != 3 {
		t.Errorf("accounts/views/statuses = %d/%d/%d; want 2/3/3", len(meta.Accounts), len(meta.AccountViews), len(meta.Statuses))
	}

	// 与 /api/v1/meta 是同一份：JSON 逐字相同
	api := apiv1.New(apiv1.Deps{Categories: stubCatRepo{}, Specials: specials,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()
	mrec := httptest.NewRecorder()
	api.ServeHTTP(mrec, httptest.NewRequest(http.MethodGet, "/meta", nil))
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(mrec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal([]byte(raw), &a)
	_ = json.Unmarshal(env.Data, &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Errorf("SSR 嵌入的 meta 与 /api/v1/meta 不一致:\nssr=%s\napi=%s", ja, jb)
	}
}

// 专项功能未启用（specialView 为 nil）时页面照样渲染，meta.specials 是空数组
func TestListTransactionsEmbedsMetaWithoutSpecials(t *testing.T) {
	txRepo := newStubTxRepo()
	h, _ := newTxPageHandler(t, txRepo, &stubSpecialRepo{})
	h.specialView = nil
	rec := httptest.NewRecorder()
	h.ListTransactions(rec, httptest.NewRequest(http.MethodGet, "/transactions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200（%s）", rec.Code, rec.Body)
	}
	var meta usecase.MetaView
	if err := json.Unmarshal([]byte(embeddedJSON(t, rec.Body.String(), "data-meta")), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Specials == nil || len(meta.Specials) != 0 {
		t.Errorf("specials = %#v; want 空数组", meta.Specials)
	}
}

// 页面样式统一在 app.css，不再有页内 <style>；「归入当前筛选的全部」入口在批量条里（有专项时才出现）
func TestTransactionsPageHasNoInlineStyle(t *testing.T) {
	txRepo := newStubTxRepo()
	h, _ := newTxPageHandler(t, txRepo, &stubSpecialRepo{})
	rec := httptest.NewRecorder()
	h.ListTransactions(rec, httptest.NewRequest(http.MethodGet, "/transactions", nil))
	body := rec.Body.String()
	if strings.Contains(body, "<style") {
		t.Errorf("transactions 页面里还有页内 <style>; want 全部并进 app.css")
	}
	if !strings.Contains(body, "applyBatchSpecialByFilter()") {
		t.Errorf("页面缺少「归入当前筛选的全部 N 条」入口")
	}
}
