package sqlite

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return db, nil
}

// Analyze 跑一次 ANALYZE，把真实的列分布写进 sqlite_stat1 供代价优化器使用。
//
// 没有统计信息时 SQLite 只能按固定猜测估行数——`special_id IS NOT NULL` 猜 1/4、
// `status = 'confirmed'` 猜 1/10——于是所有专项聚合都去走 idx_tx_status 扫十分之一张表，
// 迁移 014 专门建的 idx_tx_special 永远不会被选中。实测 10 万行下 SumByProject 49.5ms、
// 跑过 ANALYZE 后 6.7ms；月/季对比条的专项那一组 87.0ms → 10.4ms。
//
// 为什么不写死 INDEXED BY：收益随数据分布变化（专项占比 2% 时 7 倍、10% 时 2.4 倍），
// 该由代价优化器按当下的数据决定，而不是在 SQL 里钉死一个索引。
//
// 为什么在启动时跑一次而不是 PRAGMA optimize：PRAGMA optimize 只考虑"本连接上被查询过的表"，
// 而 database/sql 是连接池，每次调用落在哪条连接上不可控，触发时机因此不可预期。
// 统计会随数据增长变陈旧，但让这条 plan 翻车的是"专项占全表的比例"而不是绝对行数——
// 行数整体涨大时两个候选索引的估算等比放大，选择不变；比例真的变了（比如新开了一堆专项），
// 下次进程启动重跑即可。代价是启动时一次性 ~350ms（10 万行），换掉每次 /report、/specials
// 都要白付的几十毫秒。
func Analyze(db *sql.DB) error {
	if _, err := db.Exec("ANALYZE"); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return nil
}

func Migrate(db *sql.DB) error {
	goose.SetBaseFS(embeddedMigrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
