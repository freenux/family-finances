package usecase

import (
	"testing"

	"family-finances/internal/domain"
)

func TestClassifyByCustomRules(t *testing.T) {
	row := domain.RawBillRow{
		Counterparty:     "深圳通",
		Description:      "地铁乘车",
		PlatformCategory: "交通出行",
		Direction:        domain.DirectionExpense,
	}
	rules := []domain.CategoryRule{
		{
			Pattern:     "深圳通",
			PatternType: "exact",
			Field:       "counterparty",
			CategoryID:  "expense.necessary.transport",
			IsActive:    true,
		},
	}

	got, ok := ClassifyByCustomRules(row, rules)
	if !ok || got != "expense.necessary.transport" {
		t.Fatalf("ClassifyByCustomRules() = %q, %v; want transport, true", got, ok)
	}
}

func TestClassifyByCustomRulesIgnoresInactive(t *testing.T) {
	row := domain.RawBillRow{Description: "物业费"}
	rules := []domain.CategoryRule{
		{
			Pattern:     "物业",
			PatternType: "contains",
			Field:       "description",
			CategoryID:  "expense.fixed.housing",
			IsActive:    false,
		},
	}

	if got, ok := ClassifyByCustomRules(row, rules); ok || got != "" {
		t.Fatalf("ClassifyByCustomRules() = %q, %v; want empty, false", got, ok)
	}
}
