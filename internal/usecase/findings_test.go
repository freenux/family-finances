package usecase

import (
	"strings"
	"testing"

	"family-finances/internal/domain"
)

func expenseGroup(items ...domain.CategoryAggregation) []domain.CategoryGroupAggregation {
	return []domain.CategoryGroupAggregation{{GroupID: "expense.discretion", Items: items}}
}

func TestComputeFindingsSavingsRate(t *testing.T) {
	cases := []struct {
		name       string
		kpi        domain.ReportKPI
		streak     int
		wantHasKey bool
		wantSub    string
	}{
		{
			name:       "no income → 结余率 finding 缺席",
			kpi:        domain.ReportKPI{TotalIncome: 0},
			wantHasKey: false,
		},
		{
			name:       "有收入但未连续达标 → 只报当期",
			kpi:        domain.ReportKPI{TotalIncome: 10000, SurplusRate: 0.32},
			streak:     1,
			wantHasKey: true,
			wantSub:    "本期结余率",
		},
		{
			name:       "连续两期以上达标 → 强调连续性",
			kpi:        domain.ReportKPI{TotalIncome: 10000, SurplusRate: 0.35},
			streak:     3,
			wantHasKey: true,
			wantSub:    "连续 3 期",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := computeFindings(findingsInput{KPI: tc.kpi, SavingsStreak: tc.streak})
			var f *Finding
			for i := range findings {
				if findings[i].Key == "savings_rate" {
					f = &findings[i]
				}
			}
			if tc.wantHasKey && f == nil {
				t.Fatalf("findings = %+v; want a savings_rate entry", findings)
			}
			if !tc.wantHasKey && f != nil {
				t.Fatalf("findings = %+v; want no savings_rate entry", findings)
			}
			if f != nil && !strings.Contains(f.Text, tc.wantSub) {
				t.Fatalf("text = %q; want substring %q", f.Text, tc.wantSub)
			}
		})
	}
}

func TestTopExpenseChangePicksLargestPctAndSkipsZeroPrev(t *testing.T) {
	cur := expenseGroup(
		domain.CategoryAggregation{CategoryID: "expense.discretion.shopping", Name: "购物消费", Amount: 12400},
		domain.CategoryAggregation{CategoryID: "expense.discretion.leisure", Name: "娱乐休闲", Amount: 5000},
		domain.CategoryAggregation{CategoryID: "expense.discretion.new", Name: "新增类目", Amount: 3000}, // 上期不存在，应跳过
	)
	prev := expenseGroup(
		domain.CategoryAggregation{CategoryID: "expense.discretion.shopping", Name: "购物消费", Amount: 10000}, // +24%
		domain.CategoryAggregation{CategoryID: "expense.discretion.leisure", Name: "娱乐休闲", Amount: 4900},   // +2%
		domain.CategoryAggregation{CategoryID: "expense.discretion.dead", Name: "已消失类目", Amount: 0},        // prev=0，应跳过防除零
	)

	f, ok := topExpenseChange(cur, prev)
	if !ok {
		t.Fatal("topExpenseChange() ok = false; want true")
	}
	if f.Key != "expense_top_change_expense.discretion.shopping" {
		t.Fatalf("Key = %q; want shopping category key", f.Key)
	}
	if !strings.Contains(f.Text, "+24") {
		t.Fatalf("Text = %q; want +24%% change", f.Text)
	}
}

func TestTopExpenseChangeNoQualifyingCategory(t *testing.T) {
	cur := expenseGroup(domain.CategoryAggregation{CategoryID: "expense.discretion.new", Amount: 100})
	prev := expenseGroup() // 没有任何重叠类目
	if _, ok := topExpenseChange(cur, prev); ok {
		t.Fatal("topExpenseChange() ok = true; want false when no overlapping category has prev>0")
	}
}

func TestComputeFindingsDiscretionRatio(t *testing.T) {
	cases := []struct {
		name    string
		kpi     domain.ReportKPI
		wantSub string
	}{
		{"低于阈值不告警", domain.ReportKPI{TotalExpense: 10000, DiscretionRatio: 0.20, DiscretionWarning: false}, "20.0%"},
		{"超过阈值告警", domain.ReportKPI{TotalExpense: 10000, DiscretionRatio: 0.40, DiscretionWarning: true}, "超过 35% 警戒线"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := computeFindings(findingsInput{KPI: tc.kpi})
			var text string
			for _, f := range findings {
				if f.Key == "discretion_ratio" {
					text = f.Text
				}
			}
			if !strings.Contains(text, tc.wantSub) {
				t.Fatalf("text = %q; want substring %q", text, tc.wantSub)
			}
		})
	}
}

func TestComputeFindingsNetWorthDeltaAndCashCoverage(t *testing.T) {
	cur := &domain.AssetSnapshot{
		NetWorth: 2860000,
		Data:     map[string]int64{"asset.cash": 5800000, "asset.mmf": 9200000},
	}
	prev := &domain.AssetSnapshot{NetWorth: 2755000}

	findings := computeFindings(findingsInput{
		KPI:               domain.ReportKPI{},
		CurSnapshot:       cur,
		PrevSnapshot:      prev,
		MonthlyAvgExpense: 2000000, // 分
	})

	var netWorthText, cashText string
	for _, f := range findings {
		switch f.Key {
		case "net_worth_delta":
			netWorthText = f.Text
		case "cash_coverage_months":
			cashText = f.Text
		}
	}
	if !strings.Contains(netWorthText, "+") {
		t.Fatalf("net_worth_delta text = %q; want positive delta", netWorthText)
	}
	if !strings.Contains(cashText, "7.5") {
		t.Fatalf("cash_coverage_months text = %q; want ~7.5 个月 ((58000+92000)/20000)", cashText)
	}
}

func TestComputeFindingsNoSnapshotOmitsSnapshotFindings(t *testing.T) {
	findings := computeFindings(findingsInput{KPI: domain.ReportKPI{}})
	for _, f := range findings {
		if f.Key == "net_worth_delta" || f.Key == "cash_coverage_months" {
			t.Fatalf("findings = %+v; want no snapshot-derived findings when snapshot is nil", findings)
		}
	}
}
