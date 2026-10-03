package sqlite

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// txSortColumns 排序键白名单 → 列名。SQL 里只会出现 map 的值，绝不拼接入参。
var txSortColumns = map[string]string{
	"occurred_at":  "occurred_at",
	"amount":       "amount",
	"counterparty": "counterparty",
	"category_id":  "category_id",
	"special_id":   "special_id",
	"status":       "status",
	"member":       "member",
	"account":      "account",
	"source":       "source",
	"direction":    "direction",
}

// escapeLike 转义 LIKE 通配符，配合 ESCAPE '\' 使用
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}

// ruleWhere 规则预筛的 SQL 形式，语义对齐 domain.RuleMatches：
// 字段集 counterparty/description/platform_category，exact 全等，否则子串，大小写不敏感。
// 子串用 instr 而不是 LIKE，免去通配符转义问题。
func ruleWhere(rule domain.CategoryRule) (string, []any) {
	pattern := strings.ToLower(strings.TrimSpace(rule.Pattern))
	if pattern == "" {
		return "0", nil
	}
	var cols []string
	switch rule.Field {
	case "counterparty":
		cols = []string{"counterparty"}
	case "description":
		cols = []string{"description"}
	case "platform_category":
		cols = []string{"platform_category"}
	default:
		cols = []string{"counterparty", "description", "platform_category"}
	}
	parts := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for _, c := range cols {
		if rule.PatternType == "exact" {
			parts = append(parts, "lower(COALESCE("+c+",'')) = ?")
		} else {
			parts = append(parts, "instr(lower(COALESCE("+c+",'')), ?) > 0")
		}
		args = append(args, pattern)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// buildTxWhere 流水查询的唯一 WHERE 构造：取行与合计两条 SQL 共用，避免条件写跑偏。
// 返回值以 " WHERE ..." 开头（无条件时为空串）。
func buildTxWhere(q port.TransactionQuery) (string, []any) {
	var conds []string
	var args []any
	add := func(c string, a ...any) {
		conds = append(conds, c)
		args = append(args, a...)
	}
	if !q.Period.Start.IsZero() {
		add("occurred_at >= ? AND occurred_at < ?", q.Period.Start, q.Period.End)
	}
	if q.Account.IsStorageAccount() {
		add("account = ?", string(q.Account))
	}
	if q.Direction == string(domain.DirectionIncome) || q.Direction == string(domain.DirectionExpense) {
		add("direction = ?", q.Direction)
	}
	if len(q.Sources) > 0 {
		var ors []string
		var a []any
		for _, s := range q.Sources {
			if s == "csv" {
				ors = append(ors, "source LIKE 'csv:%'")
			} else {
				ors = append(ors, "source = ?")
				a = append(a, s)
			}
		}
		add("("+strings.Join(ors, " OR ")+")", a...)
	}
	if len(q.Statuses) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(q.Statuses)), ",")
		a := make([]any, len(q.Statuses))
		for i, s := range q.Statuses {
			a[i] = s
		}
		add("status IN ("+ph+")", a...)
	}
	switch q.Category {
	case "":
	case port.FilterNone:
		add("category_id IS NULL")
	default:
		add("category_id = ?", q.Category)
	}
	switch q.Special {
	case "":
	case port.FilterNone:
		add("special_id IS NULL")
	case port.FilterAny:
		add("special_id IS NOT NULL")
	default:
		add("special_id = ?", q.Special)
	}
	switch q.Member {
	case "":
	case port.FilterNone:
		add("(member = '' OR member IS NULL)")
	default:
		add("member = ?", q.Member)
	}
	if q.Keyword != "" {
		like := "%" + escapeLike(q.Keyword) + "%"
		add(`(COALESCE(counterparty,'') LIKE ? ESCAPE '\' OR COALESCE(description,'') LIKE ? ESCAPE '\'
 OR COALESCE(note,'') LIKE ? ESCAPE '\' OR COALESCE(raw_row,'') LIKE ? ESCAPE '\')`, like, like, like, like)
	}
	if q.Rule != nil {
		c, a := ruleWhere(*q.Rule)
		add(c, a...)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// QueryTransactions 筛选/排序/分页全部下推 SQL；Total 与 Totals 另用一条聚合 SQL 取全集。
func (r *TransactionRepo) QueryTransactions(ctx context.Context, q port.TransactionQuery) (port.TransactionPage, error) {
	var page port.TransactionPage
	where, args := buildTxWhere(q)

	err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN direction = 'income'  THEN amount END), 0),
       COALESCE(SUM(CASE WHEN direction = 'expense' THEN amount END), 0)
FROM transactions`+where, args...).Scan(&page.Total, &page.Totals.IncomeFen, &page.Totals.ExpenseFen)
	if err != nil {
		return page, fmt.Errorf("count: %w", err)
	}

	col, ok := txSortColumns[q.SortBy]
	if !ok {
		col = "occurred_at"
	}
	dir := "ASC"
	if q.SortDesc {
		dir = "DESC"
	}
	// id DESC 是稳定 tiebreaker：排序键相同的行顺序固定，翻页才不漏不重
	sqlText := selectTxSQL + where + " ORDER BY " + col + " " + dir + ", id DESC"
	qargs := append([]any(nil), args...)
	if q.Limit > 0 {
		sqlText += " LIMIT ? OFFSET ?"
		qargs = append(qargs, q.Limit, max(q.Offset, 0))
	}
	rows, err := r.db.QueryContext(ctx, sqlText, qargs...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	page.Rows, err = scanTxs(rows)
	return page, err
}

// DistinctMembers 周期+账户内的非空成员标注，Go 侧按拼音/字典序（中文用 Unicode 码点序）排序
func (r *TransactionRepo) DistinctMembers(ctx context.Context, p domain.Period, account domain.Account) ([]string, error) {
	where, args := buildTxWhere(port.TransactionQuery{Period: p, Account: account})
	if where == "" {
		where = " WHERE member != ''"
	} else {
		where += " AND member != ''"
	}
	rows, err := r.db.QueryContext(ctx, "SELECT DISTINCT member FROM transactions"+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// ApplyCategoryByRule 见 port 接口说明。
func (r *TransactionRepo) ApplyCategoryByRule(ctx context.Context, p domain.Period, account domain.Account, rule domain.CategoryRule, direction string) (int, error) {
	if rule.CategoryID == "" {
		return 0, fmt.Errorf("规则没有目标科目，无法批量应用")
	}
	where, args := buildTxWhere(port.TransactionQuery{
		Period: p, Account: account, Direction: direction, Rule: &rule,
		Statuses: []string{string(domain.TxStatusPendingReview), string(domain.TxStatusConfirmed)},
	})
	where += " AND NOT (category_id IS ? AND status = 'confirmed')"
	all := append([]any{rule.CategoryID, time.Now()}, args...)
	all = append(all, rule.CategoryID)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		"UPDATE transactions SET category_id = ?, status = 'confirmed', updated_at = ?"+where, all...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

var _ port.TransactionQueryRepo = (*TransactionRepo)(nil)
