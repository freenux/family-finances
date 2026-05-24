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
	txRepo   port.TransactionRepo
	ruleRepo port.CategoryRuleRepo
	trigger  func()
}

func NewImportBill(txRepo port.TransactionRepo, ruleRepo port.CategoryRuleRepo) *ImportBill {
	return &ImportBill{txRepo: txRepo, ruleRepo: ruleRepo}
}

// WithTrigger 挂一个导入成功后的钩子（例如唤醒 LLM 后台分类）
func (uc *ImportBill) WithTrigger(f func()) *ImportBill {
	uc.trigger = f
	return uc
}

type ImportBillInput struct {
	Source   domain.Source
	Account  domain.Account
	Filename string
	Reader   io.Reader
}

// Execute 解析账单 → 本地规则分类 → 批量入库（事务内去重）。
// 未命中分类的行以 status=pending_review + category_id=NULL 落地，等 LLM 或人工处理。
// "应跳过"的支出行（转账/中性交易等）以 status=excluded 落地，方便人工恢复。
// 收入行暂不导入。
func (uc *ImportBill) Execute(ctx context.Context, in ImportBillInput) (port.ImportResult, error) {
	if !in.Account.IsStorageAccount() {
		return port.ImportResult{}, fmt.Errorf("未指定账户归属（husband/wife）")
	}
	parser, ok := bill.ParserFor(in.Source)
	if !ok {
		return port.ImportResult{}, fmt.Errorf("不支持的账单来源: %s", in.Source)
	}

	rawRows, err := parser.Parse(in.Reader)
	if err != nil {
		return port.ImportResult{}, fmt.Errorf("解析账单失败: %w", err)
	}
	customRules, err := uc.ruleRepo.ListActiveRules(ctx)
	if err != nil {
		return port.ImportResult{}, fmt.Errorf("读取分类规则失败: %w", err)
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
		if r.Direction == domain.DirectionIncome {
			skippedInvalid++
			continue
		}

		catID, skip, _ := ClassifyByCustomRules(r, customRules)
		status := domain.TxStatusConfirmed
		if skip {
			status = domain.TxStatusExcluded
		} else if catID == "" {
			status = domain.TxStatusPendingReview
			pendingCat++
		}
		rows = append(rows, port.ImportRow{
			TransactionNo: r.TransactionNo,
			Tx: domain.Transaction{
				ID:               uuid.NewString(),
				Source:           r.Source,
				Account:          in.Account,
				ImportBatchID:    batchID,
				OccurredAt:       r.OccurredAt,
				Counterparty:     r.Counterparty,
				Description:      r.Description,
				PlatformCategory: r.PlatformCategory,
				Amount:           r.Amount,
				Direction:        r.Direction,
				Status:           status,
				CategoryID:       catID,
				RawRow:           r.RawRow,
				CreatedAt:        now,
				UpdatedAt:        now,
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
		Account:     in.Account,
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
	res.EarliestOccurredAt = periodStart
	if uc.trigger != nil && pendingCat > 0 {
		uc.trigger()
	}
	return res, nil
}
