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

	got, skip, ok := ClassifyByCustomRules(row, rules)
	if !ok || skip || got != "expense.necessary.transport" {
		t.Fatalf("ClassifyByCustomRules() = %q, %v, %v; want transport, false, true", got, skip, ok)
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

	if got, skip, ok := ClassifyByCustomRules(row, rules); ok || skip || got != "" {
		t.Fatalf("ClassifyByCustomRules() = %q, %v, %v; want empty, false, false", got, skip, ok)
	}
}

func TestClassifyByCustomRulesSupportsSkip(t *testing.T) {
	row := domain.RawBillRow{
		PlatformCategory: "转账",
		Direction:        domain.DirectionExpense,
	}
	rules := []domain.CategoryRule{
		{
			Pattern:     "转账",
			PatternType: "exact",
			Field:       "platform_category",
			IsActive:    true,
		},
	}

	got, skip, ok := ClassifyByCustomRules(row, rules)
	if !ok || !skip || got != "" {
		t.Fatalf("ClassifyByCustomRules() = %q, %v, %v; want empty, true, true", got, skip, ok)
	}
}
