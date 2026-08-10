package usecase

import (
	"context"
	"fmt"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// GoalView 财务目标页用例：列表 + 进度 + 月供 + 目标过载 finding
type GoalView struct {
	goalRepo  port.FinancialGoalRepo
	assetRepo port.AssetSnapshotRepo
	txRepo    port.TransactionRepo
	now       func() time.Time
}

func NewGoalView(goalRepo port.FinancialGoalRepo, assetRepo port.AssetSnapshotRepo, txRepo port.TransactionRepo) *GoalView {
	return &GoalView{goalRepo: goalRepo, assetRepo: assetRepo, txRepo: txRepo, now: time.Now}
}

// GoalWithProgress 一条目标 + 进度
type GoalWithProgress struct {
	Goal     domain.FinancialGoal
	Progress domain.GoalProgress
}

// GoalViewData 页面数据
type GoalViewData struct {
	Goals             []GoalWithProgress
	TotalMonthlyFen   int64 // Σ各目标月供
	AvgMonthlySurplus int64 // 近 4 季月均结余（分）
	Overloaded        bool  // 月供合计 > 月均结余
	SnapshotPeriod    string
	HasSnapshot       bool
}

// Load 组装页面数据
func (uc *GoalView) Load(ctx context.Context) (GoalViewData, error) {
	goals, err := uc.goalRepo.ListAllGoals(ctx)
	if err != nil {
		return GoalViewData{}, err
	}
	var snapData map[string]int64
	var snapPeriod string
	snaps, err := uc.assetRepo.ListByPeriodAsc(ctx, 1)
	if err != nil {
		return GoalViewData{}, err
	}
	if len(snaps) > 0 {
		snapData = snaps[len(snaps)-1].Data
		snapPeriod = snaps[len(snaps)-1].Period
	}

	now := uc.now()
	data := GoalViewData{SnapshotPeriod: snapPeriod, HasSnapshot: snapData != nil}
	for _, g := range goals {
		p := domain.CalcGoalProgress(g, now, snapData)
		data.TotalMonthlyFen += p.MonthlyRequiredFen
		data.Goals = append(data.Goals, GoalWithProgress{Goal: g, Progress: p})
	}

	data.AvgMonthlySurplus, err = uc.avgMonthlySurplus(ctx, now)
	if err != nil {
		return GoalViewData{}, err
	}
	data.Overloaded = data.TotalMonthlyFen > 0 && data.AvgMonthlySurplus > 0 &&
		data.TotalMonthlyFen > data.AvgMonthlySurplus
	// 月均结余为负也算过载（没有结余可供储蓄）
	if data.TotalMonthlyFen > 0 && data.AvgMonthlySurplus <= 0 {
		data.Overloaded = true
	}
	return data, nil
}

// avgMonthlySurplus 近 4 个季度 (收入−支出)/12
func (uc *GoalView) avgMonthlySurplus(ctx context.Context, now time.Time) (int64, error) {
	end := domain.CurrentQuarter(now).End
	buckets := trailingQuarterBuckets(end, 4)
	// 日常口径：攒钱能力看的是日常结余，不该被一次装修判成"没有储蓄能力"
	income, err := uc.txRepo.SumByBuckets(ctx, buckets, domain.DirectionIncome, domain.AccountFamily, domain.ScopeDaily)
	if err != nil {
		return 0, err
	}
	expense, err := uc.txRepo.SumByBuckets(ctx, buckets, domain.DirectionExpense, domain.AccountFamily, domain.ScopeDaily)
	if err != nil {
		return 0, err
	}
	var in, out int64
	for _, b := range income {
		in += b.Amount
	}
	for _, b := range expense {
		out += b.Amount
	}
	return (in - out) / 12, nil
}

// Findings 目标过载 finding（ContextPack FindingSource）
func (uc *GoalView) Findings(ctx context.Context, _ domain.Period) ([]Finding, error) {
	data, err := uc.Load(ctx)
	if err != nil {
		return nil, err
	}
	if !data.Overloaded {
		return nil, nil
	}
	return []Finding{{
		Key: "goal_overload",
		Text: fmt.Sprintf("目标过载：各财务目标每月应存合计 %s 元，超过近 4 季月均结余 %s 元",
			formatYuanFen(data.TotalMonthlyFen), formatYuanFen(data.AvgMonthlySurplus)),
	}}, nil
}
