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

// TestClassifyByCustomRulesIncomeRows 收入行的规则匹配是不对称的：
// 只认往来科目（报销到账、收回借款），其余一律当未命中。
//
// 放行往来是因为它们是"垫付出去/借出去"那半边的对手方——不收进来两头就抵不平，
// 一笔垫付会被永远记成支出。不放行其它是因为工资/租金往往要区分是谁的，
// 让用户自己挑；顺带也免得微信收红包把待核对队列淹掉。
func TestClassifyByCustomRulesIncomeRows(t *testing.T) {
	rules := []domain.CategoryRule{
		{Pattern: "报销", PatternType: "contains", Field: "any", CategoryID: "transfer.reimburse", Priority: 85, IsActive: true},
		{Pattern: "转账", PatternType: "exact", Field: "platform_category", CategoryID: "transfer.internal", Priority: 90, IsActive: true},
		{Pattern: "公司", PatternType: "contains", Field: "counterparty", CategoryID: "income.salary.husband", Priority: 100, IsActive: true},
	}

	tests := []struct {
		name        string
		row         domain.RawBillRow
		wantCat     string
		wantSkip    bool
		wantMatched bool
	}{
		{
			name:    "收入 + 往来规则 → 命中",
			row:     domain.RawBillRow{Counterparty: "公司财务", Description: "差旅报销", PlatformCategory: "转账", Direction: domain.DirectionIncome},
			wantCat: "transfer.reimburse", wantMatched: true,
		},
		{
			name: "收入 + 非往来规则 → 当未命中，留给人工",
			row:  domain.RawBillRow{Counterparty: "公司", Description: "工资", PlatformCategory: "工资", Direction: domain.DirectionIncome},
		},
		{
			name:    "支出 + 往来规则 → 照常命中",
			row:     domain.RawBillRow{Counterparty: "张三", Description: "转账给张三", PlatformCategory: "转账", Direction: domain.DirectionExpense},
			wantCat: "transfer.internal", wantMatched: true,
		},
		{
			name:    "支出 + 普通规则 → 不受影响",
			row:     domain.RawBillRow{Counterparty: "公司食堂", Description: "午餐", Direction: domain.DirectionExpense},
			wantCat: "income.salary.husband", wantMatched: true,
		},
		{
			name: "收入 + 谁都没命中",
			row:  domain.RawBillRow{Counterparty: "李四", Description: "红包", Direction: domain.DirectionIncome},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCat, gotSkip, gotMatched := ClassifyByCustomRules(tt.row, rules)
			if gotCat != tt.wantCat || gotSkip != tt.wantSkip || gotMatched != tt.wantMatched {
				t.Errorf("ClassifyByCustomRules() = (%q, %v, %v); want (%q, %v, %v)",
					gotCat, gotSkip, gotMatched, tt.wantCat, tt.wantSkip, tt.wantMatched)
			}
		})
	}
}

// TestClassifyByCustomRulesIncomeStopsAtFirstMatch 收入行撞上非往来规则时就此收手，
// 不继续往下找更弱的匹配——规则是按 priority 排好序的，第一个命中的就是最该生效的那条。
func TestClassifyByCustomRulesIncomeStopsAtFirstMatch(t *testing.T) {
	rules := []domain.CategoryRule{
		{Pattern: "工资", PatternType: "contains", Field: "any", CategoryID: "income.salary.husband", Priority: 10, IsActive: true},
		{Pattern: "公司", PatternType: "contains", Field: "any", CategoryID: "transfer.reimburse", Priority: 20, IsActive: true},
	}
	row := domain.RawBillRow{Counterparty: "公司", Description: "工资", Direction: domain.DirectionIncome}

	gotCat, _, gotMatched := ClassifyByCustomRules(row, rules)
	if gotMatched || gotCat != "" {
		t.Errorf("ClassifyByCustomRules() = (%q, matched=%v); want 未命中——高优先级的工资规则命中后就该收手，不能退而求其次捡到后面的往来规则", gotCat, gotMatched)
	}
}
