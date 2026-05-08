package handler

import (
	"context"
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
	render    *web.Renderer
	addTx     *usecase.AddTransaction
	queryRep  *usecase.QueryReport
	txRepo    port.TransactionRepo
	catRepo   port.CategoryRepo
	log       *slog.Logger
}

func New(r *web.Renderer, addTx *usecase.AddTransaction, qr *usecase.QueryReport,
	txRepo port.TransactionRepo, catRepo port.CategoryRepo, log *slog.Logger) *Handler {
	return &Handler{render: r, addTx: addTx, queryRep: qr, txRepo: txRepo, catRepo: catRepo, log: log}
}

type pageBase struct {
	Title         string
	Nav           string
	Period        domain.Period
	PeriodOptions []string
}

// 生成最近 8 个季度 + 最近 5 年作为下拉选项
func periodOptions(p domain.Period) []string {
	now := time.Now()
	switch p.Type {
	case domain.PeriodQuarterly:
		curr := domain.CurrentQuarter(now)
		opts := make([]string, 0, 8)
		cursor := curr
		for i := 0; i < 8; i++ {
			opts = append(opts, cursor.Label)
			cursor = cursor.Previous()
		}
		return opts
	case domain.PeriodAnnual:
		y := now.Year()
		opts := make([]string, 0, 5)
		for i := 0; i < 5; i++ {
			opts = append(opts, strconv.Itoa(y-i))
		}
		return opts
	}
	return nil
}

// 从 query 参数解析 Period；缺省返回当前季度
func parsePeriodFromQuery(r *http.Request) (domain.Period, error) {
	typeStr := r.URL.Query().Get("type")
	if typeStr == "" {
		typeStr = string(domain.PeriodQuarterly)
	}
	label := r.URL.Query().Get("period")
	if label == "" {
		if typeStr == string(domain.PeriodAnnual) {
			label = strconv.Itoa(time.Now().Year())
		} else {
			label = domain.CurrentQuarter(time.Now()).Label
		}
	}
	return domain.ParsePeriod(label)
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
	rep, err := h.queryRep.Execute(r.Context(), p)
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := dashboardVM{
		pageBase: pageBase{Title: "仪表盘", Nav: "dashboard", Period: p, PeriodOptions: periodOptions(p)},
		Report:   rep,
	}
	if err := h.render.RenderPage(w, "dashboard", vm); err != nil {
		h.serverError(w, err)
	}
}

// HTMX 片段：只返回报表视图
func (h *Handler) PartialReport(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rep, err := h.queryRep.Execute(r.Context(), p)
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := dashboardVM{
		pageBase: pageBase{Period: p, PeriodOptions: periodOptions(p)},
		Report:   rep,
	}
	if err := h.render.RenderPartial(w, "report_view", vm); err != nil {
		h.serverError(w, err)
	}
}

// ----- Transactions list -----

type txListVM struct {
	pageBase
	Transactions []domain.Transaction
	Categories   []domain.Category
}

func (h *Handler) ListTransactions(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	txs, err := h.txRepo.List(r.Context(), p)
	if err != nil {
		h.serverError(w, err)
		return
	}
	cats, err := h.catRepo.ListAll(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := txListVM{
		pageBase:     pageBase{Title: "收支流水", Nav: "transactions", Period: p, PeriodOptions: periodOptions(p)},
		Transactions: txs,
		Categories:   cats,
	}
	if err := h.render.RenderPage(w, "transactions", vm); err != nil {
		h.serverError(w, err)
	}
}

// ----- New Transaction form -----

type txFormData struct {
	OccurredAt   string
	Direction    string
	AmountYuan   string
	CategoryID   string
	Counterparty string
	Description  string
}

type txNewVM struct {
	pageBase
	IncomeCategories  []domain.Category
	ExpenseCategories []domain.Category
	Form              txFormData
	Error             string
}

func (h *Handler) NewTransactionForm(w http.ResponseWriter, r *http.Request) {
	vm, err := h.buildNewVM(r.Context(), txFormData{
		OccurredAt: time.Now().Format("2006-01-02"),
		Direction:  "expense",
	}, "")
	if err != nil {
		h.serverError(w, err)
		return
	}
	if err := h.render.RenderPage(w, "transaction_new", vm); err != nil {
		h.serverError(w, err)
	}
}

func (h *Handler) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	form := txFormData{
		OccurredAt:   r.FormValue("occurred_at"),
		Direction:    r.FormValue("direction"),
		AmountYuan:   r.FormValue("amount_yuan"),
		CategoryID:   r.FormValue("category_id"),
		Counterparty: strings.TrimSpace(r.FormValue("counterparty")),
		Description:  strings.TrimSpace(r.FormValue("description")),
	}

	occurredAt, err := time.ParseInLocation("2006-01-02", form.OccurredAt, time.Local)
	if err != nil {
		h.renderFormError(w, r.Context(), form, "发生日期格式错误")
		return
	}
	amountYuan, err := strconv.ParseFloat(form.AmountYuan, 64)
	if err != nil || amountYuan <= 0 {
		h.renderFormError(w, r.Context(), form, "请填写有效的金额")
		return
	}
	amountFen := int64(amountYuan*100 + 0.5) // 分

	_, err = h.addTx.Execute(r.Context(), usecase.AddTransactionInput{
		OccurredAt:   occurredAt,
		Counterparty: form.Counterparty,
		Description:  form.Description,
		Amount:       amountFen,
		Direction:    domain.Direction(form.Direction),
		CategoryID:   form.CategoryID,
	})
	if err != nil {
		h.renderFormError(w, r.Context(), form, err.Error())
		return
	}

	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

func (h *Handler) renderFormError(w http.ResponseWriter, ctx context.Context, form txFormData, msg string) {
	vm, err := h.buildNewVM(ctx, form, msg)
	if err != nil {
		h.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	if err := h.render.RenderPage(w, "transaction_new", vm); err != nil {
		h.serverError(w, err)
	}
}

func (h *Handler) buildNewVM(ctx context.Context, form txFormData, errMsg string) (txNewVM, error) {
	cats, err := h.catRepo.ListAll(ctx)
	if err != nil {
		return txNewVM{}, err
	}
	var incomes, expenses []domain.Category
	for _, c := range cats {
		if c.Level != 2 {
			continue
		}
		switch c.Type {
		case domain.CategoryTypeIncome:
			incomes = append(incomes, c)
		case domain.CategoryTypeExpense:
			expenses = append(expenses, c)
		}
	}
	return txNewVM{
		pageBase:          pageBase{Title: "录入流水", Nav: "transactions"},
		IncomeCategories:  incomes,
		ExpenseCategories: expenses,
		Form:              form,
		Error:             errMsg,
	}, nil
}

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	h.log.Error("server error", "err", err)
	http.Error(w, "服务器错误: "+err.Error(), http.StatusInternalServerError)
}
