package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type AddTransactionInput struct {
	OccurredAt   time.Time
	Counterparty string
	Description  string
	Amount       int64 // 分
	Direction    domain.Direction
	CategoryID   string
}

type AddTransaction struct {
	txRepo  port.TransactionRepo
	catRepo port.CategoryRepo
}

func NewAddTransaction(tx port.TransactionRepo, cat port.CategoryRepo) *AddTransaction {
	return &AddTransaction{txRepo: tx, catRepo: cat}
}

func (uc *AddTransaction) Execute(ctx context.Context, in AddTransactionInput) (domain.Transaction, error) {
	if in.Amount <= 0 {
		return domain.Transaction{}, fmt.Errorf("金额必须大于 0")
	}
	if in.Direction != domain.DirectionIncome && in.Direction != domain.DirectionExpense {
		return domain.Transaction{}, fmt.Errorf("方向必须是 income 或 expense")
	}
	if in.CategoryID == "" {
		return domain.Transaction{}, fmt.Errorf("请选择科目")
	}
	now := time.Now()
	t := domain.Transaction{
		ID:           uuid.NewString(),
		Source:       domain.SourceManual,
		OccurredAt:   in.OccurredAt,
		Counterparty: in.Counterparty,
		Description:  in.Description,
		Amount:       in.Amount,
		Direction:    in.Direction,
		Status:       domain.TxStatusConfirmed,
		CategoryID:   in.CategoryID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := uc.txRepo.Insert(ctx, t); err != nil {
		return domain.Transaction{}, err
	}
	return t, nil
}
