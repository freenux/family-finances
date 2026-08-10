package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ---- 统计口径（domain.Scope）在真实 SQLite 上的过滤正确性 ----
//
// 专项开支是与科目正交的第三个维度：daily 剔除专项、special 只看专项、all 全口径。
// 整个设计的地基是「daily + special == all」这条不变式——只要它成立，任何按口径
// 拆开的展示都能和全口径合计对上账。下面四个聚合方法逐个显式断言这条。

// scopeFixture 一份含日常流水 + 两个专项流水的夹具，覆盖账户、状态、周期边界几个维度。
type scopeFixture struct {
	db      *sql.DB
	txRepo  *TransactionRepo
	spRepo  *SpecialProjectRepo
	period  domain.Period // 2026Q2
	buckets []port.PeriodBucket
}

// fixtureTx 夹具里的一行流水
type fixtureTx struct {
	id        string
	account   domain.Account
	day       time.Time
	amount    int64
	direction domain.Direction
	status    domain.TxStatus
	category  string
	special   string // 空 = 日常
}

const (
	spReno = "sp-reno" // 装修
	spCar  = "sp-car"  // 购车
)

// 夹具流水表：2026Q2 内 3 笔日常支出 + 3 笔专项支出（分属两个专项）+
// 2 笔非 confirmed（任何口径都不该计入）+ 1 笔收入 + 1 笔周期外。
func fixtureRows() []fixtureTx {
	d := func(m, day int) time.Time { return time.Date(2026, time.Month(m), day, 10, 0, 0, 0, time.Local) }
	return []fixtureTx{
		// —— 日常支出 ——
		{"d1", domain.AccountHusband, d(4, 10), 1000, domain.DirectionExpense, domain.TxStatusConfirmed, "expense.necessary.food", ""},
		{"d2", domain.AccountWife, d(5, 5), 2000, domain.DirectionExpense, domain.TxStatusConfirmed, "expense.discretion.shopping", ""},
		{"d3", domain.AccountHusband, d(6, 20), 500, domain.DirectionExpense, domain.TxStatusConfirmed, "expense.necessary.food", ""},
		// —— 专项支出：装修横跨"房屋维护"和"购物消费"两个科目，购车单独一笔 ——
		{"s1", domain.AccountHusband, d(4, 15), 100000, domain.DirectionExpense, domain.TxStatusConfirmed, "expense.family.home_maintenance", spReno},
		{"s2", domain.AccountWife, d(5, 18), 30000, domain.DirectionExpense, domain.TxStatusConfirmed, "expense.discretion.shopping", spReno},
		{"s3", domain.AccountHusband, d(6, 1), 200000, domain.DirectionExpense, domain.TxStatusConfirmed, "expense.family.home_maintenance", spCar},
		// —— 非 confirmed：任何口径下都不计入 ——
		{"p1", domain.AccountHusband, d(5, 10), 9999, domain.DirectionExpense, domain.TxStatusPendingReview, "expense.necessary.food", ""},
		{"e1", domain.AccountWife, d(5, 11), 8888, domain.DirectionExpense, domain.TxStatusExcluded, "expense.discretion.shopping", spReno},
		// —— 收入（方向过滤用）——
		{"i1", domain.AccountHusband, d(4, 1), 500000, domain.DirectionIncome, domain.TxStatusConfirmed, "income.salary.husband", ""},
		// —— 周期外：2026Q1，End 独占语义外的一笔 ——
		{"o1", domain.AccountHusband, time.Date(2026, 3, 31, 12, 0, 0, 0, time.Local), 777, domain.DirectionExpense, domain.TxStatusConfirmed, "expense.necessary.food", ""},
	}
}

func newScopeFixture(t *testing.T) *scopeFixture {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	spRepo := NewSpecialProjectRepo(db)
	for _, p := range []domain.SpecialProject{
		{ID: spReno, Name: "2026 老房装修", StartedOn: time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local), BudgetFen: 180000},
		{ID: spCar, Name: "换车", StartedOn: time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local), BudgetFen: 300000},
	} {
		p := p
		if err := spRepo.Upsert(ctx, &p); err != nil {
			t.Fatalf("upsert special %s: %v", p.ID, err)
		}
	}

	txRepo := NewTransactionRepo(db)
	now := time.Now()
	for _, row := range fixtureRows() {
		if err := txRepo.Insert(ctx, domain.Transaction{
			ID:         row.id,
			Source:     domain.SourceManual,
			Account:    row.account,
			OccurredAt: row.day,
			Amount:     row.amount,
			Direction:  row.direction,
			Status:     row.status,
			CategoryID: row.category,
			SpecialID:  row.special,
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}

	p, err := domain.ParsePeriod("2026Q2")
	if err != nil {
		t.Fatalf("parse period: %v", err)
	}
	loc := time.Local
	return &scopeFixture{
		db: db, txRepo: txRepo, spRepo: spRepo, period: p,
		buckets: []port.PeriodBucket{
			{Label: "2026-04", Start: time.Date(2026, 4, 1, 0, 0, 0, 0, loc), End: time.Date(2026, 5, 1, 0, 0, 0, 0, loc)},
			{Label: "2026-05", Start: time.Date(2026, 5, 1, 0, 0, 0, 0, loc), End: time.Date(2026, 6, 1, 0, 0, 0, 0, loc)},
			{Label: "2026-06", Start: time.Date(2026, 6, 1, 0, 0, 0, 0, loc), End: time.Date(2026, 7, 1, 0, 0, 0, 0, loc)},
		},
	}
}

// wantSum 按夹具算出期望值：给定账户 + 方向 + 口径，2026Q2 内 confirmed 流水的合计
func wantSum(account domain.Account, dir domain.Direction, scope domain.Scope) int64 {
	var total int64
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	for _, r := range fixtureRows() {
		if r.status != domain.TxStatusConfirmed || r.direction != dir {
			continue
		}
		if r.day.Before(start) || !r.day.Before(end) {
			continue
		}
		if account.IsStorageAccount() && r.account != account {
			continue
		}
		switch scope {
		case domain.ScopeDaily:
			if r.special != "" {
				continue
			}
		case domain.ScopeSpecial:
			if r.special == "" {
				continue
			}
		}
		total += r.amount
	}
	return total
}

// allAccounts 三种账户口径：family 合并，husband/wife 各自过滤
var allAccounts = []domain.Account{domain.AccountFamily, domain.AccountHusband, domain.AccountWife}

// ---- A1. AggregateByCategory ----

func TestAggregateByCategoryScope(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	// 按科目断言：口径 → 科目 → 金额。未列出的科目应为 0。
	tests := []struct {
		name    string
		account domain.Account
		scope   domain.Scope
		want    map[string]int64
	}{
		{
			name: "家庭·日常", account: domain.AccountFamily, scope: domain.ScopeDaily,
			want: map[string]int64{
				"expense.necessary.food":      1500,
				"expense.discretion.shopping": 2000,
				"income.salary.husband":       500000,
			},
		},
		{
			name: "家庭·仅专项", account: domain.AccountFamily, scope: domain.ScopeSpecial,
			want: map[string]int64{
				"expense.family.home_maintenance": 300000,
				"expense.discretion.shopping":     30000,
			},
		},
		{
			name: "家庭·全部", account: domain.AccountFamily, scope: domain.ScopeAll,
			want: map[string]int64{
				"expense.necessary.food":          1500,
				"expense.discretion.shopping":     32000,
				"expense.family.home_maintenance": 300000,
				"income.salary.husband":           500000,
			},
		},
		{
			name: "男主·日常", account: domain.AccountHusband, scope: domain.ScopeDaily,
			want: map[string]int64{
				"expense.necessary.food": 1500,
				"income.salary.husband":  500000,
			},
		},
		{
			name: "男主·仅专项", account: domain.AccountHusband, scope: domain.ScopeSpecial,
			want: map[string]int64{"expense.family.home_maintenance": 300000},
		},
		{
			name: "女主·日常", account: domain.AccountWife, scope: domain.ScopeDaily,
			want: map[string]int64{"expense.discretion.shopping": 2000},
		},
		{
			name: "女主·仅专项", account: domain.AccountWife, scope: domain.ScopeSpecial,
			want: map[string]int64{"expense.discretion.shopping": 30000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aggs, err := f.txRepo.AggregateByCategory(ctx, f.period, tt.account, tt.scope)
			if err != nil {
				t.Fatalf("AggregateByCategory() error = %v", err)
			}
			// LEFT JOIN 保证每个二级科目都有一行（无流水时为 0），这点本身也是回归点：
			// 口径过滤必须留在 ON 里，挪到 WHERE 会把无匹配流水的科目整行滤掉。
			if len(aggs) < len(tt.want) {
				t.Fatalf("聚合行数 = %d，少于期望的非零科目数 %d（口径过滤可能被挪到了 WHERE）", len(aggs), len(tt.want))
			}
			got := map[string]int64{}
			for _, a := range aggs {
				if a.Amount != 0 {
					got[a.CategoryID] = a.Amount
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("非零科目 = %v; want %v", got, tt.want)
			}
			for id, want := range tt.want {
				if got[id] != want {
					t.Fatalf("科目 %s = %d; want %d（全部非零科目：%v）", id, got[id], want, got)
				}
			}
		})
	}
}

// TestAggregateByCategoryScopeInvariant daily + special == all，逐科目逐账户成立
func TestAggregateByCategoryScopeInvariant(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	sumByCat := func(account domain.Account, scope domain.Scope) map[string]int64 {
		t.Helper()
		aggs, err := f.txRepo.AggregateByCategory(ctx, f.period, account, scope)
		if err != nil {
			t.Fatalf("AggregateByCategory(%s,%s) error = %v", account, scope, err)
		}
		out := map[string]int64{}
		for _, a := range aggs {
			out[a.CategoryID] = a.Amount
		}
		return out
	}

	for _, acc := range allAccounts {
		t.Run(string(acc), func(t *testing.T) {
			daily, special, all := sumByCat(acc, domain.ScopeDaily), sumByCat(acc, domain.ScopeSpecial), sumByCat(acc, domain.ScopeAll)
			if len(all) != len(daily) || len(all) != len(special) {
				t.Fatalf("三种口径返回的科目行数不一致：daily=%d special=%d all=%d", len(daily), len(special), len(all))
			}
			for id, allAmt := range all {
				if got := daily[id] + special[id]; got != allAmt {
					t.Fatalf("科目 %s：daily(%d) + special(%d) = %d，但 all = %d", id, daily[id], special[id], got, allAmt)
				}
			}
		})
	}
}

// ---- A2. SumByBuckets ----

func TestSumByBucketsScope(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		account domain.Account
		dir     domain.Direction
		scope   domain.Scope
		want    []int64 // 2026-04 / 2026-05 / 2026-06
	}{
		{"家庭·支出·日常", domain.AccountFamily, domain.DirectionExpense, domain.ScopeDaily, []int64{1000, 2000, 500}},
		{"家庭·支出·仅专项", domain.AccountFamily, domain.DirectionExpense, domain.ScopeSpecial, []int64{100000, 30000, 200000}},
		{"家庭·支出·全部", domain.AccountFamily, domain.DirectionExpense, domain.ScopeAll, []int64{101000, 32000, 200500}},
		{"男主·支出·日常", domain.AccountHusband, domain.DirectionExpense, domain.ScopeDaily, []int64{1000, 0, 500}},
		{"男主·支出·仅专项", domain.AccountHusband, domain.DirectionExpense, domain.ScopeSpecial, []int64{100000, 0, 200000}},
		{"女主·支出·日常", domain.AccountWife, domain.DirectionExpense, domain.ScopeDaily, []int64{0, 2000, 0}},
		{"女主·支出·仅专项", domain.AccountWife, domain.DirectionExpense, domain.ScopeSpecial, []int64{0, 30000, 0}},
		// 收入没有专项：special 口径全 0，daily 与 all 相同
		{"家庭·收入·日常", domain.AccountFamily, domain.DirectionIncome, domain.ScopeDaily, []int64{500000, 0, 0}},
		{"家庭·收入·仅专项", domain.AccountFamily, domain.DirectionIncome, domain.ScopeSpecial, []int64{0, 0, 0}},
		{"家庭·收入·全部", domain.AccountFamily, domain.DirectionIncome, domain.ScopeAll, []int64{500000, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.txRepo.SumByBuckets(ctx, f.buckets, tt.dir, tt.account, tt.scope)
			if err != nil {
				t.Fatalf("SumByBuckets() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("桶数 = %d; want %d", len(got), len(tt.want))
			}
			for i, w := range tt.want {
				if got[i].Amount != w {
					t.Fatalf("桶 %s = %d; want %d", got[i].Label, got[i].Amount, w)
				}
			}
		})
	}
}

// TestSumByBucketsScopeInvariant daily + special == all，逐桶逐账户成立
func TestSumByBucketsScopeInvariant(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	for _, acc := range allAccounts {
		for _, dir := range []domain.Direction{domain.DirectionExpense, domain.DirectionIncome} {
			t.Run(string(acc)+"·"+string(dir), func(t *testing.T) {
				sums := map[domain.Scope][]port.PeriodBucket{}
				for _, s := range []domain.Scope{domain.ScopeDaily, domain.ScopeSpecial, domain.ScopeAll} {
					got, err := f.txRepo.SumByBuckets(ctx, f.buckets, dir, acc, s)
					if err != nil {
						t.Fatalf("SumByBuckets(%s) error = %v", s, err)
					}
					sums[s] = got
				}
				var totalAll int64
				for i := range f.buckets {
					d, sp, all := sums[domain.ScopeDaily][i].Amount, sums[domain.ScopeSpecial][i].Amount, sums[domain.ScopeAll][i].Amount
					if d+sp != all {
						t.Fatalf("桶 %s：daily(%d) + special(%d) = %d，但 all = %d", f.buckets[i].Label, d, sp, d+sp, all)
					}
					totalAll += all
				}
				// 与按夹具手算的周期合计对上（季度 = 三个月桶之和）
				if want := wantSum(acc, dir, domain.ScopeAll); totalAll != want {
					t.Fatalf("三桶合计 = %d; want %d", totalAll, want)
				}
			})
		}
	}
}

// ---- A3. TopTransactions ----

func TestTopTransactionsScope(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		account domain.Account
		scope   domain.Scope
		wantIDs []string // 按 amount desc
	}{
		{"家庭·日常", domain.AccountFamily, domain.ScopeDaily, []string{"d2", "d1", "d3"}},
		{"家庭·仅专项", domain.AccountFamily, domain.ScopeSpecial, []string{"s3", "s1", "s2"}},
		{"家庭·全部", domain.AccountFamily, domain.ScopeAll, []string{"s3", "s1", "s2", "d2", "d1", "d3"}},
		{"男主·日常", domain.AccountHusband, domain.ScopeDaily, []string{"d1", "d3"}},
		{"男主·仅专项", domain.AccountHusband, domain.ScopeSpecial, []string{"s3", "s1"}},
		{"女主·全部", domain.AccountWife, domain.ScopeAll, []string{"s2", "d2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tops, err := f.txRepo.TopTransactions(ctx, f.period, domain.DirectionExpense, tt.account, tt.scope, 50)
			if err != nil {
				t.Fatalf("TopTransactions() error = %v", err)
			}
			var gotIDs []string
			for _, tx := range tops {
				gotIDs = append(gotIDs, tx.ID)
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("ids = %v; want %v", gotIDs, tt.wantIDs)
			}
			for i, want := range tt.wantIDs {
				if gotIDs[i] != want {
					t.Fatalf("ids = %v; want %v", gotIDs, tt.wantIDs)
				}
			}
			// 专项名要跟着 join 出来，前端 Top 榜单靠它打标
			for _, tx := range tops {
				switch tx.SpecialID {
				case "":
					if tx.SpecialName != "" {
						t.Fatalf("%s 无专项却带出专项名 %q", tx.ID, tx.SpecialName)
					}
				case spReno:
					if tx.SpecialName != "2026 老房装修" {
						t.Fatalf("%s 专项名 = %q; want 2026 老房装修", tx.ID, tx.SpecialName)
					}
				case spCar:
					if tx.SpecialName != "换车" {
						t.Fatalf("%s 专项名 = %q; want 换车", tx.ID, tx.SpecialName)
					}
				}
			}
		})
	}
}

// TestTopTransactionsScopeInvariant daily ∪ special == all（条数与金额两方面）
func TestTopTransactionsScopeInvariant(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	for _, acc := range allAccounts {
		t.Run(string(acc), func(t *testing.T) {
			sum := func(scope domain.Scope) (int64, int) {
				tops, err := f.txRepo.TopTransactions(ctx, f.period, domain.DirectionExpense, acc, scope, 100)
				if err != nil {
					t.Fatalf("TopTransactions(%s) error = %v", scope, err)
				}
				var total int64
				for _, tx := range tops {
					total += tx.Amount
				}
				return total, len(tops)
			}
			dAmt, dN := sum(domain.ScopeDaily)
			sAmt, sN := sum(domain.ScopeSpecial)
			aAmt, aN := sum(domain.ScopeAll)
			if dAmt+sAmt != aAmt {
				t.Fatalf("金额：daily(%d) + special(%d) = %d，但 all = %d", dAmt, sAmt, dAmt+sAmt, aAmt)
			}
			if dN+sN != aN {
				t.Fatalf("条数：daily(%d) + special(%d) = %d，但 all = %d", dN, sN, dN+sN, aN)
			}
			if want := wantSum(acc, domain.DirectionExpense, domain.ScopeAll); aAmt != want {
				t.Fatalf("全口径合计 = %d; want %d", aAmt, want)
			}
		})
	}
}

// ---- A4. ListForRecurring ----

func TestListForRecurringScope(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()
	from, to := f.period.Start, f.period.End

	tests := []struct {
		name  string
		scope domain.Scope
		want  []int64 // 按 occurred_at 升序的金额
	}{
		{"日常", domain.ScopeDaily, []int64{1000, 2000, 500}},
		{"仅专项", domain.ScopeSpecial, []int64{100000, 30000, 200000}},
		{"全部", domain.ScopeAll, []int64{1000, 100000, 2000, 30000, 200000, 500}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.txRepo.ListForRecurring(ctx, from, to, tt.scope)
			if err != nil {
				t.Fatalf("ListForRecurring() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("条数 = %d; want %d", len(got), len(tt.want))
			}
			for i, w := range tt.want {
				if got[i].Amount != w {
					t.Fatalf("第 %d 条金额 = %d; want %d", i, got[i].Amount, w)
				}
			}
		})
	}
}

// TestListForRecurringScopeInvariant daily + special == all
func TestListForRecurringScopeInvariant(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	sum := func(scope domain.Scope) (int64, int) {
		txs, err := f.txRepo.ListForRecurring(ctx, f.period.Start, f.period.End, scope)
		if err != nil {
			t.Fatalf("ListForRecurring(%s) error = %v", scope, err)
		}
		var total int64
		for _, tx := range txs {
			total += tx.Amount
		}
		return total, len(txs)
	}
	dAmt, dN := sum(domain.ScopeDaily)
	sAmt, sN := sum(domain.ScopeSpecial)
	aAmt, aN := sum(domain.ScopeAll)
	if dAmt+sAmt != aAmt || dN+sN != aN {
		t.Fatalf("daily(%d 元/%d 条) + special(%d/%d) 应等于 all(%d/%d)", dAmt, dN, sAmt, sN, aAmt, aN)
	}
	if want := wantSum(domain.AccountFamily, domain.DirectionExpense, domain.ScopeAll); aAmt != want {
		t.Fatalf("全口径合计 = %d; want %d", aAmt, want)
	}
}

// ---- A5. 非 confirmed 流水在任何口径下都不计入 ----

// TestNonConfirmedExcludedFromEveryScope p1（日常 pending_review）与 e1（专项 excluded）
// 的金额都很显眼（9999 / 8888），只要哪个口径漏了过滤，合计立刻对不上。
func TestNonConfirmedExcludedFromEveryScope(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	scopes := []domain.Scope{domain.ScopeDaily, domain.ScopeSpecial, domain.ScopeAll}
	for _, acc := range allAccounts {
		for _, scope := range scopes {
			t.Run(string(acc)+"·"+string(scope), func(t *testing.T) {
				want := wantSum(acc, domain.DirectionExpense, scope)

				// AggregateByCategory
				aggs, err := f.txRepo.AggregateByCategory(ctx, f.period, acc, scope)
				if err != nil {
					t.Fatalf("AggregateByCategory() error = %v", err)
				}
				var aggTotal int64
				for _, a := range aggs {
					if len(a.CategoryID) >= 8 && a.CategoryID[:8] == "expense." {
						aggTotal += a.Amount
					}
				}
				if aggTotal != want {
					t.Fatalf("AggregateByCategory 支出合计 = %d; want %d（非 confirmed 流水疑似被计入）", aggTotal, want)
				}

				// SumByBuckets
				bs, err := f.txRepo.SumByBuckets(ctx, f.buckets, domain.DirectionExpense, acc, scope)
				if err != nil {
					t.Fatalf("SumByBuckets() error = %v", err)
				}
				var bucketTotal int64
				for _, b := range bs {
					bucketTotal += b.Amount
				}
				if bucketTotal != want {
					t.Fatalf("SumByBuckets 合计 = %d; want %d（非 confirmed 流水疑似被计入）", bucketTotal, want)
				}

				// TopTransactions
				tops, err := f.txRepo.TopTransactions(ctx, f.period, domain.DirectionExpense, acc, scope, 100)
				if err != nil {
					t.Fatalf("TopTransactions() error = %v", err)
				}
				var topTotal int64
				for _, tx := range tops {
					if tx.ID == "p1" || tx.ID == "e1" {
						t.Fatalf("TopTransactions 返回了非 confirmed 流水 %s", tx.ID)
					}
					topTotal += tx.Amount
				}
				if topTotal != want {
					t.Fatalf("TopTransactions 合计 = %d; want %d", topTotal, want)
				}
			})
		}
	}

	// ListForRecurring 不分账户，单独断一次
	for _, scope := range scopes {
		txs, err := f.txRepo.ListForRecurring(ctx, f.period.Start, f.period.End, scope)
		if err != nil {
			t.Fatalf("ListForRecurring() error = %v", err)
		}
		var total int64
		for _, tx := range txs {
			total += tx.Amount
		}
		if want := wantSum(domain.AccountFamily, domain.DirectionExpense, scope); total != want {
			t.Fatalf("ListForRecurring(%s) 合计 = %d; want %d", scope, total, want)
		}
	}
}
