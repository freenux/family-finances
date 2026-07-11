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

func newTestAssetSnapshotRepo(t *testing.T) *AssetSnapshotRepo {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewAssetSnapshotRepo(db)
}

func TestAssetSnapshotRepoGetByPeriodNotFound(t *testing.T) {
	repo := newTestAssetSnapshotRepo(t)
	_, err := repo.GetByPeriod(context.Background(), "2026Q2")
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("GetByPeriod() error = %v; want port.ErrNotFound", err)
	}
}

func TestAssetSnapshotRepoUpsertIsIdempotentByPeriod(t *testing.T) {
	repo := newTestAssetSnapshotRepo(t)
	ctx := context.Background()

	snap := domain.AssetSnapshot{
		ID:           "snap-1",
		Period:       "2026Q2",
		SnapshotDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local),
		Data:         map[string]int64{"asset.cash": 100000},
		NetWorth:     100000,
		CreatedAt:    time.Now(),
	}
	if err := repo.Upsert(ctx, &snap); err != nil {
		t.Fatalf("Upsert() first error = %v", err)
	}

	// 同 period 再次写入（覆盖）
	snap2 := snap
	snap2.Data = map[string]int64{"asset.cash": 200000}
	snap2.NetWorth = 200000
	if err := repo.Upsert(ctx, &snap2); err != nil {
		t.Fatalf("Upsert() second error = %v", err)
	}

	got, err := repo.GetByPeriod(ctx, "2026Q2")
	if err != nil {
		t.Fatalf("GetByPeriod() error = %v", err)
	}
	if got.NetWorth != 200000 {
		t.Fatalf("NetWorth = %d; want 200000 (覆盖后的值)", got.NetWorth)
	}
	if got.Data["asset.cash"] != 200000 {
		t.Fatalf("Data[asset.cash] = %d; want 200000", got.Data["asset.cash"])
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("len(ListAll()) = %d; want 1 (同 period 应覆盖而非新增行)", len(all))
	}
}

func TestAssetSnapshotRepoListByPeriodAscOrdersAscending(t *testing.T) {
	repo := newTestAssetSnapshotRepo(t)
	ctx := context.Background()
	periods := []string{"2025Q4", "2026Q1", "2026Q2"}
	for i, p := range periods {
		snap := domain.AssetSnapshot{
			ID:           "snap-" + p,
			Period:       p,
			SnapshotDate: time.Now(),
			Data:         map[string]int64{"asset.cash": int64(i)},
			NetWorth:     int64(i),
			CreatedAt:    time.Now(),
		}
		if err := repo.Upsert(ctx, &snap); err != nil {
			t.Fatalf("Upsert(%s) error = %v", p, err)
		}
	}

	got, err := repo.ListByPeriodAsc(ctx, 12)
	if err != nil {
		t.Fatalf("ListByPeriodAsc() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d; want 3", len(got))
	}
	for i, p := range periods {
		if got[i].Period != p {
			t.Fatalf("got[%d].Period = %q; want %q (want ascending order)", i, got[i].Period, p)
		}
	}

	limited, err := repo.ListByPeriodAsc(ctx, 2)
	if err != nil {
		t.Fatalf("ListByPeriodAsc(limit=2) error = %v", err)
	}
	if len(limited) != 2 || limited[0].Period != "2026Q1" || limited[1].Period != "2026Q2" {
		t.Fatalf("limited = %+v; want the 2 most recent periods ascending", limited)
	}
}
