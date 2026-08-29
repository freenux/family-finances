package domain

import "time"

// AIReport 是落库的一份 AI 季/年财报（对应 reports 表）。
// income_data/expense_data/kpi_data/comparison/ai_analysis 等以 JSON 字符串形式保存，
// 由 usecase 层负责编解码（其结构定义见 usecase.ContextPack 相关类型）。
type AIReport struct {
	ID          string
	Period      string     // "2026Q2" / "2026"
	PeriodType  PeriodType // PeriodQuarterly | PeriodAnnual
	GeneratedAt time.Time
	IncomeData  string // JSON: {"total_fen":.., "groups":[...]}
	ExpenseData string // JSON: 同上
	KPIData     string // JSON: {"kpi":{...}, "findings":[...], "snapshot":{...}}
	Comparison  string // JSON: {"prev_period":"...", "income_delta":.., "expense_delta":.., "net_worth_delta":..}
	// DataScope 上面这四列里的数字是按哪个口径算出来的（对应 reports.data_scope）。
	// 口径拆分（迁移 015）之前生成的存档是 ScopeAll（含专项），之后生成的是 ScopeDaily
	// （专项已剔除，单列在上下文包的 special 一节）。历史报告不重新生成，靠这个标记
	// 让渲染层按存储值选文案——否则全口径数字会被标成「日常收入 / 日常支出」。
	DataScope  Scope
	AIPrompt   string
	AIAnalysis string // JSON: {"summary":"...","highlights":[...],"risks":[...],"advice":[...]}
	AIModel    string
	Status     string // draft | final
	IsFrozen   bool
	CreatedAt  time.Time
}
