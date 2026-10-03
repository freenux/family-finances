package usecase

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ruleDirection 规则预筛与批量应用共用的方向约束，只看支出
const ruleDirection = string(domain.DirectionExpense)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// TxQuery 流水列表用例：参数归一 + 查询 + DTO 装配。不碰 HTTP。
type TxQuery struct {
	txRepo      port.TransactionQueryRepo
	catRepo     port.CategoryRepo
	ruleRepo    port.CategoryRuleRepo
	specialRepo port.SpecialProjectRepo // 可选：nil 时 special_text 为空
	nav         PeriodNav
}

func NewTxQuery(tx port.TransactionQueryRepo, cat port.CategoryRepo, rules port.CategoryRuleRepo) *TxQuery {
	return &TxQuery{txRepo: tx, catRepo: cat, ruleRepo: rules}
}

// WithSpecialRepo 注入专项仓库，用于填 special_text
func (q *TxQuery) WithSpecialRepo(r port.SpecialProjectRepo) *TxQuery {
	q.specialRepo = r
	return q
}

// WithNav 替换周期导航（测试注入时钟）
func (q *TxQuery) WithNav(n PeriodNav) *TxQuery {
	q.nav = n
	return q
}

// TxQueryRequest 原始 query 参数，未校验；归一在 Execute 里做。
type TxQueryRequest struct {
	Type, Period, Account string
	Direction             string
	Source                string // 逗号分隔
	Status                string // 逗号分隔
	Category, Special     string
	Member, Keyword       string
	RuleID                string
	Sort, Order           string
	Page, PageSize        string
}

type TxRow struct {
	ID            string `json:"id"`
	OccurredAt    string `json:"occurred_at"`
	OccurredText  string `json:"occurred_text"`
	Counterparty  string `json:"counterparty"`
	Description   string `json:"description"`
	Note          string `json:"note"`
	RawRow        string `json:"raw_row"`
	Member        string `json:"member"`
	Account       string `json:"account"`
	AccountText   string `json:"account_text"`
	Source        string `json:"source"`
	SourceText    string `json:"source_text"`
	AmountFen     int64  `json:"amount_fen"`
	AmountText    string `json:"amount_text"`
	Direction     string `json:"direction"`
	DirectionText string `json:"direction_text"`
	Status        string `json:"status"`
	StatusText    string `json:"status_text"`
	CategoryID    string `json:"category_id"`
	CategoryText  string `json:"category_text"`
	SpecialID     string `json:"special_id"`
	SpecialText   string `json:"special_text"`
}

type PageInfo struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

type TotalsView struct {
	IncomeFen   int64  `json:"income_fen"`
	IncomeText  string `json:"income_text"`
	ExpenseFen  int64  `json:"expense_fen"`
	ExpenseText string `json:"expense_text"`
	NetFen      int64  `json:"net_fen"`
	NetText     string `json:"net_text"`
}

type FacetOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type FacetsView struct {
	Members []FacetOption `json:"members"`
}

type RuleView struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
}

type TxQueryResult struct {
	Rows   []TxRow       `json:"rows"`
	Period PeriodNavView `json:"period"`
	Page   PageInfo      `json:"page"`
	Totals TotalsView    `json:"totals"`
	Facets FacetsView    `json:"facets"`
	Rule   *RuleView     `json:"rule,omitempty"`
}

// txSortKeys 对外排序键 → repo 列名键。白名单之外一律回落 occurred_at。
var txSortKeys = map[string]string{
	"occurred_at":  "occurred_at",
	"amount":       "amount",
	"counterparty": "counterparty",
	"category":     "category_id",
	"special":      "special_id",
	"status":       "status",
	"member":       "member",
	"account":      "account",
	"source":       "source",
	"direction":    "direction",
}

var validStatuses = map[string]bool{
	string(domain.TxStatusPendingReview): true,
	string(domain.TxStatusConfirmed):     true,
	string(domain.TxStatusExcluded):      true,
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseAccount(s string) domain.Account {
	switch domain.Account(s) {
	case domain.AccountHusband:
		return domain.AccountHusband
	case domain.AccountWife:
		return domain.AccountWife
	default:
		return domain.AccountFamily
	}
}

// Execute 归一参数 → 查询 → 装配 DTO
func (q *TxQuery) Execute(ctx context.Context, req TxQueryRequest) (TxQueryResult, error) {
	var res TxQueryResult
	var rulePtr *domain.CategoryRule
	var rule domain.CategoryRule
	ruleID := strings.TrimSpace(req.RuleID)
	if ruleID != "" {
		r, err := q.ruleRepo.GetRule(ctx, ruleID)
		if err != nil {
			if errors.Is(err, port.ErrNotFound) {
				return res, fmt.Errorf("规则不存在: %w", err)
			}
			return res, err
		}
		rule, rulePtr = r, &r
	}

	var p domain.Period
	var err error
	if rulePtr != nil && strings.TrimSpace(req.Period) == "" && strings.TrimSpace(req.Type) == "" {
		p = q.nav.ForRuleView() // ?rule_id= 的例外，原因见 ForRuleView
	} else if p, err = q.nav.Resolve(req.Type, req.Period); err != nil {
		return res, err
	}
	acc := parseAccount(req.Account)

	dir := ""
	if req.Direction == string(domain.DirectionIncome) || req.Direction == string(domain.DirectionExpense) {
		dir = req.Direction
	}
	if rulePtr != nil {
		// 规则预览只看支出：ClassifyByCustomRules 对收入流水直接 return，规则永远不会分类收入，
		// 预览必须镜像分类器，且与 ApplyRule 的改动集合完全一致（覆盖请求里传的 direction）
		dir = ruleDirection
	}
	// status 缺省不含 excluded；全是非法值时同样视为没传
	var statuses []string
	for _, s := range splitCSV(req.Status) {
		if validStatuses[s] {
			statuses = append(statuses, s)
		}
	}
	if len(statuses) == 0 {
		statuses = []string{string(domain.TxStatusPendingReview), string(domain.TxStatusConfirmed)}
	}

	sortKey, ok := txSortKeys[req.Sort]
	if !ok {
		sortKey = "occurred_at"
	}
	desc := !strings.EqualFold(req.Order, "asc") // 缺省 desc

	page, _ := strconv.Atoi(strings.TrimSpace(req.Page))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(strings.TrimSpace(req.PageSize))
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}

	out, err := q.txRepo.QueryTransactions(ctx, port.TransactionQuery{
		Period: p, Account: acc, Direction: dir,
		Sources:  splitCSV(req.Source),
		Statuses: statuses,
		Category: req.Category, Special: req.Special, Member: req.Member,
		Keyword: strings.TrimSpace(req.Keyword),
		Rule:    rulePtr,
		SortBy:  sortKey, SortDesc: desc,
		Offset: (page - 1) * size, Limit: size,
	})
	if err != nil {
		return res, err
	}

	cats, err := q.catRepo.ListAll(ctx)
	if err != nil {
		return res, err
	}
	catName := make(map[string]string, len(cats))
	for _, c := range cats {
		catName[c.ID] = c.Name
	}
	specialName := map[string]string{}
	if q.specialRepo != nil {
		sps, err := q.specialRepo.ListAll(ctx)
		if err != nil {
			return res, err
		}
		for _, s := range sps {
			specialName[s.ID] = s.Name
		}
	}
	members, err := q.txRepo.DistinctMembers(ctx, p, acc)
	if err != nil {
		return res, err
	}

	res.Rows = make([]TxRow, 0, len(out.Rows))
	for _, t := range out.Rows {
		res.Rows = append(res.Rows, buildTxRow(t, catName, specialName))
	}
	res.Period = q.nav.Nav(p)
	res.Page = PageInfo{Page: page, PageSize: size, Total: out.Total, TotalPages: (out.Total + size - 1) / size}
	res.Totals = TotalsView{
		IncomeFen: out.Totals.IncomeFen, IncomeText: FormatYuan(out.Totals.IncomeFen),
		ExpenseFen: out.Totals.ExpenseFen, ExpenseText: FormatYuan(out.Totals.ExpenseFen),
		NetFen: out.Totals.IncomeFen - out.Totals.ExpenseFen, NetText: FormatYuan(out.Totals.IncomeFen - out.Totals.ExpenseFen),
	}
	res.Facets.Members = append(res.Facets.Members, FacetOption{Value: port.FilterNone, Label: "未标注"})
	for _, m := range members {
		res.Facets.Members = append(res.Facets.Members, FacetOption{Value: m, Label: m})
	}
	if rulePtr != nil {
		res.Rule = &RuleView{ID: rule.ID, Label: RuleLabel(rule), CategoryID: rule.CategoryID, CategoryName: catName[rule.CategoryID]}
	}
	return res, nil
}

func buildTxRow(t domain.Transaction, catName, specialName map[string]string) TxRow {
	return TxRow{
		ID:           t.ID,
		OccurredAt:   t.OccurredAt.Format(time.RFC3339),
		OccurredText: t.OccurredAt.Format("2006-01-02 15:04"),
		Counterparty: t.Counterparty, Description: t.Description, Note: t.Note, RawRow: t.RawRow,
		Member:  t.Member,
		Account: string(t.Account), AccountText: accountText(t.Account),
		Source: string(t.Source), SourceText: sourceText(string(t.Source)),
		AmountFen: t.Amount, AmountText: FormatYuan(t.Amount),
		Direction: string(t.Direction), DirectionText: directionText(t.Direction),
		Status: string(t.Status), StatusText: statusText(t.Status),
		CategoryID: t.CategoryID, CategoryText: catName[t.CategoryID],
		SpecialID: t.SpecialID, SpecialText: specialName[t.SpecialID],
	}
}

func accountText(a domain.Account) string {
	if a == domain.AccountHusband || a == domain.AccountWife {
		return a.Label()
	}
	return string(a)
}

func sourceText(s string) string {
	if name, ok := strings.CutPrefix(s, "csv:"); ok {
		return "CSV·" + name
	}
	switch s {
	case "alipay":
		return "支付宝"
	case "wechat":
		return "微信"
	case "manual":
		return "手填"
	}
	return s
}

func directionText(d domain.Direction) string {
	switch d {
	case domain.DirectionIncome:
		return "收入"
	case domain.DirectionExpense:
		return "支出"
	}
	return string(d)
}

// statusText 沿用 transactions.html 里的现有措辞
func statusText(s domain.TxStatus) string {
	switch s {
	case domain.TxStatusPendingReview:
		return "待处理"
	case domain.TxStatusConfirmed:
		return "已确认"
	case domain.TxStatusExcluded:
		return "已排除"
	}
	return string(s)
}

// FormatYuan 分 → "1,234.50"，千分位 + 两位小数，负号在最前（对齐 tx_table.js 的 fmtYuan）
func FormatYuan(fen int64) string {
	sign := ""
	u := uint64(fen)
	if fen < 0 {
		sign = "-"
		u = -u // 对 MinInt64 同样正确
	}
	digits := strconv.FormatUint(u/100, 10)
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return fmt.Sprintf("%s%s.%02d", sign, b.String(), u%100)
}

// RuleLabel 规则的可读描述，如「交易对方 包含「星巴克」」
func RuleLabel(r domain.CategoryRule) string {
	field := "任意字段"
	switch r.Field {
	case "counterparty":
		field = "交易对方"
	case "description":
		field = "商品说明"
	case "platform_category":
		field = "平台分类"
	}
	kind := "包含"
	if r.PatternType == "exact" {
		kind = "等于"
	}
	return fmt.Sprintf("%s %s「%s」", field, kind, r.Pattern)
}

// ApplyRule 把规则批量应用到周期+账户内命中的流水：改科目并置 confirmed，返回改动行数。
// 规则没有目标科目（跳过导入类）时返回 0 和说明性错误，不会清空任何人的分类。
func (q *TxQuery) ApplyRule(ctx context.Context, ruleID string, p domain.Period, acc domain.Account) (int, error) {
	rule, err := q.ruleRepo.GetRule(ctx, ruleID)
	if err != nil {
		return 0, err
	}
	if rule.CategoryID == "" {
		return 0, fmt.Errorf("该规则是「跳过导入」类规则，没有目标科目，无法批量应用")
	}
	cats, err := q.catRepo.ListAll(ctx)
	if err != nil {
		return 0, err
	}
	for _, c := range cats {
		if c.ID != rule.CategoryID {
			continue
		}
		if c.Type == domain.CategoryTypeIncome {
			return 0, fmt.Errorf("规则指向收入科目 %s：分类器对收入流水不做自动分类，这是一条永远不会命中的死规则，不予批量应用", c.Name)
		}
		// 方向与 Execute 的规则预筛同为 ruleDirection，保证两边集合相同
		return q.txRepo.ApplyCategoryByRule(ctx, p, acc, rule, ruleDirection)
	}
	return 0, fmt.Errorf("规则指向的科目 %s 不存在", rule.CategoryID)
}
