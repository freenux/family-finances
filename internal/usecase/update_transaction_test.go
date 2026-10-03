package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

func sp(s string) *string { return &s }

// failingCats ListAll 恒失败：模拟 DB 故障
type failingCats struct{ err error }

func (f failingCats) ListAll(context.Context) ([]domain.Category, error) { return nil, f.err }

// failingEnsurer Ensure 恒返回给定错误
type failingEnsurer struct{ err error }

func (f failingEnsurer) Ensure(context.Context, string) error { return f.err }

func newUpdateUC(t *testing.T) (*UpdateTransaction, *fakeTransactionRepo) {
	t.Helper()
	repo := &fakeTransactionRepo{}
	specials := NewSpecialView(&fakeSpecialProjectRepo{projects: []domain.SpecialProject{{ID: "sp-1", Name: "装修"}}})
	return NewUpdateTransaction(repo, &fakeCategoryRepo{cats: testCategories()}, specials), repo
}

func TestUpdateTransactionValidation(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		patch     TxPatch
		wantInput bool // true = 期望 ErrInvalidInput
		wantMsg   string
	}{
		{"二级科目通过", "t1", TxPatch{CategoryID: sp("expense.discretion.shopping")}, false, ""},
		{"一级科目被拒", "t1", TxPatch{CategoryID: sp("expense.discretion")}, true, "请选择有效的二级分类"},
		{"不存在的科目被拒", "t1", TxPatch{CategoryID: sp("nope")}, true, "请选择有效的二级分类"},
		{"空串清空分类放行", "t1", TxPatch{CategoryID: sp("")}, false, ""},
		{"非法状态", "t1", TxPatch{Status: sp("weird")}, true, "状态不合法"},
		{"family 不是存储账户", "t1", TxPatch{Account: sp("family")}, true, "账户不合法"},
		{"成员过长", "t1", TxPatch{Member: sp(strings.Repeat("字", 21))}, true, "成员标注过长"},
		{"成员恰好 20 字放行", "t1", TxPatch{Member: sp(strings.Repeat("字", 20))}, false, ""},
		{"专项不存在", "t1", TxPatch{SpecialID: sp("ghost")}, true, "专项不存在"},
		{"专项存在", "t1", TxPatch{SpecialID: sp(" sp-1 ")}, false, ""},
		{"空串归回日常", "t1", TxPatch{SpecialID: sp("")}, false, ""},
		{"缺 id", "", TxPatch{Note: sp("x")}, true, "缺少流水 id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, repo := newUpdateUC(t)
			err := uc.Update(context.Background(), tt.id, tt.patch)
			if got := errors.Is(err, ErrInvalidInput); got != tt.wantInput {
				t.Fatalf("err = %v; ErrInvalidInput = %v, want %v", err, got, tt.wantInput)
			}
			if tt.wantInput {
				if !strings.Contains(err.Error(), tt.wantMsg) {
					t.Errorf("message = %q; want 含 %q（面向用户的中文）", err.Error(), tt.wantMsg)
				}
				if len(repo.updates) != 0 {
					t.Errorf("校验失败却仍写库: %+v", repo.updates)
				}
			}
		})
	}
}

func TestUpdateTransactionPatchAssembly(t *testing.T) {
	t.Run("非空分类自动 confirmed", func(t *testing.T) {
		uc, repo := newUpdateUC(t)
		if err := uc.Update(context.Background(), "t1", TxPatch{CategoryID: sp("expense.discretion.shopping")}); err != nil {
			t.Fatal(err)
		}
		if p := repo.updates[0]; p.Status == nil || *p.Status != domain.TxStatusConfirmed {
			t.Errorf("status = %v; want confirmed（指定分类即视为人工确认）", p.Status)
		}
	})
	t.Run("显式 status 覆盖自动 confirmed", func(t *testing.T) {
		uc, repo := newUpdateUC(t)
		if err := uc.Update(context.Background(), "t1", TxPatch{CategoryID: sp("expense.discretion.shopping"), Status: sp("excluded")}); err != nil {
			t.Fatal(err)
		}
		if p := repo.updates[0]; *p.Status != domain.TxStatusExcluded {
			t.Errorf("status = %s; want excluded（同请求显式给的优先）", *p.Status)
		}
	})
	t.Run("清空分类不碰状态", func(t *testing.T) {
		uc, repo := newUpdateUC(t)
		if err := uc.Update(context.Background(), "t1", TxPatch{CategoryID: sp("")}); err != nil {
			t.Fatal(err)
		}
		if p := repo.updates[0]; p.Status != nil || p.CategoryID == nil || *p.CategoryID != "" {
			t.Errorf("patch = %+v; want 清空分类且 status 不动", p)
		}
	})
	t.Run("专项与成员 trim", func(t *testing.T) {
		uc, repo := newUpdateUC(t)
		if err := uc.Update(context.Background(), "t1", TxPatch{SpecialID: sp("  sp-1 "), Member: sp(" 张三 ")}); err != nil {
			t.Fatal(err)
		}
		if p := repo.updates[0]; *p.SpecialID != "sp-1" || *p.Member != "张三" {
			t.Errorf("patch = special %q member %q; want trim 后的值", *p.SpecialID, *p.Member)
		}
	})
	t.Run("流水不存在透传 port.ErrNotFound", func(t *testing.T) {
		uc, repo := newUpdateUC(t)
		repo.updateErr = port.ErrNotFound
		if err := uc.Update(context.Background(), "t1", TxPatch{Note: sp("x")}); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("err = %v; want ErrNotFound", err)
		}
	})
}

// 故障必须原样冒泡为「非 ErrInvalidInput」，由适配器映射成 500；绝不能被当成用户错。
func TestUpdateTransactionFaultsAreNotUserErrors(t *testing.T) {
	boom := errors.New("disk I/O error: SECRET")
	tests := []struct {
		name  string
		build func() *UpdateTransaction
		patch TxPatch
	}{
		{"科目表读取失败", func() *UpdateTransaction {
			return NewUpdateTransaction(&fakeTransactionRepo{}, failingCats{boom}, nil)
		}, TxPatch{CategoryID: sp("expense.discretion.shopping")}},
		{"专项校验遇到 DB 故障", func() *UpdateTransaction {
			return NewUpdateTransaction(&fakeTransactionRepo{}, &fakeCategoryRepo{}, failingEnsurer{boom})
		}, TxPatch{SpecialID: sp("sp-1")}},
		{"写库失败", func() *UpdateTransaction {
			return NewUpdateTransaction(&fakeTransactionRepo{updateErr: boom}, &fakeCategoryRepo{}, nil)
		}, TxPatch{Note: sp("x")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.build().Update(context.Background(), "t1", tt.patch)
			if !errors.Is(err, boom) || errors.Is(err, ErrInvalidInput) || errors.Is(err, port.ErrNotFound) {
				t.Errorf("err = %v; want 原样返回的故障，且不是 ErrInvalidInput / ErrNotFound", err)
			}
		})
	}
}

func TestUpdateTransactionSpecialsDisabled(t *testing.T) {
	uc := NewUpdateTransaction(&fakeTransactionRepo{}, &fakeCategoryRepo{}, nil)
	err := uc.Update(context.Background(), "t1", TxPatch{SpecialID: sp("sp-1")})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "专项功能未启用") {
		t.Errorf("err = %v; want ErrInvalidInput「专项功能未启用」", err)
	}
	if err := uc.Update(context.Background(), "t1", TxPatch{SpecialID: sp("")}); err != nil {
		t.Errorf("空串归回日常不依赖专项功能，err = %v", err)
	}
}

func TestAssignSpecial(t *testing.T) {
	many := make([]string, MaxBatchTxIDs+1)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	tests := []struct {
		name      string
		ids       []string
		special   *string
		wantInput string // 非空 = 期望 ErrInvalidInput 且 message 含此串
		wantIDs   []string
	}{
		{"空 ids", nil, sp("sp-1"), "请选择要归类的流水", nil},
		{"超上限", many, sp("sp-1"), "一次最多处理 1000 条流水", nil},
		{"恰好上限放行", many[:MaxBatchTxIDs], sp("sp-1"), "", many[:MaxBatchTxIDs]},
		{"缺 special_id", []string{"a"}, nil, "缺少 special_id", nil},
		{"专项不存在", []string{"a"}, sp("ghost"), "专项不存在", nil},
		{"滤掉空 id", []string{"a", "", "b"}, sp("sp-1"), "", []string{"a", "b"}},
		{"空串归回日常", []string{"a"}, sp(""), "", []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, repo := newUpdateUC(t)
			_, err := uc.AssignSpecial(context.Background(), tt.ids, tt.special)
			if tt.wantInput != "" {
				if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), tt.wantInput) {
					t.Fatalf("err = %v; want ErrInvalidInput 含 %q", err, tt.wantInput)
				}
				if len(repo.batchIDs) != 0 {
					t.Errorf("被拒绝却仍写库: %v", repo.batchIDs)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(repo.batchIDs) != fmt.Sprint(tt.wantIDs) {
				t.Errorf("下发的 ids = %v; want %v", repo.batchIDs, tt.wantIDs)
			}
		})
	}
}

func TestSpecialViewEnsureDistinguishesNotFoundFromFault(t *testing.T) {
	boom := errors.New("db down")
	notFound := NewSpecialView(&fakeSpecialProjectRepo{})
	if err := notFound.Ensure(context.Background(), "x"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("不存在: err = %v; want 包装了 port.ErrNotFound", err)
	}
	fault := NewSpecialView(&failingSpecialRepo{fakeSpecialProjectRepo{}, boom})
	if err := fault.Ensure(context.Background(), "x"); !errors.Is(err, boom) || errors.Is(err, port.ErrNotFound) {
		t.Errorf("故障: err = %v; want 原样返回且不是 ErrNotFound", err)
	}
}

type failingSpecialRepo struct {
	fakeSpecialProjectRepo
	err error
}

func (f *failingSpecialRepo) Get(context.Context, string) (domain.SpecialProject, error) {
	return domain.SpecialProject{}, f.err
}

func TestApplyRuleSentinels(t *testing.T) {
	// 规则指向收入科目 / 无目标科目 / 科目不存在 都是 ErrRuleNotApplicable，且 message 是面向用户的中文
	cats := testCategories()
	tests := []struct {
		name string
		rule domain.CategoryRule
		want string
	}{
		{"死规则", domain.CategoryRule{ID: "r", CategoryID: "income.salary.husband"}, "永远不会命中的死规则"},
		{"无目标科目", domain.CategoryRule{ID: "r"}, "没有目标科目"},
		{"科目不存在", domain.CategoryRule{ID: "r", CategoryID: "gone"}, "不存在"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewTxQuery(&fakeTransactionRepo{}, &fakeCategoryRepo{cats: cats}, &fakeCategoryRuleRepo{rules: []domain.CategoryRule{tt.rule}})
			_, err := q.ApplyRule(context.Background(), "r", domain.Period{}, domain.AccountFamily)
			if !errors.Is(err, ErrRuleNotApplicable) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v; want ErrRuleNotApplicable 含 %q", err, tt.want)
			}
		})
	}
}

func TestTxQueryExecuteInvalidPeriodIsSentinel(t *testing.T) {
	q, _, _ := newTestTxQuery(t)
	_, err := q.Execute(context.Background(), TxQueryRequest{Period: "garbage"})
	if !errors.Is(err, ErrInvalidPeriod) {
		t.Errorf("err = %v; want ErrInvalidPeriod", err)
	}
	_, err = q.Execute(context.Background(), TxQueryRequest{RuleID: "nope"})
	if !errors.Is(err, port.ErrNotFound) {
		t.Errorf("规则不存在: err = %v; want 包装了 port.ErrNotFound", err)
	}
}
