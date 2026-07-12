package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// fakeBudgetRepo ----

type fakeBudgetRepo struct {
	budgets map[string]int64
}

func (f *fakeBudgetRepo) ListBudgets(context.Context) ([]domain.Budget, error) {
	var out []domain.Budget
	for cat, amount := range f.budgets {
		out = append(out, domain.Budget{ID: cat, CategoryID: cat, QuarterAmount: amount})
	}
	return out, nil
}

func (f *fakeBudgetRepo) ReplaceBudgets(_ context.Context, amounts map[string]int64) error {
	f.budgets = map[string]int64{}
	for k, v := range amounts {
		if v > 0 {
			f.budgets[k] = v
		}
	}
	return nil
}

var _ port.BudgetRepo = (*fakeBudgetRepo)(nil)

func TestBudgetStatus(t *testing.T) {
	cases := []struct {
		name   string
		budget int64
		spent  int64
		want   string
	}{
		{"无预算", 0, 5000, "none"},
		{"正常", 10000, 5000, "ok"},
		{"95% 边界仍 ok", 10000, 9500, "ok"},
		{"接近（>95%）", 10000, 9600, "near"},
		{"100% 边界仍 near", 10000, 10000, "near"},
		{"超支（>100%）", 10000, 10001, "over"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := budgetStatus(tc.budget, tc.spent); got != tc.want {
				t.Fatalf("budgetStatus(%d,%d) = %s; want %s", tc.budget, tc.spent, got, tc.want)
			}
		})
	}
}

func TestBudgetViewFindingsOnlyOverspent(t *testing.T) {
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	quarterLabel := domain.CurrentQuarter(now).Label
	txRepo := &fakeTransactionRepo{periodAgg: map[string][]domain.CategoryAggregation{
		quarterLabel: {
			{CategoryID: "expense.discretion.shopping", Name: "购物消费", Amount: 130000},
			{CategoryID: "expense.fixed.housing", Name: "居住成本", Amount: 50000},
		},
	}}
	budgetRepo := &fakeBudgetRepo{budgets: map[string]int64{
		"expense.discretion.shopping": 100000, // 130% 超支
		"expense.fixed.housing":       100000, // 50% 正常
	}}
	uc := NewBudgetView(budgetRepo, txRepo, &fakeCategoryRepo{cats: testCategories()}, &fakeProfileRepo{})
	uc.now = func() time.Time { return now }

	findings, err := uc.Findings(context.Background(), domain.Period{})
	if err != nil {
		t.Fatalf("Findings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v; want 1 (仅超支科目)", findings)
	}
	if findings[0].Key != "budget_over_expense.discretion.shopping" {
		t.Fatalf("key = %s; want budget_over_expense.discretion.shopping", findings[0].Key)
	}
	if !strings.Contains(findings[0].Text, "130%") {
		t.Fatalf("text = %q; want 执行 130%%", findings[0].Text)
	}
}

func TestBudgetViewLoadTotals(t *testing.T) {
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	quarterLabel := domain.CurrentQuarter(now).Label
	txRepo := &fakeTransactionRepo{periodAgg: map[string][]domain.CategoryAggregation{
		quarterLabel: {
			{CategoryID: "expense.discretion.shopping", Amount: 130000},
		},
	}}
	budgetRepo := &fakeBudgetRepo{budgets: map[string]int64{
		"expense.discretion.shopping": 100000,
		"expense.fixed.housing":       200000,
	}}
	uc := NewBudgetView(budgetRepo, txRepo, &fakeCategoryRepo{cats: testCategories()}, &fakeProfileRepo{})
	uc.now = func() time.Time { return now }

	data, err := uc.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if data.TotalBudgetFen != 300000 || data.TotalSpentFen != 130000 || data.OverCount != 1 {
		t.Fatalf("totals = budget %d spent %d over %d; want 300000/130000/1",
			data.TotalBudgetFen, data.TotalSpentFen, data.OverCount)
	}
	// 只列支出科目（income.salary.husband 不应出现在预算行里）
	for _, r := range data.Rows {
		if strings.HasPrefix(r.CategoryID, "income.") {
			t.Fatalf("rows 含收入科目: %+v", r)
		}
	}
}
