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
	Member   string // 成员标注（可空）
	Filename string
	Reader   io.Reader
}

// Execute 解析账单 → 本地规则分类 → 批量入库（事务内去重）。
// 未命中分类的行以 status=pending_review + category_id=NULL 落地，等 LLM 或人工处理。
// 命中往来科目（转账/借还款/报销垫付）的行以 status=excluded 落地，不进任何聚合，
// 但在流水页上有名有姓，能被人工纠正——见 domain.IsTransferCategory。
// 收入行只在命中往来科目时导入（报销到账、收回借款），其余暂不导入。
func (uc *ImportBill) Execute(ctx context.Context, in ImportBillInput) (port.ImportResult, error) {
	parser, ok := bill.ParserFor(in.Source)
	if !ok {
		return port.ImportResult{}, fmt.Errorf("不支持的账单来源: %s", in.Source)
	}
	return uc.ExecuteWithParser(ctx, in, parser)
}

// ExecuteWithParser 用显式解析器走同一条 解析→规则分类→事务去重入库 链路
// （通用 CSV 导入用：解析器由映射动态构造，不在 ParserFor 注册表里）。
func (uc *ImportBill) ExecuteWithParser(ctx context.Context, in ImportBillInput, parser bill.Parser) (port.ImportResult, error) {
	if !in.Account.IsStorageAccount() {
		return port.ImportResult{}, fmt.Errorf("未指定账户归属（husband/wife）")
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
		transferRows   int
	)
	for _, r := range rawRows {
		catID, skip, _ := ClassifyByCustomRules(r, customRules)
		isTransfer := domain.IsTransferCategory(catID)

		// 收入行只放行往来科目：报销到账、收回借款是"垫付出去/借出去"那半边的
		// 对手方，不收进来两头就抵不平（一笔垫付会被永远记成支出）。工资/租金/
		// 收红包这类仍然不导入，免得微信收款把待核对队列淹掉。
		if r.Direction == domain.DirectionIncome && !isTransfer {
			skippedInvalid++
			continue
		}

		status := domain.TxStatusConfirmed
		switch {
		case skip || isTransfer:
			// skip 是 category_id 为空的老式"跳过导入"规则（用户自建的还可能有）；
			// isTransfer 是迁移 016 之后内置规则的落点。两者都是不计收支。
			status = domain.TxStatusExcluded
			transferRows++
		case catID == "":
			status = domain.TxStatusPendingReview
			pendingCat++
		}
		rows = append(rows, port.ImportRow{
			TransactionNo: r.TransactionNo,
			Tx: domain.Transaction{
				ID:               uuid.NewString(),
				Source:           r.Source,
				Account:          in.Account,
				Member:           in.Member,
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
		Source:      parser.Source(),
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
	res.TransferRows = transferRows
	res.EarliestOccurredAt = periodStart
	if uc.trigger != nil && pendingCat > 0 {
		uc.trigger()
	}
	return res, nil
}
