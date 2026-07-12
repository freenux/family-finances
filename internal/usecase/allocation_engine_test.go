package usecase

import (
	"math"
	"testing"

	"family-finances/internal/domain"
)

// balancedProfile → C3（平衡 3 + 中年限 1 + 中稳定 1 = 5），权益上限 50%
func balancedProfile() domain.FamilyProfile {
	return domain.FamilyProfile{
		MainAge: 35, RiskAppetite: domain.RiskBalanced,
		Horizon: domain.HorizonMedium, IncomeStability: domain.IncomeMedium,
	}
}

func bandByKey(t *testing.T, r AllocationResult, key string) AllocationBand {
	t.Helper()
	for _, b := range r.Bands {
		if b.Key == key {
			return b
		}
	}
	t.Fatalf("band %s not found in %+v", key, r.Bands)
	return AllocationBand{}
}

func TestComputeAllocationBands(t *testing.T) {
	cases := []struct {
		name       string
		profile    domain.FamilyProfile
		snap       map[string]int64
		key        string
		wantMin    float64
		wantMax    float64
		wantCur    float64
		wantStatus string
	}{
		{
			name:    "C3 权益区间 [35,50]，现状 40% 在区间内",
			profile: balancedProfile(),
			snap: map[string]int64{
				"asset.cash": 30000, "asset.deposit": 30000,
				"asset.equity_cn": 40000, // investable=100000, equity 40%
			},
			key: "band_equity_share", wantMin: 35, wantMax: 50, wantCur: 40, wantStatus: "within",
		},
		{
			name:    "权益 60% 超出 C3 上限判 above",
			profile: balancedProfile(),
			snap: map[string]int64{
				"asset.cash": 40000, "asset.equity_cn": 60000,
			},
			key: "band_equity_share", wantMin: 35, wantMax: 50, wantCur: 60, wantStatus: "above",
		},
		{
			name:    "权益 10% 低于下限判 below",
			profile: balancedProfile(),
			snap: map[string]int64{
				"asset.cash": 90000, "asset.equity_cn": 10000,
			},
			key: "band_equity_share", wantMin: 35, wantMax: 50, wantCur: 10, wantStatus: "below",
		},
		{
			name:    "C1 区间下限触底 5%",
			profile: domain.FamilyProfile{MainAge: 65, RiskAppetite: domain.RiskConservative, Horizon: domain.HorizonShort, IncomeStability: domain.IncomeUnstable},
			snap:    map[string]int64{"asset.cash": 90000, "asset.equity_cn": 10000},
			key:     "band_equity_share", wantMin: 5, wantMax: 20, wantCur: 10, wantStatus: "within",
		},
		{
			name:    "海外占长期桶 40% 超 C3 上限 20%",
			profile: balancedProfile(),
			snap: map[string]int64{
				"asset.equity_cn": 60000, "asset.equity_global": 40000,
			},
			key: "band_overseas_share", wantMin: 0, wantMax: 20, wantCur: 40, wantStatus: "above",
		},
		{
			name:    "黄金 10% 恰好在上限（边界 within）",
			profile: balancedProfile(),
			snap: map[string]int64{
				"asset.equity_cn": 90000, "asset.gold": 10000,
			},
			key: "band_gold_share", wantMin: 0, wantMax: 10, wantCur: 10, wantStatus: "within",
		},
		{
			name:    "空长期桶现状 0% 不越界",
			profile: balancedProfile(),
			snap:    map[string]int64{"asset.cash": 100000},
			key:     "band_gold_share", wantMin: 0, wantMax: 10, wantCur: 0, wantStatus: "within",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ComputeAllocation(tc.profile, true, tc.snap)
			b := bandByKey(t, r, tc.key)
			if math.Abs(b.MinPct-tc.wantMin) > 1e-9 || math.Abs(b.MaxPct-tc.wantMax) > 1e-9 {
				t.Fatalf("band [%v,%v]; want [%v,%v]", b.MinPct, b.MaxPct, tc.wantMin, tc.wantMax)
			}
			if math.Abs(b.CurrentPct-tc.wantCur) > 1e-9 {
				t.Fatalf("CurrentPct = %v; want %v", b.CurrentPct, tc.wantCur)
			}
			if b.Status != tc.wantStatus {
				t.Fatalf("Status = %s; want %s", b.Status, tc.wantStatus)
			}
		})
	}
}

func TestComputeAllocationOverseasCapVariesByGrade(t *testing.T) {
	cases := []struct {
		grade   string
		profile domain.FamilyProfile
		wantCap float64
	}{
		{"C1", domain.FamilyProfile{MainAge: 65, RiskAppetite: domain.RiskConservative, Horizon: domain.HorizonShort, IncomeStability: domain.IncomeUnstable}, 10},
		{"C3", balancedProfile(), 20},
		{"C5", domain.FamilyProfile{MainAge: 30, RiskAppetite: domain.RiskAggressive, Horizon: domain.HorizonLong, IncomeStability: domain.IncomeStable}, 30},
	}
	for _, tc := range cases {
		t.Run(tc.grade, func(t *testing.T) {
			r := ComputeAllocation(tc.profile, true, map[string]int64{})
			if r.Grade != tc.grade {
				t.Fatalf("Grade = %s; want %s", r.Grade, tc.grade)
			}
			b := bandByKey(t, r, "band_overseas_share")
			if b.MaxPct != tc.wantCap {
				t.Fatalf("overseas cap = %v; want %v", b.MaxPct, tc.wantCap)
			}
		})
	}
}

func TestComputeAllocationNoSnapshot(t *testing.T) {
	r := ComputeAllocation(balancedProfile(), true, nil)
	if r.HasSnapshot {
		t.Fatal("HasSnapshot = true; want false")
	}
	for _, b := range r.Bands {
		if b.Status != "unknown" || b.HasCurrent {
			t.Fatalf("band %s = %+v; want unknown/no current", b.Key, b)
		}
	}
	if len(r.OutOfBand()) != 0 {
		t.Fatalf("OutOfBand() = %+v; want empty when snapshot missing", r.OutOfBand())
	}
}

func TestComputeAllocationAssumptionKeysStable(t *testing.T) {
	r := ComputeAllocation(balancedProfile(), true, map[string]int64{})
	want := []string{"assume_grade", "assume_equity_band", "assume_investable_scope", "assume_overseas_cap", "assume_gold_cap"}
	if len(r.Assumptions) != len(want) {
		t.Fatalf("assumptions = %+v; want %d", r.Assumptions, len(want))
	}
	for i, k := range want {
		if r.Assumptions[i].Key != k {
			t.Fatalf("assumptions[%d].Key = %s; want %s", i, r.Assumptions[i].Key, k)
		}
	}
}

func TestAllocationOutOfBand(t *testing.T) {
	r := ComputeAllocation(balancedProfile(), true, map[string]int64{
		"asset.cash": 40000, "asset.equity_cn": 36000, "asset.equity_global": 24000,
	})
	// investable=100000: equity 60% > 50 above; long=60000: overseas 40% > 20 above; gold 0 within
	out := r.OutOfBand()
	if len(out) != 2 {
		t.Fatalf("OutOfBand() = %+v; want 2 entries", out)
	}
	if out[0].Key != "band_equity_share" || out[1].Key != "band_overseas_share" {
		t.Fatalf("OutOfBand keys = %s,%s", out[0].Key, out[1].Key)
	}
}
