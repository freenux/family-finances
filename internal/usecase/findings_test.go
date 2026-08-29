package usecase

import (
	"strings"
	"testing"

	"family-finances/internal/domain"
)

func expenseGroup(items ...domain.CategoryAggregation) []domain.CategoryGroupAggregation {
	return []domain.CategoryGroupAggregation{{GroupID: "expense.discretion", Items: items}}
}

// TestComputeFindingsSavingsRate 结余率 finding 一律用日常口径。
// 旧版本用 KPI.SurplusRate（全口径）：连胜的历史桶算的是日常口径，
// 当期却拿全口径来比，等于 N−1 个日常期 vs 1 个全口径当期。
func TestComputeFindingsSavingsRate(t *testing.T) {
	cases := []struct {
		name       string
		kpi        domain.ReportKPI
		streak     int
		wantHasKey bool
		wantSubs   []string
		notSubs    []string
	}{
		{
			name:       "日常收入为 0 → 结余率 finding 缺席（分母为 0，比值没意义）",
			kpi:        domain.ReportKPI{TotalIncome: 0},
			wantHasKey: false,
		},
		{
			name:       "收入全在专项里（日常收入 0）→ 同样缺席",
			kpi:        domain.ReportKPI{TotalIncome: 800000, DailyIncome: 0, SpecialIncome: 800000},
			wantHasKey: false,
		},
		{
			name:       "有日常收入但未连续达标 → 只报当期",
			kpi:        domain.ReportKPI{TotalIncome: 10000, DailyIncome: 10000, DailySurplusRate: 0.32},
			streak:     1,
			wantHasKey: true,
			wantSubs:   []string{"本期日常结余率", "32.0%"},
		},
		{
			name:       "连续两期以上达标 → 强调连续性，用日常口径的当期值",
			kpi:        domain.ReportKPI{TotalIncome: 10000, DailyIncome: 10000, DailySurplusRate: 0.35},
			streak:     3,
			wantHasKey: true,
			wantSubs:   []string{"连续 3 期", "35.0%"},
		},
		{
			// 缺陷 4 的场景：装修季全口径结余率被砸成负数，日常口径仍然达标。
			// finding 必须报日常口径的 80.0%，且把全口径那一档也写清楚，
			// 不能只丢一个 -3.6% 让 AI 以为这家人不会存钱。
			name: "装修季：报日常口径，并附上全口径真实现金流",
			kpi: domain.ReportKPI{
				TotalIncome: 28000000, DailyIncome: 20000000, SpecialIncome: 8000000,
				DailyExpense: 4000000, SpecialExpense: 25000000,
				SurplusRate: -1000000.0 / 28000000, DailySurplusRate: 0.8,
			},
			streak:     4,
			wantHasKey: true,
			wantSubs:   []string{"连续 4 期", "80.0%", "全口径结余率", "-3.6%"},
		},
		{
			// 没有专项时不啰嗦：两个口径本来就相等，只报一个数
			name:       "无专项：不追加全口径那一句",
			kpi:        domain.ReportKPI{TotalIncome: 10000, DailyIncome: 10000, DailySurplusRate: 0.4, SurplusRate: 0.4},
			streak:     1,
			wantHasKey: true,
			wantSubs:   []string{"40.0%"},
			notSubs:    []string{"全口径"},
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
			if !tc.wantHasKey {
				if f != nil {
					t.Fatalf("findings = %+v; want no savings_rate entry", findings)
				}
				return
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(f.Text, sub) {
					t.Fatalf("text = %q; want substring %q", f.Text, sub)
				}
			}
			for _, sub := range tc.notSubs {
				if strings.Contains(f.Text, sub) {
					t.Fatalf("text = %q; 不该含 %q", f.Text, sub)
				}
			}
		})
	}
}

// TestComputeFindingsSavingsRateIgnoresAllScopeRate 反向钉死：日常口径的数不变，
// 只把全口径结余率改得天差地别，finding 报的比例不能跟着动。
func TestComputeFindingsSavingsRateIgnoresAllScopeRate(t *testing.T) {
	for _, allScope := range []float64{-4, -0.5, 0, 0.9} {
		kpi := domain.ReportKPI{
			TotalIncome: 10000, DailyIncome: 10000,
			DailySurplusRate: 0.42, SurplusRate: allScope,
		}
		findings := computeFindings(findingsInput{KPI: kpi, SavingsStreak: 1})
		var text string
		for _, f := range findings {
			if f.Key == "savings_rate" {
				text = f.Text
			}
		}
		if !strings.Contains(text, "42.0%") {
			t.Fatalf("SurplusRate=%v 时 text = %q; want 含日常口径的 42.0%%", allScope, text)
		}
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

// TestComputeFindingsDiscretionRatio 守卫必须跟着分母走：比值分母已经是 DailyExpense，
// 守 TotalExpense 会在"本期支出全部归入专项"时输出一条
// 「可自由支配支出占比 0.0%」的确定性结论，并进入 LLM 的 refs 白名单。
func TestComputeFindingsDiscretionRatio(t *testing.T) {
	cases := []struct {
		name     string
		kpi      domain.ReportKPI
		wantHas  bool
		wantSubs []string
	}{
		{
			name:     "低于阈值不告警",
			kpi:      domain.ReportKPI{TotalExpense: 10000, DailyExpense: 10000, DiscretionRatio: 0.20},
			wantHas:  true,
			wantSubs: []string{"占日常支出", "20.0%"},
		},
		{
			name:     "超过阈值告警",
			kpi:      domain.ReportKPI{TotalExpense: 10000, DailyExpense: 10000, DiscretionRatio: 0.40, DiscretionWarning: true},
			wantHas:  true,
			wantSubs: []string{"40.0%", "超过 35% 警戒线"},
		},
		{
			name: "本期支出全部归入专项（日常支出 0）→ 不产出这条 finding",
			kpi: domain.ReportKPI{
				TotalExpense: 500000, DailyExpense: 0, SpecialExpense: 500000,
				DiscretionRatio: 0,
			},
			wantHas: false,
		},
		{
			name:     "有日常支出，专项再大也照常产出",
			kpi:      domain.ReportKPI{TotalExpense: 19000000, DailyExpense: 40000, SpecialExpense: 18960000, DiscretionRatio: 0.5, DiscretionWarning: true},
			wantHas:  true,
			wantSubs: []string{"50.0%", "超过 35% 警戒线"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := computeFindings(findingsInput{KPI: tc.kpi})
			var found bool
			var text string
			for _, f := range findings {
				if f.Key == "discretion_ratio" {
					found, text = true, f.Text
				}
			}
			if found != tc.wantHas {
				t.Fatalf("discretion_ratio 存在 = %v; want %v（findings=%+v）", found, tc.wantHas, findings)
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(text, sub) {
					t.Fatalf("text = %q; want substring %q", text, sub)
				}
			}
		})
	}
}

// TestComputeFindingsDiscretionRatioNotInRefWhitelist 把缺陷 6 的后果钉死：
// 日常支出为 0 时这条结论既不出现在 findings 里，也就不该进 refs 白名单——
// 否则 LLM 可以合法引用一句"占比 0.0%"的假结论。
func TestComputeFindingsDiscretionRatioNotInRefWhitelist(t *testing.T) {
	findings := computeFindings(findingsInput{KPI: domain.ReportKPI{
		TotalIncome: 100000, DailyIncome: 100000, DailySurplusRate: 1,
		TotalExpense: 500000, DailyExpense: 0, SpecialExpense: 500000,
	}})
	pack := ContextPack{Findings: findings}
	if pack.RefWhitelist()["discretion_ratio"] {
		t.Fatalf("refs 白名单里出现了 discretion_ratio；findings = %+v", findings)
	}
	// 同期其它 finding 不受牵连
	if !pack.RefWhitelist()["savings_rate"] {
		t.Fatalf("savings_rate 被误伤；findings = %+v", findings)
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
