package domain

import (
	"strings"
	"time"
)

type CategoryType string

const (
	CategoryTypeIncome  CategoryType = "income"
	CategoryTypeExpense CategoryType = "expense"
	// CategoryTypeTransfer 资金往来：转账/借还款/报销垫付。既不是收入也不是支出。
	CategoryTypeTransfer CategoryType = "transfer"
)

// TransferPrefix 资金往来科目的 ID 前缀，与 "income." / "expense." 并列的第三个顶层命名空间。
const TransferPrefix = "transfer."

// IsTransferCategory 判断科目是否属于「资金往来」——内部转账、借还款、报销垫付。
//
// 这类流水只是钱换了个口袋（提现/还信用卡），或是债权债务的一出一回
// （借出→收回、垫付→报销到账），两头相抵净影响为零，不构成收支。
//
// 它们必须以 TxStatusExcluded（UI 标签「不计收支」）落地，绝不能是 confirmed：
// SumByBuckets / TopTransactions / ListForRecurring 只按 direction + status 过滤、
// 不看科目，一条 confirmed 的转账会被四笔钱、周报、目标进度、月/季对比条、
// Top 榜单五处当成真支出。（现金流表与饼图走的是 income./expense. 前缀分流，
// transfer. 两边都落不进，那条路本身是安全的。）
//
// 三个写入点共用这一个判据，别各写各的：ImportBill 落地、PATCH 改分类、LLM 回写。
func IsTransferCategory(id string) bool {
	return strings.HasPrefix(id, TransferPrefix)
}

type Category struct {
	ID         string
	ParentID   string
	Level      int
	Name       string
	GroupEmoji string
	Type       CategoryType
	SortOrder  int
}

// CategoryAggregation 表示某个二级科目在某期间的聚合结果
type CategoryAggregation struct {
	CategoryID string
	Name       string
	ParentID   string
	Amount     int64
}

type CategoryRule struct {
	ID          string
	Pattern     string
	PatternType string
	Field       string
	CategoryID  string
	Priority    int
	Source      string
	IsActive    bool
	CreatedAt   time.Time
}
