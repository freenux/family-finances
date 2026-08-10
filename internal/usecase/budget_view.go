package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// BudgetView 预算页用例：季度预算 vs 实际 + 周期交易 + 90 天现金流
type BudgetView struct {
	budgetRepo port.BudgetRepo
	txRepo     port.TransactionRepo
	catRepo    port.CategoryRepo
	policyRepo port.InsurancePolicyRepo
	now        func() time.Time
}

func NewBudgetView(budgetRepo port.BudgetRepo, txRepo port.TransactionRepo, catRepo port.CategoryRepo,
	policyRepo port.InsurancePolicyRepo) *BudgetView {
	return &BudgetView{budgetRepo: budgetRepo, txRepo: txRepo, catRepo: catRepo, policyRepo: policyRepo, now: time.Now}
}

// BudgetRow 一个科目的预算对照
type BudgetRow struct {
	CategoryID string
	Name       string
	GroupName  string
	BudgetFen  int64
	SpentFen   int64
	Ratio      float64 // spent/budget
	Status     string  // none | ok | near(>95%) | over(>100%)
}

// BudgetViewData 页面数据
type BudgetViewData struct {
	Quarter        string
	Rows           []BudgetRow
	TotalBudgetFen int64
	TotalSpentFen  int64
	OverCount      int
	Recurring      []RecurringItem
	Cashflow       []MonthCashflow
	MonthlyInflow  int64
}

// Load 组装页面数据
func (uc *BudgetView) Load(ctx context.Context) (BudgetViewData, error) {
	now := uc.now()
	quarter := domain.CurrentQuarter(now)
	rows, err := uc.budgetRows(ctx, quarter)
	if err != nil {
		return BudgetViewData{}, err
	}
	data := BudgetViewData{Quarter: quarter.Label, Rows: rows}
	for _, r := range rows {
		data.TotalBudgetFen += r.BudgetFen
		data.TotalSpentFen += r.SpentFen
		if r.Status == "over" {
			data.OverCount++
		}
	}

	items, inflow, err := uc.recurringAndInflow(ctx, now)
	if err != nil {
		return BudgetViewData{}, err
	}
	policies, err := uc.policyRepo.ListAllPolicies(ctx)
	if err != nil {
		return BudgetViewData{}, err
	}
	data.Recurring = items
	data.MonthlyInflow = inflow
	data.Cashflow = ProjectCashflow(items, policies, inflow, now)
	return data, nil
}

// budgetRows 当前季度 confirmed 支出（家庭口径）按二级科目 vs 预算
func (uc *BudgetView) budgetRows(ctx context.Context, quarter domain.Period) ([]BudgetRow, error) {
	budgets, err := uc.budgetRepo.ListBudgets(ctx)
	if err != nil {
		return nil, err
	}
	budgetByCat := make(map[string]int64, len(budgets))
	for _, b := range budgets {
		budgetByCat[b.CategoryID] = b.QuarterAmount
	}
	// 预算是日常盘子，专项走 /specials 单独的预算/执行率
	aggs, err := uc.txRepo.AggregateByCategory(ctx, quarter, domain.AccountFamily, domain.ScopeDaily)
	if err != nil {
		return nil, err
	}
	spentByCat := make(map[string]int64, len(aggs))
	for _, a := range aggs {
		spentByCat[a.CategoryID] = a.Amount
	}
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	groupName := map[string]string{}
	for _, c := range cats {
		if c.Level == 1 {
			groupName[c.ID] = c.Name
		}
	}

	var rows []BudgetRow
	for _, c := range cats {
		if c.Level != 2 || !strings.HasPrefix(c.ID, "expense.") {
			continue
		}
		row := BudgetRow{
			CategoryID: c.ID,
			Name:       c.Name,
			GroupName:  groupName[c.ParentID],
			BudgetFen:  budgetByCat[c.ID],
			SpentFen:   spentByCat[c.ID],
		}
		row.Status = budgetStatus(row.BudgetFen, row.SpentFen)
		if row.BudgetFen > 0 {
			row.Ratio = float64(row.SpentFen) / float64(row.BudgetFen)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func budgetStatus(budget, spent int64) string {
	if budget <= 0 {
		return "none"
	}
	ratio := float64(spent) / float64(budget)
	switch {
	case ratio > 1.0:
		return "over"
	case ratio > 0.95:
		return "near"
	default:
		return "ok"
	}
}

// recurringAndInflow 近 12 个月支出识别周期项 + 近 6 个月工资类月均流入
func (uc *BudgetView) recurringAndInflow(ctx context.Context, now time.Time) ([]RecurringItem, int64, error) {
	// 日常口径：装修期连着刷卡会被周期识别误判成"周期性订阅"
	txs, err := uc.txRepo.ListForRecurring(ctx, now.AddDate(-1, 0, 0), now.AddDate(0, 0, 1), domain.ScopeDaily)
	if err != nil {
		return nil, 0, err
	}
	items := DetectRecurring(txs)

	sixMonths := domain.Period{Start: now.AddDate(0, -6, 0), End: now.AddDate(0, 0, 1)}
	aggs, err := uc.txRepo.AggregateByCategory(ctx, sixMonths, domain.AccountFamily, domain.ScopeDaily)
	if err != nil {
		return nil, 0, err
	}
	var salary int64
	for _, a := range aggs {
		if strings.HasPrefix(a.CategoryID, "income.salary") {
			salary += a.Amount
		}
	}
	return items, salary / 6, nil
}

// Findings 预算超支 finding（ContextPack FindingSource）：每个超支科目一条 budget_over_<catID>
func (uc *BudgetView) Findings(ctx context.Context, _ domain.Period) ([]Finding, error) {
	quarter := domain.CurrentQuarter(uc.now())
	rows, err := uc.budgetRows(ctx, quarter)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, r := range rows {
		if r.Status != "over" {
			continue
		}
		out = append(out, Finding{
			Key: "budget_over_" + r.CategoryID,
			Text: fmt.Sprintf("「%s」本季支出 %s 元，超出预算 %s 元（执行 %.0f%%）",
				r.Name, formatYuanFen(r.SpentFen), formatYuanFen(r.BudgetFen), r.Ratio*100),
		})
	}
	return out, nil
}
