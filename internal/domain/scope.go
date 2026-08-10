package domain

// Scope 统计口径：与科目正交的第三个维度，决定一次聚合算不算专项开支。
// 和 Account 里的 family 一样，它是纯查询侧的概念，不会作为存储值落库。
//
//	daily   —— 剔除专项，用于趋势/同比环比/预算/攒钱能力等"日常基线"判断
//	all     —— 日常 + 专项，真实现金流
//	special —— 只看专项
type Scope string

const (
	ScopeAll     Scope = "all"
	ScopeDaily   Scope = "daily"
	ScopeSpecial Scope = "special"
)

// ParseScope 解析口径；空串与非法值一律退回 ScopeDaily——
// 默认视图必须是干净的（一次装修就能把全局同比拉到 +368%）。
func ParseScope(s string) Scope {
	switch Scope(s) {
	case ScopeAll:
		return ScopeAll
	case ScopeSpecial:
		return ScopeSpecial
	default:
		return ScopeDaily
	}
}

// Label 给 UI 展示用
func (s Scope) Label() string {
	switch s {
	case ScopeAll:
		return "全部"
	case ScopeSpecial:
		return "仅专项"
	case ScopeDaily:
		return "日常"
	}
	return string(s)
}

// SQLFilter 生成拼在 WHERE/ON 后面的口径片段，col 是流水表里专项外键列
// （带表别名时传 "t.special_id"）。集中在这里，避免每个 repo 方法各写各的。
// col 只来自仓库内部常量，不接受外部输入。
func (s Scope) SQLFilter(col string) string {
	switch s {
	case ScopeDaily:
		return " AND " + col + " IS NULL"
	case ScopeSpecial:
		return " AND " + col + " IS NOT NULL"
	default: // ScopeAll
		return ""
	}
}
