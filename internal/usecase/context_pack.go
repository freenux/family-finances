package usecase

import (
	"context"
	"fmt"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ContextPack 是喂给 LLM 的"财务上下文包"，序列化为稳定 JSON。
// M2 的配置解释与 P3 对话复用同一构造函数（ContextPackBuilder.Build）。
// 铁律 4：只含聚合数与快照科目余额，不含交易对手方、单笔明细。
type ContextPack struct {
	Period     string           `json:"period"`
	PeriodType string           `json:"period_type"`
	Income     MoneySection     `json:"income"`
	Expense    MoneySection     `json:"expense"`
	KPI        ContextKPI       `json:"kpi"`
	Compare    ContextCompare   `json:"compare"`
	Snapshot   *ContextSnapshot `json:"snapshot"`
	Findings   []Finding        `json:"findings"`
}

// MoneySection 收入或支出的聚合：合计 + 按（二级）科目展开
type MoneySection struct {
	TotalFen int64             `json:"total_fen"`
	Groups   []MoneyGroupEntry `json:"groups"`
}

type MoneyGroupEntry struct {
	CategoryID string `json:"category_id"`
	Name       string `json:"name"`
	AmountFen  int64  `json:"amount_fen"`
}

type ContextKPI struct {
	SavingsRate     float64 `json:"savings_rate"`
	DiscretionRatio float64 `json:"discretion_ratio"`
	DiscretionAlert bool    `json:"discretion_alert"`
}

type ContextCompare struct {
	PrevPeriod    string `json:"prev_period"`
	IncomeDelta   int64  `json:"income_delta"`
	ExpenseDelta  int64  `json:"expense_delta"`
	NetWorthDelta *int64 `json:"net_worth_delta,omitempty"`
}

type ContextSnapshot struct {
	Period      string           `json:"period"`
	NetWorthFen int64            `json:"net_worth_fen"`
	Items       map[string]int64 `json:"items"`
}

// Finding 是一条确定性诊断结论，key 稳定不变，是 LLM refs 的白名单来源。
type Finding struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// RefWhitelist 返回本包所有 finding key 的集合，供 refs 校验使用。
func (p ContextPack) RefWhitelist() map[string]bool {
	set := make(map[string]bool, len(p.Findings))
	for _, f := range p.Findings {
		set[f.Key] = true
	}
	return set
}

// ContextPackBuilder 组装 ContextPack：确定性计算，不发起任何 LLM 调用。
type ContextPackBuilder struct {
	queryReport *QueryReport
	assetRepo   port.AssetSnapshotRepo
	txRepo      port.TransactionRepo
}

func NewContextPackBuilder(qr *QueryReport, assetRepo port.AssetSnapshotRepo, txRepo port.TransactionRepo) *ContextPackBuilder {
	return &ContextPackBuilder{queryReport: qr, assetRepo: assetRepo, txRepo: txRepo}
}

const savingsRateStreakThreshold = 0.30
const discretionAlertThreshold = 0.35
const maxStreakLookback = 8

// Build 组装期间 p 的上下文包。account 固定为 family（AI 只看家庭总账，不下钻到男主/女主）。
func (b *ContextPackBuilder) Build(ctx context.Context, p domain.Period) (ContextPack, error) {
	cur, err := b.queryReport.Execute(ctx, p, domain.AccountFamily)
	if err != nil {
		return ContextPack{}, fmt.Errorf("query current report: %w", err)
	}
	prevPeriod := p.Previous()
	prev, err := b.queryReport.Execute(ctx, prevPeriod, domain.AccountFamily)
	if err != nil {
		return ContextPack{}, fmt.Errorf("query prev report: %w", err)
	}

	pack := ContextPack{
		Period:     p.Label,
		PeriodType: string(p.Type),
		Income:     flattenMoneySection(cur.IncomeGroups, cur.KPI.TotalIncome),
		Expense:    flattenMoneySection(cur.ExpenseGroups, cur.KPI.TotalExpense),
		KPI: ContextKPI{
			SavingsRate:     cur.KPI.SurplusRate,
			DiscretionRatio: cur.KPI.DiscretionRatio,
			DiscretionAlert: cur.KPI.DiscretionWarning,
		},
		Compare: ContextCompare{
			PrevPeriod:   prevPeriod.Label,
			IncomeDelta:  cur.KPI.TotalIncome - prev.KPI.TotalIncome,
			ExpenseDelta: cur.KPI.TotalExpense - prev.KPI.TotalExpense,
		},
	}

	// 资产快照（可为空）
	curSnap, err := b.assetRepo.GetByPeriod(ctx, p.Label)
	if err != nil && err != port.ErrNotFound {
		return ContextPack{}, fmt.Errorf("get current snapshot: %w", err)
	}
	if curSnap != nil {
		pack.Snapshot = &ContextSnapshot{Period: curSnap.Period, NetWorthFen: curSnap.NetWorth, Items: curSnap.Data}
	}
	var prevSnap *domain.AssetSnapshot
	if p.Type == domain.PeriodQuarterly {
		prevSnap, err = b.assetRepo.GetByPeriod(ctx, prevPeriod.Label)
		if err != nil && err != port.ErrNotFound {
			return ContextPack{}, fmt.Errorf("get prev snapshot: %w", err)
		}
	}
	if curSnap != nil && prevSnap != nil {
		delta := curSnap.NetWorth - prevSnap.NetWorth
		pack.Compare.NetWorthDelta = &delta
	}

	// 结余率连续达标的期数（含当期，向前回溯）
	streak, err := b.savingsRateStreak(ctx, p, cur.KPI.SurplusRate)
	if err != nil {
		return ContextPack{}, err
	}

	monthlyAvgExpense, err := b.trailingMonthlyAvgExpense(ctx, p.End)
	if err != nil {
		return ContextPack{}, err
	}

	pack.Findings = computeFindings(findingsInput{
		Period:            p,
		KPI:               cur.KPI,
		SavingsStreak:     streak,
		CurExpenseGroups:  cur.ExpenseGroups,
		PrevExpenseGroups: prev.ExpenseGroups,
		CurSnapshot:       curSnap,
		PrevSnapshot:      prevSnap,
		MonthlyAvgExpense: monthlyAvgExpense,
	})

	return pack, nil
}

func flattenMoneySection(groups []domain.CategoryGroupAggregation, total int64) MoneySection {
	sec := MoneySection{TotalFen: total}
	for _, g := range groups {
		for _, item := range g.Items {
			sec.Groups = append(sec.Groups, MoneyGroupEntry{
				CategoryID: item.CategoryID,
				Name:       item.Name,
				AmountFen:  item.Amount,
			})
		}
	}
	return sec
}

// savingsRateStreak 从当期往前数，结余率连续 >= 阈值的期数（含当期）。
// 当期不达标则返回 0。
func (b *ContextPackBuilder) savingsRateStreak(ctx context.Context, p domain.Period, curRate float64) (int, error) {
	if curRate < savingsRateStreakThreshold {
		return 0, nil
	}
	streak := 1
	cursor := p.Previous()
	for i := 0; i < maxStreakLookback; i++ {
		rep, err := b.queryReport.Execute(ctx, cursor, domain.AccountFamily)
		if err != nil {
			return 0, fmt.Errorf("query streak period %s: %w", cursor.Label, err)
		}
		if rep.KPI.TotalIncome == 0 || rep.KPI.SurplusRate < savingsRateStreakThreshold {
			break
		}
		streak++
		cursor = cursor.Previous()
	}
	return streak, nil
}

// trailingMonthlyAvgExpense 近 4 个季度（以 end 为右边界，独占）支出总额折算的月均值：
// sum(近 4 季度支出) / 12。用于计算活钱覆盖月数。
func (b *ContextPackBuilder) trailingMonthlyAvgExpense(ctx context.Context, end time.Time) (int64, error) {
	buckets := trailingQuarterBuckets(end, 4)
	summed, err := b.txRepo.SumByBuckets(ctx, buckets, domain.DirectionExpense, domain.AccountFamily)
	if err != nil {
		return 0, fmt.Errorf("sum trailing quarters: %w", err)
	}
	var total int64
	for _, bk := range summed {
		total += bk.Amount
	}
	return total / 12, nil
}

// trailingQuarterBuckets 生成 n 个连续、互不重叠、按时间升序的季度桶，末桶恰好以 end 结尾（独占）。
func trailingQuarterBuckets(end time.Time, n int) []port.PeriodBucket {
	buckets := make([]port.PeriodBucket, n)
	for i := 0; i < n; i++ {
		bucketEnd := end.AddDate(0, -3*(n-1-i), 0)
		bucketStart := bucketEnd.AddDate(0, -3, 0)
		buckets[i] = port.PeriodBucket{
			Label: fmt.Sprintf("q%d", i),
			Start: bucketStart,
			End:   bucketEnd,
		}
	}
	return buckets
}
