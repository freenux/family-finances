package usecase

import (
	"fmt"
	"math"

	"family-finances/internal/domain"
)

// findingsInput 是 computeFindings 的输入，全部是已经算好的确定性数据（铁律 1）。
type findingsInput struct {
	Period        domain.Period
	KPI           domain.ReportKPI
	SavingsStreak int // 含当期，日常口径结余率连续达标的期数；0 表示当期未达标
	// CurExpenseGroups / PrevExpenseGroups 日常口径支出分组（剔除专项）：
	// 类目环比若混进装修/购车，每期都会指着被专项顶起来的那个科目喊涨。
	CurExpenseGroups  []domain.CategoryGroupAggregation
	PrevExpenseGroups []domain.CategoryGroupAggregation
	CurSnapshot       *domain.AssetSnapshot
	PrevSnapshot      *domain.AssetSnapshot
	MonthlyAvgExpense int64 // 分，近 4 季度支出均值折算的月均值
}

// computeFindings 是纯函数：给定确定性输入，产出稳定 key 的诊断结论列表。
// M1 至少覆盖：结余率及连续性、支出类目环比 Top 变动、可自由支配占比告警、
// 净资产环比、活钱覆盖月数（有快照时）。
func computeFindings(in findingsInput) []Finding {
	var out []Finding

	// 结余率一律用日常口径：连胜的历史桶（savingsRateStreak）算的就是日常口径，
	// 当期若混用全口径，等于拿 N−1 个日常期跟 1 个全口径当期比。
	// 专项存在时把全口径（真实现金流）那一档也写出来，免得读者以为装修没花钱。
	if in.KPI.DailyIncome > 0 {
		text := fmt.Sprintf("本期日常结余率 %s（剔除专项）", pctString(in.KPI.DailySurplusRate))
		if in.SavingsStreak >= 2 {
			text = fmt.Sprintf("日常结余率连续 %d 期 ≥30%%（本期 %s）", in.SavingsStreak, pctString(in.KPI.DailySurplusRate))
		}
		if in.KPI.SpecialExpense != 0 || in.KPI.SpecialIncome != 0 {
			text += fmt.Sprintf("；含专项的全口径结余率 %s", pctString(in.KPI.SurplusRate))
		}
		out = append(out, Finding{Key: "savings_rate", Text: text})
	}

	if f, ok := topExpenseChange(in.CurExpenseGroups, in.PrevExpenseGroups); ok {
		out = append(out, f)
	}

	// 守卫跟着分母走：比值的分母是 DailyExpense，本期支出若全部归入专项
	// （DailyExpense == 0 而 TotalExpense > 0），比值恒为 0，不能当成"占比 0.0%"
	// 的确定性结论发给 LLM——那会直接进 refs 白名单。
	if in.KPI.DailyExpense > 0 {
		text := fmt.Sprintf("可自由支配支出占日常支出 %s", pctString(in.KPI.DiscretionRatio))
		if in.KPI.DiscretionWarning {
			text += "，超过 35% 警戒线"
		}
		out = append(out, Finding{Key: "discretion_ratio", Text: text})
	}

	if in.CurSnapshot != nil && in.PrevSnapshot != nil && in.PrevSnapshot.NetWorth != 0 {
		delta := in.CurSnapshot.NetWorth - in.PrevSnapshot.NetWorth
		pct := float64(delta) / math.Abs(float64(in.PrevSnapshot.NetWorth))
		sign := ""
		if delta > 0 {
			sign = "+"
		}
		out = append(out, Finding{
			Key:  "net_worth_delta",
			Text: fmt.Sprintf("净资产环比 %s%s（%s%s 元）", sign, pctString(pct), signStr(delta), formatYuanFen(delta)),
		})
	}

	if in.CurSnapshot != nil && in.MonthlyAvgExpense > 0 {
		liquid := in.CurSnapshot.Data["asset.cash"] + in.CurSnapshot.Data["asset.mmf"]
		months := float64(liquid) / float64(in.MonthlyAvgExpense)
		out = append(out, Finding{
			Key:  "cash_coverage_months",
			Text: fmt.Sprintf("活钱（现金+货币基金）可覆盖约 %.1f 个月支出", months),
		})
	}

	return out
}

type leafEntry struct {
	Name   string
	Amount int64
}

func flattenLeafAmounts(groups []domain.CategoryGroupAggregation) map[string]leafEntry {
	m := map[string]leafEntry{}
	for _, g := range groups {
		for _, item := range g.Items {
			m[item.CategoryID] = leafEntry{Name: item.Name, Amount: item.Amount}
		}
	}
	return m
}

// topExpenseChange 找出环比变动幅度（百分比）最大的支出类目（要求上期该类目 >0，避免除零/无穷大）。
func topExpenseChange(cur, prev []domain.CategoryGroupAggregation) (Finding, bool) {
	curMap := flattenLeafAmounts(cur)
	prevMap := flattenLeafAmounts(prev)

	var bestID, bestName string
	var bestPct float64
	found := false
	for id, c := range curMap {
		p, ok := prevMap[id]
		if !ok || p.Amount <= 0 {
			continue
		}
		pct := float64(c.Amount-p.Amount) / float64(p.Amount)
		if !found || math.Abs(pct) > math.Abs(bestPct) {
			bestID, bestName, bestPct = id, c.Name, pct
			found = true
		}
	}
	if !found {
		return Finding{}, false
	}
	sign := ""
	if bestPct > 0 {
		sign = "+"
	}
	return Finding{
		Key:  "expense_top_change_" + bestID,
		Text: fmt.Sprintf("「%s」类支出环比 %s%s", bestName, sign, pctString(bestPct)),
	}, true
}

func pctString(r float64) string {
	return fmt.Sprintf("%.1f%%", r*100)
}

func signStr(fen int64) string {
	if fen < 0 {
		return "-"
	}
	return ""
}

func formatYuanFen(fen int64) string {
	if fen < 0 {
		fen = -fen
	}
	return fmt.Sprintf("%d.%02d", fen/100, fen%100)
}
