package port

import (
	"context"

	"family-finances/internal/domain"
)

// TransactionUpdate 流水人工修正内容
type TransactionUpdate struct {
	CategoryID *string // nil 表示不修改；空字符串表示清空
	Note       *string
	Status     *domain.TxStatus
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

type TransactionRepo interface {
	Insert(ctx context.Context, tx domain.Transaction) error
	InsertBatch(ctx context.Context, batch domain.ImportBatch, rows []ImportRow) (ImportResult, error)
	Update(ctx context.Context, id string, patch TransactionUpdate) error
	Get(ctx context.Context, id string) (domain.Transaction, error)
	List(ctx context.Context, p domain.Period) ([]domain.Transaction, error)
	ListPendingCategory(ctx context.Context, limit int) ([]domain.Transaction, error)
	AggregateByCategory(ctx context.Context, p domain.Period) ([]domain.CategoryAggregation, error)
}

type CategoryRepo interface {
	ListAll(ctx context.Context) ([]domain.Category, error)
	ListByType(ctx context.Context, t domain.CategoryType) ([]domain.Category, error)
}
