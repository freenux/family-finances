package usecase

import (
	"context"
	"errors"
	"testing"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// fakeBulkRepo 记录 SetSpecialByQuery 的入参
type fakeBulkRepo struct {
	calls []port.TransactionQuery
	sids  []string
	n     int
	err   error
}

func (f *fakeBulkRepo) SetSpecialByQuery(_ context.Context, q port.TransactionQuery, sid string) (int, error) {
	f.calls = append(f.calls, q)
	f.sids = append(f.sids, sid)
	return f.n, f.err
}

func newByFilterUC(t *testing.T) (*UpdateTransaction, *fakeBulkRepo, *fakeTransactionRepo) {
	t.Helper()
	q, listRepo, _ := newTestTxQuery(t)
	bulk := &fakeBulkRepo{n: 118}
	specials := NewSpecialView(&fakeSpecialProjectRepo{projects: []domain.SpecialProject{{ID: "sp-1", Name: "装修"}}})
	uc := NewUpdateTransaction(&fakeTransactionRepo{}, &fakeCategoryRepo{cats: testCategories()}, specials).WithFilter(q, bulk)
	return uc, bulk, listRepo
}

// 「改到的行」与「列表上看到的行」靠同一份归一保证一致：同一个请求，
// 列表查询收到的筛选条件 == 批量改收到的筛选条件（除了排序与分页）。
func TestAssignSpecialByFilterUsesSameFilterAsList(t *testing.T) {
	req := TxQueryRequest{
		Type: "monthly", Period: "2025-06", Account: "husband", Direction: "expense",
		Source: "alipay,csv", Status: "confirmed", Category: port.FilterNone, Member: "张三", Keyword: " 咖啡 ",
		Sort: "amount", Order: "asc", Page: "3", PageSize: "20",
	}
	uc, bulk, _ := newByFilterUC(t)
	if _, err := uc.AssignSpecialByFilter(context.Background(), req, sp("sp-1")); err != nil {
		t.Fatal(err)
	}
	// 同一个 TxQuery 跑一遍列表，拿到列表实际使用的筛选
	lq, lrepo, _ := newTestTxQuery(t)
	if _, err := lq.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	want := lrepo.lastQuery
	want.SortBy, want.SortDesc, want.Offset, want.Limit = "", false, 0, 0 // 排序与分页不属于筛选
	got := bulk.calls[0]
	if got.Period.Label != want.Period.Label || got.Account != want.Account || got.Direction != want.Direction ||
		got.Category != want.Category || got.Member != want.Member || got.Keyword != want.Keyword ||
		got.Special != want.Special || len(got.Sources) != len(want.Sources) || len(got.Statuses) != len(want.Statuses) {
		t.Errorf("批量改的筛选 = %+v; want 与列表一致 %+v", got, want)
	}
	if got.Limit != 0 || got.Offset != 0 || got.SortBy != "" {
		t.Errorf("批量改带了分页/排序 %+v; want 全集（不止当页）", got)
	}
	if bulk.sids[0] != "sp-1" {
		t.Errorf("special_id = %q; want sp-1", bulk.sids[0])
	}
}

func TestAssignSpecialByFilterValidation(t *testing.T) {
	tests := []struct {
		name      string
		req       TxQueryRequest
		special   *string
		wantInput bool
		wantCalls int
	}{
		{"专项存在", TxQueryRequest{}, sp("sp-1"), false, 1},
		{"空串归回日常", TxQueryRequest{}, sp(""), false, 1},
		{"缺 special_id", TxQueryRequest{}, nil, true, 0},
		{"专项不存在", TxQueryRequest{}, sp("ghost"), true, 0},
		{"周期非法", TxQueryRequest{Period: "2025Q9"}, sp("sp-1"), false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, bulk, _ := newByFilterUC(t)
			_, err := uc.AssignSpecialByFilter(context.Background(), tt.req, tt.special)
			if got := errors.Is(err, ErrInvalidInput); got != tt.wantInput {
				t.Errorf("err = %v; ErrInvalidInput = %v, want %v", err, got, tt.wantInput)
			}
			if tt.name == "周期非法" && !errors.Is(err, ErrInvalidPeriod) {
				t.Errorf("err = %v; want ErrInvalidPeriod", err)
			}
			if len(bulk.calls) != tt.wantCalls {
				t.Errorf("写库次数 = %d; want %d（校验失败不得写库）", len(bulk.calls), tt.wantCalls)
			}
		})
	}
}

func TestAssignSpecialByFilterFaultsAreNotUserErrors(t *testing.T) {
	boom := errors.New("db down")
	t.Run("专项校验遇 DB 故障", func(t *testing.T) {
		q, _, _ := newTestTxQuery(t)
		bulk := &fakeBulkRepo{}
		uc := NewUpdateTransaction(&fakeTransactionRepo{}, &fakeCategoryRepo{}, failingEnsurer{err: boom}).WithFilter(q, bulk)
		_, err := uc.AssignSpecialByFilter(context.Background(), TxQueryRequest{}, sp("sp-1"))
		if !errors.Is(err, boom) || errors.Is(err, ErrInvalidInput) {
			t.Errorf("err = %v; want 原样的故障而不是「专项不存在」", err)
		}
	})
	t.Run("写库失败", func(t *testing.T) {
		uc, bulk, _ := newByFilterUC(t)
		bulk.err = boom
		_, err := uc.AssignSpecialByFilter(context.Background(), TxQueryRequest{}, sp("sp-1"))
		if !errors.Is(err, boom) {
			t.Errorf("err = %v; want %v", err, boom)
		}
	})
	t.Run("未装配", func(t *testing.T) {
		uc := NewUpdateTransaction(&fakeTransactionRepo{}, &fakeCategoryRepo{}, nil)
		_, err := uc.AssignSpecialByFilter(context.Background(), TxQueryRequest{}, sp(""))
		if err == nil || errors.Is(err, ErrInvalidInput) {
			t.Errorf("err = %v; want 内部错误", err)
		}
	})
}
