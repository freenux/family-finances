package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// ---- 迁移 015（历史财报存档的口径标注）的 up / down ----
//
// 015 只做一件事：给 reports 加 data_scope，默认 'all'。
// 关键在"默认值就是存量行的真实口径"——015 之前落库的报告确实是全口径生成的，
// 所以不需要任何数据回填；新报告由 usecase 显式写 'daily'。

// columnDefault pragma table_info 里这一列的默认值与 NOT NULL 标记
func columnDefault(t *testing.T, db *sql.DB, table, column string) (dflt sql.NullString, notNull int, found bool) {
	t.Helper()
	rows, err := db.Query(`SELECT name, "notnull", dflt_value FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var nn int
		var d sql.NullString
		if err := rows.Scan(&name, &nn, &d); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == column {
			dflt, notNull, found = d, nn, true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return dflt, notNull, found
}

func TestMigration015Up(t *testing.T) {
	db := newMigratedDB(t)

	if !columnExists(t, db, "reports", "data_scope") {
		t.Fatal("reports.data_scope 列不存在")
	}
	dflt, notNull, _ := columnDefault(t, db, "reports", "data_scope")
	if notNull != 1 {
		t.Fatalf("reports.data_scope notnull = %d; want 1", notNull)
	}
	if !dflt.Valid || dflt.String != "'all'" {
		t.Fatalf("reports.data_scope 默认值 = %v; want 'all'（存量行的真实口径）", dflt)
	}
}

// TestMigration015BackfillsNothing 015 之前落库的行，升级后必须自动带上 'all'。
// 这正是"不需要回填"的依据：先迁到 014、写一行、再迁到 015。
func TestMigration015BackfillsNothing(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	goose.SetBaseFS(embeddedMigrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.UpTo(db, "migrations", 14); err != nil {
		t.Fatalf("goose up to 014: %v", err)
	}
	// 口径拆分之前的那批存档：全口径数字，没有 data_scope 这一列
	if _, err := db.Exec(`
INSERT INTO reports (id, period, period_type, generated_at, kpi_data, status, created_at)
VALUES ('rep-legacy', '2026Q1', 'quarterly', ?, '{"kpi":{"savings_rate":0.25}}', 'final', ?)`,
		time.Now(), time.Now()); err != nil {
		t.Fatalf("insert legacy report: %v", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	var scope string
	if err := db.QueryRow(`SELECT data_scope FROM reports WHERE id = 'rep-legacy'`).Scan(&scope); err != nil {
		t.Fatalf("select data_scope: %v", err)
	}
	if scope != "all" {
		t.Fatalf("存量行 data_scope = %q; want \"all\"（默认值即真实口径，无需回填）", scope)
	}
}

// TestMigration015Down 回滚后 data_scope 消失，reports 表本身与其中的数据都还在
func TestMigration015Down(t *testing.T) {
	db := newMigratedDB(t)

	if _, err := db.Exec(`
INSERT INTO reports (id, period, period_type, generated_at, data_scope, status, created_at)
VALUES ('rep-1', '2026Q2', 'quarterly', ?, 'daily', 'final', ?)`, time.Now(), time.Now()); err != nil {
		t.Fatalf("insert report: %v", err)
	}

	goose.SetBaseFS(embeddedMigrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.DownTo(db, "migrations", 14); err != nil {
		t.Fatalf("goose down to 014: %v", err)
	}

	if columnExists(t, db, "reports", "data_scope") {
		t.Fatal("down 后 reports.data_scope 列仍然存在")
	}
	if !tableExists(t, db, "reports") {
		t.Fatal("down 误删了 reports 表")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reports`).Scan(&n); err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if n != 1 {
		t.Fatalf("down 后 reports 行数 = %d; want 1（只掉一列，不该丢数据）", n)
	}
}
