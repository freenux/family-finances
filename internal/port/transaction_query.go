package port

import (
	"context"

	"family-finances/internal/domain"
)

// 流水列表查询里的哨兵值（Category / Special / Member 字段）。
const (
	FilterNone = "__none__" // 无值：category/special 为 NULL，member 为空串或 NULL
	FilterAny  = "__any__"  // 仅 Special：任意专项（IS NOT NULL）
)

// TransactionQuery 流水列表查询条件。零值字段 = 不过滤。
// 这是「真实现金流」视图，故意不带 domain.Scope：专项取舍只由 Special 字段控制。
type TransactionQuery struct {
	Period    domain.Period
	Account   domain.Account
	Direction string               // "" | "income" | "expense"
	Sources   []string             // 空 = 不过滤；元素 "csv" 匹配所有 source LIKE 'csv:%'
	Statuses  []string             // 空 = 不过滤（缺省值由 usecase 层决定，repo 只管照做）
	Category  string               // "" 不过滤；FilterNone = category_id IS NULL
	Special   string               // "" 不过滤；FilterNone = IS NULL；FilterAny = IS NOT NULL；其它 = 等于
	Member    string               // "" 不过滤；FilterNone = member 为空串或 NULL
	Keyword   string               // 匹配 counterparty/description/note/raw_row
	Rule      *domain.CategoryRule // 非 nil 时按规则预筛，语义与 domain.RuleMatches 一致
	SortBy    string               // 列名键，已由 usecase 白名单校验；repo 再兜底一次
	SortDesc  bool
	Offset    int
	Limit     int // <=0 = 不限
}

// TransactionTotals 满足筛选的全集合计（分）
type TransactionTotals struct{ IncomeFen, ExpenseFen int64 }

// TransactionPage 一页流水 + 全集统计
type TransactionPage struct {
	Rows   []domain.Transaction
	Total  int               // 满足筛选的总行数（不是当页行数）
	Totals TransactionTotals // 满足筛选的全集合计（不是当页合计）
}

// TransactionQueryRepo 流水列表查询契约。
//
// 设计文档要求把这几个方法加在 TransactionRepo 上，但 adapter/web/handler 的测试替身
// （stubTxRepo）实现了整个 TransactionRepo，而那个目录本阶段不许动，直接加会让
// handler 测试编译失败。所以先独立成一个接口，sqlite.TransactionRepo 同时实现两者；
// 等 handler 阶段补完替身后，可把这三个方法并入 TransactionRepo 并删掉本接口。
type TransactionQueryRepo interface {
	// QueryTransactions 流水列表：筛选、排序、分页全部在 SQL 里完成。
	// Total / Totals 是满足筛选的全集统计，与 Limit/Offset 无关。
	QueryTransactions(ctx context.Context, q TransactionQuery) (TransactionPage, error)
	// DistinctMembers 周期+账户范围内出现过的非空成员标注（中文序）；不受其它筛选影响
	DistinctMembers(ctx context.Context, p domain.Period, account domain.Account) ([]string, error)
	// ApplyCategoryByRule 把周期+账户内被规则命中、且不是「已是该科目且 confirmed」的流水
	// 改为 rule.CategoryID 并置 confirmed；单事务单条 UPDATE，返回改动行数。
	// direction 非空时只改该方向（usecase 恒传 expense，与规则预筛集合一致）；excluded 的流水不动。rule.CategoryID 为空时返回错误。
	ApplyCategoryByRule(ctx context.Context, p domain.Period, account domain.Account, rule domain.CategoryRule, direction string) (int, error)
}
