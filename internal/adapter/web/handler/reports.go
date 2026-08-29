package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

type reportHistoryItem struct {
	Period      string
	PeriodType  string
	Label       string
	GeneratedAt time.Time
}

type reportViewModel struct {
	Report      domain.AIReport
	Income      usecase.MoneySection
	Expense     usecase.MoneySection
	KPI         usecase.ContextKPI
	Compare     usecase.ContextCompare
	Snapshot    *usecase.ContextSnapshot
	Findings    []usecase.Finding
	FindingText map[string]string
	Content     usecase.AIReportContent
	// Labels 按这份存档自己的口径选好的 chip 文案（见 scopeLabelsFor）
	Labels reportScopeLabels
	// ShowAllScopeRate 是否额外给一档「全口径结余率」。只有日常口径的存档才需要：
	// 全口径存档的主结余率本身就是全口径，再挂一个同名 chip 是重复；而它的
	// kpi_data 里也根本没有 savings_rate_all_scope 这个键（解出来恒为 0）。
	ShowAllScopeRate bool
}

// reportScopeLabels 一份存档的口径文案。历史存档是口径拆分之前生成的、数字为全口径，
// 用新的「日常」文案渲染就等于把全口径数字标成日常口径——存量数据被误标。
type reportScopeLabels struct {
	Income      string // 收入 chip 前缀
	Expense     string // 支出 chip 前缀
	SavingsRate string // 结余率 chip 前缀
	Discretion  string // 自由裁量占比 chip 前缀
	Note        string // chips 下面那句口径说明
	Legacy      bool   // 全口径旧存档：模板据此提示"生成于口径拆分之前"
}

// scopeLabelsFor 按存档口径选文案。daily 之外的一切（含空值）都按全口径处理，
// 与 sqlite.storedScope 的读取口径保持一致。
func scopeLabelsFor(s domain.Scope) reportScopeLabels {
	if s == domain.ScopeDaily {
		return reportScopeLabels{
			Income:      "日常收入",
			Expense:     "日常支出",
			SavingsRate: "日常结余率",
			Discretion:  "自由裁量占日常支出",
			Note: "以上收支明细与结余率均为日常口径（已剔除装修/购车这类专项开支）；" +
				"全口径结余率才是本期真实现金流。上下文包里专项单列一节，AI 两边都能看到。",
		}
	}
	return reportScopeLabels{
		Income:      "收入",
		Expense:     "支出",
		SavingsRate: "结余率",
		Discretion:  "自由裁量占比",
		Note: "本报告生成于口径拆分之前，数字为全口径（含装修/购车这类专项开支），" +
			"所以标签没有「日常」二字；与新报告的日常口径不可直接对比。",
		Legacy: true,
	}
}

type reportsVM struct {
	pageBase
	History        []reportHistoryItem
	Selected       *reportViewModel
	LLMEnabled     bool
	SelectedPeriod string
	SelectedType   string
	PeriodOptions  []string
}

// Reports GET /reports?period=2026Q2 —— 历史财报列表 + 当前选中财报（读库，不调 LLM）
func (h *Handler) Reports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	label := q.Get("period")
	if label == "" {
		// 与现金流表/流水页/StatsAPI 同一套默认：上一个完整季度。
		// 各页面各自默认会让"现金流表看 2026Q2、点进财报却默认 2026Q3"。
		label = defaultPeriodFor(domain.PeriodQuarterly, time.Now()).Label
	}
	p, err := domain.ParsePeriod(label)
	if err != nil || (p.Type != domain.PeriodQuarterly && p.Type != domain.PeriodAnnual) {
		http.Error(w, "非法的期间，财报仅支持季度或年度", http.StatusBadRequest)
		return
	}

	all, err := h.reportRepo.ListAll(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	history := make([]reportHistoryItem, 0, len(all))
	for _, rep := range all {
		history = append(history, reportHistoryItem{
			Period:      rep.Period,
			PeriodType:  string(rep.PeriodType),
			Label:       reportLabel(rep.Period, rep.PeriodType),
			GeneratedAt: rep.GeneratedAt,
		})
	}

	var selected *reportViewModel
	rep, err := h.reportRepo.GetByPeriod(r.Context(), p.Label, p.Type)
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		h.serverError(w, err)
		return
	}
	if err == nil {
		vm := buildReportViewModel(*rep)
		selected = &vm
	}

	vm := reportsVM{
		pageBase:       pageBase{Title: "AI 财报", Nav: "reports", Flash: h.flash.pop(w, r)},
		History:        history,
		Selected:       selected,
		LLMEnabled:     h.genReport.Enabled(),
		SelectedPeriod: p.Label,
		SelectedType:   string(p.Type),
		PeriodOptions:  reportPeriodOptions(time.Now(), p.Label),
	}
	h.renderPage(w, http.StatusOK, "reports", vm)
}

// GenerateReportSubmit POST /reports/generate —— form: period；同步生成（30s 超时）→ 302 回 GET
func (h *Handler) GenerateReportSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.flash.set(w, "表单解析失败")
		http.Redirect(w, r, "/reports", http.StatusSeeOther)
		return
	}
	label := r.FormValue("period")
	p, err := domain.ParsePeriod(label)
	if err != nil || (p.Type != domain.PeriodQuarterly && p.Type != domain.PeriodAnnual) {
		h.flash.set(w, "请选择合法的季度或年度期间")
		http.Redirect(w, r, "/reports", http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := h.genReport.Execute(ctx, p); err != nil {
		h.log.Error("generate report", "period", p.Label, "err", err)
		h.flash.set(w, "生成失败："+err.Error())
	} else {
		h.flash.set(w, p.Label+" 财报已生成并存档。")
	}
	http.Redirect(w, r, "/reports?"+url.Values{"period": {p.Label}}.Encode(), http.StatusSeeOther)
}

func buildReportViewModel(rep domain.AIReport) reportViewModel {
	var income, expense usecase.MoneySection
	_ = json.Unmarshal([]byte(rep.IncomeData), &income)
	_ = json.Unmarshal([]byte(rep.ExpenseData), &expense)
	var kpiEnv usecase.KPIDataEnvelope
	_ = json.Unmarshal([]byte(rep.KPIData), &kpiEnv)
	var compare usecase.ContextCompare
	_ = json.Unmarshal([]byte(rep.Comparison), &compare)
	var content usecase.AIReportContent
	_ = json.Unmarshal([]byte(rep.AIAnalysis), &content)

	findingText := make(map[string]string, len(kpiEnv.Findings))
	for _, f := range kpiEnv.Findings {
		findingText[f.Key] = f.Text
	}

	return reportViewModel{
		Report:      rep,
		Income:      income,
		Expense:     expense,
		KPI:         kpiEnv.KPI,
		Compare:     compare,
		Snapshot:    kpiEnv.Snapshot,
		Findings:    kpiEnv.Findings,
		FindingText: findingText,
		Content:     content,
		// 按存档自己的口径渲染，而不是按"现在的生成口径"
		Labels:           scopeLabelsFor(rep.DataScope),
		ShowAllScopeRate: rep.DataScope == domain.ScopeDaily && kpiEnv.KPI.SavingsRateAllScope != 0,
	}
}

func reportLabel(period string, pt domain.PeriodType) string {
	if pt == domain.PeriodAnnual {
		return period + " 年度财报"
	}
	return period + " 季度财报"
}

// reportPeriodOptions 给期间下拉用。第一项是页面默认周期（上一个完整季度），
// 与 Reports 的缺省选中保持一致；再往前 3 季，然后补上仍在进行中的当前季度/当前年度
// （未走完，数字有误导性，所以排在后面但仍可选），最后确保 selected 一定在列表里。
func reportPeriodOptions(now time.Time, selected string) []string {
	seen := map[string]bool{}
	var opts []string
	add := func(label string) {
		if !seen[label] {
			seen[label] = true
			opts = append(opts, label)
		}
	}
	p := defaultPeriodFor(domain.PeriodQuarterly, now)
	add(p.Label)
	for i := 0; i < 3; i++ {
		p = p.Previous()
		add(p.Label)
	}
	add(domain.CurrentQuarter(now).Label)
	add(defaultPeriodFor(domain.PeriodAnnual, now).Label)
	add(strconv.Itoa(now.Year()))
	add(selected)
	return opts
}
