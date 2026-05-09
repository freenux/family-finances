package usecase

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"family-finances/internal/adapter/bill"
	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type ImportBill struct {
	txRepo  port.TransactionRepo
	trigger func()
}

func NewImportBill(txRepo port.TransactionRepo) *ImportBill {
	return &ImportBill{txRepo: txRepo}
}

// WithTrigger 挂一个导入成功后的钩子（例如唤醒 LLM 后台分类）
func (uc *ImportBill) WithTrigger(f func()) *ImportBill {
	uc.trigger = f
	return uc
}

type ImportBillInput struct {
	Source   domain.Source
	Filename string
	Reader   io.Reader
}

// Execute 解析账单 → 本地规则分类 → 批量入库（事务内去重）。
// 未命中分类的行以 status=pending_review + category_id=NULL 落地，等 LLM 或人工处理。
// "应跳过"的行（转账/中性交易等）不入库，仅计入 SkippedInvalid。
func (uc *ImportBill) Execute(ctx context.Context, in ImportBillInput) (port.ImportResult, error) {
	parser, ok := bill.ParserFor(in.Source)
	if !ok {
		return port.ImportResult{}, fmt.Errorf("不支持的账单来源: %s", in.Source)
	}

	rawRows, err := parser.Parse(in.Reader)
	if err != nil {
		return port.ImportResult{}, fmt.Errorf("解析账单失败: %w", err)
	}

	batchID := uuid.NewString()
	now := time.Now()
	var (
		rows           []port.ImportRow
		periodStart    time.Time
		periodEnd      time.Time
		skippedInvalid int
		pendingCat     int
	)
	for _, r := range rawRows {
		catID, skip, _ := ClassifyByRules(r)
		if skip {
			skippedInvalid++
			continue
		}
		status := domain.TxStatusConfirmed
		if catID == "" {
			status = domain.TxStatusPendingReview
			pendingCat++
		}
		rows = append(rows, port.ImportRow{
			TransactionNo: r.TransactionNo,
			Tx: domain.Transaction{
				ID:            uuid.NewString(),
				Source:        r.Source,
				ImportBatchID: batchID,
				OccurredAt:    r.OccurredAt,
				Counterparty:  r.Counterparty,
				Description:   r.Description,
				Amount:        r.Amount,
				Direction:     r.Direction,
				Status:        status,
				CategoryID:    catID,
				RawRow:        r.RawRow,
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		})

		if periodStart.IsZero() || r.OccurredAt.Before(periodStart) {
			periodStart = r.OccurredAt
		}
		if r.OccurredAt.After(periodEnd) {
			periodEnd = r.OccurredAt
		}
	}

	batch := domain.ImportBatch{
		ID:          batchID,
		Source:      in.Source,
		Filename:    in.Filename,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		TotalRows:   len(rawRows),
		CreatedAt:   now,
	}

	res, err := uc.txRepo.InsertBatch(ctx, batch, rows)
	if err != nil {
		return port.ImportResult{}, err
	}
	res.TotalRows = len(rawRows)
	res.SkippedInvalid = skippedInvalid
	res.PendingCategory = pendingCat
	if uc.trigger != nil && pendingCat > 0 {
		uc.trigger()
	}
	return res, nil
}
