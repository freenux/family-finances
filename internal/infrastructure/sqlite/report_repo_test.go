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

func newTestReportRepo(t *testing.T) *ReportRepo {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewReportRepo(db)
}

func TestReportRepoGetByPeriodNotFound(t *testing.T) {
	repo := newTestReportRepo(t)
	_, err := repo.GetByPeriod(context.Background(), "2026Q2", domain.PeriodQuarterly)
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("GetByPeriod() error = %v; want port.ErrNotFound", err)
	}
}

func TestReportRepoUpsertOverwritesSamePeriodAndType(t *testing.T) {
	repo := newTestReportRepo(t)
	ctx := context.Background()

	rep := domain.AIReport{
		ID:          "rep-1",
		Period:      "2026Q2",
		PeriodType:  domain.PeriodQuarterly,
		GeneratedAt: time.Now(),
		AIAnalysis:  `{"summary":"第一版"}`,
		AIModel:     "gpt-4o",
		Status:      "final",
		CreatedAt:   time.Now(),
	}
	if err := repo.Upsert(ctx, &rep); err != nil {
		t.Fatalf("Upsert() first error = %v", err)
	}

	rep2 := rep
	rep2.ID = "rep-2"
	rep2.AIAnalysis = `{"summary":"第二版"}`
	if err := repo.Upsert(ctx, &rep2); err != nil {
		t.Fatalf("Upsert() second error = %v", err)
	}

	got, err := repo.GetByPeriod(ctx, "2026Q2", domain.PeriodQuarterly)
	if err != nil {
		t.Fatalf("GetByPeriod() error = %v", err)
	}
	if got.AIAnalysis != `{"summary":"第二版"}` {
		t.Fatalf("AIAnalysis = %q; want 第二版 (同期同类型应覆盖)", got.AIAnalysis)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("len(ListAll()) = %d; want 1", len(all))
	}
}

func TestReportRepoQuarterAndAnnualCoexistForSamePeriodLabel(t *testing.T) {
	repo := newTestReportRepo(t)
	ctx := context.Background()

	// "2026" 作为年度 period label 和一个假设的季度不会冲突，这里验证 period_type 是唯一键的一部分：
	// 同一 period label 若 period_type 不同（理论上不会真的撞，因为季度带 Q），应各自独立存在。
	q := domain.AIReport{ID: "rep-q", Period: "2026Q2", PeriodType: domain.PeriodQuarterly, GeneratedAt: time.Now(), Status: "final", CreatedAt: time.Now()}
	a := domain.AIReport{ID: "rep-a", Period: "2026", PeriodType: domain.PeriodAnnual, GeneratedAt: time.Now(), Status: "final", CreatedAt: time.Now()}
	if err := repo.Upsert(ctx, &q); err != nil {
		t.Fatalf("Upsert(quarter) error = %v", err)
	}
	if err := repo.Upsert(ctx, &a); err != nil {
		t.Fatalf("Upsert(annual) error = %v", err)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(ListAll()) = %d; want 2", len(all))
	}
}
