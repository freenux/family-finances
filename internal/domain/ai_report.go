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
	AIPrompt    string
	AIAnalysis  string // JSON: {"summary":"...","highlights":[...],"risks":[...],"advice":[...]}
	AIModel     string
	Status      string // draft | final
	IsFrozen    bool
	CreatedAt   time.Time
}
