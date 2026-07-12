package usecase

import (
	"math"
	"testing"
	"time"

	"family-finances/internal/domain"
)

func TestDrawdownRangeInterpolation(t *testing.T) {
	cases := []struct {
		equity   float64
		wantLow  float64
		wantHigh float64
	}{
		{20, 5, 10},    // 锚点
		{50, 15, 25},   // 锚点
		{80, 25, 40},   // 锚点
		{35, 10, 17.5}, // 20–50 中点
		{65, 20, 32.5}, // 50–80 中点
		{10, 5, 10},    // 下界截断
		{90, 25, 40},   // 上界截断
	}
	for _, tc := range cases {
		low, high := drawdownRange(tc.equity)
		if math.Abs(low-tc.wantLow) > 1e-9 || math.Abs(high-tc.wantHigh) > 1e-9 {
			t.Fatalf("drawdownRange(%v) = (%v,%v); want (%v,%v)", tc.equity, low, high, tc.wantLow, tc.wantHigh)
		}
	}
}

func TestBlendedReturn(t *testing.T) {
	low, high := blendedReturn(50)
	if math.Abs(low-3.0) > 1e-9 || math.Abs(high-5.5) > 1e-9 {
		t.Fatalf("blendedReturn(50) = (%v,%v); want (3.0,5.5)", low, high)
	}
	low0, high0 := blendedReturn(0)
	if low0 != 2.0 || high0 != 3.0 {
		t.Fatalf("blendedReturn(0) = (%v,%v); want 固收区间", low0, high0)
	}
}

func TestMonthsToReach(t *testing.T) {
	cases := []struct {
		name    string
		target  int64
		current int64
		monthly int64
		rate    float64
		wantMax int // 月数上限断言
		wantMin int // 月数下限断言
		wantOK  bool
	}{
		{"已达标为 0", 100, 100, 0, 5, 0, 0, true},
		{"零利率直线攒钱 10 个月", 1000, 0, 100, 0, 10, 10, true},
		{"高利率快于零利率", 1000000, 500000, 10000, 8, 40, 1, true},
		{"无月供无利率不可达", 1000, 0, 0, 0, 600, 600, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			months, ok := monthsToReach(tc.target, tc.current, tc.monthly, tc.rate)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if months < tc.wantMin || months > tc.wantMax {
				t.Fatalf("months = %d; want [%d,%d]", months, tc.wantMin, tc.wantMax)
			}
		})
	}
	// 单调性：利率越高月数越少（或相等）
	zero, _ := monthsToReach(2000000, 0, 20000, 0)
	high, _ := monthsToReach(2000000, 0, 20000, 8)
	if high > zero {
		t.Fatalf("高利率月数 %d 不应多于零利率 %d", high, zero)
	}
}

func TestComputeScenarios(t *testing.T) {
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	goals := []domain.FinancialGoal{
		{Description: "教育金", TargetAmount: 80000000, CurrentAmountFen: 30000000, TargetYM: "2039-09"}, // 最早
		{Description: "养老", TargetAmount: 100000000, TargetYM: "2050-01"},
		{Description: "无期限", TargetAmount: 100},
	}
	presets := ComputeScenarios(goals, nil, now)
	if len(presets) != 3 {
		t.Fatalf("presets = %d; want 3", len(presets))
	}
	if presets[0].Key != "conservative" || presets[2].Key != "aggressive" {
		t.Fatalf("preset 顺序错误: %+v", presets)
	}
	for _, p := range presets {
		if !p.HasGoal || p.GoalName != "教育金" {
			t.Fatalf("preset %s 应选最早目标教育金: %+v", p.Key, p)
		}
		if !p.Reachable {
			t.Fatalf("preset %s 应可达: %+v", p.Key, p)
		}
		if p.MonthsBest > p.MonthsWorst {
			t.Fatalf("preset %s: 上界收益月数 %d 不应多于下界 %d", p.Key, p.MonthsBest, p.MonthsWorst)
		}
		if p.MonthsWorst > p.BaselineMonths {
			t.Fatalf("preset %s: 有收益时不应慢于 0%% 直线口径 %d < %d", p.Key, p.BaselineMonths, p.MonthsWorst)
		}
	}
	// 进取档回报与回撤都更高
	if presets[2].ReturnHighPct <= presets[0].ReturnHighPct || presets[2].DrawdownHighPct <= presets[0].DrawdownHighPct {
		t.Fatalf("进取档区间应高于保守档: %+v vs %+v", presets[2], presets[0])
	}
	// 无目标时 HasGoal=false
	empty := ComputeScenarios(nil, nil, now)
	for _, p := range empty {
		if p.HasGoal {
			t.Fatalf("无目标时 HasGoal 应为 false: %+v", p)
		}
	}
}
