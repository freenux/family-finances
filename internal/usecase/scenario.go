package usecase

import (
	"time"

	"family-finances/internal/domain"
)

// 情景实验室（v2 #14，教育演示）：三档预设的长期桶权益比例（该档区间中点）→
// 预期年化区间、历史最大回撤区间（静态系数表线性插值）、对最早财务目标达成时间的影响
// （按区间上下界复利）。全部确定性纯函数，不调 LLM；页面固定标注"假设与系数为教育演示，非预测"。

// ScenarioPreset 一档情景
type ScenarioPreset struct {
	Key             string  `json:"key"`               // conservative | balanced | aggressive
	Name            string  `json:"name"`              // 保守 / 均衡 / 进取
	EquityPct       float64 `json:"equity_pct"`        // 长期桶权益比例（档位区间中点）
	ReturnLowPct    float64 `json:"return_low_pct"`    // 预期年化下界（%）
	ReturnHighPct   float64 `json:"return_high_pct"`   // 预期年化上界（%）
	DrawdownLowPct  float64 `json:"drawdown_low_pct"`  // 历史最大回撤区间下界（%）
	DrawdownHighPct float64 `json:"drawdown_high_pct"` // 上界（%）

	// 最早目标的达成时间影响（HasGoal 时有效）
	HasGoal        bool   `json:"has_goal"`
	GoalName       string `json:"goal_name"`
	BaselineMonths int    `json:"baseline_months"` // 0% 收益直线攒钱所需月数
	MonthsBest     int    `json:"months_best"`     // 上界收益下所需月数
	MonthsWorst    int    `json:"months_worst"`    // 下界收益下所需月数
	Reachable      bool   `json:"reachable"`       // 600 个月内可达
}

// 静态假设系数（教育演示口径，修改需同步页面文案）：
//
//	权益长期年化假设 4%–8%，固收 2%–3%，组合 = 线性加权；
//	最大回撤系数表：权益 20% → 5–10%，50% → 15–25%，80% → 25–40%，线性插值。
const (
	equityReturnLow  = 4.0
	equityReturnHigh = 8.0
	fixedReturnLow   = 2.0
	fixedReturnHigh  = 3.0
)

var scenarioPresets = []struct {
	Key, Name string
	EquityPct float64 // C2 [20,35] / C3 [35,50] / C5 [65,80] 区间中点
}{
	{"conservative", "保守", 27.5},
	{"balanced", "均衡", 42.5},
	{"aggressive", "进取", 72.5},
}

var drawdownAnchors = []struct{ Equity, Low, High float64 }{
	{20, 5, 10},
	{50, 15, 25},
	{80, 25, 40},
}

// drawdownRange 静态系数表线性插值（区间外按端点截断外推斜率）
func drawdownRange(equityPct float64) (low, high float64) {
	a := drawdownAnchors
	if equityPct <= a[0].Equity {
		return a[0].Low, a[0].High
	}
	if equityPct >= a[len(a)-1].Equity {
		return a[len(a)-1].Low, a[len(a)-1].High
	}
	for i := 1; i < len(a); i++ {
		if equityPct <= a[i].Equity {
			t := (equityPct - a[i-1].Equity) / (a[i].Equity - a[i-1].Equity)
			return a[i-1].Low + t*(a[i].Low-a[i-1].Low), a[i-1].High + t*(a[i].High-a[i-1].High)
		}
	}
	return a[len(a)-1].Low, a[len(a)-1].High
}

func blendedReturn(equityPct float64) (low, high float64) {
	w := equityPct / 100
	return w*equityReturnLow + (1-w)*fixedReturnLow, w*equityReturnHigh + (1-w)*fixedReturnHigh
}

const scenarioMaxMonths = 600

// monthsToReach 复利：current 起步、每月存 monthly、月利率 = annual/12，达到 target 所需月数。
// 超过 600 个月返回 (600, false)。
func monthsToReach(target, current, monthly int64, annualRatePct float64) (int, bool) {
	if target <= 0 || current >= target {
		return 0, true
	}
	if monthly <= 0 && annualRatePct <= 0 {
		return scenarioMaxMonths, false
	}
	r := annualRatePct / 100 / 12
	balance := float64(current)
	for m := 1; m <= scenarioMaxMonths; m++ {
		balance = balance*(1+r) + float64(monthly)
		if balance >= float64(target) {
			return m, true
		}
	}
	return scenarioMaxMonths, false
}

// earliestGoal 剩余月数最小的有期限目标
func earliestGoal(goals []domain.FinancialGoal, now time.Time) (domain.FinancialGoal, bool) {
	best := -1
	var out domain.FinancialGoal
	for _, g := range goals {
		months, ok := g.MonthsUntilTarget(now)
		if !ok || g.TargetAmount <= 0 {
			continue
		}
		if best == -1 || months < best {
			best = months
			out = g
		}
	}
	return out, best != -1
}

// ComputeScenarios 纯函数：三档情景对比。
// snapshot 供目标 linked_codes 取当前值；goals 为空或均无期限时 HasGoal=false。
func ComputeScenarios(goals []domain.FinancialGoal, snapshot map[string]int64, now time.Time) []ScenarioPreset {
	goal, hasGoal := earliestGoal(goals, now)
	var progress domain.GoalProgress
	if hasGoal {
		progress = domain.CalcGoalProgress(goal, now, snapshot)
	}

	out := make([]ScenarioPreset, 0, len(scenarioPresets))
	for _, p := range scenarioPresets {
		sp := ScenarioPreset{Key: p.Key, Name: p.Name, EquityPct: p.EquityPct}
		sp.ReturnLowPct, sp.ReturnHighPct = blendedReturn(p.EquityPct)
		sp.DrawdownLowPct, sp.DrawdownHighPct = drawdownRange(p.EquityPct)

		if hasGoal && progress.MonthlyRequiredFen >= 0 {
			sp.HasGoal = true
			sp.GoalName = goal.Description
			sp.BaselineMonths = progress.RemainingMonths
			monthly := progress.MonthlyRequiredFen
			best, okBest := monthsToReach(goal.TargetAmount, progress.CurrentFen, monthly, sp.ReturnHighPct)
			worst, okWorst := monthsToReach(goal.TargetAmount, progress.CurrentFen, monthly, sp.ReturnLowPct)
			sp.MonthsBest = best
			sp.MonthsWorst = worst
			sp.Reachable = okBest && okWorst
		}
		out = append(out, sp)
	}
	return out
}
