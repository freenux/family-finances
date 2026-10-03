package sqlite

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// bulkFixture 跨多页的数据：2025-07 内 130 笔 husband 支出（前 100 笔未分类），
// 另有：同月 wife、同月已排除、同月收入、2025-08 的未分类。
func bulkFixture(t *testing.T) *TransactionRepo {
	t.Helper()
	r := newTestQueryRepo(t)
	for i := 0; i < 130; i++ {
		i := i
		qInsert(t, r, qTx(fmt.Sprintf("h-%03d", i), func(x *domain.Transaction) {
			x.OccurredAt = qBase.Add(time.Duration(i) * time.Hour)
			if i >= 100 {
				x.CategoryID = "expense.discretion.shopping"
			}
			if i%10 == 0 {
				x.Source = domain.Source("csv:招行")
			}
			if i%7 == 0 {
				x.Member = "张三"
			}
			x.Counterparty = fmt.Sprintf("商户%d", i%5)
		}))
	}
	qInsert(t, r,
		qTx("wife-1", func(x *domain.Transaction) { x.Account = domain.AccountWife }),
		qTx("excl-1", func(x *domain.Transaction) { x.Status = domain.TxStatusExcluded }),
		qTx("inc-1", func(x *domain.Transaction) { x.Direction = domain.DirectionIncome }),
		qTx("aug-1", func(x *domain.Transaction) { x.OccurredAt = time.Date(2025, 8, 3, 9, 0, 0, 0, time.Local) }),
	)
	return r
}

func specialsOf(t *testing.T, r *TransactionRepo, special string) []string {
	t.Helper()
	pg, err := r.QueryTransactions(context.Background(), port.TransactionQuery{
		Special: special, Statuses: []string{"pending_review", "confirmed", "excluded"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sortedCopy(txIDs(pg.Rows))
}

// 用 filter 归类一次，所有命中行（跨页）都被改，不命中的一行不动。
func TestSetSpecialByQueryCoversAllPages(t *testing.T) {
	r := bulkFixture(t)
	ctx := context.Background()
	q := port.TransactionQuery{
		Period: qPeriod(t), Account: domain.AccountHusband, Category: port.FilterNone, Direction: "expense",
		Statuses: []string{"pending_review", "confirmed"},
	}
	// 列表按 50 条一页：第一页只有 50 行，总数 100
	first := q
	first.Limit = 50
	pg, err := r.QueryTransactions(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Total != 100 || len(pg.Rows) != 50 {
		t.Fatalf("前置：total/rows = %d/%d; want 100/50", pg.Total, len(pg.Rows))
	}
	full := q
	wantIDs := func() []string {
		all, _ := r.QueryTransactions(ctx, full)
		return sortedCopy(txIDs(all.Rows))
	}()

	// 带上分页与排序也必须被忽略（调用方偷懒把列表 query 整个传进来）
	q.Limit, q.Offset, q.SortBy, q.SortDesc = 50, 50, "amount", true
	n, err := r.SetSpecialByQuery(ctx, q, qSpA)
	if err != nil {
		t.Fatal(err)
	}
	if n != 100 {
		t.Errorf("updated = %d; want 100（整个筛选结果，不是当页的 50）", n)
	}
	if got := specialsOf(t, r, qSpA); !reflect.DeepEqual(got, wantIDs) {
		t.Errorf("被归入专项的行 = %d 条; want 恰好是筛选命中的 %d 条（含第 2 页）", len(got), len(wantIDs))
	}
	// 不命中的行没被碰：已分类的 30 笔、wife、excluded、收入、八月
	for _, id := range []string{"h-100", "h-129", "wife-1", "excl-1", "inc-1", "aug-1"} {
		got, err := r.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.SpecialID != "" {
			t.Errorf("%s 不在筛选内却被改成了 %q", id, got.SpecialID)
		}
	}

	// 归回日常：空串
	n, err = r.SetSpecialByQuery(ctx, port.TransactionQuery{Period: qPeriod(t), Special: qSpA}, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 100 || len(specialsOf(t, r, qSpA)) != 0 {
		t.Errorf("归回日常 n = %d, 剩余 %d; want 100 / 0", n, len(specialsOf(t, r, qSpA)))
	}
}

// 不变式：对任意筛选，SetSpecialByQuery 改到的集合 == QueryTransactions 列出的全集。
// 两边共用 buildTxWhere，这条测试是防止将来谁给其中一边另写一份条件。
func TestSetSpecialByQueryMatchesListForAnyFilter(t *testing.T) {
	period := func() domain.Period { return qPeriod(t) }
	st := []string{"pending_review", "confirmed"}
	tests := []struct {
		name string
		q    func() port.TransactionQuery
	}{
		{"未分类", func() port.TransactionQuery {
			return port.TransactionQuery{Period: period(), Category: port.FilterNone, Statuses: st}
		}},
		{"csv 来源", func() port.TransactionQuery {
			return port.TransactionQuery{Period: period(), Sources: []string{"csv"}, Statuses: st}
		}},
		{"成员 + 方向", func() port.TransactionQuery {
			return port.TransactionQuery{Period: period(), Member: "张三", Direction: "expense", Statuses: st}
		}},
		{"成员未标注", func() port.TransactionQuery {
			return port.TransactionQuery{Period: period(), Member: port.FilterNone, Statuses: st}
		}},
		{"关键词", func() port.TransactionQuery {
			return port.TransactionQuery{Period: period(), Keyword: "商户3", Statuses: st}
		}},
		{"含已排除", func() port.TransactionQuery {
			return port.TransactionQuery{Period: period(), Statuses: []string{"excluded"}}
		}},
		{"账户视图 family", func() port.TransactionQuery {
			return port.TransactionQuery{Period: period(), Account: domain.AccountFamily, Statuses: st}
		}},
		{"规则预筛", func() port.TransactionQuery {
			rule := domain.CategoryRule{Pattern: "商户1", PatternType: "contains", Field: "counterparty"}
			return port.TransactionQuery{Period: period(), Rule: &rule, Direction: "expense", Statuses: st}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bulkFixture(t)
			ctx := context.Background()
			q := tt.q()
			all, err := r.QueryTransactions(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if all.Total == 0 {
				t.Fatal("前置：筛选无命中，用例无意义")
			}
			n, err := r.SetSpecialByQuery(ctx, q, qSpB)
			if err != nil {
				t.Fatal(err)
			}
			if n != all.Total {
				t.Errorf("updated = %d; want 列表总数 %d", n, all.Total)
			}
			if got, want := specialsOf(t, r, qSpB), sortedCopy(txIDs(all.Rows)); !reflect.DeepEqual(got, want) {
				t.Errorf("改到 %d 行、列表 %d 行，集合不一致（两边必须共用 buildTxWhere）", len(got), len(want))
			}
		})
	}
}

func TestSetSpecialByQueryRefusesWithoutPeriod(t *testing.T) {
	r := bulkFixture(t)
	if _, err := r.SetSpecialByQuery(context.Background(), port.TransactionQuery{}, qSpA); err == nil {
		t.Error("无周期的筛选应被拒绝，否则一个空条件会改全表")
	}
	if got := specialsOf(t, r, qSpA); len(got) != 0 {
		t.Errorf("被拒绝后仍改了 %d 行", len(got))
	}
}
