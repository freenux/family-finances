package domain

import "time"

type CategoryType string

const (
	CategoryTypeIncome  CategoryType = "income"
	CategoryTypeExpense CategoryType = "expense"
)

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
