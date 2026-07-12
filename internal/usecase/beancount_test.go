package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
)

func TestBeancountAccountMapping(t *testing.T) {
	cases := []struct {
		catID string
		dir   domain.Direction
		want  string
	}{
		{"expense.discretion.shopping", domain.DirectionExpense, "Expenses:Discretion:Shopping"},
		{"income.salary.husband", domain.DirectionIncome, "Income:Salary:Husband"},
		{"expense.family.home_maintenance", domain.DirectionExpense, "Expenses:Family:HomeMaintenance"},
		{"", domain.DirectionExpense, "Expenses:Uncategorized"},
		{"", domain.DirectionIncome, "Income:Uncategorized"},
	}
	for _, tc := range cases {
		if got := beancountCategoryAccount(tc.catID, tc.dir); got != tc.want {
			t.Fatalf("beancountCategoryAccount(%q) = %s; want %s", tc.catID, got, tc.want)
		}
	}
}

func TestBeancountAssetAccount(t *testing.T) {
	cases := []struct{ src, want string }{
		{"wechat", "Assets:WeChat"},
		{"alipay", "Assets:Alipay"},
		{"manual", "Assets:Manual"},
		{"csv:bank statement", "Assets:Csv:BankStatement"},
		{"csv:银行流水", "Assets:Csv:Generic"}, // 中文段剔除后回退
	}
	for _, tc := range cases {
		if got := beancountAssetAccount(tc.src); got != tc.want {
			t.Fatalf("beancountAssetAccount(%q) = %s; want %s", tc.src, got, tc.want)
		}
	}
}

func TestBuildBeancount(t *testing.T) {
	txRepo := &fakeTransactionRepo{allTxs: []domain.Transaction{
		{
			ID: "t1", OccurredAt: time.Date(2026, 5, 10, 12, 0, 0, 0, time.Local),
			Source: domain.SourceManual, Direction: domain.DirectionExpense, Amount: 12345,
			CategoryID: "expense.necessary.food", Counterparty: `他说"好"`, Description: "买菜",
			Status: domain.TxStatusConfirmed,
		},
		{
			ID: "t2", OccurredAt: time.Date(2026, 5, 12, 9, 0, 0, 0, time.Local),
			Source: domain.SourceWechat, Direction: domain.DirectionIncome, Amount: 300000,
			CategoryID: "income.salary.husband", Counterparty: "公司", Description: "工资",
			Status: domain.TxStatusConfirmed,
		},
		{
			ID: "t3", OccurredAt: time.Date(2026, 5, 13, 9, 0, 0, 0, time.Local),
			Source: domain.SourceManual, Direction: domain.DirectionExpense, Amount: 999,
			Status: domain.TxStatusExcluded, // 应被排除
		},
	}}
	uc := NewExport(txRepo, &fakeCategoryRepo{cats: testCategories()}, &fakeCategoryRuleRepo{},
		&fakeAssetSnapshotRepo{}, &fakeReportRepo{})
	out, err := uc.BuildBeancount(context.Background())
	if err != nil {
		t.Fatalf("BuildBeancount() error = %v", err)
	}
	for _, want := range []string{
		`option "operating_currency" "CNY"`,
		"1970-01-01 open Assets:Manual CNY",
		"1970-01-01 open Expenses:Necessary:Food CNY",
		`2026-05-10 * "他说\"好\"" "买菜"`,
		"  Expenses:Necessary:Food  123.45 CNY",
		"  Assets:Manual  -123.45 CNY",
		"  Income:Salary:Husband  -3000.00 CNY",
		"  Assets:WeChat  3000.00 CNY",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "999") || strings.Contains(out, "9.99") {
		t.Fatalf("excluded 行不应导出:\n%s", out)
	}
}

func TestBeancountEscapeNeutralizesNewline(t *testing.T) {
	in := "正常描述\n2026-01-01 * \"伪造\" \"分录\"\r\n  Assets:Evil  1.00 CNY"
	got := beancountEscape(in)
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("beancountEscape 输出仍含换行,可伪造分录: %q", got)
	}
	if !strings.Contains(got, `\"伪造\"`) {
		t.Fatalf("引号未转义: %q", got)
	}
}
