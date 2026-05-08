package port

import (
	"context"

	"family-finances/internal/domain"
)

type TransactionRepo interface {
	Insert(ctx context.Context, tx domain.Transaction) error
	List(ctx context.Context, p domain.Period) ([]domain.Transaction, error)
	AggregateByCategory(ctx context.Context, p domain.Period) ([]domain.CategoryAggregation, error)
}

type CategoryRepo interface {
	ListAll(ctx context.Context) ([]domain.Category, error)
	ListByType(ctx context.Context, t domain.CategoryType) ([]domain.Category, error)
}
