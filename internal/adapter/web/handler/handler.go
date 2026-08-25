package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"family-finances/internal/adapter/web"
	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

func newID() string { return uuid.NewString() }

type Handler struct {
	render       *web.Renderer
	importBill   *usecase.ImportBill
	queryRep     *usecase.QueryReport
	queryStats   *usecase.QueryStats
	assetSvc     *usecase.AssetSnapshotService
	genReport    *usecase.GenerateReport
	genAdvice    *usecase.GenerateAdvice
	bucketEng    *usecase.BucketEngine
	exportUC     *usecase.Export
	txRepo       port.TransactionRepo
	catRepo      port.CategoryRepo
	ruleRepo     port.CategoryRuleRepo
	reportRepo   port.ReportRepo
	profileRepo  port.FamilyProfileRepo
	goalRepo     port.FinancialGoalRepo
	goalView     *usecase.GoalView
	policyRepo   port.InsurancePolicyRepo
	insView      *usecase.InsuranceView
	budgetRepo   port.BudgetRepo
	budgetView   *usecase.BudgetView
	ask          *usecase.Ask
	digestRepo   port.DigestRepo
	digestSvc    *usecase.DigestService
	digestSender usecase.DigestSender
	templateRepo port.ImportTemplateRepo
	specialRepo  port.SpecialProjectRepo
	specialView  *usecase.SpecialView
	log          *slog.Logger
	flash        *flashStore
	auth         *authManager
}

// Deps 聚合 Handler 依赖，避免构造函数参数无限增长
type Deps struct {
	Render       *web.Renderer
	ImportBill   *usecase.ImportBill
	QueryReport  *usecase.QueryReport
	QueryStats   *usecase.QueryStats
	AssetSvc     *usecase.AssetSnapshotService
	GenReport    *usecase.GenerateReport
	GenAdvice    *usecase.GenerateAdvice
	BucketEng    *usecase.BucketEngine
	Export       *usecase.Export
	TxRepo       port.TransactionRepo
	CatRepo      port.CategoryRepo
	RuleRepo     port.CategoryRuleRepo
	ReportRepo   port.ReportRepo
	ProfileRepo  port.FamilyProfileRepo
	GoalRepo     port.FinancialGoalRepo
	GoalView     *usecase.GoalView
	PolicyRepo   port.InsurancePolicyRepo
	InsView      *usecase.InsuranceView
	BudgetRepo   port.BudgetRepo
	BudgetView   *usecase.BudgetView
	Ask          *usecase.Ask
	DigestRepo   port.DigestRepo
	DigestSvc    *usecase.DigestService
	DigestSender usecase.DigestSender
	TemplateRepo port.ImportTemplateRepo
	SpecialRepo  port.SpecialProjectRepo
	SpecialView  *usecase.SpecialView
	Log          *slog.Logger
	AuthKey      string
}

func New(d Deps) *Handler {
	return &Handler{
		render:       d.Render,
		importBill:   d.ImportBill,
		queryRep:     d.QueryReport,
		queryStats:   d.QueryStats,
		assetSvc:     d.AssetSvc,
		genReport:    d.GenReport,
		genAdvice:    d.GenAdvice,
		bucketEng:    d.BucketEng,
		exportUC:     d.Export,
		txRepo:       d.TxRepo,
		catRepo:      d.CatRepo,
		ruleRepo:     d.RuleRepo,
		reportRepo:   d.ReportRepo,
		profileRepo:  d.ProfileRepo,
		goalRepo:     d.GoalRepo,
		goalView:     d.GoalView,
		policyRepo:   d.PolicyRepo,
		insView:      d.InsView,
		budgetRepo:   d.BudgetRepo,
		budgetView:   d.BudgetView,
		ask:          d.Ask,
		digestRepo:   d.DigestRepo,
		digestSvc:    d.DigestSvc,
		digestSender: d.DigestSender,
		templateRepo: d.TemplateRepo,
		specialRepo:  d.SpecialRepo,
		specialView:  d.SpecialView,
		log:          d.Log,
		flash:        newFlashStore(),
		auth:         newAuthManager(d.AuthKey),
	}
}

type pageBase struct {
	Title   string
	Nav     string
	Period  domain.Period
	Flash   string
	Account domain.Account
}

// renderPage 渲染整页 HTML。显式设置 Content-Type（且在 WriteHeader 前），
// 否则 gzip 中间件在 WriteHeader 时看不到类型、不会压缩 HTML。
func (h *Handler) renderPage(w http.ResponseWriter, status int, page string, vm any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := h.render.RenderPage(w, page, vm); err != nil {
		h.serverError(w, err)
	}
}

// renderPartial 渲染 HTMX 片段，同样先设置 Content-Type
func (h *Handler) renderPartial(w http.ResponseWriter, name string, vm any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.render.RenderPartial(w, name, vm); err != nil {
		h.serverError(w, err)
	}
}

// parsePeriodFromQuery 从 querystring 解析 type/period。
// URL 未显式指定（或指定的 period 跟 type 对不上）时，退回 defaultType 粒度下的默认周期——
// 调用方按自己页面的粒度传入 defaultType，几个页面各自默认值不同，不能共用一个全局默认。
func parsePeriodFromQuery(r *http.Request, defaultType domain.PeriodType) (domain.Period, error) {
	typeStr := r.URL.Query().Get("type")
	if typeStr == "" {
		typeStr = string(defaultType)
	}
	label := r.URL.Query().Get("period")
	// 若 label 与 type 不匹配（例如切换季→年时旧 label 还是 2026Q1），退回到 type 的默认值
	labelMatchesType := label != "" &&
		((typeStr == string(domain.PeriodQuarterly) && strings.Contains(label, "Q")) ||
			(typeStr == string(domain.PeriodAnnual) && !strings.Contains(label, "Q") && !strings.Contains(label, "-")) ||
			(typeStr == string(domain.PeriodMonthly) && strings.Contains(label, "-")))
	if !labelMatchesType {
		label = defaultPeriodFor(domain.PeriodType(typeStr), time.Now()).Label
	}
	return domain.ParsePeriod(label)
}

// defaultPeriodFor 返回给定粒度「上一个完整周期」的 Period，作为默认周期兜底：
// 当期还没走完，数字有误导性（环比/同比都会失真），所以默认值统一取上一个完整周期，
// 而不是当期——三个页面、StatsAPI 都靠这一个函数对齐口径，改阈值/规则只用改这一处。
func defaultPeriodFor(t domain.PeriodType, now time.Time) domain.Period {
	switch t {
	case domain.PeriodAnnual:
		return domain.CurrentYear(now).Previous()
	case domain.PeriodMonthly:
		return domain.CurrentMonth(now).Previous()
	default: // domain.PeriodQuarterly，以及任何非法值一律按季度处理（与原逻辑一致）
		return domain.CurrentQuarter(now).Previous()
	}
}

// periodTypeFromGranularityAlias 把 StatsAPI 用的短别名（month/quarter/year）转成
// domain.PeriodType，好复用 defaultPeriodFor——两处「粒度」词表不同纯粹是历史遗留，
// 这里只做一次翻译，不改 StatsAPI 对外的 querystring 约定。
func periodTypeFromGranularityAlias(gran string) domain.PeriodType {
	switch gran {
	case "month":
		return domain.PeriodMonthly
	case "year":
		return domain.PeriodAnnual
	default: // "quarter" 以及任何非法值
		return domain.PeriodQuarterly
	}
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
	p, err := parsePeriodFromQuery(r, domain.PeriodQuarterly)
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
		pageBase: pageBase{Title: "现金流表", Nav: "dashboard", Period: p, Account: acc},
		Report:   rep,
	}
	h.renderPage(w, http.StatusOK, "dashboard", vm)
}

func (h *Handler) PartialReport(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r, domain.PeriodQuarterly)
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
	h.renderPartial(w, "report_view", vm)
}

// ----- Stats -----

type statsVM struct {
	pageBase
	// 再平衡告警：最新快照的长期桶配置偏出引擎区间时非空（查询时现算，无新表）
	Rebalance       []usecase.AllocationBand
	RebalancePeriod string
}

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	vm := statsVM{pageBase: pageBase{Title: "仪表盘", Nav: "stats"}}
	if bands, period, err := h.rebalanceAlerts(r.Context()); err != nil {
		// 再平衡是增值信息，失败不阻塞仪表盘
		h.log.Warn("rebalance alerts", "err", err)
	} else {
		vm.Rebalance = bands
		vm.RebalancePeriod = period
	}
	h.renderPage(w, http.StatusOK, "stats", vm)
}

// rebalanceAlerts 现算最新快照 vs 配置区间，返回偏出区间的条目
func (h *Handler) rebalanceAlerts(ctx context.Context) ([]usecase.AllocationBand, string, error) {
	in, err := h.bucketEng.LoadInputs(ctx)
	if err != nil {
		return nil, "", err
	}
	if in.Snapshot == nil || in.Profile == nil {
		// 无快照或未填画像时不打扰（区间无从对照/等级只是缺省值）
		return nil, "", nil
	}
	alloc := usecase.ComputeAllocation(*in.Profile, true, in.Snapshot.Data)
	return alloc.OutOfBand(), in.Snapshot.Period, nil
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
		// gran 用的是前端短别名（month/quarter/year），跟 parsePeriodFromQuery 的
		// type（monthly/quarterly/annual）不是一套词表，这里转一下再复用同一份
		// defaultPeriodFor，避免两处各写一份「默认取哪期」的逻辑。
		label = defaultPeriodFor(periodTypeFromGranularityAlias(gran), time.Now()).Label
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
	// 缺省 daily：默认视图必须是干净的，一次装修就能把全局同比拉到 +368%
	scope := domain.ParseScope(q.Get("scope"))

	view, err := h.queryStats.Execute(r.Context(), p, dir, acc, scope, 10)
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

	// 与 QueryStats 的 Top 榜单同口径：恒为全口径，剔了大额就不叫大额榜单了
	tops, err := h.txRepo.TopTransactions(r.Context(), p, dir, acc, domain.ScopeAll, limit)
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
			Special:  t.SpecialName,
		})
	}
	writeJSON(w, out)
}

// ----- Category rules -----

type rulesVM struct {
	pageBase
	Rules            []domain.CategoryRule
	Categories       []domain.Category
	FilterCategoryID string
	TotalRules       int
	Error            string
}

func (h *Handler) Rules(w http.ResponseWriter, r *http.Request) {
	h.renderRules(w, r, "")
}

func (h *Handler) CreateRule(w http.ResponseWriter, r *http.Request) {
	rule, err := h.ruleFromForm(r)
	if err != nil {
		h.renderRulesWithStatus(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	rule.ID = newID()
	rule.Source = "user"
	rule.IsActive = true
	rule.CreatedAt = time.Now()
	if err := h.ruleRepo.InsertRule(r.Context(), rule); err != nil {
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "规则已新增。")
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (h *Handler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	rule, err := h.ruleFromForm(r)
	if err != nil {
		h.renderRulesWithStatus(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	rule.ID = id
	if err := h.ruleRepo.UpdateRule(r.Context(), rule); err != nil {
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "规则已保存。")
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (h *Handler) ToggleRule(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	active := r.FormValue("active") == "1"
	if err := h.ruleRepo.SetRuleActive(r.Context(), id, active); err != nil {
		h.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (h *Handler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	if err := h.ruleRepo.DeleteRule(r.Context(), id); err != nil {
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "规则已删除。")
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (h *Handler) renderRules(w http.ResponseWriter, r *http.Request, msg string) {
	h.renderRulesWithStatus(w, r, msg, http.StatusOK)
}

func (h *Handler) renderRulesWithStatus(w http.ResponseWriter, r *http.Request, msg string, status int) {
	rules, err := h.ruleRepo.ListRules(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	totalRules := len(rules)
	cats, err := h.catRepo.ListAll(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	filterCategoryID := strings.TrimSpace(r.URL.Query().Get("category_id"))
	if filterCategoryID != "" {
		rules = filterRulesByCategory(rules, filterCategoryID)
	}
	vm := rulesVM{
		pageBase:         pageBase{Title: "分类规则", Nav: "rules", Flash: h.flash.pop(w, r)},
		Rules:            rules,
		Categories:       cats,
		FilterCategoryID: filterCategoryID,
		TotalRules:       totalRules,
		Error:            msg,
	}
	h.renderPage(w, status, "rules", vm)
}

func filterRulesByCategory(rules []domain.CategoryRule, categoryID string) []domain.CategoryRule {
	out := make([]domain.CategoryRule, 0, len(rules))
	for _, rule := range rules {
		if categoryID == "__skip__" && rule.CategoryID == "" {
			out = append(out, rule)
			continue
		}
		if rule.CategoryID == categoryID {
			out = append(out, rule)
		}
	}
	return out
}

func (h *Handler) ruleFromForm(r *http.Request) (domain.CategoryRule, error) {
	if err := r.ParseForm(); err != nil {
		return domain.CategoryRule{}, fmt.Errorf("表单解析失败")
	}
	pattern := strings.TrimSpace(r.FormValue("pattern"))
	if pattern == "" {
		return domain.CategoryRule{}, fmt.Errorf("请输入匹配内容")
	}
	patternType := r.FormValue("pattern_type")
	if patternType != "exact" {
		patternType = "contains"
	}
	field := r.FormValue("field")
	switch field {
	case "counterparty", "description", "platform_category":
	default:
		field = "any"
	}
	categoryID := r.FormValue("category_id")
	if categoryID != "" {
		if err := h.ensureLeafCategory(r.Context(), categoryID); err != nil {
			return domain.CategoryRule{}, err
		}
	}
	priority := 10
	if raw := strings.TrimSpace(r.FormValue("priority")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return domain.CategoryRule{}, fmt.Errorf("优先级必须是整数")
		}
		priority = v
	}
	return domain.CategoryRule{
		Pattern:     pattern,
		PatternType: patternType,
		Field:       field,
		CategoryID:  categoryID,
		Priority:    priority,
	}, nil
}

func (h *Handler) ensureLeafCategory(ctx context.Context, id string) error {
	cats, err := h.catRepo.ListAll(ctx)
	if err != nil {
		return err
	}
	for _, c := range cats {
		if c.ID == id && c.Level == 2 {
			return nil
		}
	}
	return fmt.Errorf("请选择有效的二级分类")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ----- Transactions list -----

type txRowJSON struct {
	ID               string `json:"id"`
	OccurredAt       string `json:"occurred_at"`
	Source           string `json:"source"`
	Account          string `json:"account"`
	Member           string `json:"member"`
	Counterparty     string `json:"counterparty"`
	Description      string `json:"description"`
	PlatformCategory string `json:"platform_category"`
	Note             string `json:"note"`
	AmountFen        int64  `json:"amount_fen"`
	Direction        string `json:"direction"`
	Status           string `json:"status"`
	CategoryID       string `json:"category_id"`
	SpecialID        string `json:"special_id"`
	RawRow           string `json:"raw_row"`
}

// specialJSON 流水页专项下拉用
type specialJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

func txRowJSONFrom(t domain.Transaction) txRowJSON {
	return txRowJSON{
		ID:               t.ID,
		OccurredAt:       t.OccurredAt.Format("2006-01-02"),
		Source:           string(t.Source),
		Account:          string(t.Account),
		Member:           t.Member,
		Counterparty:     t.Counterparty,
		Description:      t.Description,
		PlatformCategory: t.PlatformCategory,
		Note:             t.Note,
		AmountFen:        t.Amount,
		Direction:        string(t.Direction),
		Status:           string(t.Status),
		CategoryID:       t.CategoryID,
		SpecialID:        t.SpecialID,
		RawRow:           t.RawRow,
	}
}

type catJSON struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent_id"`
	Name      string `json:"name"`
	GroupName string `json:"group_name"`
	Type      string `json:"type"`
	Level     int    `json:"level"`
}

type ruleJSON struct {
	ID           string `json:"id"`
	Pattern      string `json:"pattern"`
	PatternType  string `json:"pattern_type"`
	Field        string `json:"field"`
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
}

type txListVM struct {
	pageBase
	Transactions []domain.Transaction
	Categories   []domain.Category

	// 给 Alpine 用的 JSON 内联数据
	TransactionsJSON string
	CategoriesJSON   string
	RuleJSON         string
	SpecialsJSON     string
}

func ruleJSONFromDomain(rule domain.CategoryRule, cats []domain.Category) ruleJSON {
	name := rule.CategoryID
	if name == "" {
		name = "跳过导入"
	}
	for _, c := range cats {
		if c.ID == rule.CategoryID {
			name = c.Name
			break
		}
	}
	return ruleJSON{
		ID:           rule.ID,
		Pattern:      rule.Pattern,
		PatternType:  rule.PatternType,
		Field:        rule.Field,
		CategoryID:   rule.CategoryID,
		CategoryName: name,
	}
}

func (h *Handler) ListTransactions(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r, domain.PeriodMonthly)
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
		txJSON = append(txJSON, txRowJSONFrom(t))
	}

	specials, err := h.listSpecialsJSON(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
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
	specialBytes, _ := json.Marshal(specials)
	ruleBytes := []byte("null")
	if ruleID := strings.TrimSpace(r.URL.Query().Get("rule_id")); ruleID != "" {
		rule, err := h.ruleRepo.GetRule(r.Context(), ruleID)
		if err != nil {
			http.Error(w, "规则不存在: "+err.Error(), http.StatusNotFound)
			return
		}
		ruleBytes, _ = json.Marshal(ruleJSONFromDomain(rule, cats))
	}

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
		RuleJSON:         string(ruleBytes),
		SpecialsJSON:     string(specialBytes),
	}
	h.renderPage(w, http.StatusOK, "transactions", vm)
}

// listSpecialsJSON 专项下拉选项（进行中的排前面，由 repo 的排序保证）
func (h *Handler) listSpecialsJSON(ctx context.Context) ([]specialJSON, error) {
	if h.specialRepo == nil {
		return []specialJSON{}, nil
	}
	projects, err := h.specialRepo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]specialJSON, 0, len(projects))
	for _, p := range projects {
		out = append(out, specialJSON{ID: p.ID, Name: p.Name, Active: p.IsActive()})
	}
	return out, nil
}

// ListTransactionsAPI: GET /api/transactions?type=...&period=...&account=...
// 返回与列表页 SSR 嵌入 JSON 相同的 txRowJSON 数组，供前端切换周期时客户端刷新。
func (h *Handler) ListTransactionsAPI(w http.ResponseWriter, r *http.Request) {
	p, err := parsePeriodFromQuery(r, domain.PeriodMonthly)
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
		out = append(out, txRowJSONFrom(t))
	}
	writeJSON(w, map[string]any{"transactions": out})
}

// ----- Update single transaction (PATCH via form) -----

type updateTxReq struct {
	CategoryID *string `json:"category_id"`
	Note       *string `json:"note"`
	Status     *string `json:"status"`
	Account    *string `json:"account"`
	Member     *string `json:"member"`
	SpecialID  *string `json:"special_id"` // 空字符串 = 归回日常
}

// ensureSpecial 校验专项存在；空串（清空）直接放行
func (h *Handler) ensureSpecial(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if h.specialRepo == nil {
		return fmt.Errorf("专项功能未启用")
	}
	if _, err := h.specialRepo.Get(ctx, id); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return fmt.Errorf("专项不存在")
		}
		return err
	}
	return nil
}

func (h *Handler) UpdateTransaction(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req updateTxReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	patch := port.TransactionUpdate{}
	if req.CategoryID != nil {
		v := *req.CategoryID
		if v != "" {
			if err := h.ensureLeafCategory(r.Context(), v); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
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
		switch s {
		case domain.TxStatusPendingReview, domain.TxStatusConfirmed, domain.TxStatusExcluded:
		default:
			http.Error(w, "invalid status", http.StatusBadRequest)
			return
		}
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
	if req.Member != nil {
		m := strings.TrimSpace(*req.Member)
		if len([]rune(m)) > 20 {
			http.Error(w, "成员标注过长（限 20 字）", http.StatusBadRequest)
			return
		}
		patch.Member = &m
	}
	if req.SpecialID != nil {
		v := strings.TrimSpace(*req.SpecialID)
		if err := h.ensureSpecial(r.Context(), v); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		patch.SpecialID = &v
	}
	if err := h.txRepo.Update(r.Context(), id, patch); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			http.Error(w, "流水不存在", http.StatusNotFound)
			return
		}
		h.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// batchUpdateTxReq PATCH /api/transactions/batch 的请求体
type batchUpdateTxReq struct {
	IDs       []string `json:"ids"`
	SpecialID *string  `json:"special_id"` // 空字符串 = 批量归回日常
}

// maxBatchTxIDs 一次批量操作的上限。装修一次上百笔，给足余量但不放任无界请求。
const maxBatchTxIDs = 1000

// BatchUpdateTransactions PATCH /api/transactions/batch —— 批量归入专项。
// 装修一次几十上百笔，逐条 PATCH 不可用。
func (h *Handler) BatchUpdateTransactions(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var req batchUpdateTxReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.IDs) == 0 {
		http.Error(w, "请选择要归类的流水", http.StatusBadRequest)
		return
	}
	if len(req.IDs) > maxBatchTxIDs {
		http.Error(w, fmt.Sprintf("一次最多处理 %d 条流水", maxBatchTxIDs), http.StatusBadRequest)
		return
	}
	if req.SpecialID == nil {
		http.Error(w, "缺少 special_id", http.StatusBadRequest)
		return
	}
	specialID := strings.TrimSpace(*req.SpecialID)
	if err := h.ensureSpecial(r.Context(), specialID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	patch := port.TransactionUpdate{SpecialID: &specialID}
	updated := 0
	for _, id := range req.IDs {
		if id == "" {
			continue
		}
		if err := h.txRepo.Update(r.Context(), id, patch); err != nil {
			// 单条不存在不算整体失败（前端列表可能已过期），继续处理剩下的
			if errors.Is(err, port.ErrNotFound) {
				continue
			}
			h.serverError(w, err)
			return
		}
		updated++
	}
	writeJSON(w, map[string]any{"updated": updated})
}

// ----- Import bill -----

type importVM struct {
	pageBase
	Error      string
	Categories []domain.Category
	Members    []string
}

func (h *Handler) ImportForm(w http.ResponseWriter, r *http.Request) {
	cats, err := h.catRepo.ListAll(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	members, err := h.txRepo.ListMembers(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := importVM{pageBase: pageBase{Title: "导入账单", Nav: "imports"}, Categories: cats, Members: members}
	h.renderPage(w, http.StatusOK, "imports", vm)
}

func (h *Handler) ImportSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, "上传内容无效或超过 32MB 限制", http.StatusBadRequest)
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
		h.renderImportError(w, r, "请选择账单来源")
		return
	}

	accStr := r.FormValue("account")
	acc := domain.Account(accStr)
	if !acc.IsStorageAccount() {
		h.renderImportError(w, r, "请选择账户归属（男主/女主）")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		h.renderImportError(w, r, "请选择要导入的账单文件")
		return
	}
	defer file.Close()

	res, err := h.importBill.Execute(r.Context(), usecase.ImportBillInput{
		Source:   src,
		Account:  acc,
		Member:   strings.TrimSpace(r.FormValue("member")),
		Filename: header.Filename,
		Reader:   file,
	})
	if err != nil {
		h.log.Error("import", "err", err)
		h.renderImportError(w, r, "导入失败："+err.Error())
		return
	}

	msg := fmt.Sprintf("导入完成（%s）：新增 %d 条，跳过重复 %d 条，忽略转账/无效 %d 条，未分类待处理 %d 条。",
		acc.Label(), res.InsertedRows, res.SkippedDuplicates, res.SkippedInvalid, res.PendingCategory)
	h.flash.set(w, msg)
	http.Redirect(w, r, importRedirectURL(acc, res), http.StatusSeeOther)
}

func importRedirectURL(acc domain.Account, res port.ImportResult) string {
	return transactionsRedirectURL(acc, res.EarliestOccurredAt)
}

func transactionsRedirectURL(acc domain.Account, occurredAt time.Time) string {
	q := url.Values{}
	q.Set("account", string(acc))
	if !occurredAt.IsZero() {
		q.Set("type", string(domain.PeriodMonthly))
		q.Set("period", occurredAt.Format("2006-01"))
	}
	return "/transactions?" + q.Encode()
}

func (h *Handler) renderImportError(w http.ResponseWriter, r *http.Request, msg string) {
	cats, _ := h.catRepo.ListAll(r.Context())
	members, _ := h.txRepo.ListMembers(r.Context())
	vm := importVM{pageBase: pageBase{Title: "导入账单", Nav: "imports"}, Error: msg, Categories: cats, Members: members}
	h.renderPage(w, http.StatusBadRequest, "imports", vm)
}

func (h *Handler) ManualEntrySubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderImportError(w, r, "表单解析失败")
		return
	}

	occurredStr := r.FormValue("occurred_at")
	occurredAt, err := time.ParseInLocation("2006-01-02T15:04", occurredStr, time.Local)
	if err != nil {
		h.renderImportError(w, r, "日期时间格式不正确")
		return
	}

	acc := domain.Account(r.FormValue("account"))
	if !acc.IsStorageAccount() {
		h.renderImportError(w, r, "请选择账户归属（男主/女主）")
		return
	}

	dir := domain.Direction(r.FormValue("direction"))
	if dir != domain.DirectionIncome && dir != domain.DirectionExpense {
		h.renderImportError(w, r, "请选择收支方向")
		return
	}

	amountStr := r.FormValue("amount")
	amountFen, amountErr := parseAmountToFen(amountStr)
	if amountErr != nil {
		h.renderImportError(w, r, amountErr.Error())
		return
	}

	categoryID := r.FormValue("category_id")
	if categoryID != "" {
		if err := h.ensureLeafCategory(r.Context(), categoryID); err != nil {
			h.renderImportError(w, r, err.Error())
			return
		}
	}

	now := time.Now()
	tx := domain.Transaction{
		ID:           newID(),
		Source:       domain.SourceManual,
		Account:      acc,
		Member:       strings.TrimSpace(r.FormValue("member")),
		OccurredAt:   occurredAt,
		Counterparty: strings.TrimSpace(r.FormValue("counterparty")),
		Description:  strings.TrimSpace(r.FormValue("description")),
		Note:         strings.TrimSpace(r.FormValue("note")),
		Amount:       amountFen,
		Direction:    dir,
		Status:       domain.TxStatusConfirmed,
		CategoryID:   categoryID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.txRepo.Insert(r.Context(), tx); err != nil {
		h.log.Error("manual entry insert", "err", err)
		h.renderImportError(w, r, "写入失败："+err.Error())
		return
	}

	h.flash.set(w, fmt.Sprintf("手工录入成功（%s）：%s ¥%s", acc.Label(), string(dir), amountStr))
	http.Redirect(w, r, transactionsRedirectURL(acc, occurredAt), http.StatusSeeOther)
}

// parseAmountToFen 把用户输入的元金额转成分。
// ParseFloat 会接受 "NaN"/"Inf"/超大科学计数法，必须显式拒绝，否则会写入垃圾金额。
func parseAmountToFen(s string) (int64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("金额格式不正确")
	}
	if f <= 0 {
		return 0, fmt.Errorf("金额必须为正数")
	}
	const maxYuan = 1e10 // 单笔上限 100 亿元，防 int64 溢出
	if f > maxYuan {
		return 0, fmt.Errorf("金额超出可接受范围")
	}
	return int64(math.Round(f * 100)), nil
}

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	// 细节只进日志，避免把 SQL / 文件路径等内部信息回给客户端
	h.log.Error("server error", "err", err)
	http.Error(w, "服务器内部错误，请稍后重试", http.StatusInternalServerError)
}
