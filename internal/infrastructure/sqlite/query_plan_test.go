package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
)

// TestAggregateByCategoryUsesCoveringIndex 回归保护：聚合查询必须命中迁移 013 的覆盖索引
// idx_tx_category_occurred，否则 100k 行时会退化为每科目全范围重扫（165ms→2.9ms 的差别）。
func TestAggregateByCategoryUsesCoveringIndex(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 与 AggregateByCategory 相同的 SQL（family 口径，无 account 过滤）
	const q = `
EXPLAIN QUERY PLAN
SELECT c.id, c.name, COALESCE(c.parent_id,''), COALESCE(SUM(t.amount),0) AS total
FROM categories c
LEFT JOIN transactions t
  ON t.category_id = c.id
  AND t.occurred_at >= ? AND t.occurred_at < ?
  AND t.status = 'confirmed'
WHERE c.level = 2
GROUP BY c.id, c.name, c.parent_id
ORDER BY c.sort_order`
	rows, err := db.QueryContext(context.Background(), q, "2026-04-01", "2026-07-01")
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
		t.Fatalf("query plan 未命中覆盖索引 idx_tx_category_occurred:\n%s", plan.String())
	}
}

// planFor 取一条查询的 EXPLAIN QUERY PLAN 全文
func planFor(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
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
	return plan.String()
}

// TestAnalyzeMakesSpecialIndexReachable 回归保护：迁移 014 建的 idx_tx_special 只有在
// sqlite_stat1 里有统计时才会被选中。没有统计，SQLite 按固定猜测估行数
// （special_id IS NOT NULL 猜 1/4、status='confirmed' 猜 1/10），永远去走 idx_tx_status
// 扫十分之一张表——10 万行下 SumByProject 49.5ms vs 6.7ms 的差别。
//
// 表驱动覆盖三条以 special_id 为主过滤条件的聚合，逐条断言"跑 ANALYZE 前不走专项索引、
// 跑完之后走"。谁把 main.go 里的 sqlite.Analyze 去掉，这个测试立刻红。
func TestAnalyzeMakesSpecialIndexReachable(t *testing.T) {
	f := newScopeFixture(t)
	// 造点量，否则统计信息里的行数太小、优化器的选择没有区分度
	rows := make([]fixtureTx, 0, 600)
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.Local)
	for i := 0; i < 600; i++ {
		special := ""
		if i%50 == 0 { // 2% 挂专项，与实测的分布一致
			special = spReno
		}
		rows = append(rows, fixtureTx{
			id: "bulk-" + strconv.Itoa(i), account: domain.AccountHusband,
			day: base.Add(time.Duration(i) * time.Hour), amount: int64(100 + i),
			direction: domain.DirectionExpense, status: domain.TxStatusConfirmed,
			category: "expense.necessary.food", special: special,
		})
	}
	insertFixtureRows(t, f.txRepo, rows...)

	const (
		sumByProject = `
SELECT t.special_id, COALESCE(` + netAmountSQL + `,0)
FROM transactions t
WHERE t.special_id IS NOT NULL AND t.status = 'confirmed'
GROUP BY t.special_id`
		sumByProjectInPeriod = `
SELECT t.special_id, COALESCE(` + netAmountSQL + `,0)
FROM transactions t
WHERE t.special_id IS NOT NULL AND t.status = 'confirmed'
  AND t.occurred_at >= ? AND t.occurred_at < ?
GROUP BY t.special_id`
		sumByCategoryAll = `
SELECT t.special_id, COALESCE(t.category_id,''), COALESCE(c.name,'未分类'), COALESCE(c.parent_id,''), COALESCE(` + netAmountSQL + `,0) AS total
FROM transactions t
LEFT JOIN categories c ON c.id = t.category_id
WHERE t.special_id IS NOT NULL AND t.status = 'confirmed'
GROUP BY t.special_id, t.category_id, c.name, c.parent_id
HAVING total != 0
ORDER BY t.special_id, total DESC`
	)

	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{"SumByProject", sumByProject, nil},
		{"SumByProjectInPeriod", sumByProjectInPeriod, []any{f.period.Start, f.period.End}},
		{"SumByCategoryForAllProjects", sumByCategoryAll, nil},
	}

	// 1. 没有统计信息：一条都走不到 idx_tx_special
	for _, tt := range tests {
		if plan := planFor(t, f.db, tt.sql, tt.args...); strings.Contains(plan, "idx_tx_special") {
			t.Fatalf("%s：没跑 ANALYZE 就命中了 idx_tx_special，这个回归保护失去意义了\n%s", tt.name, plan)
		}
	}

	// 2. 跑一次 ANALYZE（= main.go 启动时做的事）后，全部改走专项索引
	if err := Analyze(f.db); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := planFor(t, f.db, tt.sql, tt.args...)
			if !strings.Contains(plan, "idx_tx_special") {
				t.Fatalf("跑过 ANALYZE 仍未命中 idx_tx_special:\n%s", plan)
			}
		})
	}
}

// TestAnalyzeKeepsResultsIdentical ANALYZE 只该换计划、不该换结果。
func TestAnalyzeKeepsResultsIdentical(t *testing.T) {
	f := newNetFixture(t)
	ctx := context.Background()

	snapshot := func() string {
		sums, err := f.spRepo.SumByProject(ctx)
		if err != nil {
			t.Fatalf("SumByProject() error = %v", err)
		}
		bd, err := f.spRepo.SumByCategoryForAllProjects(ctx)
		if err != nil {
			t.Fatalf("SumByCategoryForAllProjects() error = %v", err)
		}
		daily, special, err := f.txRepo.SumByBuckets(ctx, f.buckets, domain.DirectionExpense, domain.AccountFamily)
		if err != nil {
			t.Fatalf("SumByBuckets() error = %v", err)
		}
		return fmt.Sprintf("%v|%v|%v|%v", sums, bd, daily, special)
	}
	before := snapshot()
	if err := Analyze(f.db); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("ANALYZE 改变了查询结果\nbefore=%s\nafter =%s", before, after)
	}
}

// TestAnalyzeOnEmptyDB 空库上 ANALYZE 不能报错（首次启动就会跑到这条路径）
func TestAnalyzeOnEmptyDB(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Analyze(db); err != nil {
		t.Fatalf("Analyze(空库) error = %v", err)
	}
	// 空库上跑完还得能正常查询
	if _, err := NewSpecialProjectRepo(db).SumByProject(context.Background()); err != nil {
		t.Fatalf("SumByProject(空库) error = %v", err)
	}
}
