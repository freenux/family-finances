package domain

import "time"

// SpecialProject 专项开支项目（装修、购车这类非经常性的一次性大额支出）。
// 判据是"非经常性"，不是"金额大"，所以只能人工标注，不做金额阈值自动判定。
type SpecialProject struct {
	ID        string
	Name      string
	StartedOn time.Time
	EndedOn   time.Time // 零值 = 进行中
	BudgetFen int64
	Note      string
	CreatedAt time.Time
}

// IsActive 进行中（未填结束日期）
func (p SpecialProject) IsActive() bool { return p.EndedOn.IsZero() }
