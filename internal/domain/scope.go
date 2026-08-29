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
