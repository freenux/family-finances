package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	specialView  *usecase.SpecialView
	nav          usecase.PeriodNav
	txQuery      txLister
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
	SpecialView  *usecase.SpecialView
	Nav          usecase.PeriodNav // 零值可用（系统时钟）
	TxQuery      txLister          // 流水列表用例（SSR 首屏与 /api/v1 共用）
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
		specialView:  d.SpecialView,
		nav:          d.Nav,
		txQuery:      d.TxQuery,
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

// parsePeriodFromQuery 从 querystring 解析 type/period，纯委托给 usecase.PeriodNav.Resolve
// （周期规则的唯一来源，含「type 与 period 对不上就退回该 type 的默认周期」）。
// 调用方按自己页面的粒度传入 defaultType，几个页面各自默认值不同，不能共用一个全局默认。
func (h *Handler) parsePeriodFromQuery(r *http.Request, defaultType domain.PeriodType) (domain.Period, error) {
	return periodFromQuery(h.nav, r, defaultType)
}

func periodFromQuery(nav usecase.PeriodNav, r *http.Request, defaultType domain.PeriodType) (domain.Period, error) {
	q := r.URL.Query()
	return nav.Resolve(q.Get("type"), q.Get("period"), defaultType)
}

// parsePeriodFromQuery / defaultPeriodFor / txListPeriod 是包级薄壳（用系统时钟的零值 PeriodNav），
// 供不持有 Handler 的调用方与既有测试使用；生产路径走 Handler 方法，用注入的 nav。
// 三者都是纯委托，不含任何自己的规则判断。
func parsePeriodFromQuery(r *http.Request, defaultType domain.PeriodType) (domain.Period, error) {
	return periodFromQuery(usecase.PeriodNav{}, r, defaultType)
}

// defaultPeriodFor 给定时刻 now 下的默认周期，规则全在 usecase.PeriodNav.Default。
func defaultPeriodFor(t domain.PeriodType, now time.Time) domain.Period {
	return usecase.PeriodNav{Now: func() time.Time { return now }}.Default(t)
}

// periodTypeFromGranularityAlias 把 StatsAPI 用的短别名（month/quarter/year）转成
// domain.PeriodType。这是 /api/stats 对外 querystring 的词表翻译（与 PeriodNav 的
// monthly/quarterly/annual 不是一套），属于 HTTP 层，所以留在这里；默认周期仍交给 PeriodNav。
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
	return domain.ParseAccount(r.URL.Query().Get("account"))
}

// ----- Dashboard -----

type dashboardVM struct {
	pageBase
	Report domain.ReportData
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	p, err := h.parsePeriodFromQuery(r, domain.PeriodQuarterly)
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
	p, err := h.parsePeriodFromQuery(r, domain.PeriodQuarterly)
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
		// type（monthly/quarterly/annual）不是一套词表，这里转一下再交给 PeriodNav，
		// 避免两处各写一份「默认取哪期」的逻辑。
		label = h.nav.Default(periodTypeFromGranularityAlias(gran)).Label
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

// StatsTopAPI: GET /api/stats/top?granularity=&period=&direction=&account=&scope=&limit=
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
	// 缺省 daily，与 /api/stats 一致：用户选了"日常"，Top 榜单就不该出现装修流水
	scope := domain.ParseScope(q.Get("scope"))
	limit := 10
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}

	// 与 QueryStats 的 Top 榜单同口径：跟随请求的 scope
	tops, err := h.txRepo.TopTransactions(r.Context(), p, dir, acc, scope, limit)
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

// txLister 流水列表用例（*usecase.TxQuery 满足）。SSR 首屏与 /api/v1/transactions 调的是同一个，
// DTO 形状只有一份。
type txLister interface {
	Execute(ctx context.Context, req usecase.TxQueryRequest) (usecase.TxQueryResult, error)
}

type txListVM struct {
	pageBase
	Result usecase.TxQueryResult
	// ResultJSON 整个 TxQueryResult 序列化后的 JSON，模板用 {{rawJSON}} 嵌进页面给 Alpine 首屏 hydrate
	ResultJSON string
	// MetaJSON usecase.MetaView 序列化后的 JSON（下拉选项），同样用 {{rawJSON}} 嵌入
	MetaJSON string
}

// txListPeriod 解析流水页的周期：纯委托 usecase.PeriodNav.ResolveForList
// （默认「上个月」；带 ?rule_id= 且 URL 里既没有 type 也没有 period 时用当前季度，原因见 ForRuleView）。
// 现在仅供测试钉住这条规则；ListTransactions 把同样的 type/period/rule_id 交给 TxQuery，由它内部走 ResolveForList。
func (h *Handler) txListPeriod(r *http.Request) (domain.Period, error) {
	return txListPeriodWith(h.nav, r)
}

func txListPeriodWith(nav usecase.PeriodNav, r *http.Request) (domain.Period, error) {
	q := r.URL.Query()
	return nav.ResolveForList(q.Get("type"), q.Get("period"), q.Get("rule_id"), usecase.TxListDefaultType)
}

// txListPeriod 包级薄壳，同 parsePeriodFromQuery。
func txListPeriod(r *http.Request) (domain.Period, error) {
	return txListPeriodWith(usecase.PeriodNav{}, r)
}

// ListTransactions 流水页 SSR：调 usecase.TxQuery 取首屏（筛选/排序/分页/合计都在用例里），
// 整体序列化嵌进页面。不再自己拼行 DTO，也不再把整期流水全塞给前端。
func (h *Handler) ListTransactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.txQuery.Execute(r.Context(), usecase.TxQueryRequest{
		Type: q.Get("type"), Period: q.Get("period"), Account: q.Get("account"),
		Direction: q.Get("direction"), Source: q.Get("source"), Status: q.Get("status"),
		Category: q.Get("category"), Special: q.Get("special"),
		Member: q.Get("member"), Keyword: q.Get("keyword"), RuleID: q.Get("rule_id"),
		Sort: q.Get("sort"), Order: q.Get("order"),
		Page: q.Get("page"), PageSize: q.Get("page_size"),
	})
	switch {
	case errors.Is(err, usecase.ErrInvalidPeriod):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, port.ErrNotFound):
		http.Error(w, "规则不存在", http.StatusNotFound)
		return
	case err != nil:
		h.serverError(w, err)
		return
	}
	p, _ := domain.ParsePeriod(res.Period.Key) // key 来自 PeriodNav，必然可解析
	resBytes, err := json.Marshal(res)
	if err != nil {
		h.serverError(w, err)
		return
	}
	// 下拉选项首屏就绪：与 /api/v1/meta 同一份 usecase.Meta，页面不必再多发一次请求
	meta, err := h.metaView(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.renderPage(w, http.StatusOK, "transactions", txListVM{
		pageBase: pageBase{
			Title:   "收支流水",
			Nav:     "transactions",
			Period:  p,
			Flash:   h.flash.pop(w, r),
			Account: parseAccountFromQuery(r),
		},
		Result:     res,
		ResultJSON: string(resBytes),
		MetaJSON:   string(metaBytes),
	})
}

// metaView 装配 usecase.Meta。专项功能未启用（specialView 为 nil）时必须传真 nil 接口。
func (h *Handler) metaView(ctx context.Context) (usecase.MetaView, error) {
	var specials usecase.MetaSpecialLister
	if h.specialsEnabled() {
		specials = h.specialView
	}
	return usecase.NewMeta(h.catRepo, specials, h.nav).Execute(ctx)
}

// specialsEnabled 专项功能是否可用。main.go 里无条件注入，只有裁剪过依赖的
// 测试/嵌入场景会为 nil——这时整块专项功能降级而不是 panic。
func (h *Handler) specialsEnabled() bool { return h.specialView != nil }

// ListTransactionsAPI: GET /api/transactions?type=...&period=...&account=...
// 返回与列表页 SSR 嵌入 JSON 相同的 txRowJSON 数组，供前端切换周期时客户端刷新。
func (h *Handler) ListTransactionsAPI(w http.ResponseWriter, r *http.Request) {
	p, err := h.txListPeriod(r)
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

// txUpdater 流水编辑用例。按需装配（Handler 在测试里是字面量构造的）。
// specialView 为 nil（裁剪过依赖）时必须传真 nil 接口，不能传带类型的 nil 指针。
func (h *Handler) txUpdater() *usecase.UpdateTransaction {
	var specials usecase.SpecialEnsurer
	if h.specialsEnabled() {
		specials = h.specialView
	}
	return usecase.NewUpdateTransaction(h.txRepo, h.catRepo, specials)
}

// writeTxUpdateError 校验失败 400 + usecase 给的中文；流水不存在 404；其余 500 且不回细节
func (h *Handler) writeTxUpdateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, usecase.ErrInvalidInput):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, port.ErrNotFound):
		http.Error(w, "流水不存在", http.StatusNotFound)
	default:
		h.serverError(w, err)
	}
}

// UpdateTransaction PATCH /api/transactions/{id}。校验与装配都在 usecase.UpdateTransaction，
// 这里只解析 body 并回 204。
func (h *Handler) UpdateTransaction(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req updateTxReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	err := h.txUpdater().Update(r.Context(), chiURLParam(r, "id"), usecase.TxPatch(req))
	if err != nil {
		h.writeTxUpdateError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// batchUpdateTxReq PATCH /api/transactions/batch 的请求体
type batchUpdateTxReq struct {
	IDs       []string `json:"ids"`
	SpecialID *string  `json:"special_id"` // 空字符串 = 批量归回日常
}

// maxBatchTxIDs 批量上限，常量本体在 usecase（两个适配器共用）
const maxBatchTxIDs = usecase.MaxBatchTxIDs

// BatchUpdateTransactions PATCH /api/transactions/batch —— 批量归入专项。
// 装修一次几十上百笔，逐条 PATCH 不可用。
func (h *Handler) BatchUpdateTransactions(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var req batchUpdateTxReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	updated, err := h.txUpdater().AssignSpecial(r.Context(), req.IDs, req.SpecialID)
	if err != nil {
		h.writeTxUpdateError(w, err)
		return
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
		return "/transactions?" + q.Encode()
	}
	// 没有可定位的月份（整份账单都是重复/被规则跳过）时不能空着参数走：
	// 流水页的默认周期是"上个月"，用户会看到一个空列表，以为导入把数据弄丢了。
	// 退回当前季度——盖得住当月，也盖得住刚导入的那批。
	cur := domain.CurrentQuarter(time.Now())
	q.Set("type", string(cur.Type))
	q.Set("period", cur.Label)
	return "/transactions?" + q.Encode()
}

func (h *Handler) renderImportError(w http.ResponseWriter, r *http.Request, msg string) {
	cats, _ := h.catRepo.ListAll(r.Context())
	members, _ := h.txRepo.ListMembers(r.Context())
	vm := importVM{pageBase: pageBase{Title: "导入账单", Nav: "imports"}, Error: msg, Categories: cats, Members: members}
	h.renderPage(w, http.StatusBadRequest, "imports", vm)
}

// ManualEntrySubmit POST /imports/manual（表单 + flash + 302）。
// 校验与装配都在 usecase.CreateTransaction（与 /api/v1 共用），这里只做表单解析与跳转。
func (h *Handler) ManualEntrySubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderImportError(w, r, "表单解析失败")
		return
	}

	amountStr := r.FormValue("amount")
	amountFen, err := parseAmountToFen(amountStr)
	if err != nil {
		h.renderImportError(w, r, err.Error())
		return
	}

	tx, err := usecase.NewCreateTransaction(h.txRepo, h.catRepo).Execute(r.Context(), usecase.NewTransactionInput{
		OccurredAt:   r.FormValue("occurred_at"),
		Account:      r.FormValue("account"),
		Direction:    r.FormValue("direction"),
		AmountFen:    amountFen,
		CategoryID:   r.FormValue("category_id"),
		Member:       r.FormValue("member"),
		Counterparty: r.FormValue("counterparty"),
		Description:  r.FormValue("description"),
		Note:         r.FormValue("note"),
	})
	if err != nil {
		if errors.Is(err, usecase.ErrInvalidInput) {
			h.renderImportError(w, r, err.Error())
			return
		}
		h.log.Error("manual entry insert", "err", err)
		h.renderImportError(w, r, "写入失败："+err.Error())
		return
	}

	h.flash.set(w, fmt.Sprintf("手工录入成功（%s）：%s ¥%s", tx.Account.Label(), string(tx.Direction), amountStr))
	http.Redirect(w, r, transactionsRedirectURL(tx.Account, tx.OccurredAt), http.StatusSeeOther)
}

// parseAmountToFen 表单里的元金额 → 分。换算只有 usecase.ParseYuanToFen 一份，这里留作薄封装。
func parseAmountToFen(s string) (int64, error) { return usecase.ParseYuanToFen(s) }

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	// 细节只进日志，避免把 SQL / 文件路径等内部信息回给客户端
	h.log.Error("server error", "err", err)
	http.Error(w, "服务器内部错误，请稍后重试", http.StatusInternalServerError)
}
