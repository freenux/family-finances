package usecase

import (
	"context"
	"fmt"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// 四笔钱分层诊断（v2 口径：按用途/流动性分层，不做三桶占比区间）。
// 全部确定性计算（铁律 1），LLM 只在 /advice 上解释这些结论。

type BucketStatus string

const (
	BucketOK      BucketStatus = "ok"      // 达标
	BucketLow     BucketStatus = "low"     // 偏低/缺口
	BucketHigh    BucketStatus = "high"    // 偏高/闲置
	BucketUnknown BucketStatus = "unknown" // 数据不足，无法判断
)

// LiquidBucket 活钱：现金 + 货币基金
type LiquidBucket struct {
	AmountFen         int64        `json:"amount_fen"`
	MonthlyExpenseFen int64        `json:"monthly_expense_fen"` // 计算口径的月支出
	ExpenseSource     string       `json:"expense_source"`      // "profile" | "transactions" | ""
	CoverageMonths    float64      `json:"coverage_months"`
	TargetMonths      int          `json:"target_months"`
	Status            BucketStatus `json:"status"`
}

// SteadyBucket 稳健：定存 + 银行理财/债基，对照未来 3 年已知大额支出
type SteadyBucket struct {
	AmountFen  int64        `json:"amount_fen"`
	Goals3yFen int64        `json:"goals_3y_fen"`
	GoalCount  int          `json:"goal_count"`
	HasGoals   bool         `json:"has_goals"`
	GapFen     int64        `json:"gap_fen"` // >0 表示缺口
	Status     BucketStatus `json:"status"`
}

// LongTermBucket 长期：境内权益 + 海外权益 + 黄金，桶内占比供配置引擎对照
type LongTermBucket struct {
	AmountFen       int64   `json:"amount_fen"`
	EquityCnFen     int64   `json:"equity_cn_fen"`
	EquityGlobalFen int64   `json:"equity_global_fen"`
	GoldFen         int64   `json:"gold_fen"`
	EquityPct       float64 `json:"equity_pct"` // (境内+海外)/长期桶
	GlobalPct       float64 `json:"global_pct"` // 海外/长期桶
	GoldPct         float64 `json:"gold_pct"`   // 黄金/长期桶
}

// InsuranceCheck 保障：双十检查，独立诊断不算占比桶
type InsuranceCheck struct {
	Registered           bool         `json:"registered"`
	PolicyCount          int          `json:"policy_count"`
	AnnualPremiumFen     int64        `json:"annual_premium_fen"`
	PremiumRatio         float64      `json:"premium_ratio"` // 保费/年收入
	PremiumOK            bool         `json:"premium_ok"`    // ≤10%
	LifeCoverageFen      int64        `json:"life_coverage_fen"`
	LifeCoverageMultiple float64      `json:"life_coverage_multiple"` // 寿险保额/年收入
	LifeCoverageOK       bool         `json:"life_coverage_ok"`       // ≥10 倍
	HasIncome            bool         `json:"has_income"`
	Status               BucketStatus `json:"status"`
}

// BucketDiagnosis 四笔钱诊断结果；Findings 的 key 进入 /advice refs 白名单
type BucketDiagnosis struct {
	HasSnapshot    bool           `json:"has_snapshot"`
	SnapshotPeriod string         `json:"snapshot_period"`
	HasProfile     bool           `json:"has_profile"`
	Grade          string         `json:"grade"`
	EquityCapPct   int            `json:"equity_cap_pct"`
	Liquid         LiquidBucket   `json:"liquid"`
	Steady         SteadyBucket   `json:"steady"`
	LongTerm       LongTermBucket `json:"long_term"`
	Insurance      InsuranceCheck `json:"insurance"`
	Findings       []Finding      `json:"findings"`
}

// BucketInputs 是诊断的全部输入，由 LoadInputs 组装、ComputeBuckets 纯函数消费
type BucketInputs struct {
	Snapshot                  *domain.AssetSnapshot
	Profile                   *domain.FamilyProfile // nil = 未填画像
	Goals                     []domain.FinancialGoal
	Policies                  []domain.InsurancePolicy
	TrailingMonthlyExpenseFen int64     // 近 4 季支出折算月均（分）
	Now                       time.Time // 目标期限判断的基准时间（零值时 target_ym 目标按未到期处理）
}

// BucketEngine 组装输入并执行确定性诊断
type BucketEngine struct {
	assetRepo   port.AssetSnapshotRepo
	profileRepo port.FamilyProfileRepo
	goalRepo    port.FinancialGoalRepo
	policyRepo  port.InsurancePolicyRepo
	txRepo      port.TransactionRepo
	now         func() time.Time
}

func NewBucketEngine(assetRepo port.AssetSnapshotRepo, profileRepo port.FamilyProfileRepo,
	goalRepo port.FinancialGoalRepo, policyRepo port.InsurancePolicyRepo, txRepo port.TransactionRepo) *BucketEngine {
	return &BucketEngine{
		assetRepo: assetRepo, profileRepo: profileRepo,
		goalRepo: goalRepo, policyRepo: policyRepo, txRepo: txRepo,
		now: time.Now,
	}
}

// LoadInputs 读取最新快照、画像、目标、保单与近 4 季月均支出
func (e *BucketEngine) LoadInputs(ctx context.Context) (BucketInputs, error) {
	var in BucketInputs

	snaps, err := e.assetRepo.ListByPeriodAsc(ctx, 1)
	if err != nil {
		return in, fmt.Errorf("load latest snapshot: %w", err)
	}
	if len(snaps) > 0 {
		in.Snapshot = &snaps[len(snaps)-1]
	}

	profile, err := e.profileRepo.Get(ctx)
	if err != nil && err != port.ErrNotFound {
		return in, fmt.Errorf("load profile: %w", err)
	}
	in.Profile = profile

	in.Goals, err = e.goalRepo.ListAllGoals(ctx)
	if err != nil {
		return in, fmt.Errorf("load goals: %w", err)
	}
	in.Policies, err = e.policyRepo.ListAllPolicies(ctx)
	if err != nil {
		return in, fmt.Errorf("load policies: %w", err)
	}

	// 近 4 个季度支出折算月均（以当前季度末为右边界）
	quarterEnd := domain.CurrentQuarter(e.now()).End
	buckets := trailingQuarterBuckets(quarterEnd, 4)
	// 日常口径：一次装修会把"月均支出"抬上去，活钱覆盖月数直接腰斩，纯误报
	summed, err := e.txRepo.SumByBuckets(ctx, buckets, domain.DirectionExpense, domain.AccountFamily, domain.ScopeDaily)
	if err != nil {
		return in, fmt.Errorf("sum trailing expense: %w", err)
	}
	var total int64
	for _, b := range summed {
		total += b.Amount
	}
	in.TrailingMonthlyExpenseFen = total / 12
	in.Now = e.now()

	return in, nil
}

// Diagnose = LoadInputs + ComputeBuckets
func (e *BucketEngine) Diagnose(ctx context.Context) (BucketDiagnosis, error) {
	in, err := e.LoadInputs(ctx)
	if err != nil {
		return BucketDiagnosis{}, err
	}
	return ComputeBuckets(in), nil
}

// ComputeBuckets 是纯函数：四笔钱分层诊断
func ComputeBuckets(in BucketInputs) BucketDiagnosis {
	d := BucketDiagnosis{}

	profile := domain.DefaultFamilyProfile()
	if in.Profile != nil {
		profile = *in.Profile
		d.HasProfile = true
	}
	d.Grade, d.EquityCapPct = domain.CalcProfileGrade(profile)

	data := map[string]int64{}
	if in.Snapshot != nil {
		d.HasSnapshot = true
		d.SnapshotPeriod = in.Snapshot.Period
		data = in.Snapshot.Data
	}

	d.Liquid = diagnoseLiquid(data, profile, in.TrailingMonthlyExpenseFen)
	d.Steady = diagnoseSteady(data, in.Goals, in.Now)
	d.LongTerm = diagnoseLongTerm(data)
	d.Insurance = diagnoseInsurance(in.Policies, profile.AnnualIncomeFen)
	d.Findings = bucketFindings(d)
	return d
}

func diagnoseLiquid(data map[string]int64, profile domain.FamilyProfile, trailingMonthly int64) LiquidBucket {
	b := LiquidBucket{
		AmountFen:    data["asset.cash"] + data["asset.mmf"],
		TargetMonths: profile.EmergencyMonths,
	}
	if b.TargetMonths <= 0 {
		b.TargetMonths = 6
	}
	if profile.MonthlyExpenseFen != nil && *profile.MonthlyExpenseFen > 0 {
		b.MonthlyExpenseFen = *profile.MonthlyExpenseFen
		b.ExpenseSource = "profile"
	} else if trailingMonthly > 0 {
		b.MonthlyExpenseFen = trailingMonthly
		b.ExpenseSource = "transactions"
	}
	if b.MonthlyExpenseFen <= 0 {
		b.Status = BucketUnknown
		return b
	}
	b.CoverageMonths = float64(b.AmountFen) / float64(b.MonthlyExpenseFen)
	switch {
	case b.CoverageMonths < float64(b.TargetMonths):
		b.Status = BucketLow
	case b.CoverageMonths > 2*float64(b.TargetMonths):
		b.Status = BucketHigh
	default:
		b.Status = BucketOK
	}
	return b
}

func diagnoseSteady(data map[string]int64, goals []domain.FinancialGoal, now time.Time) SteadyBucket {
	b := SteadyBucket{AmountFen: data["asset.deposit"] + data["asset.wealth"]}
	for _, g := range goals {
		// 优先 target_ym，无 target_ym 回退 years_to_achieve 下界（DueWithinYears 内部处理）
		if g.DueWithinYears(3, now) {
			b.Goals3yFen += g.TargetAmount
			b.GoalCount++
		}
	}
	b.HasGoals = b.GoalCount > 0
	if !b.HasGoals {
		b.Status = BucketUnknown // 提示到财务目标中登记 3 年内大额支出
		return b
	}
	b.GapFen = b.Goals3yFen - b.AmountFen
	if b.GapFen > 0 {
		b.Status = BucketLow
	} else {
		b.Status = BucketOK
	}
	return b
}

func diagnoseLongTerm(data map[string]int64) LongTermBucket {
	b := LongTermBucket{
		EquityCnFen:     data["asset.equity_cn"],
		EquityGlobalFen: data["asset.equity_global"],
		GoldFen:         data["asset.gold"],
	}
	b.AmountFen = b.EquityCnFen + b.EquityGlobalFen + b.GoldFen
	if b.AmountFen > 0 {
		b.EquityPct = float64(b.EquityCnFen+b.EquityGlobalFen) / float64(b.AmountFen) * 100
		b.GlobalPct = float64(b.EquityGlobalFen) / float64(b.AmountFen) * 100
		b.GoldPct = float64(b.GoldFen) / float64(b.AmountFen) * 100
	}
	return b
}

func diagnoseInsurance(policies []domain.InsurancePolicy, annualIncomeFen int64) InsuranceCheck {
	c := InsuranceCheck{PolicyCount: len(policies), HasIncome: annualIncomeFen > 0}
	if len(policies) == 0 {
		c.Status = BucketUnknown // 未登记
		return c
	}
	c.Registered = true
	for _, p := range policies {
		c.AnnualPremiumFen += p.AnnualPremium
		if p.IsLifeInsurance() {
			c.LifeCoverageFen += p.CoverageAmount
		}
	}
	if !c.HasIncome {
		c.Status = BucketUnknown // 画像未填年收入，双十无从对照
		return c
	}
	c.PremiumRatio = float64(c.AnnualPremiumFen) / float64(annualIncomeFen)
	c.PremiumOK = c.PremiumRatio <= 0.10
	c.LifeCoverageMultiple = float64(c.LifeCoverageFen) / float64(annualIncomeFen)
	c.LifeCoverageOK = c.LifeCoverageMultiple >= 10
	if c.PremiumOK && c.LifeCoverageOK {
		c.Status = BucketOK
	} else {
		c.Status = BucketLow
	}
	return c
}

// bucketFindings 把诊断转成稳定 key 的结论，作为 /advice refs 白名单的一部分
func bucketFindings(d BucketDiagnosis) []Finding {
	var out []Finding

	switch d.Liquid.Status {
	case BucketUnknown:
		out = append(out, Finding{Key: "bucket_liquid", Text: "活钱桶暂无法诊断：缺少月支出数据（画像未填且流水不足）"})
	default:
		out = append(out, Finding{Key: "bucket_liquid", Text: fmt.Sprintf(
			"活钱（现金+货基）%s 元，可覆盖 %.1f 个月支出，目标 %d 个月（%s）",
			formatYuanFen(d.Liquid.AmountFen), d.Liquid.CoverageMonths, d.Liquid.TargetMonths, bucketStatusLabel(d.Liquid.Status))})
	}

	if d.Steady.HasGoals {
		out = append(out, Finding{Key: "bucket_steady", Text: fmt.Sprintf(
			"稳健（定存+理财）%s 元，对照 3 年内 %d 项大额目标合计 %s 元（%s）",
			formatYuanFen(d.Steady.AmountFen), d.Steady.GoalCount, formatYuanFen(d.Steady.Goals3yFen), bucketStatusLabel(d.Steady.Status))})
	} else {
		out = append(out, Finding{Key: "bucket_steady", Text: fmt.Sprintf(
			"稳健（定存+理财）%s 元；未登记 3 年内大额支出目标，无法评估匹配度", formatYuanFen(d.Steady.AmountFen))})
	}

	out = append(out, Finding{Key: "bucket_longterm", Text: fmt.Sprintf(
		"长期（权益+黄金）%s 元，桶内权益 %.1f%%、海外 %.1f%%、黄金 %.1f%%",
		formatYuanFen(d.LongTerm.AmountFen), d.LongTerm.EquityPct, d.LongTerm.GlobalPct, d.LongTerm.GoldPct)})

	switch {
	case !d.Insurance.Registered:
		out = append(out, Finding{Key: "bucket_insurance", Text: "保障：未登记任何保单"})
	case !d.Insurance.HasIncome:
		out = append(out, Finding{Key: "bucket_insurance", Text: "保障：已登记保单，但画像未填年收入，双十检查无法进行"})
	default:
		out = append(out, Finding{Key: "bucket_insurance", Text: fmt.Sprintf(
			"保障：年缴保费占年收入 %.1f%%（上限 10%%），寿险保额为年收入 %.1f 倍（目标 ≥10 倍）",
			d.Insurance.PremiumRatio*100, d.Insurance.LifeCoverageMultiple)})
	}
	return out
}

func bucketStatusLabel(s BucketStatus) string {
	switch s {
	case BucketOK:
		return "达标"
	case BucketLow:
		return "偏低"
	case BucketHigh:
		return "偏高"
	default:
		return "数据不足"
	}
}
