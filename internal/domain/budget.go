package domain

import "time"

// Budget 某二级支出科目的季度预算（budgets 表）
type Budget struct {
	ID            string
	CategoryID    string
	QuarterAmount int64 // 分
	UpdatedAt     time.Time
}
