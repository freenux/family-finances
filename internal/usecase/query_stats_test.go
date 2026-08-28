package usecase

import (
	"context"
	"testing"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// bucketByLabel 从展示桶里按 label 找一条；调用方保证桶里一定有这个 label。
func bucketByLabel(t *testing.T, bs []StatsBucket, label string) StatsBucket {
	t.Helper()
	for _, b := range bs {
		if b.Label == label {
			return b
		}
	}
	labels := make([]string, len(bs))
	for i, b := range bs {
		labels[i] = b.Label
	}
	t.Fatalf("桶里没有 label=%s；现有 %v", label, labels)
	return StatsBucket{}
}

// assertAmountInvariant 核心不变式：daily.Amount + special.Amount == all.Amount，逐桶成立。
// 三组桶来自同一批 buildXxxBuckets 输出，长度、顺序、label 必须完全对齐。
func assertAmountInvariant(t *testing.T, name string, daily, special, all []StatsBucket) {
	t.Helper()
	if len(daily) != len(all) || len(special) != len(all) {
		t.Fatalf("%s: 三个口径展示桶数量不一致 daily=%d special=%d all=%d", name, len(daily), len(special), len(all))
	}
	for i := range all {
		if daily[i].Label != all[i].Label || special[i].Label != all[i].Label {
			t.Fatalf("%s[%d]: label 未对齐 daily=%s special=%s all=%s", name, i, daily[i].Label, special[i].Label, all[i].Label)
		}
		if got, want := daily[i].Amount+special[i].Amount, all[i].Amount; got != want {
			t.Fatalf("%s[%s]: daily.Amount(%d) + special.Amount(%d) = %d；want all.Amount = %d",
				name, all[i].Label, daily[i].Amount, special[i].Amount, got, want)
		}
	}
}

// TestQueryStatsCompareFollowsScope 是本次修复的核心回归测试：月度/季度对比条必须
// 跟随请求的 scope 变化，而不是像修复前那样恒为日常口径、专项恒作为叠加段。
//
// 夹具：6-8 月日常持续有开销；7 月单独砸了一笔 50 万的装修（落在 Q3）。
func TestQueryStatsCompareFollowsScope(t *testing.T) {
	txRepo := &fakeTransactionRepo{
		bucketAmts: map[string]int64{
			"2026-06": 8000, "2026-07": 12000, "2026-08": 9000,
			"2026Q2": 20000, "2026Q3": 21000,
		},
		specialBuckets: map[string]int64{
			"2026-07": 500000, // 装修落在 7 月
			"2026Q3":  500000, // 7 月属于 Q3
		},
	}
	catRepo := &fakeCategoryRepo{cats: testCategories()}
	uc := NewQueryStats(txRepo, catRepo)
	p, err := domain.ParsePeriod("2026-08")
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	ctx := context.Background()

	views := make(map[domain.Scope]StatsView, 3)
	for _, scope := range []domain.Scope{domain.ScopeDaily, domain.ScopeAll, domain.ScopeSpecial} {
		v, err := uc.Execute(ctx, p, domain.DirectionExpense, domain.AccountFamily, scope, 10)
		if err != nil {
			t.Fatalf("Execute(%s): %v", scope, err)
		}
		views[scope] = v
	}

	// ---- Amount 分别等于 日常 / 日常+专项 / 专项 ----
	daily7 := bucketByLabel(t, views[domain.ScopeDaily].MonthlyCompare, "2026-07")
	all7 := bucketByLabel(t, views[domain.ScopeAll].MonthlyCompare, "2026-07")
	special7 := bucketByLabel(t, views[domain.ScopeSpecial].MonthlyCompare, "2026-07")

	if daily7.Amount != 12000 {
		t.Fatalf("daily[2026-07].Amount = %d; want 12000", daily7.Amount)
	}
	if all7.Amount != 512000 {
		t.Fatalf("all[2026-07].Amount = %d; want 512000（12000 日常 + 500000 专项）", all7.Amount)
	}
	if special7.Amount != 500000 {
		t.Fatalf("special[2026-07].Amount = %d; want 500000", special7.Amount)
	}

	// ---- 不变式：daily.Amount + special.Amount == all.Amount，逐桶成立 ----
	assertAmountInvariant(t, "monthly", views[domain.ScopeDaily].MonthlyCompare, views[domain.ScopeSpecial].MonthlyCompare, views[domain.ScopeAll].MonthlyCompare)
	assertAmountInvariant(t, "quarterly", views[domain.ScopeDaily].QuarterlyCompare, views[domain.ScopeSpecial].QuarterlyCompare, views[domain.ScopeAll].QuarterlyCompare)

	// ---- Special 字段：daily 恒 nil / all 是专项那截 / special 等于 Amount 本身 ----
	if daily7.Special != nil {
		t.Fatalf("daily[2026-07].Special = %d; want nil（日常口径下没有专项部分）", *daily7.Special)
	}
	if all7.Special == nil || *all7.Special != 500000 {
		t.Fatalf("all[2026-07].Special = %v; want 500000", all7.Special)
	}
	if special7.Special == nil || *special7.Special != special7.Amount {
		t.Fatalf("special[2026-07].Special = %v; want 等于 Amount(%d)", special7.Special, special7.Amount)
	}

	// 没有专项的月份：all 口径下 Special 应留 nil（专项额为 0，前端不画斜纹）
	all6 := bucketByLabel(t, views[domain.ScopeAll].MonthlyCompare, "2026-06")
	if all6.Special != nil {
		t.Fatalf("all[2026-06].Special = %d; want nil（该月没有专项开销）", *all6.Special)
	}

	// ---- 季度维度重复验证一遍三态（装修落在 Q3）----
	dailyQ3 := bucketByLabel(t, views[domain.ScopeDaily].QuarterlyCompare, "2026Q3")
	allQ3 := bucketByLabel(t, views[domain.ScopeAll].QuarterlyCompare, "2026Q3")
	specialQ3 := bucketByLabel(t, views[domain.ScopeSpecial].QuarterlyCompare, "2026Q3")
	if dailyQ3.Amount != 21000 || allQ3.Amount != 521000 || specialQ3.Amount != 500000 {
		t.Fatalf("季度 2026Q3 三口径 amount = daily %d / all %d / special %d; want 21000 / 521000 / 500000",
			dailyQ3.Amount, allQ3.Amount, specialQ3.Amount)
	}
	if dailyQ3.Special != nil {
		t.Fatalf("daily[2026Q3].Special = %d; want nil", *dailyQ3.Special)
	}
	if allQ3.Special == nil || *allQ3.Special != 500000 {
		t.Fatalf("all[2026Q3].Special = %v; want 500000", allQ3.Special)
	}
	if specialQ3.Special == nil || *specialQ3.Special != specialQ3.Amount {
		t.Fatalf("special[2026Q3].Special = %v; want 等于 Amount(%d)", specialQ3.Special, specialQ3.Amount)
	}
}

// TestQueryStatsChainFollowsScope 是最能证明修复生效的一条：日常开支持平，但某一期
// 砸了一笔专项巨款。日常口径下环比应该几乎不变；全部口径下环比必须暴涨——如果代码
// 还在按旧逻辑把对比条钉死在日常口径上，这条测试的 all 分支会失败（chain ≈ 0）。
func TestQueryStatsChainFollowsScope(t *testing.T) {
	txRepo := &fakeTransactionRepo{
		bucketAmts: map[string]int64{
			"2026-06": 10000, "2026-07": 10000, "2026-08": 10000, // 日常持平
		},
		specialBuckets: map[string]int64{
			"2026-08": 500000, // 专项只砸在 8 月，7 月为 0
		},
	}
	catRepo := &fakeCategoryRepo{cats: testCategories()}
	uc := NewQueryStats(txRepo, catRepo)
	p, err := domain.ParsePeriod("2026-08")
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	ctx := context.Background()

	dailyView, err := uc.Execute(ctx, p, domain.DirectionExpense, domain.AccountFamily, domain.ScopeDaily, 10)
	if err != nil {
		t.Fatalf("Execute(daily): %v", err)
	}
	allView, err := uc.Execute(ctx, p, domain.DirectionExpense, domain.AccountFamily, domain.ScopeAll, 10)
	if err != nil {
		t.Fatalf("Execute(all): %v", err)
	}
	specialView, err := uc.Execute(ctx, p, domain.DirectionExpense, domain.AccountFamily, domain.ScopeSpecial, 10)
	if err != nil {
		t.Fatalf("Execute(special): %v", err)
	}

	dailyAug := bucketByLabel(t, dailyView.MonthlyCompare, "2026-08")
	allAug := bucketByLabel(t, allView.MonthlyCompare, "2026-08")
	specialAug := bucketByLabel(t, specialView.MonthlyCompare, "2026-08")

	// 日常口径：6/7/8 月都是 10000，环比应该接近 0（严格等于 0，因为是整数精确整除）。
	if dailyAug.Chain == nil {
		t.Fatalf("daily[2026-08].Chain = nil；基期(7月)=10000 不为 0，不该是 nil")
	}
	if c := *dailyAug.Chain; c < -0.001 || c > 0.001 {
		t.Fatalf("daily[2026-08].Chain = %v; want ≈0（日常持平——这正是本次要修复的行为）", c)
	}

	// 全部口径：基期(7月)全口径同样是 10000（无专项），当期(8月) = 10000+500000。
	// 环比应该是 (510000-10000)/10000 = 50（+5000%），远大于日常口径的 ~0。
	if allAug.Chain == nil {
		t.Fatalf("all[2026-08].Chain = nil；基期(7月)=10000 不为 0，不该是 nil")
	}
	if c := *allAug.Chain; c < 10 {
		t.Fatalf("all[2026-08].Chain = %v; want 很大（专项暴涨必须反映在全部口径的环比里，不能被摊平成日常口径的 ~0）", c)
	}

	// 仅专项口径：基期(7月)专项为 0 → 环比必须是 nil（除零保护），不能是修复过程中
	// 意外引入的 +Inf 或者被错误地当成"无穷增长"处理。这条老语义不能因为本次改动被破坏。
	if specialAug.Chain != nil {
		t.Fatalf("special[2026-08].Chain = %v; want nil（基期 7 月专项额为 0）", *specialAug.Chain)
	}
}

// TestQueryStatsTopFollowsScope 缺陷 1（用户实测报告的 bug）：Top 榜单曾经写死
// domain.ScopeAll，用户选了「日常」，大额榜首照样是那笔装修款——等于告诉用户筛选没生效。
func TestQueryStatsTopFollowsScope(t *testing.T) {
	dailyTop := port.TopTransaction{ID: "tx-daily", Counterparty: "山姆会员店", Amount: 120000}
	specialTop := port.TopTransaction{ID: "tx-special", Counterparty: "装修公司", Amount: 8000000, SpecialName: "老房装修"}

	tests := []struct {
		name    string
		scope   domain.Scope
		wantIDs []string
	}{
		{"日常：装修流水必须被剔掉", domain.ScopeDaily, []string{"tx-daily"}},
		{"仅专项：只剩装修流水", domain.ScopeSpecial, []string{"tx-special"}},
		{"全部：两笔都在，专项在前", domain.ScopeAll, []string{"tx-special", "tx-daily"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txRepo := &fakeTransactionRepo{tops: map[domain.Scope][]port.TopTransaction{
				domain.ScopeDaily:   {dailyTop},
				domain.ScopeSpecial: {specialTop},
				domain.ScopeAll:     {specialTop, dailyTop},
			}}
			uc := NewQueryStats(txRepo, &fakeCategoryRepo{cats: testCategories()})
			p, err := domain.ParsePeriod("2026Q2")
			if err != nil {
				t.Fatalf("ParsePeriod: %v", err)
			}

			view, err := uc.Execute(context.Background(), p, domain.DirectionExpense, domain.AccountFamily, tt.scope, 10)
			if err != nil {
				t.Fatalf("Execute(%s): %v", tt.scope, err)
			}

			if len(txRepo.topScopes) != 1 || txRepo.topScopes[0] != tt.scope {
				t.Fatalf("TopTransactions 收到口径 %v; want [%s]", txRepo.topScopes, tt.scope)
			}
			gotIDs := make([]string, 0, len(view.TopTransactions))
			for _, top := range view.TopTransactions {
				gotIDs = append(gotIDs, top.ID)
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("topTransactions = %v; want %v", gotIDs, tt.wantIDs)
			}
			for i := range tt.wantIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Fatalf("topTransactions = %v; want %v", gotIDs, tt.wantIDs)
				}
			}
		})
	}
}

// TestQueryStatsTopRespectsLimit 顺带钉住 limit 仍然透传（scope 改动别把它带偏）
func TestQueryStatsTopRespectsLimit(t *testing.T) {
	rows := []port.TopTransaction{{ID: "a", Amount: 3}, {ID: "b", Amount: 2}, {ID: "c", Amount: 1}}
	txRepo := &fakeTransactionRepo{tops: map[domain.Scope][]port.TopTransaction{domain.ScopeDaily: rows}}
	uc := NewQueryStats(txRepo, &fakeCategoryRepo{cats: testCategories()})
	p, _ := domain.ParsePeriod("2026Q2")

	view, err := uc.Execute(context.Background(), p, domain.DirectionExpense, domain.AccountFamily, domain.ScopeDaily, 2)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(view.TopTransactions) != 2 {
		t.Fatalf("topTransactions 条数 = %d; want 2", len(view.TopTransactions))
	}
}

// TestQueryStatsQueriesBucketsOnce 月桶、季桶各查一次就够：一条 SQL 同时返回
// 日常与专项两组，Go 侧按 scope 拼展示序列。改动前按口径各查一遍 → 4 条查询，
// 实测 10 万行下 /api/stats 的桶部分多付 190ms，而切月份/账户/方向每次都要重来。
func TestQueryStatsQueriesBucketsOnce(t *testing.T) {
	p, err := domain.ParsePeriod("2026Q2")
	if err != nil {
		t.Fatalf("parse period: %v", err)
	}
	scopes := []domain.Scope{domain.ScopeDaily, domain.ScopeAll, domain.ScopeSpecial}
	for _, scope := range scopes {
		t.Run(string(scope), func(t *testing.T) {
			txRepo := &fakeTransactionRepo{
				bucketAmts:     map[string]int64{},
				specialBuckets: map[string]int64{},
			}
			uc := NewQueryStats(txRepo, &fakeCategoryRepo{cats: testCategories()})
			if _, err := uc.Execute(context.Background(), p, domain.DirectionExpense,
				domain.AccountFamily, scope, 10); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if txRepo.bucketCalls != 2 {
				t.Fatalf("SumByBuckets 被调用 %d 次; want 2（月桶、季桶各一次，与 scope 无关）", txRepo.bucketCalls)
			}
		})
	}
}
