package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// Export 全量数据导出（/export 页面用例）：遍历各 repo，避免 handler 直接摸 SQL。
type Export struct {
	txRepo     port.TransactionRepo
	catRepo    port.CategoryRepo
	ruleRepo   port.CategoryRuleRepo
	assetRepo  port.AssetSnapshotRepo
	reportRepo port.ReportRepo
}

func NewExport(txRepo port.TransactionRepo, catRepo port.CategoryRepo, ruleRepo port.CategoryRuleRepo,
	assetRepo port.AssetSnapshotRepo, reportRepo port.ReportRepo) *Export {
	return &Export{txRepo: txRepo, catRepo: catRepo, ruleRepo: ruleRepo, assetRepo: assetRepo, reportRepo: reportRepo}
}

// ExportTxRow 是 transactions.csv 的一行
type ExportTxRow struct {
	ID           string
	OccurredAt   time.Time
	Source       string
	Account      string
	Direction    string
	AmountFen    int64
	AmountYuan   string
	CategoryID   string
	CategoryName string
	Description  string
	Counterparty string
	Note         string
	Status       string
}

// TransactionRows 全量流水，按 occurred_at 升序，附带分类名称（CSV 导出用）
func (uc *Export) TransactionRows(ctx context.Context) ([]ExportTxRow, error) {
	txs, err := uc.txRepo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	nameByID := make(map[string]string, len(cats))
	for _, c := range cats {
		nameByID[c.ID] = c.Name
	}
	rows := make([]ExportTxRow, 0, len(txs))
	for _, t := range txs {
		rows = append(rows, ExportTxRow{
			ID:           t.ID,
			OccurredAt:   t.OccurredAt,
			Source:       string(t.Source),
			Account:      string(t.Account),
			Direction:    string(t.Direction),
			AmountFen:    t.Amount,
			AmountYuan:   formatYuanFen(t.Amount),
			CategoryID:   t.CategoryID,
			CategoryName: nameByID[t.CategoryID],
			Description:  t.Description,
			Counterparty: t.Counterparty,
			Note:         t.Note,
			Status:       string(t.Status),
		})
	}
	return rows, nil
}

// FullExport 是 full.json 的顶层结构
type FullExport struct {
	ExportedAt     time.Time              `json:"exported_at"`
	Transactions   []domain.Transaction   `json:"transactions"`
	Categories     []domain.Category      `json:"categories"`
	CategoryRules  []domain.CategoryRule  `json:"category_rules"`
	AssetSnapshots []domain.AssetSnapshot `json:"asset_snapshots"`
	Reports        []domain.AIReport      `json:"reports"`
	ImportBatches  []domain.ImportBatch   `json:"import_batches"`
}

// BuildFull 汇总全量数据，供 handler 用 json.Encoder 流式写出
func (uc *Export) BuildFull(ctx context.Context) (FullExport, error) {
	txs, err := uc.txRepo.ListAll(ctx)
	if err != nil {
		return FullExport{}, err
	}
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return FullExport{}, err
	}
	rules, err := uc.ruleRepo.ListRules(ctx)
	if err != nil {
		return FullExport{}, err
	}
	snaps, err := uc.assetRepo.ListAll(ctx)
	if err != nil {
		return FullExport{}, err
	}
	reports, err := uc.reportRepo.ListAll(ctx)
	if err != nil {
		return FullExport{}, err
	}
	batches, err := uc.txRepo.ListAllImportBatches(ctx)
	if err != nil {
		return FullExport{}, err
	}
	return FullExport{
		ExportedAt:     time.Now(),
		Transactions:   txs,
		Categories:     cats,
		CategoryRules:  rules,
		AssetSnapshots: snaps,
		Reports:        reports,
		ImportBatches:  batches,
	}, nil
}

// ---- Beancount 导出 ----

// BuildBeancount 全量流水（不含 excluded）转 Beancount 复式记账文本。
// income.* → Income:...、expense.* → Expenses:...（点分路径驼峰化）；
// 资金账户按来源：Assets:WeChat / Assets:Alipay / Assets:Manual / Assets:Csv:<模板名>。
func (uc *Export) BuildBeancount(ctx context.Context) (string, error) {
	txs, err := uc.txRepo.ListAll(ctx)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("; family-finances Beancount export\n")
	sb.WriteString("; generated at " + time.Now().Format(time.RFC3339) + "\n")
	sb.WriteString("; 金额单位 CNY；收入行金额为负（复式记账）\n\n")
	sb.WriteString("option \"title\" \"家庭财务\"\n")
	sb.WriteString("option \"operating_currency\" \"CNY\"\n\n")

	type entry struct {
		date      string
		payee     string
		narration string
		catAcc    string
		assetAcc  string
		amountFen int64
		income    bool
	}
	var entries []entry
	accounts := map[string]bool{}
	for _, t := range txs {
		if t.Status == domain.TxStatusExcluded {
			continue
		}
		catAcc := beancountCategoryAccount(t.CategoryID, t.Direction)
		assetAcc := beancountAssetAccount(string(t.Source))
		accounts[catAcc] = true
		accounts[assetAcc] = true
		entries = append(entries, entry{
			date:      t.OccurredAt.Format("2006-01-02"),
			payee:     beancountEscape(t.Counterparty),
			narration: beancountEscape(t.Description),
			catAcc:    catAcc,
			assetAcc:  assetAcc,
			amountFen: t.Amount,
			income:    t.Direction == domain.DirectionIncome,
		})
	}

	accList := make([]string, 0, len(accounts))
	for a := range accounts {
		accList = append(accList, a)
	}
	sort.Strings(accList)
	for _, a := range accList {
		sb.WriteString("1970-01-01 open " + a + " CNY\n")
	}
	sb.WriteString("\n")

	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("%s * \"%s\" \"%s\"\n", e.date, e.payee, e.narration))
		amount := formatYuanPlain(e.amountFen)
		if e.income {
			// 收入：Income 腿为负
			sb.WriteString(fmt.Sprintf("  %s  -%s CNY\n", e.catAcc, amount))
			sb.WriteString(fmt.Sprintf("  %s  %s CNY\n\n", e.assetAcc, amount))
		} else {
			sb.WriteString(fmt.Sprintf("  %s  %s CNY\n", e.catAcc, amount))
			sb.WriteString(fmt.Sprintf("  %s  -%s CNY\n\n", e.assetAcc, amount))
		}
	}
	return sb.String(), nil
}

// beancountCategoryAccount 分类科目 → Beancount 账户
func beancountCategoryAccount(categoryID string, dir domain.Direction) string {
	root := "Expenses"
	if dir == domain.DirectionIncome {
		root = "Income"
	}
	if categoryID == "" {
		return root + ":Uncategorized"
	}
	parts := strings.Split(categoryID, ".")
	segs := []string{root}
	for _, p := range parts[1:] { // 跳过 income/expense 前缀
		segs = append(segs, beancountCamel(p))
	}
	if len(segs) == 1 {
		return root + ":Uncategorized"
	}
	return strings.Join(segs, ":")
}

// beancountAssetAccount 资金账户
func beancountAssetAccount(source string) string {
	switch source {
	case "wechat":
		return "Assets:WeChat"
	case "alipay":
		return "Assets:Alipay"
	case "manual":
		return "Assets:Manual"
	}
	if name, ok := strings.CutPrefix(source, "csv:"); ok {
		return "Assets:Csv:" + beancountSanitize(name)
	}
	return "Assets:" + beancountSanitize(source)
}

// beancountCamel snake_case → CamelCase
func beancountCamel(s string) string {
	parts := strings.Split(s, "_")
	var sb strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		sb.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	if sb.Len() == 0 {
		return "X"
	}
	return sb.String()
}

// beancountSanitize Beancount 账户段只允许 ASCII 字母数字；中文等字符剔除，空则回退 Generic
func beancountSanitize(s string) string {
	var sb strings.Builder
	upperNext := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			if upperNext {
				sb.WriteRune(r - 32)
				upperNext = false
			} else {
				sb.WriteRune(r)
			}
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
			upperNext = false
		default:
			upperNext = true
		}
	}
	if sb.Len() == 0 {
		return "Generic"
	}
	return sb.String()
}

func beancountEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// formatYuanPlain 分 → "1234.56"（无千位分隔，Beancount 数字格式）
func formatYuanPlain(fen int64) string {
	if fen < 0 {
		fen = -fen
	}
	return fmt.Sprintf("%d.%02d", fen/100, fen%100)
}
