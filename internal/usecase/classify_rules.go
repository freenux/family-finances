package usecase

import (
	"family-finances/internal/domain"
)

// ClassifyByCustomRules 根据数据库规则返回二级科目 ID。
// 返回 (categoryID, skip, matched)：
//   - matched=true, categoryID!="": 命中具体科目
//   - matched=true, categoryID=="": 命中"跳过导入"规则（例如转账/提现）
//   - matched=false：未命中，交由调用方入库为 NULL，后续 LLM 兜底或人工处理
func ClassifyByCustomRules(row domain.RawBillRow, rules []domain.CategoryRule) (categoryID string, skip, matched bool) {
	// 收入类暂不自动分类，让用户自己挑（工资/租金往往需要区分人）
	if row.Direction == domain.DirectionIncome {
		return "", false, false
	}

	for _, rule := range rules {
		if !rule.IsActive || rule.Pattern == "" {
			continue
		}
		if ruleMatches(row, rule) {
			return rule.CategoryID, rule.CategoryID == "", true
		}
	}
	return "", false, false
}

func ruleMatches(row domain.RawBillRow, rule domain.CategoryRule) bool {
	return domain.RuleMatches(rule, domain.RuleMatchFields{
		Counterparty:     row.Counterparty,
		Description:      row.Description,
		PlatformCategory: row.PlatformCategory,
	})
}
