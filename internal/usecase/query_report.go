package usecase

import (
	"context"
	"strings"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type QueryReport struct {
	txRepo  port.TransactionRepo
	catRepo port.CategoryRepo
}

func NewQueryReport(tx port.TransactionRepo, cat port.CategoryRepo) *QueryReport {
	return &QueryReport{txRepo: tx, catRepo: cat}
}

func (uc *QueryReport) Execute(ctx context.Context, p domain.Period, account domain.Account) (domain.ReportData, error) {
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return domain.ReportData{}, err
	}
	aggs, err := uc.txRepo.AggregateByCategory(ctx, p, account)
	if err != nil {
		return domain.ReportData{}, err
	}

	aggMap := make(map[string]int64, len(aggs))
	for _, a := range aggs {
		aggMap[a.CategoryID] = a.Amount
	}

	groupsByID := make(map[string]*domain.CategoryGroupAggregation)
	ordered := make([]*domain.CategoryGroupAggregation, 0)
	for _, c := range cats {
		if c.Level != 1 {
			continue
		}
		g := &domain.CategoryGroupAggregation{
			GroupID: c.ID, GroupName: c.Name, GroupEmoji: c.GroupEmoji,
		}
		groupsByID[c.ID] = g
		ordered = append(ordered, g)
	}
	for _, c := range cats {
		if c.Level != 2 {
			continue
		}
		g, ok := groupsByID[c.ParentID]
		if !ok {
			continue
		}
		amt := aggMap[c.ID]
		g.Items = append(g.Items, domain.CategoryAggregation{
			CategoryID: c.ID, Name: c.Name, ParentID: c.ParentID, Amount: amt,
		})
		g.Subtotal += amt
	}

	var income, expense []domain.CategoryGroupAggregation
	for _, g := range ordered {
		if strings.HasPrefix(g.GroupID, "income.") {
			income = append(income, *g)
		} else if strings.HasPrefix(g.GroupID, "expense.") {
			expense = append(expense, *g)
		}
	}

	kpi := computeKPI(income, expense)
	return domain.ReportData{
		Period: p, IncomeGroups: income, ExpenseGroups: expense, KPI: kpi,
	}, nil
}

func computeKPI(income, expense []domain.CategoryGroupAggregation) domain.ReportKPI {
	var k domain.ReportKPI
	for _, g := range income {
		k.TotalIncome += g.Subtotal
	}
	for _, g := range expense {
		k.TotalExpense += g.Subtotal
	}
	k.Surplus = k.TotalIncome - k.TotalExpense
	if k.TotalIncome > 0 {
		k.SurplusRate = float64(k.Surplus) / float64(k.TotalIncome)
	}
	for _, g := range expense {
		if g.GroupID == "expense.discretion" && k.TotalExpense > 0 {
			k.DiscretionRatio = float64(g.Subtotal) / float64(k.TotalExpense)
		}
	}
	k.DiscretionWarning = k.DiscretionRatio > 0.35
	return k
}
