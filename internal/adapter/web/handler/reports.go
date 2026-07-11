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
		label = domain.CurrentQuarter(time.Now()).Label
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
	}
}

func reportLabel(period string, pt domain.PeriodType) string {
	if pt == domain.PeriodAnnual {
		return period + " 年度财报"
	}
	return period + " 季度财报"
}

// reportPeriodOptions 给期间下拉用：当前季度往前 3 季 + 当前年度，并确保 selected 一定在列表里
func reportPeriodOptions(now time.Time, selected string) []string {
	seen := map[string]bool{}
	var opts []string
	add := func(label string) {
		if !seen[label] {
			seen[label] = true
			opts = append(opts, label)
		}
	}
	cur := domain.CurrentQuarter(now)
	add(cur.Label)
	p := cur
	for i := 0; i < 3; i++ {
		p = p.Previous()
		add(p.Label)
	}
	add(strconv.Itoa(now.Year()))
	add(selected)
	return opts
}
