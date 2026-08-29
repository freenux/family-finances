package domain

// ReportKPI 是季/年报的核心指标
type ReportKPI struct {
	TotalIncome  int64 // 总收入（分）= DailyIncome + SpecialIncome
	TotalExpense int64 // 总支出（分）= DailyExpense + SpecialExpense
	// DailyIncome 剔除专项后的日常收入（分）。换车专项里记的旧车折价收入不算日常进项。
	DailyIncome int64
	// SpecialIncome 专项收入（分），如旧车折价、装修补贴
	SpecialIncome int64
	// DailyExpense 剔除专项后的日常支出（分）。所有"基线"判断都以它为准。
	DailyExpense int64
	// SpecialExpense 专项支出（分），如装修/购车
	SpecialExpense int64
	Surplus        int64   // 结余（分）= TotalIncome − TotalExpense（真实现金流）
	SurplusRate    float64 // 结余率（全口径）
	// DailySurplus / DailySurplusRate 日常口径结余：DailyIncome − DailyExpense。
	// 一次装修不该被读成"这个家庭不会存钱"；同理专项里的收入（旧车折价）也不能
	// 掺进日常进项，否则装修季反而显得更会存钱。
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
// SpecialGroups 只含专项部分，供报表拆出"其中：专项"行；
// DailyIncomeGroups / DailyExpenseGroups 是剔除专项后的日常口径明细，
// 喂给 LLM 的上下文包用它（包内所有数都必须是同一口径，否则 AI 两段话会打架）。
type ReportData struct {
	Period        Period
	IncomeGroups  []CategoryGroupAggregation
	ExpenseGroups []CategoryGroupAggregation
	// DailyIncomeGroups / DailyExpenseGroups 日常口径明细，与 ExpenseGroups 同结构
	DailyIncomeGroups  []CategoryGroupAggregation
	DailyExpenseGroups []CategoryGroupAggregation
	// SpecialGroups 专项支出按科目分组（只保留有金额的组），与 ExpenseGroups 同结构
	SpecialGroups []CategoryGroupAggregation
	// SpecialByProject 本期各专项的净花费，按金额降序。形状对齐 CategoryAggregation：
	// 身份 + 标签 + 金额，绝不拿名字当键——两个同名专项（跨年各建一个「装修」）
	// 必须是两行，且每行都能链回 /specials?edit=<id>。
	SpecialByProject []SpecialAggregation
	KPI              ReportKPI
}

// SpecialAggregation 单个专项在一个周期内的净花费（该专项内的收入已抵扣，
// 见 SpecialProjectRepo.SumByProjectInPeriod）
type SpecialAggregation struct {
	SpecialID string
	Name      string
	Amount    int64 // 分
}

type CategoryGroupAggregation struct {
	GroupID    string
	GroupName  string
	GroupEmoji string
	Subtotal   int64
	Items      []CategoryAggregation
}
