package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ---- 迁移 016：资金往来科目 + 它在真库上的聚合安全网 ----
//
// 背景：status 一个枚举原本扛了两件事——核对进度，和"算不算真实收支"。
// 016 把后者交给科目：transfer.* 下的流水一律以 status='excluded' 落地。
//
// 这组测试守的是整个改动最脆的一环。AggregateByCategory 那条路本身是安全的
// （Go 侧按 income./expense. 前缀分流，transfer. 两边都落不进），但
// SumByBuckets / TopTransactions / ListForRecurring 只按 direction + status
// 过滤、根本不看科目。往来行一旦落成 confirmed，四笔钱、周报、目标进度、
// 月/季对比条、Top 榜单会一起把转账算成真支出。

func newMigration016DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestMigration016SeedsTransferCategories 科目树上确实长出了「资金往来」这一支。
func TestMigration016SeedsTransferCategories(t *testing.T) {
	db := newMigration016DB(t)

	tests := []struct {
		id         string
		wantLevel  int
		wantParent string
		wantType   string
	}{
		{"transfer", 1, "", "transfer"},
		{"transfer.internal", 2, "transfer", "transfer"},
		{"transfer.loan", 2, "transfer", "transfer"},
		{"transfer.reimburse", 2, "transfer", "transfer"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			var level int
			var parent sql.NullString
			var typ string
			err := db.QueryRow(
				`SELECT level, parent_id, type FROM categories WHERE id = ?`, tt.id,
			).Scan(&level, &parent, &typ)
			if err != nil {
				t.Fatalf("查 %s: %v", tt.id, err)
			}
			if level != tt.wantLevel {
				t.Errorf("level = %d; want %d", level, tt.wantLevel)
			}
			if parent.String != tt.wantParent {
				t.Errorf("parent_id = %q; want %q", parent.String, tt.wantParent)
			}
			if typ != tt.wantType {
				t.Errorf("type = %q; want %q", typ, tt.wantType)
			}
		})
	}
}

// TestMigration016RepointsBuiltinSkipRules 迁移 005 的 6 条内置跳过规则从
// category_id=NULL 改指到 transfer.internal。
//
// 意义不只是换个值：原先命中的行落地时既没科目也没解释，用户在流水页上
// 看不见、也不知道为什么被排掉，一笔被误判成"转账"的学费就永远找不回来了。
func TestMigration016RepointsBuiltinSkipRules(t *testing.T) {
	db := newMigration016DB(t)

	for _, id := range []string{
		"builtin-skip-platform-transfer",
		"builtin-skip-platform-red-packet",
		"builtin-skip-platform-withdraw",
		"builtin-skip-platform-deposit",
		"builtin-skip-platform-credit-card",
		"builtin-skip-platform-investment",
	} {
		var cat sql.NullString
		if err := db.QueryRow(`SELECT category_id FROM category_rules WHERE id = ?`, id).Scan(&cat); err != nil {
			t.Fatalf("查规则 %s: %v", id, err)
		}
		if cat.String != "transfer.internal" {
			t.Errorf("规则 %s 的 category_id = %q; want transfer.internal", id, cat.String)
		}
	}
}

// TestMigration016TransferRulesOutrankGenericTransfer 借款/报销规则的 priority
// 必须严格小于（早于）通用「转账」规则。
//
// 微信里个人借款和报销到账走的都是"转账"这个交易类型，会先撞上
// builtin-skip-platform-transfer。只有排在它前面，备注里写了"借款/报销"的
// 转账才能落到更准的科目上，否则一律被归成内部转账。
func TestMigration016TransferRulesOutrankGenericTransfer(t *testing.T) {
	db := newMigration016DB(t)

	var generic int
	if err := db.QueryRow(
		`SELECT priority FROM category_rules WHERE id = 'builtin-skip-platform-transfer'`,
	).Scan(&generic); err != nil {
		t.Fatalf("查通用转账规则: %v", err)
	}

	for _, id := range []string{
		"builtin-transfer-reimburse",
		"builtin-transfer-loan",
		"builtin-transfer-lend",
		"builtin-transfer-payback",
	} {
		var priority int
		var cat sql.NullString
		if err := db.QueryRow(
			`SELECT priority, category_id FROM category_rules WHERE id = ?`, id,
		).Scan(&priority, &cat); err != nil {
			t.Fatalf("查规则 %s: %v", id, err)
		}
		if priority >= generic {
			t.Errorf("规则 %s priority = %d; want < %d（通用转账规则）——否则备注写了借款/报销的微信转账会先被通用规则吃掉",
				id, priority, generic)
		}
		if !domain.IsTransferCategory(cat.String) {
			t.Errorf("规则 %s 指向 %q; want 一个 transfer.* 科目", id, cat.String)
		}
	}
}

// TestMigration016NoGenericRepaymentRule 故意不种通用的「还款」规则。
//
// '房贷还款'/'车贷还款' 会被一起卷走：那些是真实支出（或含利息），
// 静默从报表里消失比没分类严重得多。信用卡还款已由精确规则命中。
func TestMigration016NoGenericRepaymentRule(t *testing.T) {
	db := newMigration016DB(t)

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM category_rules WHERE pattern = '还款' AND pattern_type = 'contains'`,
	).Scan(&n); err != nil {
		t.Fatalf("查还款规则: %v", err)
	}
	if n != 0 {
		t.Errorf("存在 %d 条通用「还款」contains 规则; want 0——房贷/车贷还款会被误判成不计收支", n)
	}
}

// TestTransferRowsStayOutOfAggregations 整个改动的安全网：
// 挂在往来科目上的 excluded 行，不进任何一条聚合。
//
// 特意让往来行的方向和金额都跟真支出一样（direction=expense），
// 唯一的区别就是 status——这正是三条 direction-only 的 SQL 唯一能拦住它的地方。
func TestTransferRowsStayOutOfAggregations(t *testing.T) {
	db := newMigration016DB(t)
	txRepo := NewTransactionRepo(db)
	ctx := context.Background()
	now := time.Now()

	d := func(m, day int) time.Time { return time.Date(2026, time.Month(m), day, 10, 0, 0, 0, time.Local) }
	rows := []struct {
		id       string
		day      time.Time
		amount   int64
		dir      domain.Direction
		status   domain.TxStatus
		category string
	}{
		// 一笔正经支出，作为对照组
		{"real-1", d(5, 1), 3000, domain.DirectionExpense, domain.TxStatusConfirmed, "expense.necessary.food"},
		// 往来行：金额远大于对照组，一旦被算进去，任何断言都会明显崩掉
		{"tr-1", d(5, 2), 500000, domain.DirectionExpense, domain.TxStatusExcluded, "transfer.internal"},
		{"tr-2", d(5, 3), 800000, domain.DirectionExpense, domain.TxStatusExcluded, "transfer.loan"},
		{"tr-3", d(5, 4), 200000, domain.DirectionIncome, domain.TxStatusExcluded, "transfer.reimburse"},
	}
	for _, r := range rows {
		if err := txRepo.Insert(ctx, domain.Transaction{
			ID: r.id, Source: domain.SourceManual, Account: domain.AccountHusband,
			OccurredAt: r.day, Amount: r.amount, Direction: r.dir, Status: r.status,
			CategoryID: r.category, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}

	p, err := domain.ParsePeriod("2026Q2")
	if err != nil {
		t.Fatalf("parse period: %v", err)
	}

	t.Run("AggregateByCategory 不给往来科目算钱", func(t *testing.T) {
		aggs, err := txRepo.AggregateByCategory(ctx, p, domain.AccountFamily, domain.ScopeAll)
		if err != nil {
			t.Fatalf("AggregateByCategory: %v", err)
		}
		var sawTransferCat bool
		for _, a := range aggs {
			if domain.IsTransferCategory(a.CategoryID) {
				sawTransferCat = true
				if a.Amount != 0 {
					t.Errorf("科目 %s 聚合出 %d; want 0", a.CategoryID, a.Amount)
				}
			}
		}
		if !sawTransferCat {
			t.Error("结果里没有任何 transfer.* 科目——LEFT JOIN 应当把它们以 0 带出来")
		}
	})

	t.Run("SumByBuckets 不把转账当支出", func(t *testing.T) {
		buckets := []port.PeriodBucket{{Label: "2026Q2", Start: p.Start, End: p.End}}
		daily, special, err := txRepo.SumByBuckets(ctx, buckets, domain.DirectionExpense, domain.AccountFamily)
		if err != nil {
			t.Fatalf("SumByBuckets: %v", err)
		}
		if got := daily[0].Amount; got != 3000 {
			t.Errorf("日常支出 = %d; want 3000（只有 real-1）——往来行被算进来了", got)
		}
		if got := special[0].Amount; got != 0 {
			t.Errorf("专项支出 = %d; want 0", got)
		}
	})

	t.Run("TopTransactions 不把转账排进榜单", func(t *testing.T) {
		tops, err := txRepo.TopTransactions(ctx, p, domain.DirectionExpense, domain.AccountFamily, domain.ScopeAll, 10)
		if err != nil {
			t.Fatalf("TopTransactions: %v", err)
		}
		if len(tops) != 1 {
			t.Fatalf("Top 榜单 %d 条; want 1（只有 real-1）——8000 元的借款不该霸榜", len(tops))
		}
		if tops[0].Amount != 3000 {
			t.Errorf("榜首金额 = %d; want 3000", tops[0].Amount)
		}
	})

	t.Run("ListForRecurring 不把转账认成周期支出", func(t *testing.T) {
		got, err := txRepo.ListForRecurring(ctx, p.Start, p.End, domain.ScopeDaily)
		if err != nil {
			t.Fatalf("ListForRecurring: %v", err)
		}
		for _, tx := range got {
			if domain.IsTransferCategory(tx.CategoryID) {
				t.Errorf("往来流水 %s 进了周期识别候选集", tx.ID)
			}
		}
		if len(got) != 1 {
			t.Errorf("候选 %d 条; want 1（只有 real-1）", len(got))
		}
	})
}

// TestMigration016Down 回滚必须能跑通，而且是在"已经有流水挂在往来科目上"的
// 真实状态下——transactions.category_id 和 category_rules.category_id 都有
// FOREIGN KEY 指向 categories(id)，不先把这两处引用解开，DELETE 科目会被 FK 挡住。
func TestMigration016Down(t *testing.T) {
	db := newMigration016DB(t)
	txRepo := NewTransactionRepo(db)
	ctx := context.Background()
	now := time.Now()

	if err := txRepo.Insert(ctx, domain.Transaction{
		ID: "tr-1", Source: domain.SourceManual, Account: domain.AccountHusband,
		OccurredAt: now, Amount: 100000, Direction: domain.DirectionExpense,
		Status: domain.TxStatusExcluded, CategoryID: "transfer.loan",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert 往来流水: %v", err)
	}

	// DownTo(15) 而不是 Down()：Down 只回滚最后一个迁移，后面每加一版
	// 这里测的就不再是 016 了。
	goose.SetBaseFS(embeddedMigrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.DownTo(db, "migrations", 15); err != nil {
		t.Fatalf("goose down to 015: %v", err)
	}

	var nCats int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM categories WHERE id = 'transfer' OR id LIKE 'transfer.%'`,
	).Scan(&nCats); err != nil {
		t.Fatalf("查科目: %v", err)
	}
	if nCats != 0 {
		t.Errorf("down 后还剩 %d 个 transfer 科目; want 0", nCats)
	}

	// 挂在往来科目上的流水本身要保留，只是解开科目引用——回滚不该毁数据
	var cat sql.NullString
	if err := db.QueryRow(`SELECT category_id FROM transactions WHERE id = 'tr-1'`).Scan(&cat); err != nil {
		t.Fatalf("查流水: %v", err)
	}
	if cat.Valid && cat.String != "" {
		t.Errorf("down 后 tr-1 的 category_id = %q; want NULL", cat.String)
	}

	// 6 条内置跳过规则回到 category_id=NULL 的老样子
	var nRepointed int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM category_rules WHERE category_id LIKE 'transfer.%'`,
	).Scan(&nRepointed); err != nil {
		t.Fatalf("查规则: %v", err)
	}
	if nRepointed != 0 {
		t.Errorf("down 后还有 %d 条规则指向 transfer.*; want 0", nRepointed)
	}
	var nSkip int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM category_rules WHERE id = 'builtin-skip-platform-transfer' AND category_id IS NULL`,
	).Scan(&nSkip); err != nil {
		t.Fatalf("查跳过规则: %v", err)
	}
	if nSkip != 1 {
		t.Error("down 后 builtin-skip-platform-transfer 没回到 category_id=NULL")
	}

	// 新增的 4 条往来规则应当被删掉，而不是留成孤儿
	var nNew int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM category_rules WHERE id LIKE 'builtin-transfer-%'`,
	).Scan(&nNew); err != nil {
		t.Fatalf("查新增规则: %v", err)
	}
	if nNew != 0 {
		t.Errorf("down 后还剩 %d 条 builtin-transfer-* 规则; want 0", nNew)
	}

	// 回滚后还能重新 up（迁移不是单向门）
	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("回滚后重新 goose up: %v", err)
	}
}
