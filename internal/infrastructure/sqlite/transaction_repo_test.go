package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

func newTestRepo(t *testing.T) *TransactionRepo {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewTransactionRepo(db)
}

func insertTestTx(t *testing.T, repo *TransactionRepo, id string, occurredAt time.Time, amount int64, status domain.TxStatus) {
	t.Helper()
	now := time.Now()
	err := repo.Insert(context.Background(), domain.Transaction{
		ID:         id,
		Source:     domain.SourceManual,
		Account:    domain.AccountHusband,
		OccurredAt: occurredAt,
		Amount:     amount,
		Direction:  domain.DirectionExpense,
		Status:     status,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("insert tx %s: %v", id, err)
	}
}

func TestUpdateReturnsNotFoundForMissingID(t *testing.T) {
	repo := newTestRepo(t)
	note := "备注"
	err := repo.Update(context.Background(), "no-such-id", port.TransactionUpdate{Note: &note})
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("Update(missing id) error = %v; want port.ErrNotFound", err)
	}
}

func TestUpdateExistingRow(t *testing.T) {
	repo := newTestRepo(t)
	insertTestTx(t, repo, "tx-1", time.Date(2026, 5, 10, 12, 0, 0, 0, time.Local), 1000, domain.TxStatusPendingReview)

	note := "改备注"
	if err := repo.Update(context.Background(), "tx-1", port.TransactionUpdate{Note: &note}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got, err := repo.Get(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Note != note {
		t.Fatalf("Note = %q; want %q", got.Note, note)
	}
}

// TestUpdateLeavesUnsetFieldsAlone patch 里为 nil 的字段一句 SQL 都不写。
// 这是 LLM 兜底分类（只发 CategoryID + Status）不会把用户手工标好的
// special_id / 备注冲掉的底层保证，配合 usecase 侧的 TestClassifyPendingKeepsSpecialID 一起看。
func TestUpdateLeavesUnsetFieldsAlone(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	// 一笔已归入装修专项、但还没分类的待确认流水
	insertFixtureRows(t, f.txRepo, fixtureTx{
		id: "u1", account: domain.AccountWife,
		day: time.Date(2026, 5, 9, 10, 0, 0, 0, time.Local), amount: 5000,
		direction: domain.DirectionExpense, status: domain.TxStatusPendingReview,
		category: "", special: spReno,
	})
	note := "装修定金"
	if err := f.txRepo.Update(ctx, "u1", port.TransactionUpdate{Note: &note}); err != nil {
		t.Fatalf("Update(note) error = %v", err)
	}

	// LLM 兜底：只补分类 + 转 confirmed
	cat := "expense.family.home_maintenance"
	status := domain.TxStatusConfirmed
	if err := f.txRepo.Update(ctx, "u1", port.TransactionUpdate{CategoryID: &cat, Status: &status}); err != nil {
		t.Fatalf("Update(category) error = %v", err)
	}

	got, err := f.txRepo.Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.SpecialID != spReno {
		t.Fatalf("special_id = %q; want %q（patch 没提到它就不该动）", got.SpecialID, spReno)
	}
	if got.Note != note {
		t.Fatalf("note = %q; want %q", got.Note, note)
	}
	if got.Account != domain.AccountWife {
		t.Fatalf("account = %q; want %q", got.Account, domain.AccountWife)
	}
	if got.CategoryID != cat || got.Status != domain.TxStatusConfirmed {
		t.Fatalf("category/status = %q/%q; want %q/confirmed", got.CategoryID, got.Status, cat)
	}

	// 补分类后这笔就该计入专项净额：130000 + 5000
	sums, err := f.spRepo.SumByProject(ctx)
	if err != nil {
		t.Fatalf("SumByProject() error = %v", err)
	}
	if sums[spReno] != 135000 {
		t.Fatalf("专项 %s 净额 = %d; want 135000（补分类后这笔应计入）", spReno, sums[spReno])
	}
}

func TestSumByBuckets(t *testing.T) {
	repo := newTestRepo(t)
	loc := time.Local
	// 三个月的桶：2026-04 / 2026-05 / 2026-06
	buckets := []port.PeriodBucket{
		{Label: "2026-04", Start: time.Date(2026, 4, 1, 0, 0, 0, 0, loc), End: time.Date(2026, 5, 1, 0, 0, 0, 0, loc)},
		{Label: "2026-05", Start: time.Date(2026, 5, 1, 0, 0, 0, 0, loc), End: time.Date(2026, 6, 1, 0, 0, 0, 0, loc)},
		{Label: "2026-06", Start: time.Date(2026, 6, 1, 0, 0, 0, 0, loc), End: time.Date(2026, 7, 1, 0, 0, 0, 0, loc)},
	}

	insertTestTx(t, repo, "a", time.Date(2026, 4, 15, 9, 0, 0, 0, loc), 100, domain.TxStatusConfirmed)
	insertTestTx(t, repo, "b", time.Date(2026, 5, 1, 0, 0, 0, 0, loc), 200, domain.TxStatusConfirmed) // 桶边界起点，算 5 月
	insertTestTx(t, repo, "c", time.Date(2026, 5, 31, 23, 59, 59, 0, loc), 300, domain.TxStatusConfirmed)
	insertTestTx(t, repo, "d", time.Date(2026, 5, 20, 8, 0, 0, 0, loc), 999, domain.TxStatusExcluded)  // 非 confirmed 不计
	insertTestTx(t, repo, "e", time.Date(2026, 3, 31, 12, 0, 0, 0, loc), 888, domain.TxStatusConfirmed) // 范围外不计
	insertTestTx(t, repo, "f", time.Date(2026, 7, 1, 0, 0, 0, 0, loc), 777, domain.TxStatusConfirmed)   // End 独占，不计

	got, err := repo.SumByBuckets(context.Background(), buckets, domain.DirectionExpense, domain.AccountFamily, domain.ScopeAll)
	if err != nil {
		t.Fatalf("SumByBuckets() error = %v", err)
	}
	wantAmounts := []int64{100, 500, 0}
	for i, w := range wantAmounts {
		if got[i].Amount != w {
			t.Fatalf("bucket %s amount = %d; want %d", got[i].Label, got[i].Amount, w)
		}
	}

	// 按账户过滤：wife 名下无流水
	gotWife, err := repo.SumByBuckets(context.Background(), buckets, domain.DirectionExpense, domain.AccountWife, domain.ScopeAll)
	if err != nil {
		t.Fatalf("SumByBuckets(wife) error = %v", err)
	}
	for _, b := range gotWife {
		if b.Amount != 0 {
			t.Fatalf("wife bucket %s amount = %d; want 0", b.Label, b.Amount)
		}
	}

	// 空桶列表
	empty, err := repo.SumByBuckets(context.Background(), nil, domain.DirectionExpense, domain.AccountFamily, domain.ScopeAll)
	if err != nil || len(empty) != 0 {
		t.Fatalf("SumByBuckets(nil) = %v, %v; want empty, nil", empty, err)
	}
}
