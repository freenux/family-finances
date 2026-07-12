package usecase

import (
	"math"
	"testing"

	"family-finances/internal/domain"
)

func fenPtr(v int64) *int64 { return &v }

func snapWith(data map[string]int64) *domain.AssetSnapshot {
	return &domain.AssetSnapshot{Period: "2026Q2", Data: data}
}

func TestComputeBucketsLiquid(t *testing.T) {
	cases := []struct {
		name         string
		in           BucketInputs
		wantStatus   BucketStatus
		wantCoverage float64
		wantSource   string
	}{
		{
			name: "画像月支出优先于流水折算",
			in: BucketInputs{
				Snapshot:                  snapWith(map[string]int64{"asset.cash": 6000000, "asset.mmf": 6000000}),
				Profile:                   &domain.FamilyProfile{MonthlyExpenseFen: fenPtr(2000000), EmergencyMonths: 6},
				TrailingMonthlyExpenseFen: 9999999,
			},
			wantStatus: BucketOK, wantCoverage: 6, wantSource: "profile",
		},
		{
			name: "画像未填月支出时用流水折算",
			in: BucketInputs{
				Snapshot:                  snapWith(map[string]int64{"asset.cash": 3000000}),
				Profile:                   &domain.FamilyProfile{EmergencyMonths: 6},
				TrailingMonthlyExpenseFen: 1000000,
			},
			wantStatus: BucketLow, wantCoverage: 3, wantSource: "transactions",
		},
		{
			name: "覆盖超过目标 2 倍判偏高",
			in: BucketInputs{
				Snapshot:                  snapWith(map[string]int64{"asset.cash": 26000000}),
				Profile:                   &domain.FamilyProfile{MonthlyExpenseFen: fenPtr(2000000), EmergencyMonths: 6},
				TrailingMonthlyExpenseFen: 0,
			},
			wantStatus: BucketHigh, wantCoverage: 13, wantSource: "profile",
		},
		{
			name: "无月支出数据无法诊断",
			in: BucketInputs{
				Snapshot: snapWith(map[string]int64{"asset.cash": 100}),
			},
			wantStatus: BucketUnknown, wantCoverage: 0, wantSource: "",
		},
		{
			name: "覆盖恰好等于目标为达标（边界）",
			in: BucketInputs{
				Snapshot: snapWith(map[string]int64{"asset.cash": 12000000}),
				Profile:  &domain.FamilyProfile{MonthlyExpenseFen: fenPtr(2000000), EmergencyMonths: 6},
			},
			wantStatus: BucketOK, wantCoverage: 6, wantSource: "profile",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := ComputeBuckets(tc.in)
			if d.Liquid.Status != tc.wantStatus {
				t.Fatalf("Liquid.Status = %s; want %s (%+v)", d.Liquid.Status, tc.wantStatus, d.Liquid)
			}
			if math.Abs(d.Liquid.CoverageMonths-tc.wantCoverage) > 1e-9 {
				t.Fatalf("CoverageMonths = %v; want %v", d.Liquid.CoverageMonths, tc.wantCoverage)
			}
			if d.Liquid.ExpenseSource != tc.wantSource {
				t.Fatalf("ExpenseSource = %q; want %q", d.Liquid.ExpenseSource, tc.wantSource)
			}
		})
	}
}

func TestComputeBucketsSteady(t *testing.T) {
	goals := []domain.FinancialGoal{
		{TargetAmount: 10000000, YearsToAchieve: "2"},   // 3 年内 ✓
		{TargetAmount: 5000000, YearsToAchieve: "3-5"},  // 下界 3 ✓
		{TargetAmount: 99999999, YearsToAchieve: "10+"}, // 10 年后 ✗
		{TargetAmount: 88888888, YearsToAchieve: ""},    // 无法解析 ✗
	}
	cases := []struct {
		name       string
		steadyFen  int64
		goals      []domain.FinancialGoal
		wantStatus BucketStatus
		wantGoals  int64
		wantCount  int
	}{
		{"无目标登记为 unknown", 10000000, nil, BucketUnknown, 0, 0},
		{"稳健足以覆盖 3 年内目标", 20000000, goals, BucketOK, 15000000, 2},
		{"稳健不足出现缺口", 10000000, goals, BucketLow, 15000000, 2},
		{"恰好覆盖为达标（边界）", 15000000, goals, BucketOK, 15000000, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := ComputeBuckets(BucketInputs{
				Snapshot: snapWith(map[string]int64{"asset.deposit": tc.steadyFen}),
				Goals:    tc.goals,
			})
			if d.Steady.Status != tc.wantStatus || d.Steady.Goals3yFen != tc.wantGoals || d.Steady.GoalCount != tc.wantCount {
				t.Fatalf("Steady = %+v; want status=%s goals=%d count=%d", d.Steady, tc.wantStatus, tc.wantGoals, tc.wantCount)
			}
		})
	}
}

func TestComputeBucketsLongTermShares(t *testing.T) {
	d := ComputeBuckets(BucketInputs{
		Snapshot: snapWith(map[string]int64{
			"asset.equity_cn":     60000,
			"asset.equity_global": 30000,
			"asset.gold":          10000,
		}),
	})
	lt := d.LongTerm
	if lt.AmountFen != 100000 {
		t.Fatalf("AmountFen = %d; want 100000", lt.AmountFen)
	}
	if math.Abs(lt.EquityPct-90) > 1e-9 || math.Abs(lt.GlobalPct-30) > 1e-9 || math.Abs(lt.GoldPct-10) > 1e-9 {
		t.Fatalf("shares = %.1f/%.1f/%.1f; want 90/30/10", lt.EquityPct, lt.GlobalPct, lt.GoldPct)
	}

	empty := ComputeBuckets(BucketInputs{})
	if empty.LongTerm.AmountFen != 0 || empty.LongTerm.EquityPct != 0 {
		t.Fatalf("empty long term = %+v; want zeros", empty.LongTerm)
	}
}

func TestComputeBucketsInsurance(t *testing.T) {
	life := domain.InsurancePolicy{InsuranceType: "定期寿险", CoverageAmount: 400000000, AnnualPremium: 1000000}
	critical := domain.InsurancePolicy{InsuranceType: "重疾险", CoverageAmount: 50000000, AnnualPremium: 1500000}

	cases := []struct {
		name        string
		policies    []domain.InsurancePolicy
		income      int64
		wantStatus  BucketStatus
		wantPremOK  bool
		wantLifeOK  bool
		wantMulti   float64
		wantPremPct float64
	}{
		{"无保单为未登记", nil, 40000000, BucketUnknown, false, false, 0, 0},
		{"有保单但未填年收入无法双十", []domain.InsurancePolicy{life}, 0, BucketUnknown, false, false, 0, 0},
		{
			"双十全达标", []domain.InsurancePolicy{life, critical}, 40000000,
			BucketOK, true, true, 10, 0.0625,
		},
		{
			"保费超 10% 判偏低",
			[]domain.InsurancePolicy{{InsuranceType: "寿险", CoverageAmount: 400000000, AnnualPremium: 5000000}},
			40000000, BucketLow, false, true, 10, 0.125,
		},
		{
			"寿险保额不足 10 倍判偏低",
			[]domain.InsurancePolicy{{InsuranceType: "寿险", CoverageAmount: 100000000, AnnualPremium: 1000000}},
			40000000, BucketLow, true, false, 2.5, 0.025,
		},
		{
			"重疾保额不计入寿险倍数",
			[]domain.InsurancePolicy{critical},
			40000000, BucketLow, true, false, 0, 0.0375,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := ComputeBuckets(BucketInputs{
				Profile:  &domain.FamilyProfile{AnnualIncomeFen: tc.income},
				Policies: tc.policies,
			})
			ins := d.Insurance
			if ins.Status != tc.wantStatus {
				t.Fatalf("Status = %s; want %s (%+v)", ins.Status, tc.wantStatus, ins)
			}
			if ins.Status == BucketUnknown {
				return
			}
			if ins.PremiumOK != tc.wantPremOK || ins.LifeCoverageOK != tc.wantLifeOK {
				t.Fatalf("PremiumOK=%v LifeOK=%v; want %v/%v", ins.PremiumOK, ins.LifeCoverageOK, tc.wantPremOK, tc.wantLifeOK)
			}
			if math.Abs(ins.LifeCoverageMultiple-tc.wantMulti) > 1e-9 {
				t.Fatalf("LifeCoverageMultiple = %v; want %v", ins.LifeCoverageMultiple, tc.wantMulti)
			}
			if math.Abs(ins.PremiumRatio-tc.wantPremPct) > 1e-9 {
				t.Fatalf("PremiumRatio = %v; want %v", ins.PremiumRatio, tc.wantPremPct)
			}
		})
	}
}

func TestComputeBucketsFindingsKeysStable(t *testing.T) {
	d := ComputeBuckets(BucketInputs{})
	want := []string{"bucket_liquid", "bucket_steady", "bucket_longterm", "bucket_insurance"}
	if len(d.Findings) != len(want) {
		t.Fatalf("findings = %+v; want %d entries", d.Findings, len(want))
	}
	for i, k := range want {
		if d.Findings[i].Key != k {
			t.Fatalf("findings[%d].Key = %s; want %s", i, d.Findings[i].Key, k)
		}
	}
}
