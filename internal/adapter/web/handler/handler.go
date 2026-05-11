package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"family-finances/internal/adapter/web"
	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

type Handler struct {
	render     *web.Renderer
	importBill *usecase.ImportBill
	queryRep   *usecase.QueryReport
	queryStats *usecase.QueryStats
	txRepo     port.TransactionRepo
	catRepo    port.CategoryRepo
	log        *slog.Logger
	flash      *flashStore
}

func New(r *web.Renderer, importBill *usecase.ImportBill, qr *usecase.QueryReport, qs *usecase.QueryStats,
	txRepo port.TransactionRepo, catRepo port.CategoryRepo, log *slog.Logger) *Handler {
	return &Handler{
		render:     r,
		importBill: importBill,
		queryRep:   qr,
		queryStats: qs,
		txRepo:     txRepo,
		catRepo:    catRepo,
		log:        log,
		flash:      newFlashStore(),
	}
}

type pageBase struct {
	Title   string
	Nav     string
	Period  domain.Period
	Flash   string
	Account domain.Account
}

func parsePeriodFromQuery(r *http.Request) (domain.Period, error) {
	typeStr := r.URL.Query().Get("type")
	if typeStr == "" {
		typeStr = string(domain.PeriodQuarterly)
	}
	label := r.URL.Query().Get("period")
	// 若 label 与 type 不匹配（例如切换季→年时旧 label 还是 2026Q1），退回到 type 的默认值
	labelMatchesType := label != "" &&
		((typeStr == string(domain.PeriodQuarterly) && strings.Contains(label, "Q")) ||
			(typeStr == string(domain.PeriodAnnual) && !strings.Contains(label, "Q") && !strings.Contains(label, "-")) ||
			(typeStr == string(domain.PeriodMonthly) && strings.Contains(label, "-")))
	if !labelMatchesType {
		switch typeStr {
		case string(domain.PeriodAnnual):
			label = strconv.Itoa(time.Now().Year())
		case string(domain.PeriodMonthly):
			label = domain.CurrentMonth(time.Now()).Label
		default:
			label = domain.CurrentQuarter(time.Now()).Label
		}
	}
	return domain.ParsePeriod(label)
}

// parseAccountFromQuery 默认 family
func parseAccountFromQuery(r *http.Request) domain.Account {
	v := r.URL.Query().Get("account")
	switch v {
	case string(domain.AccountHusband):
		return domain.AccountHusband
	case string(domain.AccountWife):
		return domain.AccountWife
	default:
		return domain.AccountFamily
	}
}

// ----- Dashboard -----

type dashboardVM struct {
	pageBase
	Report domain.ReportData
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	acc := parseAccountFromQuery(r)
	rep, err := h.queryRep.Execute(r.Context(), p, acc)
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := dashboardVM{
		pageBase: pageBase{Title: "仪表盘", Nav: "dashboard", Period: p, Account: acc},
		Report:   rep,
	}
	if err := h.render.RenderPage(w, "dashboard", vm); err != nil {
		h.serverError(w, err)
	}
}

func (h *Handler) PartialReport(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	acc := parseAccountFromQuery(r)
	rep, err := h.queryRep.Execute(r.Context(), p, acc)
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := dashboardVM{
		pageBase: pageBase{Period: p, Account: acc},
		Report:   rep,
	}
	if err := h.render.RenderPartial(w, "report_view", vm); err != nil {
		h.serverError(w, err)
	}
}

// ----- Stats -----

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	vm := pageBase{Title: "统计", Nav: "stats"}
	if err := h.render.RenderPage(w, "stats", vm); err != nil {
		h.serverError(w, err)
	}
}

// StatsAPI: GET /api/stats?granularity=month|quarter|year&period=2026-05&direction=expense&account=family
func (h *Handler) StatsAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	gran := q.Get("granularity")
	if gran == "" {
		gran = "month"
	}
	label := q.Get("period")
	if label == "" {
		switch gran {
		case "month":
			label = domain.CurrentMonth(time.Now()).Label
		case "quarter":
			label = domain.CurrentQuarter(time.Now()).Label
		case "year":
			label = strconv.Itoa(time.Now().Year())
		}
	}
	p, err := domain.ParsePeriod(label)
	if err != nil {
		http.Error(w, "invalid period: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := domain.Direction(q.Get("direction"))
	if dir == "" {
		dir = domain.DirectionExpense
	}
	acc := parseAccountFromQuery(r)

	view, err := h.queryStats.Execute(r.Context(), p, dir, acc, 10)
	if err != nil {
		h.serverError(w, err)
		return
	}
	writeJSON(w, view)
}

// StatsTopAPI: GET /api/stats/top?granularity=&period=&direction=&account=&limit=
// 独立端点让"点柱状图切 Top"走轻量查询，不用重跑全套聚合
func (h *Handler) StatsTopAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := domain.ParsePeriod(q.Get("period"))
	if err != nil {
		http.Error(w, "invalid period: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := domain.Direction(q.Get("direction"))
	if dir == "" {
		dir = domain.DirectionExpense
	}
	acc := parseAccountFromQuery(r)
	limit := 10
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}

	tops, err := h.txRepo.TopTransactions(r.Context(), p, dir, acc, limit)
	if err != nil {
		h.serverError(w, err)
		return
	}
	out := make([]usecase.StatsTopTx, 0, len(tops))
	for _, t := range tops {
		desc := t.Description
		if t.Note != "" {
			if desc != "" {
				desc = desc + "（" + t.Note + "）"
			} else {
				desc = t.Note
			}
		}
		cat := t.CategoryName
		if cat == "" {
			cat = "未分类"
		}
		who := t.Counterparty
		if who == "" {
			who = "—"
		}
		out = append(out, usecase.StatsTopTx{
			ID:       t.ID,
			Date:     t.OccurredAt.Format("01-02"),
			Who:      who,
			Desc:     desc,
			Amount:   t.Amount,
			Category: cat,
			Account:  string(t.Account),
		})
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ----- Transactions list -----

type txRowJSON struct {
	ID           string `json:"id"`
	OccurredAt   string `json:"occurred_at"`
	Source       string `json:"source"`
	Account      string `json:"account"`
	Counterparty string `json:"counterparty"`
	Description  string `json:"description"`
	Note         string `json:"note"`
	AmountFen    int64  `json:"amount_fen"`
	Direction    string `json:"direction"`
	Status       string `json:"status"`
	CategoryID   string `json:"category_id"`
}

type catJSON struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent_id"`
	Name      string `json:"name"`
	GroupName string `json:"group_name"`
	Type      string `json:"type"`
	Level     int    `json:"level"`
}

type txListVM struct {
	pageBase
	Transactions []domain.Transaction
	Categories   []domain.Category

	// 给 Alpine 用的 JSON 内联数据
	TransactionsJSON string
	CategoriesJSON   string
}

func (h *Handler) ListTransactions(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	acc := parseAccountFromQuery(r)
	txs, err := h.txRepo.List(r.Context(), p, acc)
	if err != nil {
		h.serverError(w, err)
		return
	}
	cats, err := h.catRepo.ListAll(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}

	txJSON := make([]txRowJSON, 0, len(txs))
	for _, t := range txs {
		txJSON = append(txJSON, txRowJSON{
			ID:           t.ID,
			OccurredAt:   t.OccurredAt.Format("2006-01-02"),
			Source:       string(t.Source),
			Account:      string(t.Account),
			Counterparty: t.Counterparty,
			Description:  t.Description,
			Note:         t.Note,
			AmountFen:    t.Amount,
			Direction:    string(t.Direction),
			Status:       string(t.Status),
			CategoryID:   t.CategoryID,
		})
	}

	catNameByID := make(map[string]string, len(cats))
	for _, c := range cats {
		if c.Level == 1 {
			catNameByID[c.ID] = c.Name
		}
	}
	catJSONs := make([]catJSON, 0, len(cats))
	for _, c := range cats {
		groupName := ""
		if c.Level == 2 {
			groupName = catNameByID[c.ParentID]
		}
		catJSONs = append(catJSONs, catJSON{
			ID:        c.ID,
			ParentID:  c.ParentID,
			Name:      c.Name,
			GroupName: groupName,
			Type:      string(c.Type),
			Level:     c.Level,
		})
	}

	txBytes, _ := json.Marshal(txJSON)
	catBytes, _ := json.Marshal(catJSONs)

	vm := txListVM{
		pageBase: pageBase{
			Title:   "收支流水",
			Nav:     "transactions",
			Period:  p,
			Flash:   h.flash.pop(w, r),
			Account: acc,
		},
		Transactions:     txs,
		Categories:       cats,
		TransactionsJSON: string(txBytes),
		CategoriesJSON:   string(catBytes),
	}
	if err := h.render.RenderPage(w, "transactions", vm); err != nil {
		h.serverError(w, err)
	}
}

// ListTransactionsAPI: GET /api/transactions?type=...&period=...&account=...
// 返回与列表页 SSR 嵌入 JSON 相同的 txRowJSON 数组，供前端切换周期时客户端刷新。
func (h *Handler) ListTransactionsAPI(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	acc := parseAccountFromQuery(r)
	txs, err := h.txRepo.List(r.Context(), p, acc)
	if err != nil {
		h.serverError(w, err)
		return
	}
	out := make([]txRowJSON, 0, len(txs))
	for _, t := range txs {
		out = append(out, txRowJSON{
			ID:           t.ID,
			OccurredAt:   t.OccurredAt.Format("2006-01-02"),
			Source:       string(t.Source),
			Account:      string(t.Account),
			Counterparty: t.Counterparty,
			Description:  t.Description,
			Note:         t.Note,
			AmountFen:    t.Amount,
			Direction:    string(t.Direction),
			Status:       string(t.Status),
			CategoryID:   t.CategoryID,
		})
	}
	writeJSON(w, map[string]any{"transactions": out})
}

// ----- Update single transaction (PATCH via form) -----

type updateTxReq struct {
	CategoryID *string `json:"category_id"`
	Note       *string `json:"note"`
	Status     *string `json:"status"`
	Account    *string `json:"account"`
}

func (h *Handler) UpdateTransaction(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	var req updateTxReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	patch := port.TransactionUpdate{}
	if req.CategoryID != nil {
		v := *req.CategoryID
		patch.CategoryID = &v
		// 如果指定了分类，把 pending_review 自动转 confirmed
		if v != "" {
			st := domain.TxStatusConfirmed
			patch.Status = &st
		}
	}
	if req.Note != nil {
		v := *req.Note
		patch.Note = &v
	}
	if req.Status != nil {
		s := domain.TxStatus(*req.Status)
		patch.Status = &s
	}
	if req.Account != nil {
		a := domain.Account(*req.Account)
		if !a.IsStorageAccount() {
			http.Error(w, "invalid account", http.StatusBadRequest)
			return
		}
		patch.Account = &a
	}
	if err := h.txRepo.Update(r.Context(), id, patch); err != nil {
		h.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- Import bill -----

type importVM struct {
	pageBase
	Error string
}

func (h *Handler) ImportForm(w http.ResponseWriter, r *http.Request) {
	vm := importVM{pageBase: pageBase{Title: "导入账单", Nav: "imports"}}
	if err := h.render.RenderPage(w, "imports", vm); err != nil {
		h.serverError(w, err)
	}
}

func (h *Handler) ImportSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceStr := r.FormValue("source")
	var src domain.Source
	switch sourceStr {
	case "alipay":
		src = domain.SourceAlipay
	case "wechat":
		src = domain.SourceWechat
	default:
		h.renderImportError(w, "请选择账单来源")
		return
	}

	accStr := r.FormValue("account")
	acc := domain.Account(accStr)
	if !acc.IsStorageAccount() {
		h.renderImportError(w, "请选择账户归属（男主/女主）")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		h.renderImportError(w, "请选择要导入的账单文件")
		return
	}
	defer file.Close()

	res, err := h.importBill.Execute(r.Context(), usecase.ImportBillInput{
		Source:   src,
		Account:  acc,
		Filename: header.Filename,
		Reader:   file,
	})
	if err != nil {
		h.log.Error("import", "err", err)
		h.renderImportError(w, "导入失败："+err.Error())
		return
	}

	msg := fmt.Sprintf("导入完成（%s）：新增 %d 条，跳过重复 %d 条，忽略转账/无效 %d 条，未分类待处理 %d 条。",
		acc.Label(), res.InsertedRows, res.SkippedDuplicates, res.SkippedInvalid, res.PendingCategory)
	h.flash.set(w, msg)
	http.Redirect(w, r, "/transactions?account="+string(acc), http.StatusSeeOther)
}

func (h *Handler) renderImportError(w http.ResponseWriter, msg string) {
	vm := importVM{pageBase: pageBase{Title: "导入账单", Nav: "imports"}, Error: msg}
	w.WriteHeader(http.StatusBadRequest)
	if err := h.render.RenderPage(w, "imports", vm); err != nil {
		h.serverError(w, err)
	}
}

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	h.log.Error("server error", "err", err)
	http.Error(w, "服务器错误: "+err.Error(), http.StatusInternalServerError)
}
