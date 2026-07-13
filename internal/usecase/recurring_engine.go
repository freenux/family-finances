package usecase

import (
	"sort"
	"time"

	"family-finances/internal/domain"
)

// 周期交易识别与 90 天现金流外推（全部确定性，无 LLM）。
// 识别规则（docs/dev-design.md §8.3）：
//   按（交易对手，空则描述前缀）分组；组内金额差 ≤5%；
//   相邻间隔 ≈30±5（月）/ 90±10（季）/ 365±20（年）天；
//   月/季项 ≥3 次、年项 ≥2 次。

type RecurringCadence string

const (
	CadenceMonthly   RecurringCadence = "monthly"
	CadenceQuarterly RecurringCadence = "quarterly"
	CadenceYearly    RecurringCadence = "yearly"
)

// RecurringItem 一条识别出的周期支出
type RecurringItem struct {
	Name      string           `json:"name"`
	Cadence   RecurringCadence `json:"cadence"`
	AmountFen int64            `json:"amount_fen"` // 组内均值
	LastDate  time.Time        `json:"last_date"`
	NextDate  time.Time        `json:"next_date"`
	Count     int              `json:"count"` // 观察到的次数
}

const descPrefixRunes = 12

// recurringGroupKey 分组键：交易对手优先，空则描述前 12 个字符；两者皆空跳过
func recurringGroupKey(t domain.Transaction) string {
	if t.Counterparty != "" {
		return t.Counterparty
	}
	runes := []rune(t.Description)
	if len(runes) == 0 {
		return ""
	}
	if len(runes) > descPrefixRunes {
		runes = runes[:descPrefixRunes]
	}
	return string(runes)
}

// DetectRecurring 纯函数：在交易列表（应为近 12 个月 confirmed 支出，
// 生产路径经 ListForRecurring 已在 SQL 侧过滤）中识别周期项。
// 输入无需有序；输出按下一次发生时间升序。方向/状态过滤保留作防御。
func DetectRecurring(txs []domain.Transaction) []RecurringItem {
	groups := map[string][]domain.Transaction{}
	for _, t := range txs {
		if t.Direction != domain.DirectionExpense || t.Status != domain.TxStatusConfirmed {
			continue
		}
		key := recurringGroupKey(t)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], t)
	}

	var out []RecurringItem
	for key, list := range groups {
		if item, ok := classifyGroup(key, list); ok {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NextDate.Equal(out[j].NextDate) {
			return out[i].NextDate.Before(out[j].NextDate)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func classifyGroup(name string, list []domain.Transaction) (RecurringItem, bool) {
	if len(list) < 2 {
		return RecurringItem{}, false
	}
	sort.Slice(list, func(i, j int) bool { return list[i].OccurredAt.Before(list[j].OccurredAt) })

	// 金额差 ≤5%（相对均值）
	var sum, min, max int64
	min = list[0].Amount
	max = list[0].Amount
	for _, t := range list {
		sum += t.Amount
		if t.Amount < min {
			min = t.Amount
		}
		if t.Amount > max {
			max = t.Amount
		}
	}
	mean := sum / int64(len(list))
	if mean <= 0 || float64(max-min) > 0.05*float64(mean) {
		return RecurringItem{}, false
	}

	// 相邻间隔天数
	intervals := make([]float64, 0, len(list)-1)
	for i := 1; i < len(list); i++ {
		intervals = append(intervals, list[i].OccurredAt.Sub(list[i-1].OccurredAt).Hours()/24)
	}
	cadence, ok := classifyIntervals(intervals, len(list))
	if !ok {
		return RecurringItem{}, false
	}

	last := list[len(list)-1].OccurredAt
	return RecurringItem{
		Name:      name,
		Cadence:   cadence,
		AmountFen: mean,
		LastDate:  last,
		NextDate:  nextByCadence(last, cadence),
		Count:     len(list),
	}, true
}

func classifyIntervals(intervals []float64, occurrences int) (RecurringCadence, bool) {
	within := func(lo, hi float64) bool {
		for _, d := range intervals {
			if d < lo || d > hi {
				return false
			}
		}
		return true
	}
	switch {
	case occurrences >= 3 && within(25, 35):
		return CadenceMonthly, true
	case occurrences >= 3 && within(80, 100):
		return CadenceQuarterly, true
	case occurrences >= 2 && within(345, 385):
		return CadenceYearly, true
	}
	return "", false
}

func nextByCadence(last time.Time, c RecurringCadence) time.Time {
	switch c {
	case CadenceMonthly:
		return last.AddDate(0, 1, 0)
	case CadenceQuarterly:
		return last.AddDate(0, 3, 0)
	default:
		return last.AddDate(1, 0, 0)
	}
}

// ---- 90 天现金流外推 ----

// CashflowEntry 时间线上的一笔预计流出
type CashflowEntry struct {
	Date      time.Time `json:"date"`
	Name      string    `json:"name"`
	AmountFen int64     `json:"amount_fen"`
	Kind      string    `json:"kind"` // recurring | insurance
}

// MonthCashflow 一个自然月的现金流预测
type MonthCashflow struct {
	Month      string          `json:"month"` // "2026-07"
	InflowFen  int64           `json:"inflow_fen"`
	OutflowFen int64           `json:"outflow_fen"`
	NetFen     int64           `json:"net_fen"`
	Entries    []CashflowEntry `json:"entries"`
}

const cashflowDays = 90

// ProjectCashflow 纯函数：未来 90 天现金流，按自然月分组。
// 流出 = 周期项外推 + 保单缴费日；流入 = monthlyInflowFen（近 6 个月工资类月均）。
func ProjectCashflow(items []RecurringItem, policies []domain.InsurancePolicy, monthlyInflowFen int64, now time.Time) []MonthCashflow {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	deadline := today.AddDate(0, 0, cashflowDays)

	var entries []CashflowEntry
	for _, item := range items {
		for d := item.NextDate; !d.After(deadline); d = nextByCadence(d, item.Cadence) {
			if d.Before(today) {
				continue // 已过期的 next（数据陈旧）不进未来时间线
			}
			entries = append(entries, CashflowEntry{Date: d, Name: item.Name, AmountFen: item.AmountFen, Kind: "recurring"})
		}
	}
	for _, p := range policies {
		next, ok := NextRenewal(p, today)
		if !ok || p.AnnualPremium <= 0 || next.After(deadline) {
			continue
		}
		name := p.InsuredPerson + p.InsuranceType + "续期"
		entries = append(entries, CashflowEntry{Date: next, Name: name, AmountFen: p.AnnualPremium, Kind: "insurance"})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Date.Before(entries[j].Date) })

	// 自然月骨架：当月起到 deadline 所在月
	var months []MonthCashflow
	idx := map[string]int{}
	for cursor := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location()); !cursor.After(deadline); cursor = cursor.AddDate(0, 1, 0) {
		label := cursor.Format("2006-01")
		idx[label] = len(months)
		months = append(months, MonthCashflow{Month: label, InflowFen: monthlyInflowFen})
	}
	for _, e := range entries {
		label := e.Date.Format("2006-01")
		if i, ok := idx[label]; ok {
			months[i].Entries = append(months[i].Entries, e)
			months[i].OutflowFen += e.AmountFen
		}
	}
	for i := range months {
		months[i].NetFen = months[i].InflowFen - months[i].OutflowFen
	}
	return months
}
