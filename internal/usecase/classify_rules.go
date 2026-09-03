package usecase

import (
	"strings"

	"family-finances/internal/domain"
)

// ClassifyByCustomRules 根据数据库规则返回二级科目 ID。
// 返回 (categoryID, skip, matched)：
//   - matched=true, categoryID!="": 命中具体科目
//   - matched=true, categoryID=="": 命中"跳过导入"规则（例如转账/提现）
//   - matched=false：未命中，交由调用方入库为 NULL，后续 LLM 兜底或人工处理
//
// 收入行只认往来科目（报销到账、收回借款）——它们是"垫付出去/借出去"那半边的
// 对手方，不认进来两头就抵不平。工资/租金这类真收入仍然不自动分类：往往要区分
// 是谁的，让用户自己挑。
func ClassifyByCustomRules(row domain.RawBillRow, rules []domain.CategoryRule) (categoryID string, skip, matched bool) {
	for _, rule := range rules {
		if !rule.IsActive || rule.Pattern == "" {
			continue
		}
		if !ruleMatches(row, rule) {
			continue
		}
		if row.Direction == domain.DirectionIncome && !domain.IsTransferCategory(rule.CategoryID) {
			// 收入行撞上了非往来规则。就此收手而不是继续往下找：规则是按
			// priority 排好序的，第一个命中的就是最该生效的那条，继续扫只会
			// 捡到更弱的匹配。
			return "", false, false
		}
		return rule.CategoryID, rule.CategoryID == "", true
	}
	return "", false, false
}

func ruleMatches(row domain.RawBillRow, rule domain.CategoryRule) bool {
	pattern := strings.ToLower(strings.TrimSpace(rule.Pattern))
	if pattern == "" {
		return false
	}
	for _, value := range ruleFieldValues(row, rule.Field) {
		value = strings.ToLower(value)
		switch rule.PatternType {
		case "exact":
			if value == pattern {
				return true
			}
		default:
			if strings.Contains(value, pattern) {
				return true
			}
		}
	}
	return false
}

func ruleFieldValues(row domain.RawBillRow, field string) []string {
	switch field {
	case "counterparty":
		return []string{row.Counterparty}
	case "description":
		return []string{row.Description}
	case "platform_category":
		return []string{row.PlatformCategory}
	default:
		return []string{row.Counterparty, row.Description, row.PlatformCategory}
	}
}
