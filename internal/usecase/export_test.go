package usecase

import (
	"context"
	"testing"
	"time"

	"family-finances/internal/domain"
)

func TestExportTransactionRowsFillsCategoryNameAndYuan(t *testing.T) {
	txRepo := &fakeTransactionRepo{allTxs: []domain.Transaction{
		{
			ID:         "tx-1",
			OccurredAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			Source:     domain.SourceAlipay,
			Account:    domain.AccountHusband,
			Direction:  domain.DirectionExpense,
			Amount:     123456,
			CategoryID: "expense.discretion.shopping",
			Status:     domain.TxStatusConfirmed,
		},
		{
			ID:         "tx-2",
			OccurredAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
			Source:     domain.SourceWechat,
			Account:    domain.AccountWife,
			Direction:  domain.DirectionIncome,
			Amount:     900000,
			CategoryID: "", // 未分类
			Status:     domain.TxStatusPendingReview,
		},
	}}
	catRepo := &fakeCategoryRepo{cats: testCategories()}
	uc := NewExport(txRepo, catRepo, &fakeCategoryRuleRepo{}, &fakeAssetSnapshotRepo{}, &fakeReportRepo{})

	rows, err := uc.TransactionRows(context.Background())
	if err != nil {
		t.Fatalf("TransactionRows() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d; want 2", len(rows))
	}
	if rows[0].CategoryName != "购物消费" {
		t.Fatalf("CategoryName = %q; want 购物消费", rows[0].CategoryName)
	}
	if rows[0].AmountYuan != "1234.56" {
		t.Fatalf("AmountYuan = %q; want 1234.56", rows[0].AmountYuan)
	}
	if rows[1].CategoryName != "" {
		t.Fatalf("CategoryName for uncategorized row = %q; want empty", rows[1].CategoryName)
	}
}

func TestExportBuildFullAggregatesAllSources(t *testing.T) {
	txRepo := &fakeTransactionRepo{
		allTxs:     []domain.Transaction{{ID: "tx-1"}},
		allBatches: []domain.ImportBatch{{ID: "batch-1"}},
	}
	catRepo := &fakeCategoryRepo{cats: testCategories()}
	ruleRepo := &fakeCategoryRuleRepo{rules: []domain.CategoryRule{{ID: "rule-1"}}}
	assetRepo := &fakeAssetSnapshotRepo{byPeriod: map[string]domain.AssetSnapshot{
		"2026Q2": {ID: "snap-1", Period: "2026Q2"},
	}}
	reportRepo := &fakeReportRepo{saved: []domain.AIReport{{ID: "report-1"}}}

	uc := NewExport(txRepo, catRepo, ruleRepo, assetRepo, reportRepo)
	full, err := uc.BuildFull(context.Background())
	if err != nil {
		t.Fatalf("BuildFull() error = %v", err)
	}
	if len(full.Transactions) != 1 || full.Transactions[0].ID != "tx-1" {
		t.Fatalf("Transactions = %+v", full.Transactions)
	}
	if len(full.Categories) != len(testCategories()) {
		t.Fatalf("Categories len = %d; want %d", len(full.Categories), len(testCategories()))
	}
	if len(full.CategoryRules) != 1 || full.CategoryRules[0].ID != "rule-1" {
		t.Fatalf("CategoryRules = %+v", full.CategoryRules)
	}
	if len(full.AssetSnapshots) != 1 || full.AssetSnapshots[0].ID != "snap-1" {
		t.Fatalf("AssetSnapshots = %+v", full.AssetSnapshots)
	}
	if len(full.Reports) != 1 || full.Reports[0].ID != "report-1" {
		t.Fatalf("Reports = %+v", full.Reports)
	}
	if len(full.ImportBatches) != 1 || full.ImportBatches[0].ID != "batch-1" {
		t.Fatalf("ImportBatches = %+v", full.ImportBatches)
	}
	if full.ExportedAt.IsZero() {
		t.Fatal("ExportedAt is zero; want populated timestamp")
	}
}
