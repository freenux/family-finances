package usecase

import (
	"context"
	"math"
	"testing"

	"family-finances/internal/domain"
)

// ---- computeKPI：自由裁量占比的分母口径（本次修的真 bug）----
//
// 旧公式 DiscretionRatio = 自由裁量 / TotalExpense。装修季分母暴涨会把占比稀释到
// 阈值以下，35% 告警正好在最该响的时候静默关掉。现在分子分母都取日常口径。

// group 造一个一级分组（只关心 GroupID 与 Subtotal，KPI 计算不看明细）
func group(id string, subtotal int64) domain.CategoryGroupAggregation {
	return domain.CategoryGroupAggregation{GroupID: id, GroupName: id, Subtotal: subtotal}
}

const kpiEps = 1e-9

func TestComputeKPI(t *testing.T) {
	tests := []struct {
		name string
		// income / expense 是全口径分组，dailyExpense 是剔除专项后的支出分组
		income       []domain.CategoryGroupAggregation
		expense      []domain.CategoryGroupAggregation
		dailyExpense []domain.CategoryGroupAggregation
		want         domain.ReportKPI
	}{
		{
			// 核心回归：日常自由裁量占日常支出 50%，同时有一大笔专项支出（装修 15 万）。
			// 旧算法分母是全口径 19 万 → 占比 10.5%，不告警；新算法必须仍是 50% 且告警。
			name:   "装修季：日常自由裁量 50%，专项 15 万不得稀释告警",
			income: []domain.CategoryGroupAggregation{group("income.salary", 300000)},
			expense: []domain.CategoryGroupAggregation{
				group("expense.fixed", 20000),
				group("expense.discretion", 20000),
				group("expense.family", 150000), // 其中 15 万是装修专项
			},
			dailyExpense: []domain.CategoryGroupAggregation{
				group("expense.fixed", 20000),
				group("expense.discretion", 20000),
				group("expense.family", 0),
			},
			want: domain.ReportKPI{
				TotalIncome: 300000, TotalExpense: 190000,
				DailyExpense: 40000, SpecialExpense: 150000,
				Surplus: 110000, SurplusRate: 110000.0 / 300000,
				DailySurplus: 260000, DailySurplusRate: 260000.0 / 300000,
				DiscretionRatio: 0.5, DiscretionWarning: true,
			},
		},
		{
			name:         "无专项：日常口径与全口径完全一致",
			income:       []domain.CategoryGroupAggregation{group("income.salary", 100000)},
			expense:      []domain.CategoryGroupAggregation{group("expense.fixed", 30000), group("expense.discretion", 10000)},
			dailyExpense: []domain.CategoryGroupAggregation{group("expense.fixed", 30000), group("expense.discretion", 10000)},
			want: domain.ReportKPI{
				TotalIncome: 100000, TotalExpense: 40000,
				DailyExpense: 40000, SpecialExpense: 0,
				Surplus: 60000, SurplusRate: 0.6,
				DailySurplus: 60000, DailySurplusRate: 0.6,
				DiscretionRatio: 0.25, DiscretionWarning: false,
			},
		},
		{
			name:         "刚好 35%：阈值是严格大于，不告警",
			income:       []domain.CategoryGroupAggregation{group("income.salary", 100000)},
			expense:      []domain.CategoryGroupAggregation{group("expense.fixed", 6500), group("expense.discretion", 3500)},
			dailyExpense: []domain.CategoryGroupAggregation{group("expense.fixed", 6500), group("expense.discretion", 3500)},
			want: domain.ReportKPI{
				TotalIncome: 100000, TotalExpense: 10000,
				DailyExpense: 10000, SpecialExpense: 0,
				Surplus: 90000, SurplusRate: 0.9,
				DailySurplus: 90000, DailySurplusRate: 0.9,
				DiscretionRatio: 0.35, DiscretionWarning: false,
			},
		},
		{
			// 极端：本期支出全是专项（日常支出 0）。分母为 0 时占比留 0，不能除零。
			name:   "本期只有专项支出：占比留 0 不除零",
			income: []domain.CategoryGroupAggregation{group("income.salary", 100000)},
			expense: []domain.CategoryGroupAggregation{
				group("expense.discretion", 0),
				group("expense.family", 500000),
			},
			dailyExpense: []domain.CategoryGroupAggregation{
				group("expense.discretion", 0),
				group("expense.family", 0),
			},
			want: domain.ReportKPI{
				TotalIncome: 100000, TotalExpense: 500000,
				DailyExpense: 0, SpecialExpense: 500000,
				Surplus: -400000, SurplusRate: -4,
				DailySurplus: 100000, DailySurplusRate: 1,
				DiscretionRatio: 0, DiscretionWarning: false,
			},
		},
		{
			name:         "零收入：结余率留 0 不除零",
			income:       nil,
			expense:      []domain.CategoryGroupAggregation{group("expense.discretion", 5000)},
			dailyExpense: []domain.CategoryGroupAggregation{group("expense.discretion", 5000)},
			want: domain.ReportKPI{
				TotalIncome: 0, TotalExpense: 5000,
				DailyExpense: 5000, SpecialExpense: 0,
				Surplus: -5000, SurplusRate: 0,
				DailySurplus: -5000, DailySurplusRate: 0,
				DiscretionRatio: 1, DiscretionWarning: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeKPI(tt.income, tt.expense, tt.dailyExpense)

			intFields := []struct {
				name      string
				got, want int64
			}{
				{"TotalIncome", got.TotalIncome, tt.want.TotalIncome},
				{"TotalExpense", got.TotalExpense, tt.want.TotalExpense},
				{"DailyExpense", got.DailyExpense, tt.want.DailyExpense},
				{"SpecialExpense", got.SpecialExpense, tt.want.SpecialExpense},
				{"Surplus", got.Surplus, tt.want.Surplus},
				{"DailySurplus", got.DailySurplus, tt.want.DailySurplus},
			}
			for _, f := range intFields {
				if f.got != f.want {
					t.Errorf("%s = %d; want %d", f.name, f.got, f.want)
				}
			}

			floatFields := []struct {
				name      string
				got, want float64
			}{
				{"SurplusRate", got.SurplusRate, tt.want.SurplusRate},
				{"DailySurplusRate", got.DailySurplusRate, tt.want.DailySurplusRate},
				{"DiscretionRatio", got.DiscretionRatio, tt.want.DiscretionRatio},
			}
			for _, f := range floatFields {
				if math.Abs(f.got-f.want) > kpiEps {
					t.Errorf("%s = %v; want %v", f.name, f.got, f.want)
				}
			}

			if got.DiscretionWarning != tt.want.DiscretionWarning {
				t.Errorf("DiscretionWarning = %v; want %v（阈值 35%%，严格大于）", got.DiscretionWarning, tt.want.DiscretionWarning)
			}

			// 恒等式：全口径支出 = 日常 + 专项。所有按口径拆开的展示都靠它对账。
			if got.DailyExpense+got.SpecialExpense != got.TotalExpense {
				t.Errorf("TotalExpense(%d) != DailyExpense(%d) + SpecialExpense(%d)",
					got.TotalExpense, got.DailyExpense, got.SpecialExpense)
			}
			// Surplus 语义未变：仍是全口径真实现金流
			if got.Surplus != got.TotalIncome-got.TotalExpense {
				t.Errorf("Surplus(%d) != TotalIncome(%d) - TotalExpense(%d)（全口径语义被改动）",
					got.Surplus, got.TotalIncome, got.TotalExpense)
			}
			// DailySurplus 是日常口径结余
			if got.DailySurplus != got.TotalIncome-got.DailyExpense {
				t.Errorf("DailySurplus(%d) != TotalIncome(%d) - DailyExpense(%d)",
					got.DailySurplus, got.TotalIncome, got.DailyExpense)
			}
		})
	}
}

// TestComputeKPIDiscretionDenominatorIsDailyNotTotal 把这次修的 bug 单独钉死：
// 同一组自由裁量支出，只要往期间里塞一笔专项，旧公式（分母 = TotalExpense）
// 就会把占比从 50% 稀释到 10.5% 并关掉告警。新公式必须对专项完全不敏感。
func TestComputeKPIDiscretionDenominatorIsDailyNotTotal(t *testing.T) {
	income := []domain.CategoryGroupAggregation{group("income.salary", 300000)}
	dailyExpense := []domain.CategoryGroupAggregation{
		group("expense.fixed", 20000),
		group("expense.discretion", 20000),
	}

	tests := []struct {
		name    string
		special int64
	}{
		{"没有专项", 0},
		{"一笔小额专项", 10000},
		{"装修 15 万", 150000},
		{"购车 100 万", 10000000},
	}

	var base domain.ReportKPI
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expense := append(append([]domain.CategoryGroupAggregation(nil), dailyExpense...),
				group("expense.family", tt.special))
			got := computeKPI(income, expense, dailyExpense)

			if math.Abs(got.DiscretionRatio-0.5) > kpiEps {
				t.Fatalf("DiscretionRatio = %v; want 0.5（分母被专项污染了）", got.DiscretionRatio)
			}
			if !got.DiscretionWarning {
				t.Fatal("DiscretionWarning = false; want true（35% 告警在装修季被静默关掉正是本次修的 bug）")
			}
			if got.SpecialExpense != tt.special {
				t.Fatalf("SpecialExpense = %d; want %d", got.SpecialExpense, tt.special)
			}

			// 旧公式对照：专项一大，占比就被稀释到阈值以下
			oldRatio := 20000.0 / float64(got.TotalExpense)
			if tt.special >= 150000 && oldRatio > 0.35 {
				t.Fatalf("对照用例失效：旧公式占比 %v 仍超阈值，用例没能覆盖被稀释的场景", oldRatio)
			}

			if i == 0 {
				base = got
			} else if got.DiscretionRatio != base.DiscretionRatio || got.DailyExpense != base.DailyExpense {
				t.Fatalf("日常口径指标随专项变化：DiscretionRatio %v→%v, DailyExpense %d→%d",
					base.DiscretionRatio, got.DiscretionRatio, base.DailyExpense, got.DailyExpense)
			}
		})
	}
}

// ---- QueryReport.Execute：日常/专项两次聚合拼出全口径 ----

func TestQueryReportExecuteSplitsSpecial(t *testing.T) {
	// 日常：工资 30 万、购物 2 万、居住 2 万；专项：购物 3 万 + 居住 12 万（装修横跨两个科目）
	txRepo := &fakeTransactionRepo{
		periodAgg: map[string][]domain.CategoryAggregation{
			"2026Q2": {
				{CategoryID: "income.salary.husband", Amount: 300000},
				{CategoryID: "expense.discretion.shopping", Amount: 20000},
				{CategoryID: "expense.fixed.housing", Amount: 20000},
			},
		},
		specialAgg: map[string][]domain.CategoryAggregation{
			"2026Q2": {
				{CategoryID: "expense.discretion.shopping", Amount: 30000},
				{CategoryID: "expense.fixed.housing", Amount: 120000},
			},
		},
	}
	spRepo := &fakeSpecialProjectRepo{
		projects: []domain.SpecialProject{{ID: "sp-reno", Name: "2026 老房装修"}},
		inPeriod: map[string]map[string]int64{"2026Q2": {"sp-reno": 150000}},
	}
	uc := NewQueryReport(txRepo, &fakeCategoryRepo{cats: testCategories()}).WithSpecialRepo(spRepo)

	p, _ := domain.ParsePeriod("2026Q2")
	data, err := uc.Execute(context.Background(), p, domain.AccountFamily)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	k := data.KPI
	if k.TotalIncome != 300000 {
		t.Fatalf("TotalIncome = %d; want 300000", k.TotalIncome)
	}
	// 全口径支出 = 日常 4 万 + 专项 15 万
	if k.TotalExpense != 190000 || k.DailyExpense != 40000 || k.SpecialExpense != 150000 {
		t.Fatalf("支出拆分 = total %d / daily %d / special %d; want 190000 / 40000 / 150000",
			k.TotalExpense, k.DailyExpense, k.SpecialExpense)
	}
	// 装修落在"购物消费"里，全口径分母会让自由裁量占比失真；日常口径必须是 50%
	if math.Abs(k.DiscretionRatio-0.5) > kpiEps || !k.DiscretionWarning {
		t.Fatalf("DiscretionRatio = %v, Warning = %v; want 0.5 / true", k.DiscretionRatio, k.DiscretionWarning)
	}
	if k.Surplus != 110000 || k.DailySurplus != 260000 {
		t.Fatalf("Surplus = %d, DailySurplus = %d; want 110000 / 260000", k.Surplus, k.DailySurplus)
	}

	// IncomeGroups / ExpenseGroups 是全口径，语义与加专项之前一致
	var expenseTotal int64
	for _, g := range data.ExpenseGroups {
		expenseTotal += g.Subtotal
	}
	if expenseTotal != k.TotalExpense {
		t.Fatalf("ExpenseGroups 合计 = %d; want %d（分组必须是全口径）", expenseTotal, k.TotalExpense)
	}

	// SpecialGroups 只列有金额的组与科目
	var specialTotal int64
	for _, g := range data.SpecialGroups {
		if g.Subtotal == 0 {
			t.Fatalf("SpecialGroups 含金额为 0 的组 %s", g.GroupID)
		}
		specialTotal += g.Subtotal
		for _, it := range g.Items {
			if it.Amount == 0 {
				t.Fatalf("SpecialGroups[%s] 含金额为 0 的科目 %s", g.GroupID, it.CategoryID)
			}
		}
	}
	if specialTotal != 150000 {
		t.Fatalf("SpecialGroups 合计 = %d; want 150000", specialTotal)
	}
	if len(data.SpecialGroups) != 2 {
		t.Fatalf("SpecialGroups 组数 = %d; want 2（装修横跨自由裁量 + 固定刚性）", len(data.SpecialGroups))
	}

	// SpecialByProject 用专项名做键
	if got := data.SpecialByProject["2026 老房装修"]; got != 150000 {
		t.Fatalf("SpecialByProject = %v; want {2026 老房装修: 150000}", data.SpecialByProject)
	}
}

// TestQueryReportExecuteWithoutSpecialRepo 未注入专项仓库时按项目拆行留空，其余照常
func TestQueryReportExecuteWithoutSpecialRepo(t *testing.T) {
	txRepo := &fakeTransactionRepo{periodAgg: map[string][]domain.CategoryAggregation{
		"2026Q2": {
			{CategoryID: "income.salary.husband", Amount: 100000},
			{CategoryID: "expense.discretion.shopping", Amount: 10000},
		},
	}}
	uc := NewQueryReport(txRepo, &fakeCategoryRepo{cats: testCategories()})

	p, _ := domain.ParsePeriod("2026Q2")
	data, err := uc.Execute(context.Background(), p, domain.AccountFamily)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if data.SpecialByProject != nil {
		t.Fatalf("SpecialByProject = %v; want nil", data.SpecialByProject)
	}
	if len(data.SpecialGroups) != 0 {
		t.Fatalf("SpecialGroups = %v; want 空", data.SpecialGroups)
	}
	if data.KPI.SpecialExpense != 0 || data.KPI.DailyExpense != data.KPI.TotalExpense {
		t.Fatalf("无专项时 daily 应等于 total：daily %d / total %d / special %d",
			data.KPI.DailyExpense, data.KPI.TotalExpense, data.KPI.SpecialExpense)
	}
}
