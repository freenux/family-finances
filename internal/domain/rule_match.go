package domain

import "strings"

// RuleMatchFields 参与规则匹配的字段集合。分类器和流水预览共用这一份，
// 不含 note / raw_row：分类器永远看不到它们，预览也不该看。
type RuleMatchFields struct {
	Counterparty     string
	Description      string
	PlatformCategory string
}

// RuleMatches 规则匹配的唯一实现：大小写不敏感，Pattern 前后空白忽略，空 Pattern 不匹配；
// PatternType="exact" 要求全等，其它一律子串包含；Field 为 counterparty / description /
// platform_category 时只看对应字段，其它值当作 any（三个字段都看）。
// 不检查 IsActive，由调用方决定。
func RuleMatches(rule CategoryRule, f RuleMatchFields) bool {
	pattern := strings.ToLower(strings.TrimSpace(rule.Pattern))
	if pattern == "" {
		return false
	}
	for _, value := range ruleFieldValues(f, rule.Field) {
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

func ruleFieldValues(f RuleMatchFields, field string) []string {
	switch field {
	case "counterparty":
		return []string{f.Counterparty}
	case "description":
		return []string{f.Description}
	case "platform_category":
		return []string{f.PlatformCategory}
	default:
		return []string{f.Counterparty, f.Description, f.PlatformCategory}
	}
}
