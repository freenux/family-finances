package port

import (
	"context"
	"errors"
	"time"

	"family-finances/internal/domain"
)

// ErrNotFound 目标记录不存在（例如按 id 更新时无匹配行）
var ErrNotFound = errors.New("record not found")

// TransactionUpdate 流水人工修正内容
type TransactionUpdate struct {
	CategoryID *string // nil 表示不修改；空字符串表示清空
	Note       *string
	Status     *domain.TxStatus
	Account    *domain.Account
}

// ImportResult 一次账单导入的结果
type ImportResult struct {
	TotalRows          int
	InsertedRows       int
	SkippedDuplicates  int
	SkippedInvalid     int
	PendingCategory    int
	EarliestOccurredAt time.Time
}

// ImportRow 导入时的候选行：Transaction 主体 + 平台交易号（用于去重）
type ImportRow struct {
	Tx            domain.Transaction
	TransactionNo string
}

// TopTransaction 大额流水条目（statistics 页 Top-N）
type TopTransaction struct {
	ID           string
	OccurredAt   time.Time
	Counterparty string
	Description  string
	Note         string
	Amount       int64
	Direction    domain.Direction
	Account      domain.Account
	CategoryID   string
	CategoryName string
}

// PeriodBucket 单个周期（月或季）的聚合点，用于月度/季度对比条
type PeriodBucket struct {
	Label  string // "2026-05" 或 "2026Q2"
	Start  time.Time
	End    time.Time
	Amount int64
}

type TransactionRepo interface {
	Insert(ctx context.Context, tx domain.Transaction) error
	InsertBatch(ctx context.Context, batch domain.ImportBatch, rows []ImportRow) (ImportResult, error)
	Update(ctx context.Context, id string, patch TransactionUpdate) error
	Get(ctx context.Context, id string) (domain.Transaction, error)
	List(ctx context.Context, p domain.Period, account domain.Account) ([]domain.Transaction, error)
	ListPendingCategory(ctx context.Context, limit int) ([]domain.Transaction, error)
	AggregateByCategory(ctx context.Context, p domain.Period, account domain.Account) ([]domain.CategoryAggregation, error)
	// SumByBuckets 按给定的周期桶返回 [{label, amount}]，方向过滤，状态='confirmed'。
	// 桶必须按时间升序且互不重叠（实现依赖此约定做单次扫描归桶）。
	SumByBuckets(ctx context.Context, buckets []PeriodBucket, direction domain.Direction, account domain.Account) ([]PeriodBucket, error)
	// TopTransactions 周期内按 |amount| desc 的前 N 条；account=family 合并统计
	TopTransactions(ctx context.Context, p domain.Period, direction domain.Direction, account domain.Account, limit int) ([]TopTransaction, error)
	// ListAll 全量流水，按 occurred_at 升序；供 /export 使用
	ListAll(ctx context.Context) ([]domain.Transaction, error)
	// ListAllImportBatches 全量导入批次；供 /export 使用
	ListAllImportBatches(ctx context.Context) ([]domain.ImportBatch, error)
}

type CategoryRepo interface {
	ListAll(ctx context.Context) ([]domain.Category, error)
	ListByType(ctx context.Context, t domain.CategoryType) ([]domain.Category, error)
}

type CategoryRuleRepo interface {
	ListRules(ctx context.Context) ([]domain.CategoryRule, error)
	ListActiveRules(ctx context.Context) ([]domain.CategoryRule, error)
	GetRule(ctx context.Context, id string) (domain.CategoryRule, error)
	InsertRule(ctx context.Context, rule domain.CategoryRule) error
	UpdateRule(ctx context.Context, rule domain.CategoryRule) error
	SetRuleActive(ctx context.Context, id string, active bool) error
	DeleteRule(ctx context.Context, id string) error
}

// AssetSnapshotRepo 资产快照（asset_snapshots 表）
type AssetSnapshotRepo interface {
	// Upsert 按 period 唯一键覆盖写入
	Upsert(ctx context.Context, snap *domain.AssetSnapshot) error
	// GetByPeriod 未找到时返回 ErrNotFound
	GetByPeriod(ctx context.Context, period string) (*domain.AssetSnapshot, error)
	// ListByPeriodAsc 返回最近 limit 个快照，按 period 升序排列（供曲线绘制）
	ListByPeriodAsc(ctx context.Context, limit int) ([]domain.AssetSnapshot, error)
	// ListAll 全量快照，按 period 升序；供 /export 使用
	ListAll(ctx context.Context) ([]domain.AssetSnapshot, error)
}

// FamilyProfileRepo 家庭风险画像（family_profile 单行表）
type FamilyProfileRepo interface {
	// Get 未填写过时返回 ErrNotFound
	Get(ctx context.Context) (*domain.FamilyProfile, error)
	// Upsert 覆盖写入单行（id 固定 'default'）
	Upsert(ctx context.Context, p *domain.FamilyProfile) error
}

// FinancialGoalRepo 财务目标（financial_goals 表）
type FinancialGoalRepo interface {
	ListAllGoals(ctx context.Context) ([]domain.FinancialGoal, error)
	UpsertGoal(ctx context.Context, g *domain.FinancialGoal) error
	DeleteGoal(ctx context.Context, id string) error
}

// InsurancePolicyRepo 保单（insurance_policies 表）
type InsurancePolicyRepo interface {
	ListAllPolicies(ctx context.Context) ([]domain.InsurancePolicy, error)
	UpsertPolicy(ctx context.Context, p *domain.InsurancePolicy) error
	DeletePolicy(ctx context.Context, id string) error
}

// DigestRepo 摘要推送设置（digest_settings 单行表）
type DigestRepo interface {
	// GetSettings 未配置过时返回 ErrNotFound
	GetSettings(ctx context.Context) (*domain.DigestSettings, error)
	UpsertSettings(ctx context.Context, s *domain.DigestSettings) error
	MarkSent(ctx context.Context, at time.Time) error
}

// BudgetRepo 季度预算（budgets 表）
type BudgetRepo interface {
	ListBudgets(ctx context.Context) ([]domain.Budget, error)
	// ReplaceBudgets 整表覆盖：amounts 里 >0 的科目 upsert，未出现或 =0 的科目删除
	ReplaceBudgets(ctx context.Context, amounts map[string]int64) error
}

// ReportRepo AI 季/年财报（reports 表）
type ReportRepo interface {
	// Upsert 按 (period, period_type) 唯一键覆盖写入
	Upsert(ctx context.Context, report *domain.AIReport) error
	// GetByPeriod 未找到时返回 ErrNotFound
	GetByPeriod(ctx context.Context, period string, periodType domain.PeriodType) (*domain.AIReport, error)
	// ListAll 全量历史财报，按 generated_at desc
	ListAll(ctx context.Context) ([]domain.AIReport, error)
}
