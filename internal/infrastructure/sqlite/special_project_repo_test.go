package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ---- SpecialProjectRepo CRUD 与聚合 ----
// 夹具复用 scope_test.go 的 newScopeFixture：两个专项（装修 sp-reno / 换车 sp-car）、
// 日常与专项流水各 3 笔、外加两笔非 confirmed。

func TestSpecialProjectGetNotFound(t *testing.T) {
	f := newScopeFixture(t)
	_, err := f.spRepo.Get(context.Background(), "no-such-project")
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("Get(missing) error = %v; want port.ErrNotFound", err)
	}
}

func TestSpecialProjectUpsertAndGet(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	got, err := f.spRepo.Get(ctx, spReno)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "2026 老房装修" || got.BudgetFen != 180000 {
		t.Fatalf("Get() = %+v; want name=2026 老房装修 budget=180000", got)
	}
	if !got.StartedOn.Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("StartedOn = %v; want 2026-04-01", got.StartedOn)
	}
	// 结束日期留空 = 进行中
	if !got.EndedOn.IsZero() || !got.IsActive() {
		t.Fatalf("EndedOn = %v, IsActive = %v; want 零值 + 进行中", got.EndedOn, got.IsActive())
	}

	// 同 id 再 Upsert 覆盖字段（预算改大、补上结束日期）
	upd := got
	upd.Name = "2026 老房装修（追加预算）"
	upd.BudgetFen = 250000
	upd.EndedOn = time.Date(2026, 8, 31, 0, 0, 0, 0, time.Local)
	upd.Note = "含家电"
	if err := f.spRepo.Upsert(ctx, &upd); err != nil {
		t.Fatalf("Upsert(update) error = %v", err)
	}
	after, err := f.spRepo.Get(ctx, spReno)
	if err != nil {
		t.Fatalf("Get(after update) error = %v", err)
	}
	if after.Name != upd.Name || after.BudgetFen != 250000 || after.Note != "含家电" {
		t.Fatalf("Upsert 未覆盖字段: %+v", after)
	}
	if after.IsActive() {
		t.Fatal("填了结束日期后 IsActive 应为 false")
	}

	// 覆盖写入不应把库里的项目数量变多
	all, err := f.spRepo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(ListAll()) = %d; want 2（Upsert 应按 id 覆盖而不是新增）", len(all))
	}
}

// TestSpecialProjectListAllOrder 进行中的排前面，其次按开始日期倒序
func TestSpecialProjectListAllOrder(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	// 把装修标成已结束，换车保持进行中 → 换车应排到前面
	reno, err := f.spRepo.Get(ctx, spReno)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	reno.EndedOn = time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	if err := f.spRepo.Upsert(ctx, &reno); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	all, err := f.spRepo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(all) != 2 || all[0].ID != spCar || all[1].ID != spReno {
		t.Fatalf("ListAll 顺序 = %v; want [%s %s]（进行中优先）", ids(all), spCar, spReno)
	}
}

func ids(ps []domain.SpecialProject) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

// TestSpecialProjectSumByProject 只算 confirmed：e1（excluded，8888，挂在装修上）必须被排除
func TestSpecialProjectSumByProject(t *testing.T) {
	f := newScopeFixture(t)

	got, err := f.spRepo.SumByProject(context.Background())
	if err != nil {
		t.Fatalf("SumByProject() error = %v", err)
	}
	want := map[string]int64{
		spReno: 130000, // s1 100000 + s2 30000（e1 8888 是 excluded，不算）
		spCar:  200000, // s3
	}
	if len(got) != len(want) {
		t.Fatalf("SumByProject() = %v; want %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("专项 %s 已花费 = %d; want %d（全部：%v）", id, got[id], w, got)
		}
	}
}

// TestSpecialProjectSumByProjectInPeriod 周期 + 账户过滤
func TestSpecialProjectSumByProjectInPeriod(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	q1, err := domain.ParsePeriod("2026Q1")
	if err != nil {
		t.Fatalf("parse period: %v", err)
	}
	m4, err := domain.ParsePeriod("2026-04")
	if err != nil {
		t.Fatalf("parse period: %v", err)
	}

	tests := []struct {
		name    string
		period  domain.Period
		account domain.Account
		want    map[string]int64
	}{
		{"2026Q2·家庭", f.period, domain.AccountFamily, map[string]int64{spReno: 130000, spCar: 200000}},
		{"2026Q2·男主", f.period, domain.AccountHusband, map[string]int64{spReno: 100000, spCar: 200000}},
		{"2026Q2·女主", f.period, domain.AccountWife, map[string]int64{spReno: 30000}},
		{"2026-04·家庭（只有 s1）", m4, domain.AccountFamily, map[string]int64{spReno: 100000}},
		{"2026Q1·家庭（无专项流水）", q1, domain.AccountFamily, map[string]int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.spRepo.SumByProjectInPeriod(ctx, tt.period, tt.account)
			if err != nil {
				t.Fatalf("SumByProjectInPeriod() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("SumByProjectInPeriod() = %v; want %v", got, tt.want)
			}
			for id, w := range tt.want {
				if got[id] != w {
					t.Fatalf("专项 %s = %d; want %d（全部：%v）", id, got[id], w, got)
				}
			}
		})
	}
}

// TestSpecialProjectSumByCategoryForProject 一个装修横跨多个科目，按金额降序、只返回非零
func TestSpecialProjectSumByCategoryForProject(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		project string
		want    []domain.CategoryAggregation
	}{
		{
			name: "装修横跨房屋维护 + 购物消费", project: spReno,
			want: []domain.CategoryAggregation{
				{CategoryID: "expense.family.home_maintenance", Name: "房屋维护", ParentID: "expense.family", Amount: 100000},
				{CategoryID: "expense.discretion.shopping", Name: "购物消费", ParentID: "expense.discretion", Amount: 30000},
			},
		},
		{
			name: "换车只有一个科目", project: spCar,
			want: []domain.CategoryAggregation{
				{CategoryID: "expense.family.home_maintenance", Name: "房屋维护", ParentID: "expense.family", Amount: 200000},
			},
		},
		{name: "不存在的专项返回空", project: "no-such", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.spRepo.SumByCategoryForProject(ctx, tt.project)
			if err != nil {
				t.Fatalf("SumByCategoryForProject() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("构成 = %+v; want %+v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("第 %d 项 = %+v; want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestSpecialProjectDeleteReleasesTransactions 删除专项时流水本身不删，
// 只把 special_id 置空归回日常——删完再查一次 daily 口径，金额要把这些流水算回来。
// 这与 status='excluded' 的语义区别正是专项设计的关键。
func TestSpecialProjectDeleteReleasesTransactions(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	before, err := f.txRepo.SumByBuckets(ctx, f.buckets, domain.DirectionExpense, domain.AccountFamily, domain.ScopeDaily)
	if err != nil {
		t.Fatalf("SumByBuckets(before) error = %v", err)
	}
	var beforeDaily int64
	for _, b := range before {
		beforeDaily += b.Amount
	}
	if beforeDaily != 3500 { // d1 1000 + d2 2000 + d3 500
		t.Fatalf("删除前日常支出 = %d; want 3500", beforeDaily)
	}

	if err := f.spRepo.Delete(ctx, spReno); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// 专项没了
	if _, err := f.spRepo.Get(ctx, spReno); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("Get(deleted) error = %v; want port.ErrNotFound", err)
	}
	// 流水还在，只是 special_id 被清空
	for _, id := range []string{"s1", "s2", "e1"} {
		tx, err := f.txRepo.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get(%s) error = %v（流水不该被连带删除）", id, err)
		}
		if tx.SpecialID != "" {
			t.Fatalf("流水 %s 的 special_id = %q; want 空（应归回日常）", id, tx.SpecialID)
		}
	}
	// 换车的流水不受影响
	if tx, err := f.txRepo.Get(ctx, "s3"); err != nil || tx.SpecialID != spCar {
		t.Fatalf("s3.special_id = %q (err=%v); want %s（删别的专项不该动它）", tx.SpecialID, err, spCar)
	}

	// 日常口径把 s1 / s2 算回来（e1 是 excluded，任何口径都不计）
	after, err := f.txRepo.SumByBuckets(ctx, f.buckets, domain.DirectionExpense, domain.AccountFamily, domain.ScopeDaily)
	if err != nil {
		t.Fatalf("SumByBuckets(after) error = %v", err)
	}
	var afterDaily int64
	for _, b := range after {
		afterDaily += b.Amount
	}
	if want := beforeDaily + 130000; afterDaily != want {
		t.Fatalf("删除后日常支出 = %d; want %d（s1+s2 应被算回日常）", afterDaily, want)
	}

	// 仅专项口径只剩换车
	sums, err := f.spRepo.SumByProject(ctx)
	if err != nil {
		t.Fatalf("SumByProject() error = %v", err)
	}
	if len(sums) != 1 || sums[spCar] != 200000 {
		t.Fatalf("SumByProject() = %v; want 只剩 %s=200000", sums, spCar)
	}
}

func TestSpecialProjectDeleteNotFound(t *testing.T) {
	f := newScopeFixture(t)
	err := f.spRepo.Delete(context.Background(), "no-such-project")
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("Delete(missing) error = %v; want port.ErrNotFound", err)
	}
}
