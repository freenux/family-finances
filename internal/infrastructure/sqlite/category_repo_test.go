package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

func newTestCategoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestGetNotFoundIsPortErrNotFound 「按 id 取单条」找不到时必须是 port.ErrNotFound，
// 而不是 sql.ErrNoRows：usecase / 适配器只认 port 的哨兵，认不出就会把 404 变成 500。
// 用替身的单测发现不了这件事，所以必须在真库夹具上钉住。
func TestGetNotFoundIsPortErrNotFound(t *testing.T) {
	db := newTestCategoryDB(t)
	ctx := context.Background()
	tests := []struct {
		name string
		get  func() error
	}{
		{"CategoryRepo.GetRule", func() error { _, err := NewCategoryRepo(db).GetRule(ctx, "no-such-rule"); return err }},
		{"TransactionRepo.Get", func() error { _, err := NewTransactionRepo(db).Get(ctx, "no-such-tx"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.get()
			if !errors.Is(err, port.ErrNotFound) {
				t.Fatalf("err = %v; want errors.Is(err, port.ErrNotFound)", err)
			}
			if errors.Is(err, sql.ErrNoRows) {
				t.Errorf("err 不应再暴露 sql.ErrNoRows（持久化细节不外泄）: %v", err)
			}
		})
	}
}

func TestGetRuleFound(t *testing.T) {
	repo := NewCategoryRepo(newTestCategoryDB(t))
	ctx := context.Background()
	rule := domain.CategoryRule{ID: "r-1", Pattern: "星巴克", PatternType: "contains", Field: "counterparty", IsActive: true}
	if err := repo.InsertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetRule(ctx, "r-1")
	if err != nil || got.Pattern != "星巴克" {
		t.Fatalf("GetRule = (%+v, %v); want 命中且无错", got, err)
	}
}
