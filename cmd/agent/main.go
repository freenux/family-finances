package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/infrastructure/config"
	"family-finances/internal/infrastructure/sqlite"
	"family-finances/internal/usecase"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		writeError(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("缺少命令；当前支持: import")
	}
	switch args[0] {
	case "import":
		return runImport(args[1:])
	case "-h", "--help", "help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("未知命令 %q；当前支持: import", args[0])
	}
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sourceFlag := fs.String("source", "", "账单来源: alipay 或 wechat")
	accountFlag := fs.String("account", "", "账户归属: husband 或 wife")
	fileFlag := fs.String("file", "", "账单文件路径")
	dbFlag := fs.String("db", "", "SQLite 数据库路径；默认读取 DATABASE_PATH 或 ./family.db")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sourceFlag == "" || *accountFlag == "" || *fileFlag == "" {
		return fmt.Errorf("import 需要 --source、--account、--file")
	}

	source, err := parseSource(*sourceFlag)
	if err != nil {
		return err
	}
	account := domain.Account(*accountFlag)
	if !account.IsStorageAccount() {
		return fmt.Errorf("无效账户 %q；可选: husband, wife", *accountFlag)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}
	dbPath := cfg.DatabasePath
	if *dbFlag != "" {
		dbPath = *dbFlag
	}

	file, err := os.Open(*fileFlag)
	if err != nil {
		return fmt.Errorf("打开账单文件失败: %w", err)
	}
	defer file.Close()

	db, err := sqlite.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := sqlite.Migrate(db); err != nil {
		return err
	}

	txRepo := sqlite.NewTransactionRepo(db)
	catRepo := sqlite.NewCategoryRepo(db)
	importBill := usecase.NewImportBill(txRepo, catRepo)

	res, err := importBill.Execute(context.Background(), usecase.ImportBillInput{
		Source:   source,
		Account:  account,
		Filename: *fileFlag,
		Reader:   file,
	})
	if err != nil {
		return err
	}
	writeJSON(importOutput{
		OK:                 true,
		Source:             string(source),
		Account:            string(account),
		File:               *fileFlag,
		DatabasePath:       dbPath,
		TotalRows:          res.TotalRows,
		InsertedRows:       res.InsertedRows,
		SkippedDuplicates:  res.SkippedDuplicates,
		SkippedInvalid:     res.SkippedInvalid,
		PendingCategory:    res.PendingCategory,
		EarliestOccurredAt: formatOptionalTime(res.EarliestOccurredAt),
	})
	return nil
}

func parseSource(v string) (domain.Source, error) {
	switch v {
	case string(domain.SourceAlipay):
		return domain.SourceAlipay, nil
	case string(domain.SourceWechat):
		return domain.SourceWechat, nil
	default:
		return "", fmt.Errorf("无效账单来源 %q；可选: alipay, wechat", v)
	}
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

type importOutput struct {
	OK                 bool   `json:"ok"`
	Source             string `json:"source"`
	Account            string `json:"account"`
	File               string `json:"file"`
	DatabasePath       string `json:"database_path"`
	TotalRows          int    `json:"total_rows"`
	InsertedRows       int    `json:"inserted_rows"`
	SkippedDuplicates  int    `json:"skipped_duplicates"`
	SkippedInvalid     int    `json:"skipped_invalid"`
	PendingCategory    int    `json:"pending_category"`
	EarliestOccurredAt string `json:"earliest_occurred_at,omitempty"`
}

func writeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(err error) {
	writeJSON(map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
}

func printUsage() {
	fmt.Fprintln(os.Stdout, `用法:
  agent import --source alipay|wechat --account husband|wife --file <path> [--db <path>]

说明:
  当前 CLI 只支持导入流水文件。默认数据库路径来自 .env/DATABASE_PATH，未配置时为 ./family.db。`)
}
