package domain

// ReportKPI 是季/年报的核心指标
type ReportKPI struct {
	TotalIncome       int64   // 总收入（分）
	TotalExpense      int64   // 总支出（分）
	Surplus           int64   // 结余（分）
	SurplusRate       float64 // 结余率
	DiscretionRatio   float64 // 自由裁量支出占总支出比
	DiscretionWarning bool    // 是否超 35%
}

// ReportData 聚合后的科目数据 + KPI，用于页面展示
type ReportData struct {
	Period       Period
	IncomeGroups []CategoryGroupAggregation
	ExpenseGroups []CategoryGroupAggregation
	KPI          ReportKPI
}

type CategoryGroupAggregation struct {
	GroupID    string
	GroupName  string
	GroupEmoji string
	Subtotal   int64
	Items      []CategoryAggregation
}
