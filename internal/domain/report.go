package domain

// ReportKPI 是季/年报的核心指标
type ReportKPI struct {
	TotalIncome  int64 // 总收入（分）
	TotalExpense int64 // 总支出（分）= DailyExpense + SpecialExpense
	// DailyExpense 剔除专项后的日常支出（分）。所有"基线"判断都以它为准。
	DailyExpense int64
	// SpecialExpense 专项支出（分），如装修/购车
	SpecialExpense int64
	Surplus        int64   // 结余（分）= TotalIncome − TotalExpense（真实现金流）
	SurplusRate    float64 // 结余率（全口径）
	// DailySurplus / DailySurplusRate 日常口径结余：TotalIncome − DailyExpense。
	// 一次装修不该被读成"这个家庭不会存钱"。
	DailySurplus     int64
	DailySurplusRate float64
	// DiscretionRatio 自由裁量支出占"日常支出"的比。
	// 分母必须是 DailyExpense：用全口径时装修季分母从 4 万涨到 18.5 万，
	// 占比被稀释到 5.6%，35% 告警正好在最该响的时候静默关掉。
	DiscretionRatio   float64
	DiscretionWarning bool // 是否超 35%
}

// ReportData 聚合后的科目数据 + KPI，用于页面展示。
// IncomeGroups / ExpenseGroups 是全口径（日常 + 专项），语义与加专项之前一致；
// SpecialGroups 只含专项部分，供报表拆出"其中：专项"行。
type ReportData struct {
	Period        Period
	IncomeGroups  []CategoryGroupAggregation
	ExpenseGroups []CategoryGroupAggregation
	// SpecialGroups 专项支出按科目分组（只保留有金额的组），与 ExpenseGroups 同结构
	SpecialGroups []CategoryGroupAggregation
	// SpecialByProject 本期各专项花费：专项名 → 金额（分）
	SpecialByProject map[string]int64
	KPI              ReportKPI
}

type CategoryGroupAggregation struct {
	GroupID    string
	GroupName  string
	GroupEmoji string
	Subtotal   int64
	Items      []CategoryAggregation
}
