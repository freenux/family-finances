package usecase

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"family-finances/internal/domain"
)

const packEps = 1e-9

// renovationFixture 造一个"装修/换车季"的夹具：
//
//	日常收入 20 万元、日常支出 4 万元（其中自由裁量 2 万元）；
//	专项支出 25 万元、专项收入 8 万元（旧车折价）。
//
// 上一期（2026Q1）日常收入 18 万元、日常支出 3 万元，没有专项。
// 回溯 8 个季度的桶都达标（收入 1 万元 / 支出 0.5 万元 → 结余率 50%）。
func renovationFixture(p domain.Period) (*fakeTransactionRepo, *fakeSpecialProjectRepo) {
	txRepo := &fakeTransactionRepo{
		periodAgg: map[string][]domain.CategoryAggregation{
			"2026Q2": {
				{CategoryID: "income.salary.husband", Amount: 20000000},
				{CategoryID: "expense.discretion.shopping", Amount: 2000000},
				{CategoryID: "expense.fixed.housing", Amount: 2000000},
			},
			"2026Q1": {
				{CategoryID: "income.salary.husband", Amount: 18000000},
				{CategoryID: "expense.discretion.shopping", Amount: 1000000},
				{CategoryID: "expense.fixed.housing", Amount: 2000000},
			},
		},
		specialAgg: map[string][]domain.CategoryAggregation{
			"2026Q2": {
				// 换车：支出记在居住成本以外的科目上无所谓，这里复用 housing 表示大额专项支出
				{CategoryID: "expense.fixed.housing", Amount: 25000000},
				// 旧车折价：专项内的收入
				{CategoryID: "income.salary.husband", Amount: 8000000},
			},
		},
		bucketAmts:    map[string]int64{},
		incomeBuckets: map[string]int64{},
	}
	cur := p
	for i := 0; i < maxStreakLookback; i++ {
		cur = cur.Previous()
		txRepo.incomeBuckets[cur.Label] = 1000000
		txRepo.bucketAmts[cur.Label] = 500000
	}
	spRepo := &fakeSpecialProjectRepo{
		projects: []domain.SpecialProject{
			{ID: "sp-car", Name: "2026 换车"},
			{ID: "sp-reno", Name: "老房装修"},
		},
		inPeriod: map[string]map[string]int64{
			// 净额：换车 25 万支出 − 8 万折价 = 17 万；装修本期没花钱
			"2026Q2": {"sp-car": 17000000},
		},
	}
	return txRepo, spRepo
}

func buildRenovationPack(t *testing.T) ContextPack {
	t.Helper()
	p, err := domain.ParsePeriod("2026Q2")
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	txRepo, spRepo := renovationFixture(p)
	qr := NewQueryReport(txRepo, &fakeCategoryRepo{cats: testCategories()}).WithSpecialRepo(spRepo)
	builder := NewContextPackBuilder(qr, &fakeAssetSnapshotRepo{}, txRepo)
	pack, err := builder.Build(context.Background(), p)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return pack
}

func sectionSum(sec MoneySection) int64 {
	var total int64
	for _, g := range sec.Groups {
		total += g.AmountFen
	}
	return total
}

// TestContextPackIsInternallyConsistent 缺陷 5：喂给 LLM 的包必须内部自洽。
// 修复前 Expense 明细与 total_fen 是全口径（29 万元），kpi.discretion_ratio 的分母
// 却是日常口径（4 万元）——LLM 按 groups 自己算得到 6.9%，包里写着 50%，
// 生成的两段话必然打架。
func TestContextPackIsInternallyConsistent(t *testing.T) {
	pack := buildRenovationPack(t)

	tests := []struct {
		name      string
		got, want int64
	}{
		{"income.total_fen 取日常口径", pack.Income.TotalFen, 20000000},
		{"income.groups 合计等于 total_fen", sectionSum(pack.Income), pack.Income.TotalFen},
		{"expense.total_fen 取日常口径", pack.Expense.TotalFen, 4000000},
		{"expense.groups 合计等于 total_fen", sectionSum(pack.Expense), pack.Expense.TotalFen},
		{"日常 + 专项 = 全口径支出", pack.Expense.TotalFen + pack.Special.ExpenseFen, 29000000},
		{"日常 + 专项 = 全口径收入", pack.Income.TotalFen + pack.Special.IncomeFen, 28000000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s: got %d; want %d", tt.name, tt.got, tt.want)
			}
		})
	}

	// 核心：LLM 拿 expense.groups 自己算出来的自由裁量占比，必须等于 kpi.discretion_ratio
	var discretion int64
	for _, g := range pack.Expense.Groups {
		if strings.HasPrefix(g.CategoryID, "expense.discretion.") {
			discretion += g.AmountFen
		}
	}
	selfComputed := float64(discretion) / float64(pack.Expense.TotalFen)
	if math.Abs(selfComputed-pack.KPI.DiscretionRatio) > packEps {
		t.Fatalf("按 expense.groups 自己算出 %v，包里 kpi.discretion_ratio = %v（分子分母不同口径）",
			selfComputed, pack.KPI.DiscretionRatio)
	}
	if !pack.KPI.DiscretionAlert {
		t.Fatalf("discretion_alert = false; want true（50%% 已超 35%%）")
	}

	// 结余率同理：用 income/expense 两节能反推出 kpi.savings_rate
	selfSavings := float64(pack.Income.TotalFen-pack.Expense.TotalFen) / float64(pack.Income.TotalFen)
	if math.Abs(selfSavings-pack.KPI.SavingsRate) > packEps {
		t.Fatalf("按 income/expense 自己算出 %v，包里 kpi.savings_rate = %v", selfSavings, pack.KPI.SavingsRate)
	}
	if math.Abs(pack.KPI.SavingsRate-0.8) > packEps {
		t.Fatalf("kpi.savings_rate = %v; want 0.8（日常口径）", pack.KPI.SavingsRate)
	}
	// 全口径那一档单独给，别把真实现金流藏起来
	wantAll := float64(28000000-29000000) / float64(28000000)
	if math.Abs(pack.KPI.SavingsRateAllScope-wantAll) > packEps {
		t.Fatalf("kpi.savings_rate_all_scope = %v; want %v", pack.KPI.SavingsRateAllScope, wantAll)
	}

	// 环比也必须同口径（日常）：2026Q2 日常支出 4 万元 − 2026Q1 的 3 万元 = +1 万元。
	// 若还用全口径，这里会是 +26 万元，LLM 每逢装修季都会喊"支出暴涨"。
	if pack.Compare.ExpenseDelta != 1000000 {
		t.Fatalf("compare.expense_delta = %d; want 1000000（日常口径环比）", pack.Compare.ExpenseDelta)
	}
	if pack.Compare.IncomeDelta != 2000000 {
		t.Fatalf("compare.income_delta = %d; want 2000000（日常口径环比）", pack.Compare.IncomeDelta)
	}
}

// TestContextPackSpecialSection 专项单列一节，含各专项金额（缺陷 5 的后半）
func TestContextPackSpecialSection(t *testing.T) {
	pack := buildRenovationPack(t)

	if pack.Special == nil {
		t.Fatal("pack.special = nil; want 专项一节")
	}
	if pack.Special.ExpenseFen != 25000000 || pack.Special.IncomeFen != 8000000 || pack.Special.NetFen != 17000000 {
		t.Fatalf("special = 支出 %d / 收入 %d / 净额 %d; want 25000000 / 8000000 / 17000000",
			pack.Special.ExpenseFen, pack.Special.IncomeFen, pack.Special.NetFen)
	}
	want := []SpecialProjectEntry{{Name: "2026 换车", NetFen: 17000000}}
	if len(pack.Special.Projects) != len(want) || pack.Special.Projects[0] != want[0] {
		t.Fatalf("special.projects = %+v; want %+v", pack.Special.Projects, want)
	}
	var groupTotal int64
	for _, g := range pack.Special.Groups {
		groupTotal += g.AmountFen
	}
	if groupTotal != pack.Special.ExpenseFen {
		t.Fatalf("special.groups 合计 = %d; want %d", groupTotal, pack.Special.ExpenseFen)
	}
	if pack.ScopeNote == "" {
		t.Fatal("pack.scope_note 为空；LLM 无从知道 income/expense 是哪一档口径")
	}

	// 包要能稳定序列化（prompt 里是 JSON，顺序抖动会让缓存与回归都失效）
	first, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := json.Marshal(buildRenovationPack(t))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(first) != string(again) {
			t.Fatalf("同一份数据两次组包 JSON 不一致：\n%s\n%s", first, again)
		}
	}
}

// TestContextPackNoSpecialOmitsSection 没有专项时不凭空造出 special 一节，
// 且两档结余率相等（LLM 不必解释一个不存在的差异）
func TestContextPackNoSpecialOmitsSection(t *testing.T) {
	txRepo := &fakeTransactionRepo{periodAgg: map[string][]domain.CategoryAggregation{
		"2026Q2": {
			{CategoryID: "income.salary.husband", Amount: 10000000},
			{CategoryID: "expense.discretion.shopping", Amount: 2000000},
		},
	}}
	qr := NewQueryReport(txRepo, &fakeCategoryRepo{cats: testCategories()})
	builder := NewContextPackBuilder(qr, &fakeAssetSnapshotRepo{}, txRepo)
	p, _ := domain.ParsePeriod("2026Q2")
	pack, err := builder.Build(context.Background(), p)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if pack.Special != nil {
		t.Fatalf("pack.special = %+v; want nil（本期没有任何专项收支）", pack.Special)
	}
	if pack.Income.TotalFen != 10000000 || pack.Expense.TotalFen != 2000000 {
		t.Fatalf("无专项时两档口径应相等：income %d / expense %d", pack.Income.TotalFen, pack.Expense.TotalFen)
	}
	if math.Abs(pack.KPI.SavingsRate-pack.KPI.SavingsRateAllScope) > packEps {
		t.Fatalf("无专项时 savings_rate(%v) 应等于 savings_rate_all_scope(%v)",
			pack.KPI.SavingsRate, pack.KPI.SavingsRateAllScope)
	}
}

// TestSavingsRateStreakSeededWithDailyRate 缺陷 4：连胜的起始判据必须是日常口径。
// 装修季全口径结余率 −3.6%（< 30%），日常口径 80%（达标）。用全口径当种子的话，
// savingsRateStreak 第一道 guard 就返回 0，连胜断在这个改动本来要保护的场景里。
func TestSavingsRateStreakSeededWithDailyRate(t *testing.T) {
	pack := buildRenovationPack(t)

	var text string
	for _, f := range pack.Findings {
		if f.Key == "savings_rate" {
			text = f.Text
		}
	}
	if text == "" {
		t.Fatalf("findings 里没有 savings_rate；findings = %+v", pack.Findings)
	}
	// 8 期回溯桶全部达标 + 当期 = 9
	if !strings.Contains(text, "连续 9 期") {
		t.Fatalf("savings_rate = %q; want 含「连续 9 期」（起始判据用了全口径，连胜被判负）", text)
	}
	if !strings.Contains(text, "80.0%") {
		t.Fatalf("savings_rate = %q; want 当期值用日常口径 80.0%%", text)
	}
	// 全口径那一档也要写出来，否则读者以为装修没花钱
	if !strings.Contains(text, "-3.6%") {
		t.Fatalf("savings_rate = %q; want 含全口径 -3.6%%", text)
	}
}

// TestContextPackTopExpenseChangeUsesDailyGroups 类目环比同样喂日常口径分组：
// 装修记在居住成本下，全口径会让这条 finding 每期都指着被专项顶起来的科目喊涨。
func TestContextPackTopExpenseChangeUsesDailyGroups(t *testing.T) {
	pack := buildRenovationPack(t)

	var text, key string
	for _, f := range pack.Findings {
		if strings.HasPrefix(f.Key, "expense_top_change_") {
			key, text = f.Key, f.Text
		}
	}
	if key == "" {
		t.Fatalf("findings 里没有 expense_top_change_*；findings = %+v", pack.Findings)
	}
	// 日常口径下：购物 1 万 → 2 万元（+100%），居住 2 万 → 2 万元（0%）。
	// 全口径下居住会是 2 万 → 27 万元（+1250%），会顶掉购物成为 Top。
	if key != "expense_top_change_expense.discretion.shopping" {
		t.Fatalf("expense_top_change key = %q; want 购物消费（居住被专项顶上来了）", key)
	}
	if !strings.Contains(text, "+100.0%") {
		t.Fatalf("text = %q; want +100.0%%", text)
	}
}
