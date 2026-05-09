package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
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
	txRepo     port.TransactionRepo
	catRepo    port.CategoryRepo
	log        *slog.Logger
	flash      *flashStore
}

func New(r *web.Renderer, importBill *usecase.ImportBill, qr *usecase.QueryReport,
	txRepo port.TransactionRepo, catRepo port.CategoryRepo, log *slog.Logger) *Handler {
	return &Handler{
		render:     r,
		importBill: importBill,
		queryRep:   qr,
		txRepo:     txRepo,
		catRepo:    catRepo,
		log:        log,
		flash:      newFlashStore(),
	}
}

type pageBase struct {
	Title         string
	Nav           string
	Period        domain.Period
	PeriodOptions []string
	Flash         string
}

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

type txRowJSON struct {
	ID           string `json:"id"`
	OccurredAt   string `json:"occurred_at"`
	Source       string `json:"source"`
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

	txJSON := make([]txRowJSON, 0, len(txs))
	for _, t := range txs {
		txJSON = append(txJSON, txRowJSON{
			ID:           t.ID,
			OccurredAt:   t.OccurredAt.Format("2006-01-02"),
			Source:       string(t.Source),
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
			Title:         "收支流水",
			Nav:           "transactions",
			Period:        p,
			PeriodOptions: periodOptions(p),
			Flash:         h.flash.pop(w, r),
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

// ----- Update single transaction (PATCH via form) -----

type updateTxReq struct {
	CategoryID *string `json:"category_id"`
	Note       *string `json:"note"`
	Status     *string `json:"status"`
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

	file, header, err := r.FormFile("file")
	if err != nil {
		h.renderImportError(w, "请选择要导入的账单文件")
		return
	}
	defer file.Close()

	res, err := h.importBill.Execute(r.Context(), usecase.ImportBillInput{
		Source:   src,
		Filename: header.Filename,
		Reader:   file,
	})
	if err != nil {
		h.log.Error("import", "err", err)
		h.renderImportError(w, "导入失败："+err.Error())
		return
	}

	msg := "导入完成：新增 " +
		strconv.Itoa(res.InsertedRows) + " 条，跳过重复 " +
		strconv.Itoa(res.SkippedDuplicates) + " 条，忽略转账/无效 " +
		strconv.Itoa(res.SkippedInvalid) + " 条，未分类待处理 " +
		strconv.Itoa(res.PendingCategory) + " 条。"
	h.flash.set(w, msg)
	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
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
