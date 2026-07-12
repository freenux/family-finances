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

func newTestProfileRepo(t *testing.T) *ProfileRepo {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewProfileRepo(db)
}

func TestProfileRepoGetNotFound(t *testing.T) {
	repo := newTestProfileRepo(t)
	_, err := repo.Get(context.Background())
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("Get() error = %v; want port.ErrNotFound", err)
	}
}

func TestProfileRepoUpsertSingleRow(t *testing.T) {
	repo := newTestProfileRepo(t)
	ctx := context.Background()

	monthly := int64(2000000)
	p := domain.FamilyProfile{
		ID:                 "default",
		FamilyStructure:    "夫妻+子女",
		MainAge:            36,
		IncomeStability:    domain.IncomeStable,
		AnnualIncomeFen:    40000000,
		MortgageMonthlyFen: 800000,
		MonthlyExpenseFen:  &monthly,
		EmergencyMonths:    6,
		RiskAppetite:       domain.RiskBalanced,
		Horizon:            domain.HorizonLong,
		UpdatedAt:          time.Now(),
	}
	if err := repo.Upsert(ctx, &p); err != nil {
		t.Fatalf("Upsert() first error = %v", err)
	}

	// 二次保存覆盖，且 monthly_expense 置回 NULL
	p.MainAge = 37
	p.MonthlyExpenseFen = nil
	if err := repo.Upsert(ctx, &p); err != nil {
		t.Fatalf("Upsert() second error = %v", err)
	}

	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.MainAge != 37 {
		t.Fatalf("MainAge = %d; want 37 (覆盖后的值)", got.MainAge)
	}
	if got.MonthlyExpenseFen != nil {
		t.Fatalf("MonthlyExpenseFen = %v; want nil", *got.MonthlyExpenseFen)
	}
	if got.FamilyStructure != "夫妻+子女" || got.RiskAppetite != domain.RiskBalanced {
		t.Fatalf("unexpected profile: %+v", got)
	}
}
