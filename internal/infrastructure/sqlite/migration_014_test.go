package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"family-finances/internal/domain"
)

// ---- 迁移 014（专项开支）的 up / down 与索引形状 ----
//
// 014 干了三件事：建 special_projects 表、给 transactions 加 special_id、
// 把 013 的覆盖索引从四列重建为五列（把 special_id 塞进去）。
// 第三件事是性能红线：口径过滤加进 SQL 后 special_id 不在索引里就会逐行回表，
// 013 的聚合优化（季度 165ms→2.9ms）直接作废。

func newMigratedDB(t *testing.T) *sql.DB {
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

// tableExists sqlite_master 里有没有这张表
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master(%s): %v", name, err)
	}
	return n > 0
}

// columnExists pragma table_info 里有没有这一列
func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return false
}

// indexColumns 索引的列，按索引内顺序
func indexColumns(t *testing.T, db *sql.DB, index string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, index)
	if err != nil {
		t.Fatalf("pragma index_info(%s): %v", index, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestMigration014Up(t *testing.T) {
	db := newMigratedDB(t)

	tests := []struct {
		name string
		want bool
		got  func() bool
	}{
		{"special_projects 表存在", true, func() bool { return tableExists(t, db, "special_projects") }},
		{"transactions.special_id 列存在", true, func() bool { return columnExists(t, db, "transactions", "special_id") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(); got != tt.want {
				t.Fatalf("%s = %v; want %v", tt.name, got, tt.want)
			}
		})
	}

	// 覆盖索引必须是五列，且 special_id 在里面——否则口径过滤会逐行回表
	t.Run("覆盖索引重建为五列", func(t *testing.T) {
		cols := indexColumns(t, db, "idx_tx_category_occurred")
		want := []string{"category_id", "occurred_at", "status", "special_id", "amount"}
		if len(cols) != len(want) {
			t.Fatalf("idx_tx_category_occurred 列 = %v; want %v", cols, want)
		}
		for i := range want {
			if cols[i] != want[i] {
				t.Fatalf("idx_tx_category_occurred 列 = %v; want %v", cols, want)
			}
		}
	})

	t.Run("special_id 单列索引存在", func(t *testing.T) {
		if cols := indexColumns(t, db, "idx_tx_special"); len(cols) != 1 || cols[0] != "special_id" {
			t.Fatalf("idx_tx_special 列 = %v; want [special_id]", cols)
		}
	})
}

// TestAggregateWithScopeFilterStillUsesCoveringIndex 013 的性能优化不能被 014 作废：
// 带 special_id 过滤的聚合查询仍必须走覆盖索引（query_plan_test.go 的同类回归保护）。
func TestAggregateWithScopeFilterStillUsesCoveringIndex(t *testing.T) {
	db := newMigratedDB(t)

	// 与 AggregateByCategory 在 ScopeDaily / ScopeSpecial 下生成的 SQL 一致
	base := `
EXPLAIN QUERY PLAN
SELECT c.id, c.name, COALESCE(c.parent_id,''), COALESCE(SUM(t.amount),0) AS total
FROM categories c
LEFT JOIN transactions t
  ON t.category_id = c.id
  AND t.occurred_at >= ? AND t.occurred_at < ?
  AND t.status = 'confirmed'%s
WHERE c.level = 2
GROUP BY c.id, c.name, c.parent_id
ORDER BY c.sort_order`

	tests := []struct {
		name   string
		filter string
	}{
		{"日常口径（special_id IS NULL）", " AND t.special_id IS NULL"},
		{"仅专项口径（special_id IS NOT NULL）", " AND t.special_id IS NOT NULL"},
		{"全口径（无过滤）", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := db.QueryContext(context.Background(), strings.Replace(base, "%s", tt.filter, 1),
				"2026-04-01", "2026-07-01")
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			defer rows.Close()
			var plan strings.Builder
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					t.Fatalf("scan: %v", err)
				}
				plan.WriteString(detail + "\n")
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows: %v", err)
			}
			if !strings.Contains(plan.String(), "COVERING INDEX idx_tx_category_occurred") {
				t.Fatalf("query plan 未命中覆盖索引 idx_tx_category_occurred（013 的优化被作废）:\n%s", plan.String())
			}
		})
	}
}

// TestMigration014Down goose down 后必须干净还原成 013 的样子：
// special_projects 表消失、special_id 列消失、覆盖索引回到四列。
// 库里先塞一个专项 + 一条挂在它上面的流水——真实回滚时不会是空库，
// 带 FK 引用的 DROP COLUMN / DROP TABLE 才是有风险的那一步。
func TestMigration014Down(t *testing.T) {
	db := newMigratedDB(t)

	ctx := context.Background()
	sp := domain.SpecialProject{ID: "sp-down", Name: "回滚用装修", BudgetFen: 100}
	if err := NewSpecialProjectRepo(db).Upsert(ctx, &sp); err != nil {
		t.Fatalf("upsert special: %v", err)
	}
	now := time.Now()
	if err := NewTransactionRepo(db).Insert(ctx, domain.Transaction{
		ID: "tx-down", Source: domain.SourceManual, Account: domain.AccountHusband,
		OccurredAt: now, Amount: 100, Direction: domain.DirectionExpense,
		Status: domain.TxStatusConfirmed, SpecialID: sp.ID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert tx: %v", err)
	}

	// DownTo(13) 而不是 Down()：Down 只回滚最后一个迁移，后面每加一版就会让这里
	// 测的不再是 014。回滚到 013 才是这个用例真正要断言的状态。
	goose.SetBaseFS(embeddedMigrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.DownTo(db, "migrations", 13); err != nil {
		t.Fatalf("goose down to 013: %v", err)
	}

	if tableExists(t, db, "special_projects") {
		t.Fatal("down 后 special_projects 表仍然存在")
	}
	if columnExists(t, db, "transactions", "special_id") {
		t.Fatal("down 后 transactions.special_id 列仍然存在")
	}
	if len(indexColumns(t, db, "idx_tx_special")) != 0 {
		t.Fatal("down 后 idx_tx_special 索引仍然存在")
	}
	cols := indexColumns(t, db, "idx_tx_category_occurred")
	want := []string{"category_id", "occurred_at", "status", "amount"}
	if len(cols) != len(want) {
		t.Fatalf("down 后 idx_tx_category_occurred 列 = %v; want %v（应还原成 013 的四列）", cols, want)
	}
	for i := range want {
		if cols[i] != want[i] {
			t.Fatalf("down 后 idx_tx_category_occurred 列 = %v; want %v", cols, want)
		}
	}

	// 还原后 013 的覆盖索引仍然有效（不带口径过滤的聚合查询）
	rows, err := db.Query(`
EXPLAIN QUERY PLAN
SELECT c.id, COALESCE(SUM(t.amount),0)
FROM categories c
LEFT JOIN transactions t
  ON t.category_id = c.id AND t.occurred_at >= ? AND t.occurred_at < ? AND t.status = 'confirmed'
WHERE c.level = 2
GROUP BY c.id`, "2026-04-01", "2026-07-01")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan.WriteString(detail + "\n")
	}
	if !strings.Contains(plan.String(), "COVERING INDEX idx_tx_category_occurred") {
		t.Fatalf("down 后 013 的覆盖索引失效:\n%s", plan.String())
	}
}
