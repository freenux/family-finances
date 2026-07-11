package usecase

import (
	"context"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// Export 全量数据导出（/export 页面用例）：遍历各 repo，避免 handler 直接摸 SQL。
type Export struct {
	txRepo     port.TransactionRepo
	catRepo    port.CategoryRepo
	ruleRepo   port.CategoryRuleRepo
	assetRepo  port.AssetSnapshotRepo
	reportRepo port.ReportRepo
}

func NewExport(txRepo port.TransactionRepo, catRepo port.CategoryRepo, ruleRepo port.CategoryRuleRepo,
	assetRepo port.AssetSnapshotRepo, reportRepo port.ReportRepo) *Export {
	return &Export{txRepo: txRepo, catRepo: catRepo, ruleRepo: ruleRepo, assetRepo: assetRepo, reportRepo: reportRepo}
}

// ExportTxRow 是 transactions.csv 的一行
type ExportTxRow struct {
	ID           string
	OccurredAt   time.Time
	Source       string
	Account      string
	Direction    string
	AmountFen    int64
	AmountYuan   string
	CategoryID   string
	CategoryName string
	Description  string
	Counterparty string
	Note         string
	Status       string
}

// TransactionRows 全量流水，按 occurred_at 升序，附带分类名称（CSV 导出用）
func (uc *Export) TransactionRows(ctx context.Context) ([]ExportTxRow, error) {
	txs, err := uc.txRepo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	nameByID := make(map[string]string, len(cats))
	for _, c := range cats {
		nameByID[c.ID] = c.Name
	}
	rows := make([]ExportTxRow, 0, len(txs))
	for _, t := range txs {
		rows = append(rows, ExportTxRow{
			ID:           t.ID,
			OccurredAt:   t.OccurredAt,
			Source:       string(t.Source),
			Account:      string(t.Account),
			Direction:    string(t.Direction),
			AmountFen:    t.Amount,
			AmountYuan:   formatYuanFen(t.Amount),
			CategoryID:   t.CategoryID,
			CategoryName: nameByID[t.CategoryID],
			Description:  t.Description,
			Counterparty: t.Counterparty,
			Note:         t.Note,
			Status:       string(t.Status),
		})
	}
	return rows, nil
}

// FullExport 是 full.json 的顶层结构
type FullExport struct {
	ExportedAt     time.Time              `json:"exported_at"`
	Transactions   []domain.Transaction   `json:"transactions"`
	Categories     []domain.Category      `json:"categories"`
	CategoryRules  []domain.CategoryRule  `json:"category_rules"`
	AssetSnapshots []domain.AssetSnapshot `json:"asset_snapshots"`
	Reports        []domain.AIReport      `json:"reports"`
	ImportBatches  []domain.ImportBatch   `json:"import_batches"`
}

// BuildFull 汇总全量数据，供 handler 用 json.Encoder 流式写出
func (uc *Export) BuildFull(ctx context.Context) (FullExport, error) {
	txs, err := uc.txRepo.ListAll(ctx)
	if err != nil {
		return FullExport{}, err
	}
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return FullExport{}, err
	}
	rules, err := uc.ruleRepo.ListRules(ctx)
	if err != nil {
		return FullExport{}, err
	}
	snaps, err := uc.assetRepo.ListAll(ctx)
	if err != nil {
		return FullExport{}, err
	}
	reports, err := uc.reportRepo.ListAll(ctx)
	if err != nil {
		return FullExport{}, err
	}
	batches, err := uc.txRepo.ListAllImportBatches(ctx)
	if err != nil {
		return FullExport{}, err
	}
	return FullExport{
		ExportedAt:     time.Now(),
		Transactions:   txs,
		Categories:     cats,
		CategoryRules:  rules,
		AssetSnapshots: snaps,
		Reports:        reports,
		ImportBatches:  batches,
	}, nil
}
