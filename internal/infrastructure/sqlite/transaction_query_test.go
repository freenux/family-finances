package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

const (
	qSpA = "sp-q-a"
	qSpB = "sp-q-b"
)

func newTestQueryRepo(t *testing.T) *TransactionRepo {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sp := NewSpecialProjectRepo(db)
	for _, id := range []string{qSpA, qSpB} {
		if err := sp.Upsert(context.Background(), &domain.SpecialProject{ID: id, Name: id, StartedOn: time.Now()}); err != nil {
			t.Fatalf("upsert special: %v", err)
		}
	}
	return NewTransactionRepo(db)
}

var qBase = time.Date(2025, 7, 1, 10, 0, 0, 0, time.Local)

func qTx(id string, mod func(*domain.Transaction)) domain.Transaction {
	tx := domain.Transaction{
		ID: id, Source: domain.SourceManual, Account: domain.AccountHusband,
		OccurredAt: qBase, Amount: 100, Direction: domain.DirectionExpense,
		Status: domain.TxStatusConfirmed, CreatedAt: qBase, UpdatedAt: qBase,
	}
	if mod != nil {
		mod(&tx)
	}
	return tx
}

func qInsert(t *testing.T, r *TransactionRepo, txs ...domain.Transaction) {
	t.Helper()
	for _, tx := range txs {
		if err := r.Insert(context.Background(), tx); err != nil {
			t.Fatalf("insert %s: %v", tx.ID, err)
		}
	}
}

func qPeriod(t *testing.T) domain.Period {
	t.Helper()
	p, err := domain.ParsePeriod("2025-07")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func txIDs(rows []domain.Transaction) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func sortedCopy(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}

func TestQueryTransactionsTotalsCoverWholeResultSet(t *testing.T) {
	r := newTestQueryRepo(t)
	// 60 笔：40 笔支出 100 分、20 笔收入 1000 分
	for i := 0; i < 60; i++ {
		i := i
		qInsert(t, r, qTx(fmt.Sprintf("t%02d", i), func(tx *domain.Transaction) {
			tx.OccurredAt = qBase.Add(time.Duration(i) * time.Minute)
			if i%3 == 0 {
				tx.Direction, tx.Amount = domain.DirectionIncome, 1000
			}
		}))
	}
	got, err := r.QueryTransactions(context.Background(), port.TransactionQuery{
		Period: qPeriod(t), SortBy: "occurred_at", SortDesc: true, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 10 {
		t.Fatalf("当页行数 = %d; want 10", len(got.Rows))
	}
	if got.Total != 60 {
		t.Errorf("Total = %d; want 60（满足筛选的全集行数，不是当页行数）", got.Total)
	}
	want := port.TransactionTotals{IncomeFen: 20 * 1000, ExpenseFen: 40 * 100}
	if got.Totals != want {
		t.Errorf("Totals = %+v; want %+v（必须是 60 行全集合计，而不是前 10 行的）", got.Totals, want)
	}
}

func TestQueryTransactionsFilters(t *testing.T) {
	r := newTestQueryRepo(t)
	qInsert(t, r,
		qTx("a-alipay", func(x *domain.Transaction) {
			x.Source = domain.SourceAlipay
			x.CategoryID = "expense.discretion.shopping"
		}),
		qTx("b-wechat", func(x *domain.Transaction) { x.Source = domain.SourceWechat; x.Account = domain.AccountWife }),
		qTx("c-csv1", func(x *domain.Transaction) { x.Source = "csv:招商"; x.SpecialID = qSpA }),
		qTx("d-csv2", func(x *domain.Transaction) { x.Source = "csv:建行"; x.SpecialID = qSpB; x.Member = "张三" }),
		qTx("e-income", func(x *domain.Transaction) {
			x.Direction = domain.DirectionIncome
			x.Status = domain.TxStatusPendingReview
		}),
		qTx("f-excluded", func(x *domain.Transaction) { x.Status = domain.TxStatusExcluded }),
		qTx("g-out", func(x *domain.Transaction) { x.OccurredAt = qBase.AddDate(0, 1, 0) }), // 8 月，不在周期内
	)
	p := qPeriod(t)
	tests := []struct {
		name string
		q    port.TransactionQuery
		want []string
	}{
		{"不筛状态：excluded 也在（缺省值由 usecase 决定，repo 照做）", port.TransactionQuery{}, []string{"a-alipay", "b-wechat", "c-csv1", "d-csv2", "e-income", "f-excluded", "g-out"}},
		{"周期窗口 [Start,End) 排除 8 月", port.TransactionQuery{Period: p}, []string{"a-alipay", "b-wechat", "c-csv1", "d-csv2", "e-income", "f-excluded"}},
		{"账户=wife", port.TransactionQuery{Period: p, Account: domain.AccountWife}, []string{"b-wechat"}},
		{"账户=family 不过滤", port.TransactionQuery{Period: p, Account: domain.AccountFamily}, []string{"a-alipay", "b-wechat", "c-csv1", "d-csv2", "e-income", "f-excluded"}},
		{"方向=income", port.TransactionQuery{Period: p, Direction: "income"}, []string{"e-income"}},
		{"状态 pending+confirmed 不含 excluded", port.TransactionQuery{Period: p, Statuses: []string{"pending_review", "confirmed"}}, []string{"a-alipay", "b-wechat", "c-csv1", "d-csv2", "e-income"}},
		{"状态=excluded", port.TransactionQuery{Period: p, Statuses: []string{"excluded"}}, []string{"f-excluded"}},
		{"来源 csv 匹配所有 csv:<模板>", port.TransactionQuery{Period: p, Sources: []string{"csv"}}, []string{"c-csv1", "d-csv2"}},
		{"来源 alipay+csv 用 OR 组合", port.TransactionQuery{Period: p, Sources: []string{"alipay", "csv"}}, []string{"a-alipay", "c-csv1", "d-csv2"}},
		{"分类=__none__ 即 NULL", port.TransactionQuery{Period: p, Category: "__none__", Sources: []string{"wechat"}}, []string{"b-wechat"}},
		{"分类=具体 id", port.TransactionQuery{Period: p, Category: "expense.discretion.shopping"}, []string{"a-alipay"}},
		{"专项=__none__ 即日常", port.TransactionQuery{Period: p, Special: "__none__"}, []string{"a-alipay", "b-wechat", "e-income", "f-excluded"}},
		{"专项=__any__ 任意专项", port.TransactionQuery{Period: p, Special: "__any__"}, []string{"c-csv1", "d-csv2"}},
		{"专项=具体 id", port.TransactionQuery{Period: p, Special: qSpB}, []string{"d-csv2"}},
		{"成员=__none__ 覆盖空串", port.TransactionQuery{Period: p, Member: "__none__"}, []string{"a-alipay", "b-wechat", "c-csv1", "e-income", "f-excluded"}},
		{"成员=具体名", port.TransactionQuery{Period: p, Member: "张三"}, []string{"d-csv2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.QueryTransactions(context.Background(), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if g := sortedCopy(txIDs(got.Rows)); !reflect.DeepEqual(g, sortedCopy(tt.want)) {
				t.Errorf("ids = %v; want %v", g, sortedCopy(tt.want))
			}
			if got.Total != len(tt.want) {
				t.Errorf("Total = %d; want %d（与取行共用同一 WHERE，两边必须一致）", got.Total, len(tt.want))
			}
		})
	}
}

func TestQueryTransactionsMemberNullAndEmpty(t *testing.T) {
	r := newTestQueryRepo(t)
	qInsert(t, r, qTx("empty", nil), qTx("named", func(x *domain.Transaction) { x.Member = "李四" }))
	// 绕过 Insert 造一行 member 为 NULL 的历史数据
	if _, err := r.db.Exec(`UPDATE transactions SET member = NULL WHERE id = 'empty'`); err != nil {
		t.Skipf("member 列不允许 NULL，无需覆盖: %v", err)
	}
	got, err := r.QueryTransactions(context.Background(), port.TransactionQuery{Member: "__none__"})
	if err != nil {
		t.Fatal(err)
	}
	if g := txIDs(got.Rows); !reflect.DeepEqual(g, []string{"empty"}) {
		t.Errorf("ids = %v; want [empty]（__none__ 要同时覆盖空串与 NULL）", g)
	}
}

func TestQueryTransactionsKeyword(t *testing.T) {
	r := newTestQueryRepo(t)
	qInsert(t, r,
		qTx("k-cp", func(x *domain.Transaction) { x.Counterparty = "星巴克" }),
		qTx("k-desc", func(x *domain.Transaction) { x.Description = "星巴克拿铁" }),
		qTx("k-note", func(x *domain.Transaction) { x.Note = "和同事星巴克" }),
		qTx("k-raw", func(x *domain.Transaction) { x.RawRow = "a,b,星巴克,c" }),
		qTx("k-none", func(x *domain.Transaction) { x.Counterparty = "麦当劳" }),
		qTx("pct", func(x *domain.Transaction) { x.Description = "满100%返现" }),
		qTx("pct-other", func(x *domain.Transaction) { x.Description = "满1005返现" }),
		qTx("under", func(x *domain.Transaction) { x.Description = "a_b" }),
		qTx("under-other", func(x *domain.Transaction) { x.Description = "axb" }),
		qTx("bs", func(x *domain.Transaction) { x.Description = `C:\dir` }),
		qTx("bs-other", func(x *domain.Transaction) { x.Description = `C:dir` }),
	)
	tests := []struct {
		name string
		kw   string
		want []string
	}{
		{"四个字段都能命中", "星巴克", []string{"k-cp", "k-desc", "k-note", "k-raw"}},
		{"% 是字面量，不是通配符", "0%", []string{"pct"}},
		{"_ 是字面量，不是单字符通配", "a_b", []string{"under"}},
		{"反斜杠是字面量", `C:\`, []string{"bs"}},
		{"没有命中", "不存在的词", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.QueryTransactions(context.Background(), port.TransactionQuery{Keyword: tt.kw})
			if err != nil {
				t.Fatal(err)
			}
			if g := sortedCopy(txIDs(got.Rows)); !reflect.DeepEqual(g, sortedCopy(tt.want)) && !(len(g) == 0 && len(tt.want) == 0) {
				t.Errorf("keyword %q ids = %v; want %v", tt.kw, g, sortedCopy(tt.want))
			}
		})
	}
}

func TestQueryTransactionsSort(t *testing.T) {
	r := newTestQueryRepo(t)
	qInsert(t, r,
		qTx("1", func(x *domain.Transaction) {
			x.Amount, x.Counterparty, x.Source, x.Direction = 300, "b", domain.SourceWechat, domain.DirectionIncome
			x.Account, x.Member, x.Status = domain.AccountWife, "乙", domain.TxStatusPendingReview
			x.OccurredAt = qBase.Add(2 * time.Hour)
		}),
		qTx("2", func(x *domain.Transaction) {
			x.Amount, x.Counterparty, x.Source, x.Direction = 100, "c", domain.SourceAlipay, domain.DirectionExpense
			x.Account, x.Member, x.Status = domain.AccountHusband, "丙", domain.TxStatusExcluded
			x.OccurredAt = qBase.Add(1 * time.Hour)
		}),
		qTx("3", func(x *domain.Transaction) {
			x.Amount, x.Counterparty, x.Source, x.Direction = 200, "a", domain.SourceManual, domain.DirectionExpense
			x.Account, x.Member, x.Status = domain.AccountHusband, "甲", domain.TxStatusConfirmed
			x.OccurredAt = qBase
		}),
	)
	tests := []struct {
		name   string
		sortBy string
		desc   bool
		want   []string
	}{
		{"amount asc", "amount", false, []string{"2", "3", "1"}},
		{"amount desc", "amount", true, []string{"1", "3", "2"}},
		{"counterparty asc", "counterparty", false, []string{"3", "1", "2"}},
		{"source asc 按存储值 alipay<manual<wechat", "source", false, []string{"2", "3", "1"}},
		{"direction asc expense<income，同值按 id DESC 兜底", "direction", false, []string{"3", "2", "1"}},
		{"account asc husband<wife", "account", false, []string{"3", "2", "1"}},
		{"status asc confirmed<excluded<pending_review", "status", false, []string{"3", "2", "1"}},
		{"member desc（按 UTF-8 字节序：甲>乙>丙，非拼音序）", "member", true, []string{"3", "1", "2"}},
		{"occurred_at desc", "occurred_at", true, []string{"1", "2", "3"}},
		{"未知键回落 occurred_at", "amount; DROP TABLE transactions;--", true, []string{"1", "2", "3"}},
		{"空键回落 occurred_at", "", false, []string{"3", "2", "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.QueryTransactions(context.Background(), port.TransactionQuery{SortBy: tt.sortBy, SortDesc: tt.desc})
			if err != nil {
				t.Fatalf("QueryTransactions error = %v（非法排序键不该导致 SQL 错误）", err)
			}
			if g := txIDs(got.Rows); !reflect.DeepEqual(g, tt.want) {
				t.Errorf("order = %v; want %v", g, tt.want)
			}
		})
	}
	// 注入串之后表还在
	if _, err := r.QueryTransactions(context.Background(), port.TransactionQuery{}); err != nil {
		t.Fatalf("注入串执行后表被破坏: %v", err)
	}
}

// 排序键全相同（金额都一样）时，翻完所有页拿到的 id 集合必须不重不漏。
func TestQueryTransactionsPagingStable(t *testing.T) {
	r := newTestQueryRepo(t)
	const n = 47
	for i := 0; i < n; i++ {
		qInsert(t, r, qTx(fmt.Sprintf("p%02d", i), func(x *domain.Transaction) { x.Amount = int64(100 * (i % 3)) }))
	}
	for _, sortBy := range []string{"amount", "occurred_at", "category_id"} {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s desc=%v", sortBy, desc), func(t *testing.T) {
				seen := map[string]int{}
				var total int
				for off := 0; off < n+10; off += 10 {
					pg, err := r.QueryTransactions(context.Background(), port.TransactionQuery{
						Period: qPeriod(t), SortBy: sortBy, SortDesc: desc, Offset: off, Limit: 10,
					})
					if err != nil {
						t.Fatal(err)
					}
					total = pg.Total
					for _, row := range pg.Rows {
						seen[row.ID]++
					}
				}
				if total != n || len(seen) != total {
					t.Fatalf("翻页得到 %d 个不同 id, Total=%d; want 都等于 %d（有漏行）", len(seen), total, n)
				}
				for id, c := range seen {
					if c != 1 {
						t.Errorf("id %s 出现 %d 次; want 1（有重行，说明排序缺稳定 tiebreaker）", id, c)
					}
				}
			})
		}
	}
}

func TestQueryTransactionsLimitZeroReturnsAll(t *testing.T) {
	r := newTestQueryRepo(t)
	for i := 0; i < 5; i++ {
		qInsert(t, r, qTx(fmt.Sprintf("z%d", i), nil))
	}
	got, err := r.QueryTransactions(context.Background(), port.TransactionQuery{})
	if err != nil || len(got.Rows) != 5 {
		t.Fatalf("rows=%d err=%v; want 5 行（Limit<=0 不限）", len(got.Rows), err)
	}
}

func TestDistinctMembers(t *testing.T) {
	r := newTestQueryRepo(t)
	qInsert(t, r,
		qTx("m1", func(x *domain.Transaction) { x.Member = "张三" }),
		qTx("m2", func(x *domain.Transaction) { x.Member = "张三" }),
		qTx("m3", func(x *domain.Transaction) { x.Member = "李四"; x.Account = domain.AccountWife }),
		qTx("m4", nil), // 空成员
		qTx("m5", func(x *domain.Transaction) { x.Member = "王五"; x.OccurredAt = qBase.AddDate(0, 1, 0) }), // 周期外
		qTx("m6", func(x *domain.Transaction) { x.Member = "赵六"; x.Status = domain.TxStatusExcluded }),    // 不受状态筛选影响
	)
	tests := []struct {
		name string
		acc  domain.Account
		want []string
	}{
		{"family：周期内全部去重、非空", domain.AccountFamily, []string{"张三", "李四", "赵六"}},
		{"husband 只看该账户", domain.AccountHusband, []string{"张三", "赵六"}},
		{"wife", domain.AccountWife, []string{"李四"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.DistinctMembers(context.Background(), qPeriod(t), tt.acc)
			if err != nil {
				t.Fatal(err)
			}
			want := sortedCopy(tt.want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("members = %v; want %v", got, want)
			}
		})
	}
}

// SQL 预筛必须与 domain.RuleMatches 在同一批数据上选出同一个 id 集合。
func TestQueryTransactionsRuleMatchesDomain(t *testing.T) {
	r := newTestQueryRepo(t)
	data := []domain.Transaction{
		qTx("r1", func(x *domain.Transaction) { x.Counterparty = "星巴克"; x.Description = "拿铁" }),
		qTx("r2", func(x *domain.Transaction) { x.Counterparty = "STARBUCKS Coffee" }),
		qTx("r3", func(x *domain.Transaction) { x.Description = "Starbucks"; x.PlatformCategory = "餐饮美食" }),
		qTx("r4", func(x *domain.Transaction) { x.PlatformCategory = "starbucks" }),
		qTx("r5", func(x *domain.Transaction) {
			x.Counterparty = "麦当劳"
			x.Note = "星巴克"
			x.RawRow = "starbucks"
		}), // note/raw_row 不参与
		qTx("r6", func(x *domain.Transaction) { x.Counterparty = " starbucks " }),
		qTx("r7", nil),
		qTx("r8", func(x *domain.Transaction) { x.Counterparty = "a%b"; x.Description = "x_y" }),
		qTx("r9", func(x *domain.Transaction) { x.Counterparty = "axb"; x.Description = "xzy" }),
	}
	qInsert(t, r, data...)
	rules := []domain.CategoryRule{
		{Pattern: "starbucks", PatternType: "contains", Field: "any"},
		{Pattern: "STARBUCKS", PatternType: "exact", Field: "any"},
		{Pattern: "starbucks", PatternType: "exact", Field: "counterparty"},
		{Pattern: "  Starbucks  ", Field: "description"},
		{Pattern: "starbucks", Field: "platform_category"},
		{Pattern: "星巴克", Field: "note"}, // 未知 field 当 any，且 note 不参与
		{Pattern: "星巴克", PatternType: "regex", Field: "counterparty"},
		{Pattern: "a%b", Field: "any"},
		{Pattern: "x_y", Field: "any"},
		{Pattern: "", Field: "any"},
		{Pattern: "   ", Field: "counterparty"},
		{Pattern: "咖啡", Field: "any"},
	}
	for i, rule := range rules {
		rule := rule
		t.Run(fmt.Sprintf("规则%d-%s-%s-%q", i, rule.PatternType, rule.Field, rule.Pattern), func(t *testing.T) {
			var want []string
			for _, d := range data {
				if domain.RuleMatches(rule, domain.RuleMatchFields{Counterparty: d.Counterparty, Description: d.Description, PlatformCategory: d.PlatformCategory}) {
					want = append(want, d.ID)
				}
			}
			got, err := r.QueryTransactions(context.Background(), port.TransactionQuery{Rule: &rule})
			if err != nil {
				t.Fatal(err)
			}
			if g, w := sortedCopy(txIDs(got.Rows)), sortedCopy(want); !(len(g) == 0 && len(w) == 0) && !reflect.DeepEqual(g, w) {
				t.Errorf("SQL 预筛 ids = %v; domain.RuleMatches ids = %v; want 两者一致", g, w)
			}
			if got.Total != len(want) {
				t.Errorf("Total = %d; want %d", got.Total, len(want))
			}
		})
	}
}

func TestApplyCategoryByRule(t *testing.T) {
	r := newTestQueryRepo(t)
	const shop = "expense.discretion.shopping"
	qInsert(t, r,
		qTx("a-uncat", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.Status = domain.TxStatusPendingReview }),
		qTx("b-other-cat", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.CategoryID = "expense.fixed.housing" }),
		qTx("c-already", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.CategoryID = shop }),
		qTx("d-same-pending", func(x *domain.Transaction) {
			x.Counterparty = "淘宝"
			x.CategoryID = shop
			x.Status = domain.TxStatusPendingReview
		}),
		qTx("e-excluded", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.Status = domain.TxStatusExcluded }),
		qTx("f-income", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.Direction = domain.DirectionIncome }),
		qTx("g-nomatch", func(x *domain.Transaction) { x.Counterparty = "京东" }),
		qTx("h-outside", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.OccurredAt = qBase.AddDate(0, 2, 0) }),
	)
	rule := domain.CategoryRule{Pattern: "淘宝", Field: "counterparty", CategoryID: shop}
	n, err := r.ApplyCategoryByRule(context.Background(), qPeriod(t), domain.AccountFamily, rule, "expense")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("updated = %d; want 3（a 未分类、b 别的科目、d 同科目但未确认；c 已是该科目且已确认不动）", n)
	}
	wantState := map[string][2]string{
		"a-uncat":        {shop, "confirmed"},
		"b-other-cat":    {shop, "confirmed"},
		"c-already":      {shop, "confirmed"},
		"d-same-pending": {shop, "confirmed"},
		"e-excluded":     {"", "excluded"},
		"f-income":       {"", "confirmed"},
		"g-nomatch":      {"", "confirmed"},
		"h-outside":      {"", "confirmed"},
	}
	for id, w := range wantState {
		got, err := r.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.CategoryID != w[0] || string(got.Status) != w[1] {
			t.Errorf("%s = (%q,%q); want (%q,%q)", id, got.CategoryID, got.Status, w[0], w[1])
		}
	}
	// 再应用一次：已全部一致，应为 0
	if n, _ := r.ApplyCategoryByRule(context.Background(), qPeriod(t), domain.AccountFamily, rule, "expense"); n != 0 {
		t.Errorf("重复应用 updated = %d; want 0（幂等）", n)
	}
	// 无目标科目：报错且不动任何行
	if _, err := r.ApplyCategoryByRule(context.Background(), qPeriod(t), domain.AccountFamily, domain.CategoryRule{Pattern: "淘宝"}, ""); err == nil || !strings.Contains(err.Error(), "科目") {
		t.Errorf("err = %v; want 说明性错误（规则无目标科目）", err)
	}
}

// 预筛（规则 + 支出 + 非 excluded）与批量应用必须是同一个集合：
// 预筛行数 = 应用改动行数 + 已是该科目且已确认的行数；收入行两边都不出现、也不被改。
func TestRulePrefilterAndApplySameSet(t *testing.T) {
	r := newTestQueryRepo(t)
	const shop = "expense.discretion.shopping"
	qInsert(t, r,
		qTx("e1", func(x *domain.Transaction) { x.Counterparty = "淘宝" }),
		qTx("e2", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.Status = domain.TxStatusPendingReview }),
		qTx("done", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.CategoryID = shop }),
		qTx("inc", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.Direction = domain.DirectionIncome }),
		qTx("exc", func(x *domain.Transaction) { x.Counterparty = "淘宝"; x.Status = domain.TxStatusExcluded }),
	)
	rule := domain.CategoryRule{Pattern: "淘宝", Field: "counterparty", CategoryID: shop}
	p := qPeriod(t)
	pre, err := r.QueryTransactions(context.Background(), port.TransactionQuery{
		Period: p, Rule: &rule, Direction: "expense",
		Statuses: []string{"pending_review", "confirmed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.ApplyCategoryByRule(context.Background(), p, domain.AccountFamily, rule, "expense")
	if err != nil {
		t.Fatal(err)
	}
	if pre.Total != 3 || n != 2 {
		t.Fatalf("预筛 %d 条、应用改 %d 条; want 3 和 2（差的 1 条是已确认同科目的 done）", pre.Total, n)
	}
	inc, _ := r.Get(context.Background(), "inc")
	if inc.CategoryID != "" {
		t.Errorf("收入行被改成 %q; want 不动（分类器从不分类收入）", inc.CategoryID)
	}
}
