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
//
// 同理 DailySurplus/DailySurplusRate 也必须两侧同口径：曾经是
// 全口径收入 − 日常支出，专项里记一笔旧车折价收入就能把"攒钱能力"抬上去。

// group 造一个一级分组（只关心 GroupID 与 Subtotal，KPI 计算不看明细）
func group(id string, subtotal int64) domain.CategoryGroupAggregation {
	return domain.CategoryGroupAggregation{GroupID: id, GroupName: id, Subtotal: subtotal}
}

const kpiEps = 1e-9

func TestComputeKPI(t *testing.T) {
	tests := []struct {
		name string
		// income / expense 是全口径分组；dailyIncome / dailyExpense 是剔除专项后的分组
		income       []domain.CategoryGroupAggregation
		expense      []domain.CategoryGroupAggregation
		dailyIncome  []domain.CategoryGroupAggregation
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
			dailyIncome: []domain.CategoryGroupAggregation{group("income.salary", 300000)},
			dailyExpense: []domain.CategoryGroupAggregation{
				group("expense.fixed", 20000),
				group("expense.discretion", 20000),
				group("expense.family", 0),
			},
			want: domain.ReportKPI{
				TotalIncome: 300000, TotalExpense: 190000,
				DailyIncome: 300000, SpecialIncome: 0,
				DailyExpense: 40000, SpecialExpense: 150000,
				Surplus: 110000, SurplusRate: 110000.0 / 300000,
				DailySurplus: 260000, DailySurplusRate: 260000.0 / 300000,
				DiscretionRatio: 0.5, DiscretionWarning: true,
			},
		},
		{
			// 缺陷 2 的失败场景（金额按需求原文，单位分）：换车专项里记了 8 万旧车折价收入，
			// 日常收入 20 万、日常支出 4 万、专项支出 25 万。
			// 旧公式 DailySurplus = 全口径收入(28万) − 日常支出(4万) → 85.7%，
			// 在花掉 25 万的季度反而显得更会存钱；正确值是 (20−4)/20 = 80.0%。
			name: "换车季：专项里的旧车折价收入不得抬高日常结余率",
			income: []domain.CategoryGroupAggregation{
				group("income.salary", 20000000),
				group("income.other", 8000000), // 旧车折价，挂在换车专项下
			},
			expense: []domain.CategoryGroupAggregation{
				group("expense.fixed", 4000000),
				group("expense.family", 25000000), // 换车专项
			},
			dailyIncome: []domain.CategoryGroupAggregation{
				group("income.salary", 20000000),
				group("income.other", 0),
			},
			dailyExpense: []domain.CategoryGroupAggregation{
				group("expense.fixed", 4000000),
				group("expense.family", 0),
			},
			want: domain.ReportKPI{
				TotalIncome: 28000000, TotalExpense: 29000000,
				DailyIncome: 20000000, SpecialIncome: 8000000,
				DailyExpense: 4000000, SpecialExpense: 25000000,
				Surplus: -1000000, SurplusRate: -1000000.0 / 28000000,
				DailySurplus: 16000000, DailySurplusRate: 0.8,
				DiscretionRatio: 0, DiscretionWarning: false,
			},
		},
		{
			name:         "无专项：日常口径与全口径完全一致",
			income:       []domain.CategoryGroupAggregation{group("income.salary", 100000)},
			expense:      []domain.CategoryGroupAggregation{group("expense.fixed", 30000), group("expense.discretion", 10000)},
			dailyIncome:  []domain.CategoryGroupAggregation{group("income.salary", 100000)},
			dailyExpense: []domain.CategoryGroupAggregation{group("expense.fixed", 30000), group("expense.discretion", 10000)},
			want: domain.ReportKPI{
				TotalIncome: 100000, TotalExpense: 40000,
				DailyIncome: 100000, SpecialIncome: 0,
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
			dailyIncome:  []domain.CategoryGroupAggregation{group("income.salary", 100000)},
			dailyExpense: []domain.CategoryGroupAggregation{group("expense.fixed", 6500), group("expense.discretion", 3500)},
			want: domain.ReportKPI{
				TotalIncome: 100000, TotalExpense: 10000,
				DailyIncome: 100000, SpecialIncome: 0,
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
			dailyIncome: []domain.CategoryGroupAggregation{group("income.salary", 100000)},
			dailyExpense: []domain.CategoryGroupAggregation{
				group("expense.discretion", 0),
				group("expense.family", 0),
			},
			want: domain.ReportKPI{
				TotalIncome: 100000, TotalExpense: 500000,
				DailyIncome: 100000, SpecialIncome: 0,
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
			dailyIncome:  nil,
			dailyExpense: []domain.CategoryGroupAggregation{group("expense.discretion", 5000)},
			want: domain.ReportKPI{
				TotalIncome: 0, TotalExpense: 5000,
				DailyIncome: 0, SpecialIncome: 0,
				DailyExpense: 5000, SpecialExpense: 0,
				Surplus: -5000, SurplusRate: 0,
				DailySurplus: -5000, DailySurplusRate: 0,
				DiscretionRatio: 1, DiscretionWarning: true,
			},
		},
		{
			// 收入全部落在专项里（日常收入 0）：日常结余率同样不能除零，留 0。
			name:         "日常收入为 0：日常结余率留 0 不除零",
			income:       []domain.CategoryGroupAggregation{group("income.other", 800000)},
			expense:      []domain.CategoryGroupAggregation{group("expense.fixed", 200000)},
			dailyIncome:  []domain.CategoryGroupAggregation{group("income.other", 0)},
			dailyExpense: []domain.CategoryGroupAggregation{group("expense.fixed", 200000)},
			want: domain.ReportKPI{
				TotalIncome: 800000, TotalExpense: 200000,
				DailyIncome: 0, SpecialIncome: 800000,
				DailyExpense: 200000, SpecialExpense: 0,
				Surplus: 600000, SurplusRate: 0.75,
				DailySurplus: -200000, DailySurplusRate: 0,
				DiscretionRatio: 0, DiscretionWarning: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeKPI(tt.income, tt.expense, tt.dailyIncome, tt.dailyExpense)

			intFields := []struct {
				name      string
				got, want int64
			}{
				{"TotalIncome", got.TotalIncome, tt.want.TotalIncome},
				{"TotalExpense", got.TotalExpense, tt.want.TotalExpense},
				{"DailyIncome", got.DailyIncome, tt.want.DailyIncome},
				{"SpecialIncome", got.SpecialIncome, tt.want.SpecialIncome},
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
			// 收入侧同一条恒等式：报表把"其中：专项收入"拆出来时靠它对账
			if got.DailyIncome+got.SpecialIncome != got.TotalIncome {
				t.Errorf("TotalIncome(%d) != DailyIncome(%d) + SpecialIncome(%d)",
					got.TotalIncome, got.DailyIncome, got.SpecialIncome)
			}
			// Surplus 语义未变：仍是全口径真实现金流
			if got.Surplus != got.TotalIncome-got.TotalExpense {
				t.Errorf("Surplus(%d) != TotalIncome(%d) - TotalExpense(%d)（全口径语义被改动）",
					got.Surplus, got.TotalIncome, got.TotalExpense)
			}
			// DailySurplus 两侧都必须是日常口径
			if got.DailySurplus != got.DailyIncome-got.DailyExpense {
				t.Errorf("DailySurplus(%d) != DailyIncome(%d) - DailyExpense(%d)",
					got.DailySurplus, got.DailyIncome, got.DailyExpense)
			}
		})
	}
}

// TestComputeKPIDailySurplusIgnoresSpecialIncome 把缺陷 2 单独钉死：日常收支不变，
// 只往专项里塞收入（旧车折价、装修补贴），DailySurplus/DailySurplusRate 必须纹丝不动。
// 旧公式（分子 = TotalIncome）会让专项收入越大、"攒钱能力"越强。
func TestComputeKPIDailySurplusIgnoresSpecialIncome(t *testing.T) {
	dailyIncome := []domain.CategoryGroupAggregation{group("income.salary", 20000000)}
	dailyExpense := []domain.CategoryGroupAggregation{group("expense.fixed", 4000000)}

	tests := []struct {
		name          string
		specialIncome int64
		specialSpend  int64
	}{
		{"没有专项", 0, 0},
		{"只有专项支出", 0, 25000000},
		{"旧车折价 8 万 + 换车 25 万", 8000000, 25000000},
		{"专项收入远大于日常收入", 100000000, 25000000},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			income := []domain.CategoryGroupAggregation{
				group("income.salary", 20000000),
				group("income.other", tt.specialIncome),
			}
			expense := []domain.CategoryGroupAggregation{
				group("expense.fixed", 4000000),
				group("expense.family", tt.specialSpend),
			}
			got := computeKPI(income, expense, dailyIncome, dailyExpense)

			if got.DailySurplus != 16000000 {
				t.Fatalf("DailySurplus = %d; want 16000000（分子被专项收入污染了）", got.DailySurplus)
			}
			if math.Abs(got.DailySurplusRate-0.8) > kpiEps {
				t.Fatalf("DailySurplusRate = %v; want 0.8（专项收入不该抬高日常攒钱能力）", got.DailySurplusRate)
			}
			if got.SpecialIncome != tt.specialIncome {
				t.Fatalf("SpecialIncome = %d; want %d", got.SpecialIncome, tt.specialIncome)
			}

			// 旧公式对照：分子取全口径收入时，专项收入一进来比值就被抬高
			oldRate := float64(got.TotalIncome-got.DailyExpense) / float64(got.TotalIncome)
			if i >= 2 && oldRate <= 0.8 {
				t.Fatalf("对照用例失效：旧公式算出 %v，没能覆盖「被抬高」的场景", oldRate)
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
			got := computeKPI(income, expense, income, dailyExpense)

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
	// 本期专项里没有收入：日常收入 = 全口径收入
	if k.DailyIncome != 300000 || k.SpecialIncome != 0 {
		t.Fatalf("收入拆分 = daily %d / special %d; want 300000 / 0", k.DailyIncome, k.SpecialIncome)
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

	// DailyExpenseGroups / DailyIncomeGroups 是日常口径明细（上下文包按它组包）
	var dailyExpenseTotal int64
	for _, g := range data.DailyExpenseGroups {
		dailyExpenseTotal += g.Subtotal
	}
	if dailyExpenseTotal != k.DailyExpense {
		t.Fatalf("DailyExpenseGroups 合计 = %d; want %d", dailyExpenseTotal, k.DailyExpense)
	}
	var dailyIncomeTotal int64
	for _, g := range data.DailyIncomeGroups {
		dailyIncomeTotal += g.Subtotal
	}
	if dailyIncomeTotal != k.DailyIncome {
		t.Fatalf("DailyIncomeGroups 合计 = %d; want %d", dailyIncomeTotal, k.DailyIncome)
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

	// SpecialByProject 带专项 id（模板要链回 /specials?edit=<id>）
	want := []domain.SpecialAggregation{{SpecialID: "sp-reno", Name: "2026 老房装修", Amount: 150000}}
	if len(data.SpecialByProject) != 1 || data.SpecialByProject[0] != want[0] {
		t.Fatalf("SpecialByProject = %+v; want %+v", data.SpecialByProject, want)
	}
}

// TestQueryReportSpecialByProjectKeepsIdentity 同名专项（跨年各建了一个「装修」）
// 必须是两行、金额各归各的。曾经用专项名做 map key，两条会被静默合并成一行、金额相加，
// 而且行上拿不到 id，没法链回 /specials?edit=<id>。
func TestQueryReportSpecialByProjectKeepsIdentity(t *testing.T) {
	tests := []struct {
		name     string
		projects []domain.SpecialProject
		inPeriod map[string]int64
		want     []domain.SpecialAggregation
	}{
		{
			name: "两个同名专项不合并，按金额降序",
			projects: []domain.SpecialProject{
				{ID: "sp-2025", Name: "装修"},
				{ID: "sp-2026", Name: "装修"},
			},
			inPeriod: map[string]int64{"sp-2025": 30000, "sp-2026": 120000},
			want: []domain.SpecialAggregation{
				{SpecialID: "sp-2026", Name: "装修", Amount: 120000},
				{SpecialID: "sp-2025", Name: "装修", Amount: 30000},
			},
		},
		{
			name: "同名同额：按 id 定序，保证渲染稳定",
			projects: []domain.SpecialProject{
				{ID: "sp-b", Name: "装修"},
				{ID: "sp-a", Name: "装修"},
			},
			inPeriod: map[string]int64{"sp-a": 50000, "sp-b": 50000},
			want: []domain.SpecialAggregation{
				{SpecialID: "sp-a", Name: "装修", Amount: 50000},
				{SpecialID: "sp-b", Name: "装修", Amount: 50000},
			},
		},
		{
			name:     "专项已被删除：名字退回 id，但行还在，金额不丢",
			projects: nil,
			inPeriod: map[string]int64{"sp-ghost": 7000},
			want:     []domain.SpecialAggregation{{SpecialID: "sp-ghost", Name: "sp-ghost", Amount: 7000}},
		},
		{
			name: "专项内收入抵扣后为负（旧车折价大于本期支出）：仍单独成行",
			projects: []domain.SpecialProject{
				{ID: "sp-car", Name: "换车"},
				{ID: "sp-reno", Name: "装修"},
			},
			inPeriod: map[string]int64{"sp-car": -8000000, "sp-reno": 150000},
			want: []domain.SpecialAggregation{
				{SpecialID: "sp-reno", Name: "装修", Amount: 150000},
				{SpecialID: "sp-car", Name: "换车", Amount: -8000000},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txRepo := &fakeTransactionRepo{
				specialAgg: map[string][]domain.CategoryAggregation{
					"2026Q2": {{CategoryID: "expense.fixed.housing", Amount: 150000}},
				},
			}
			spRepo := &fakeSpecialProjectRepo{
				projects: tt.projects,
				inPeriod: map[string]map[string]int64{"2026Q2": tt.inPeriod},
			}
			uc := NewQueryReport(txRepo, &fakeCategoryRepo{cats: testCategories()}).WithSpecialRepo(spRepo)

			p, _ := domain.ParsePeriod("2026Q2")
			data, err := uc.Execute(context.Background(), p, domain.AccountFamily)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(data.SpecialByProject) != len(tt.want) {
				t.Fatalf("SpecialByProject = %+v; want %d 行 %+v", data.SpecialByProject, len(tt.want), tt.want)
			}
			for i := range tt.want {
				if data.SpecialByProject[i] != tt.want[i] {
					t.Fatalf("SpecialByProject[%d] = %+v; want %+v", i, data.SpecialByProject[i], tt.want[i])
				}
			}
		})
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
	if data.KPI.SpecialIncome != 0 || data.KPI.DailyIncome != data.KPI.TotalIncome {
		t.Fatalf("无专项时收入侧 daily 应等于 total：daily %d / total %d / special %d",
			data.KPI.DailyIncome, data.KPI.TotalIncome, data.KPI.SpecialIncome)
	}
}
