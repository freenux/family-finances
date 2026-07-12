package domain

import (
	"strconv"
	"strings"
	"time"
)

// FinancialGoal 财务目标（financial_goals 表，M2 只读：稳健桶 vs 3 年内大额支出）
type FinancialGoal struct {
	ID             string
	Category       string
	Description    string
	TargetAmount   int64  // 分
	YearsToAchieve string // 自由文本，如 "3" / "3-5" / "10+"
	Priority       string
	ExpectedReturn float64
	AnnualSaving   *int64 // 分，可空
	Notes          string
	SortOrder      int
	UpdatedAt      time.Time
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
