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
