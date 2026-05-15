package port

import (
	"context"
	"time"

	"family-finances/internal/domain"
)

// TransactionUpdate 流水人工修正内容
type TransactionUpdate struct {
	CategoryID *string // nil 表示不修改；空字符串表示清空
	Note       *string
	Status     *domain.TxStatus
	Account    *domain.Account
}

// ImportResult 一次账单导入的结果
type ImportResult struct {
	TotalRows         int
	InsertedRows      int
	SkippedDuplicates int
	SkippedInvalid    int
	PendingCategory   int
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
	// SumByBuckets 按给定的周期桶返回 [{label, amount}]，方向过滤，状态='confirmed'
	SumByBuckets(ctx context.Context, buckets []PeriodBucket, direction domain.Direction, account domain.Account) ([]PeriodBucket, error)
	// TopTransactions 周期内按 |amount| desc 的前 N 条；account=family 合并统计
	TopTransactions(ctx context.Context, p domain.Period, direction domain.Direction, account domain.Account, limit int) ([]TopTransaction, error)
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
