package domain

import "testing"

func TestRuleMatches(t *testing.T) {
	f := RuleMatchFields{Counterparty: "星巴克咖啡", Description: "Latte 拿铁", PlatformCategory: "餐饮美食"}
	tests := []struct {
		name string
		rule CategoryRule
		want bool
	}{
		{"any 看交易对方", CategoryRule{Pattern: "星巴克", Field: "any"}, true},
		{"any 看商品说明", CategoryRule{Pattern: "拿铁", Field: "any"}, true},
		{"any 看平台分类", CategoryRule{Pattern: "餐饮", Field: "any"}, true},
		{"未知 field 当 any", CategoryRule{Pattern: "餐饮", Field: "note"}, true},
		{"空 field 当 any", CategoryRule{Pattern: "餐饮"}, true},
		{"大小写不敏感", CategoryRule{Pattern: "LATTE", Field: "description"}, true},
		{"pattern 前后空白被 trim", CategoryRule{Pattern: "  星巴克 ", Field: "counterparty"}, true},
		{"空 pattern 永不匹配", CategoryRule{Pattern: "", Field: "any"}, false},
		{"全空白 pattern 永不匹配", CategoryRule{Pattern: "   ", Field: "any"}, false},
		{"field 限定后不看其它字段", CategoryRule{Pattern: "拿铁", Field: "counterparty"}, false},
		{"exact 全等", CategoryRule{Pattern: "星巴克咖啡", PatternType: "exact", Field: "counterparty"}, true},
		{"exact 不接受子串", CategoryRule{Pattern: "星巴克", PatternType: "exact", Field: "counterparty"}, false},
		{"exact 大小写不敏感", CategoryRule{Pattern: "latte 拿铁", PatternType: "exact", Field: "description"}, true},
		{"未知 pattern_type 当 contains", CategoryRule{Pattern: "星巴克", PatternType: "regex", Field: "counterparty"}, true},
		{"不匹配", CategoryRule{Pattern: "麦当劳", Field: "any"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RuleMatches(tt.rule, f); got != tt.want {
				t.Errorf("RuleMatches(%+v) = %v; want %v", tt.rule, got, tt.want)
			}
		})
	}
}
