package sqlite

import (
	"strings"
	"testing"

	"family-finances/internal/domain"
)

// TestScopeFilter 口径 SQL 片段：拼在 WHERE/ON 之后，必须自带前导 " AND "。
// 这段拼接原本长在 domain.Scope 上（domain 里出现 SQL、还要 repo 把表别名传下去），
// 现在归到 sqlite 包——SQL 是持久化细节，domain 只留「日常/全部/仅专项」这个概念。
func TestScopeFilter(t *testing.T) {
	tests := []struct {
		name  string
		scope domain.Scope
		col   string
		want  string
	}{
		{"日常：只要没挂专项的", domain.ScopeDaily, "special_id", " AND special_id IS NULL"},
		{"日常·带表别名", domain.ScopeDaily, "t.special_id", " AND t.special_id IS NULL"},
		{"仅专项：只要挂了专项的", domain.ScopeSpecial, "special_id", " AND special_id IS NOT NULL"},
		{"仅专项·带表别名", domain.ScopeSpecial, "t.special_id", " AND t.special_id IS NOT NULL"},
		{"全部：不加任何条件", domain.ScopeAll, "t.special_id", ""},
		// 非法值经 ParseScope 会落到 daily，直接传进来则按 all 处理（不过滤），
		// 绝不能拼出半截 SQL 把查询打挂
		{"未知口径按全部处理", domain.Scope("garbage"), "t.special_id", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scopeFilter(tt.scope, tt.col)
			if got != tt.want {
				t.Fatalf("scopeFilter(%q, %q) = %q; want %q", tt.scope, tt.col, got, tt.want)
			}
			if got != "" && !strings.HasPrefix(got, " AND ") {
				t.Fatalf("片段 %q 缺少前导 \" AND \"，拼进 WHERE 会变成语法错误", got)
			}
		})
	}
}
