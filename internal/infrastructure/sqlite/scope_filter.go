package sqlite

import "family-finances/internal/domain"

// scopeFilter 生成拼在 WHERE/ON 后面的口径片段，col 是流水表里专项外键列
// （带表别名时传 "t.special_id"）。集中在这里，避免每个 repo 方法各写各的。
//
// SQL 片段的生成属于持久化细节，所以放在 sqlite 包而不是 domain：domain.Scope 只是
// 「日常 / 全部 / 仅专项」这个领域概念本身，它不该知道流水存在关系库里、更不该知道
// repo 给 transactions 起了什么表别名。col 因此也只来自本包内部的字面量，不接受外部输入。
func scopeFilter(s domain.Scope, col string) string {
	switch s {
	case domain.ScopeDaily:
		return " AND " + col + " IS NULL"
	case domain.ScopeSpecial:
		return " AND " + col + " IS NOT NULL"
	default: // domain.ScopeAll
		return ""
	}
}
