package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
)

func TestNewRendererParsesAllPages(t *testing.T) {
	if _, err := NewRenderer(); err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
}

func TestRenderRulesPage(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	vm := struct {
		Title            string
		Nav              string
		Rules            []domain.CategoryRule
		Categories       []domain.Category
		FilterCategoryID string
		TotalRules       int
		Flash            string
		Error            string
	}{
		Title: "分类规则",
		Nav:   "rules",
		Rules: []domain.CategoryRule{
			{
				ID:          "rule-1",
				Pattern:     "深圳通",
				PatternType: "contains",
				Field:       "counterparty",
				CategoryID:  "expense.necessary.transport",
				Priority:    5,
				IsActive:    true,
				CreatedAt:   time.Now(),
			},
		},
		Categories: []domain.Category{
			{ID: "expense.necessary", Level: 1, Name: "变动必要支出", SortOrder: 120},
			{ID: "expense.necessary.transport", ParentID: "expense.necessary", Level: 2, Name: "交通出行", SortOrder: 123},
		},
		TotalRules: 1,
	}

	var out bytes.Buffer
	if err := r.RenderPage(&out, "rules", vm); err != nil {
		t.Fatalf("RenderPage() error = %v", err)
	}
	html := out.String()
	for _, want := range []string{"RULE WORKBENCH", "筛选分类", "rule-card", "深圳通", "交通出行"} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered rules page missing %q", want)
		}
	}
	if strings.Contains(html, "rules-table") {
		t.Fatal("rendered rules page still contains old table layout")
	}
}
