package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
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
