package domain

import (
	"strconv"
	"strings"
	"time"
)

// FinancialGoal 财务目标（financial_goals 表）
type FinancialGoal struct {
	ID             string
	Category       string
	Description    string
	TargetAmount   int64  // 分
	YearsToAchieve string // 自由文本，如 "3" / "3-5" / "10+"（target_ym 为空时的回退口径）
	Priority       string
	ExpectedReturn float64
	AnnualSaving   *int64 // 分，可空
	Notes          string
	SortOrder      int
	UpdatedAt      time.Time

	// M3（迁移 008）新增
	TargetYM         string   // 目标年月 "2039-09"，可空
	CurrentAmountFen int64    // 手填当前金额（分）
	LinkedCodes      []string // 关联资产科目 code；非空时 current 用最新快照余额覆盖手填值
}

// MonthsUntilTarget 距目标的剩余月数（含当月的下限 1）；无法确定期限返回 (0, false)。
// 优先 TargetYM，为空回退 YearsToAchieve 下界 ×12。
func (g FinancialGoal) MonthsUntilTarget(now time.Time) (int, bool) {
	if g.TargetYM != "" {
		t, err := time.ParseInLocation("2006-01", g.TargetYM, time.Local)
		if err != nil {
			return 0, false
		}
		months := (t.Year()-now.Year())*12 + int(t.Month()) - int(now.Month())
		if months < 1 {
			months = 1
		}
		return months, true
	}
	if y := g.YearsLowerBound(); y >= 0 {
		months := y * 12
		if months < 1 {
			months = 1
		}
		return months, true
	}
	return 0, false
}

// DueWithinYears 是否在 years 年内到期（稳健桶匹配用；优先 target_ym）
func (g FinancialGoal) DueWithinYears(years int, now time.Time) bool {
	months, ok := g.MonthsUntilTarget(now)
	return ok && months <= years*12
}

// GoalProgress 目标进度（纯计算结果）
type GoalProgress struct {
	CurrentFen         int64   // 生效的当前金额（linked_codes 非空时为快照余额合计）
	FromLinked         bool    // current 是否来自快照联动
	ProgressRatio      float64 // current/target，可 >1
	HasDeadline        bool
	RemainingMonths    int   // >=1，仅 HasDeadline 时有效
	MonthlyRequiredFen int64 // 每月应存 = (target-current)/剩余月数，达标后为 0
}

// CalcGoalProgress 纯函数：目标 + 当前时间 + 最新季度快照（可 nil）→ 进度。
func CalcGoalProgress(g FinancialGoal, now time.Time, snapshot map[string]int64) GoalProgress {
	p := GoalProgress{CurrentFen: g.CurrentAmountFen}
	if len(g.LinkedCodes) > 0 && snapshot != nil {
		var sum int64
		for _, code := range g.LinkedCodes {
			sum += snapshot[code]
		}
		p.CurrentFen = sum
		p.FromLinked = true
	}
	if g.TargetAmount > 0 {
		p.ProgressRatio = float64(p.CurrentFen) / float64(g.TargetAmount)
	}
	months, ok := g.MonthsUntilTarget(now)
	if !ok {
		return p
	}
	p.HasDeadline = true
	p.RemainingMonths = months
	gap := g.TargetAmount - p.CurrentFen
	if gap > 0 {
		p.MonthlyRequiredFen = gap / int64(months)
	}
	return p
}

// YearsLowerBound 解析 YearsToAchieve 的下界年数；解析失败返回 -1。
// "3"→3，"3-5"→3，"10+"→10。
func (g FinancialGoal) YearsLowerBound() int {
	s := strings.TrimSpace(g.YearsToAchieve)
	if s == "" {
		return -1
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return -1
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return -1
	}
	return n
}

// InsurancePolicy 保单（insurance_policies 表，M2 只读：双十检查）
type InsurancePolicy struct {
	ID             string
	InsuredPerson  string
	InsuranceType  string // 寿险/重疾/医疗/意外…（自由文本）
	CompanyProduct string
	Coverage       string
	CoverageAmount int64 // 保额（分）
	AnnualPremium  int64 // 年缴保费（分）
	RenewalDate    time.Time
	Notes          string
	SortOrder      int
	UpdatedAt      time.Time
}

// IsLifeInsurance 判断是否寿险（双十检查里"寿险保额 ≥ 10 倍年收入"只统计寿险）
func (p InsurancePolicy) IsLifeInsurance() bool {
	return strings.Contains(p.InsuranceType, "寿险") || strings.Contains(strings.ToLower(p.InsuranceType), "life")
}
