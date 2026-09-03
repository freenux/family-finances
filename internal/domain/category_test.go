package domain

import "testing"

// TestIsTransferCategory 钉住「资金往来」的判据。
// 这个函数是三个写入点（ImportBill 落地、PATCH 改分类、LLM 回写）共用的唯一依据，
// 判错的后果不对称：把真支出错认成往来 → 它以 excluded 落地，从所有报表里消失。
func TestIsTransferCategory(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"内部转账", "transfer.internal", true},
		{"借出借入还款", "transfer.loan", true},
		{"报销垫付", "transfer.reimburse", true},
		{"将来新增的往来子科目也认", "transfer.loan.friend", true},
		{"普通支出科目", "expense.discretion.shopping", false},
		{"普通收入科目", "income.salary.husband", false},
		{"未分类（空串）不是往来", "", false},
		{"一级组本身不带点，不该命中", "transfer", false},
		{"前缀相似但不同的命名空间", "transfers.internal", false},
		{"transfer 出现在中间不算", "expense.transfer.fee", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransferCategory(tt.id); got != tt.want {
				t.Errorf("IsTransferCategory(%q) = %v; want %v", tt.id, got, tt.want)
			}
		})
	}
}
