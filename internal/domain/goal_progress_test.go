package domain

import (
	"testing"
	"time"
)

var goalNow = time.Date(2026, 7, 12, 10, 0, 0, 0, time.Local)

func TestMonthsUntilTarget(t *testing.T) {
	cases := []struct {
		name       string
		targetYM   string
		years      string
		wantMonths int
		wantOK     bool
	}{
		{"target_ym 未来", "2027-07", "", 12, true},
		{"target_ym 同月下限 1", "2026-07", "", 1, true},
		{"target_ym 已过期下限 1", "2025-01", "", 1, true},
		{"target_ym 优先于 years", "2026-10", "10+", 3, true},
		{"target_ym 非法格式回退失败", "2027/07", "", 0, false},
		{"回退 years 下界", "", "3", 36, true},
		{"years 0 年下限 1 个月", "", "0", 1, true},
		{"两者皆无", "", "", 0, false},
		{"years 无法解析", "", "长期", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := FinancialGoal{TargetYM: tc.targetYM, YearsToAchieve: tc.years}
			months, ok := g.MonthsUntilTarget(goalNow)
			if months != tc.wantMonths || ok != tc.wantOK {
				t.Fatalf("MonthsUntilTarget() = (%d, %v); want (%d, %v)", months, ok, tc.wantMonths, tc.wantOK)
			}
		})
	}
}

func TestCalcGoalProgress(t *testing.T) {
	snap := map[string]int64{"asset.deposit": 30000000, "asset.wealth": 10000000}
	cases := []struct {
		name        string
		g           FinancialGoal
		snap        map[string]int64
		wantCurrent int64
		wantLinked  bool
		wantMonthly int64
		wantHasDL   bool
		wantMonths  int
	}{
		{
			name:        "手填 current + target_ym",
			g:           FinancialGoal{TargetAmount: 12000000, CurrentAmountFen: 6000000, TargetYM: "2027-07"},
			snap:        snap,
			wantCurrent: 6000000, wantLinked: false,
			wantMonthly: 500000, wantHasDL: true, wantMonths: 12, // 缺口 60000 元 / 12 月
		},
		{
			name:        "linked_codes 覆盖手填值",
			g:           FinancialGoal{TargetAmount: 50000000, CurrentAmountFen: 1, LinkedCodes: []string{"asset.deposit", "asset.wealth"}, TargetYM: "2027-07"},
			snap:        snap,
			wantCurrent: 40000000, wantLinked: true,
			wantMonthly: 833333, wantHasDL: true, wantMonths: 12, // 缺口 100000 元 / 12
		},
		{
			name:        "linked 但无快照回退手填",
			g:           FinancialGoal{TargetAmount: 100, CurrentAmountFen: 50, LinkedCodes: []string{"asset.deposit"}},
			snap:        nil,
			wantCurrent: 50, wantLinked: false, wantHasDL: false,
		},
		{
			name:        "已达标月供为 0",
			g:           FinancialGoal{TargetAmount: 100, CurrentAmountFen: 150, TargetYM: "2027-07"},
			snap:        nil,
			wantCurrent: 150, wantMonthly: 0, wantHasDL: true, wantMonths: 12,
		},
		{
			name:        "无期限只算进度",
			g:           FinancialGoal{TargetAmount: 200, CurrentAmountFen: 50},
			snap:        nil,
			wantCurrent: 50, wantHasDL: false, wantMonthly: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := CalcGoalProgress(tc.g, goalNow, tc.snap)
			if p.CurrentFen != tc.wantCurrent || p.FromLinked != tc.wantLinked {
				t.Fatalf("current = (%d, linked=%v); want (%d, %v)", p.CurrentFen, p.FromLinked, tc.wantCurrent, tc.wantLinked)
			}
			if p.HasDeadline != tc.wantHasDL {
				t.Fatalf("HasDeadline = %v; want %v", p.HasDeadline, tc.wantHasDL)
			}
			if p.HasDeadline && p.RemainingMonths != tc.wantMonths {
				t.Fatalf("RemainingMonths = %d; want %d", p.RemainingMonths, tc.wantMonths)
			}
			if p.MonthlyRequiredFen != tc.wantMonthly {
				t.Fatalf("MonthlyRequiredFen = %d; want %d", p.MonthlyRequiredFen, tc.wantMonthly)
			}
		})
	}
}

func TestCalcGoalProgressRatio(t *testing.T) {
	p := CalcGoalProgress(FinancialGoal{TargetAmount: 200, CurrentAmountFen: 50}, goalNow, nil)
	if p.ProgressRatio != 0.25 {
		t.Fatalf("ProgressRatio = %v; want 0.25", p.ProgressRatio)
	}
	over := CalcGoalProgress(FinancialGoal{TargetAmount: 100, CurrentAmountFen: 150}, goalNow, nil)
	if over.ProgressRatio != 1.5 {
		t.Fatalf("ProgressRatio = %v; want 1.5 (可超过 1)", over.ProgressRatio)
	}
}

func TestDueWithinYearsPrefersTargetYM(t *testing.T) {
	// years 说 10 年后，但 target_ym 在 2 年内 → 按 target_ym 判定在 3 年内
	g := FinancialGoal{TargetYM: "2028-01", YearsToAchieve: "10+"}
	if !g.DueWithinYears(3, goalNow) {
		t.Fatal("DueWithinYears() = false; want true (target_ym 优先)")
	}
	// target_ym 在 5 年后 → 不在 3 年内
	g2 := FinancialGoal{TargetYM: "2031-08", YearsToAchieve: "2"}
	if g2.DueWithinYears(3, goalNow) {
		t.Fatal("DueWithinYears() = true; want false")
	}
}
