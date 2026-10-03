package usecase

import (
	"context"
	"fmt"

	"family-finances/internal/domain"
)

// ReportSource 季/年报的数据源，由 *QueryReport 满足。
type ReportSource interface {
	Execute(ctx context.Context, p domain.Period, account domain.Account) (domain.ReportData, error)
}

// ReportView 季/年报的客户端视图用例：解析周期 + 调 QueryReport + 装配可直接渲染的 DTO。
// 不重新实现聚合——日常/专项各查一次再合成全口径的逻辑只在 QueryReport 里。
type ReportView struct {
	src ReportSource
	nav PeriodNav
}

func NewReportView(src ReportSource, nav PeriodNav) *ReportView {
	return &ReportView{src: src, nav: nav}
}

// ReportDTO GET /api/v1/report 的出参。金额成对 xxx_fen/xxx_text，比率成对 xxx/xxx_text，
// 告警由服务端判定，客户端只渲染。
type ReportDTO struct {
	Period      PeriodNavView `json:"period"`
	Account     string        `json:"account"`
	AccountText string        `json:"account_text"`
	KPI         ReportKPIDTO  `json:"kpi"`
	// Cashflow 固定三行：daily / special / all，且 daily + special == all（收入、支出、结余逐列成立）
	Cashflow         []CashflowRowDTO `json:"cashflow"`
	SpecialByProject []SpecialRowDTO  `json:"special_by_project"`
}

type ReportKPIDTO struct {
	TotalIncomeFen       int64   `json:"total_income_fen"`
	TotalIncomeText      string  `json:"total_income_text"`
	TotalExpenseFen      int64   `json:"total_expense_fen"`
	TotalExpenseText     string  `json:"total_expense_text"`
	SurplusFen           int64   `json:"surplus_fen"`
	SurplusText          string  `json:"surplus_text"`
	SurplusRate          float64 `json:"surplus_rate"`
	SurplusRateText      string  `json:"surplus_rate_text"`
	DailySurplusFen      int64   `json:"daily_surplus_fen"`
	DailySurplusText     string  `json:"daily_surplus_text"`
	DailySurplusRate     float64 `json:"daily_surplus_rate"`
	DailySurplusRateText string  `json:"daily_surplus_rate_text"`
	// 自由裁量占日常支出：分母是日常口径支出（daily_expense_*），不是 total_expense_*
	DiscretionRatio     float64 `json:"discretion_ratio"`
	DiscretionRatioText string  `json:"discretion_ratio_text"`
	DiscretionWarning   bool    `json:"discretion_warning"`
	DiscretionNote      string  `json:"discretion_note"` // 面向用户的一句话说明，告警与否都有
}

type CashflowRowDTO struct {
	Key         string `json:"key"` // daily | special | all
	Label       string `json:"label"`
	IncomeFen   int64  `json:"income_fen"`
	IncomeText  string `json:"income_text"`
	ExpenseFen  int64  `json:"expense_fen"`
	ExpenseText string `json:"expense_text"`
	SurplusFen  int64  `json:"surplus_fen"`
	SurplusText string `json:"surplus_text"`
}

type SpecialRowDTO struct {
	SpecialID  string `json:"special_id"`
	Name       string `json:"name"`
	AmountFen  int64  `json:"amount_fen"`
	AmountText string `json:"amount_text"`
}

// ReportDefaultType 财报视图的缺省粒度：季度（季/年报本来就是季度/年度口径，不是月度）。
const ReportDefaultType = domain.PeriodQuarterly

// Execute type/period/account 是原始 query 值，归一规则全在 PeriodNav.Resolve 与 domain.ParseAccount。
// period 非法 → ErrInvalidPeriod。
func (uc *ReportView) Execute(ctx context.Context, typeStr, periodStr, accountStr string) (ReportDTO, error) {
	p, err := uc.nav.Resolve(typeStr, periodStr, ReportDefaultType)
	if err != nil {
		return ReportDTO{}, err
	}
	acc := domain.ParseAccount(accountStr)
	data, err := uc.src.Execute(ctx, p, acc)
	if err != nil {
		return ReportDTO{}, err
	}
	return buildReportDTO(uc.nav.Nav(p), acc, data), nil
}

func buildReportDTO(nav PeriodNavView, acc domain.Account, d domain.ReportData) ReportDTO {
	k := d.KPI
	row := func(key, label string, income, expense int64) CashflowRowDTO {
		return CashflowRowDTO{
			Key: key, Label: label,
			IncomeFen: income, IncomeText: FormatYuan(income),
			ExpenseFen: expense, ExpenseText: FormatYuan(expense),
			SurplusFen: income - expense, SurplusText: FormatYuan(income - expense),
		}
	}
	special := make([]SpecialRowDTO, 0, len(d.SpecialByProject))
	for _, sp := range d.SpecialByProject {
		special = append(special, SpecialRowDTO{
			SpecialID: sp.SpecialID, Name: sp.Name,
			AmountFen: sp.Amount, AmountText: FormatYuan(sp.Amount),
		})
	}
	return ReportDTO{
		Period: nav, Account: string(acc), AccountText: acc.Label(),
		KPI: ReportKPIDTO{
			TotalIncomeFen: k.TotalIncome, TotalIncomeText: FormatYuan(k.TotalIncome),
			TotalExpenseFen: k.TotalExpense, TotalExpenseText: FormatYuan(k.TotalExpense),
			SurplusFen: k.Surplus, SurplusText: FormatYuan(k.Surplus),
			SurplusRate: k.SurplusRate, SurplusRateText: pctString(k.SurplusRate),
			DailySurplusFen: k.DailySurplus, DailySurplusText: FormatYuan(k.DailySurplus),
			DailySurplusRate: k.DailySurplusRate, DailySurplusRateText: pctString(k.DailySurplusRate),
			DiscretionRatio: k.DiscretionRatio, DiscretionRatioText: pctString(k.DiscretionRatio),
			DiscretionWarning: k.DiscretionWarning,
			DiscretionNote:    discretionNote(k),
		},
		// 三行直接取 KPI 里已合成好的数，不在这里重算：special = total − daily 在 computeKPI 里一次成立
		Cashflow: []CashflowRowDTO{
			row("daily", "日常", k.DailyIncome, k.DailyExpense),
			row("special", "专项", k.SpecialIncome, k.SpecialExpense),
			row("all", "全口径", k.TotalIncome, k.TotalExpense),
		},
		SpecialByProject: special,
	}
}

func discretionNote(k domain.ReportKPI) string {
	line := fmt.Sprintf("自由裁量支出占日常支出（不含专项）%s", pctString(k.DiscretionRatio))
	limit := pctString(DiscretionWarnRatio)
	if k.DiscretionWarning {
		return line + "，超过 " + limit + " 的建议线，建议控制。"
	}
	return line + "，在 " + limit + " 的建议线以内。"
}
