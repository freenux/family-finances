package sqlite

import (
	"context"
	"testing"
	"time"

	"family-finances/internal/domain"
)

// ---- 批量归入专项：一个事务里一条 UPDATE ... WHERE id IN (...) ----
//
// 改动前是逐条 Update：1000 条 = 1000 个隐式事务（实测 332ms vs 18ms），
// 且中途报错时前面已提交的部分回滚不掉。这里钉住新实现的行为语义。

func TestSetSpecialForIDs(t *testing.T) {
	tests := []struct {
		name      string
		ids       []string
		specialID string
		wantN     int
		// wantSpecial 执行后各 id 的 special_id 期望值（只列关心的）
		wantSpecial map[string]string
	}{
		{
			name: "批量归入：三笔日常流水一次挂到装修上",
			ids:  []string{"d1", "d2", "d3"}, specialID: spReno, wantN: 3,
			wantSpecial: map[string]string{"d1": spReno, "d2": spReno, "d3": spReno},
		},
		{
			name: "批量清空：空串把流水归回日常",
			ids:  []string{"s1", "s2"}, specialID: "", wantN: 2,
			wantSpecial: map[string]string{"s1": "", "s2": ""},
		},
		{
			name: "混入不存在的 id：跳过，不影响其余",
			ids:  []string{"d1", "no-such", "d2"}, specialID: spCar, wantN: 2,
			wantSpecial: map[string]string{"d1": spCar, "d2": spCar},
		},
		{
			name: "重复 id 只算一次（IN (...) 的语义）",
			ids:  []string{"d1", "d1", "d1"}, specialID: spReno, wantN: 1,
			wantSpecial: map[string]string{"d1": spReno},
		},
		{
			name: "空列表：不发查询，返回 0",
			ids:  nil, specialID: spReno, wantN: 0,
			wantSpecial: map[string]string{"d1": ""},
		},
		{
			name: "全都不存在：返回 0，库里不动",
			ids:  []string{"ghost-1", "ghost-2"}, specialID: spReno, wantN: 0,
			wantSpecial: map[string]string{"d1": ""},
		},
		{
			name: "改成另一个专项：覆盖原来的归属",
			ids:  []string{"s1"}, specialID: spCar, wantN: 1,
			wantSpecial: map[string]string{"s1": spCar},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newScopeFixture(t)
			ctx := context.Background()

			n, err := f.txRepo.SetSpecialForIDs(ctx, tt.ids, tt.specialID)
			if err != nil {
				t.Fatalf("SetSpecialForIDs() error = %v", err)
			}
			if n != tt.wantN {
				t.Fatalf("updated = %d; want %d", n, tt.wantN)
			}
			for id, want := range tt.wantSpecial {
				tx, err := f.txRepo.Get(ctx, id)
				if err != nil {
					t.Fatalf("Get(%s) error = %v", id, err)
				}
				if tx.SpecialID != want {
					t.Fatalf("流水 %s 的 special_id = %q; want %q", id, tx.SpecialID, want)
				}
			}
		})
	}
}

// TestSetSpecialForIDsTouchesUpdatedAt 批量写同样要刷 updated_at，
// 否则「最近改过哪些流水」这类判断会漏掉批量操作。
func TestSetSpecialForIDsTouchesUpdatedAt(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	before, err := f.txRepo.Get(ctx, "d1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := f.txRepo.SetSpecialForIDs(ctx, []string{"d1"}, spReno); err != nil {
		t.Fatalf("SetSpecialForIDs() error = %v", err)
	}
	after, err := f.txRepo.Get(ctx, "d1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at = %v; want 晚于 %v", after.UpdatedAt, before.UpdatedAt)
	}
}

// TestSetSpecialForIDsOnlyTouchesListedRows 没被点名的流水一行都不能动——
// 一条 UPDATE ... IN (...) 写错 WHERE 就是整表覆盖，这是最贵的错法。
func TestSetSpecialForIDsOnlyTouchesListedRows(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	if _, err := f.txRepo.SetSpecialForIDs(ctx, []string{"d1"}, spCar); err != nil {
		t.Fatalf("SetSpecialForIDs() error = %v", err)
	}
	for _, row := range fixtureRows() {
		if row.id == "d1" {
			continue
		}
		tx, err := f.txRepo.Get(ctx, row.id)
		if err != nil {
			t.Fatalf("Get(%s) error = %v", row.id, err)
		}
		if tx.SpecialID != row.special {
			t.Fatalf("未点名的流水 %s 被改了：special_id = %q; want %q", row.id, tx.SpecialID, row.special)
		}
	}
}

// TestSetSpecialForIDsReflectedInAggregates 批量归入后，聚合口径立刻跟着变：
// 被挂上专项的金额从日常口径挪到专项口径，daily + special == all 仍成立。
func TestSetSpecialForIDsReflectedInAggregates(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()

	sum := func(scope domain.Scope) int64 {
		t.Helper()
		var total int64
		for _, b := range sumBucketsScope(t, f.txRepo, f.buckets, domain.DirectionExpense, domain.AccountFamily, scope) {
			total += b.Amount
		}
		return total
	}
	dailyBefore, specialBefore, allBefore := sum(domain.ScopeDaily), sum(domain.ScopeSpecial), sum(domain.ScopeAll)

	// d2 是 2000 分的日常支出，批量挂到装修上
	if _, err := f.txRepo.SetSpecialForIDs(ctx, []string{"d2"}, spReno); err != nil {
		t.Fatalf("SetSpecialForIDs() error = %v", err)
	}
	dailyAfter, specialAfter, allAfter := sum(domain.ScopeDaily), sum(domain.ScopeSpecial), sum(domain.ScopeAll)

	if dailyAfter != dailyBefore-2000 {
		t.Fatalf("日常合计 = %d; want %d（d2 应从日常挪走）", dailyAfter, dailyBefore-2000)
	}
	if specialAfter != specialBefore+2000 {
		t.Fatalf("专项合计 = %d; want %d（d2 应挪进专项）", specialAfter, specialBefore+2000)
	}
	if allAfter != allBefore {
		t.Fatalf("全口径合计 = %d; want %d（归类只是换口径，不改总额）", allAfter, allBefore)
	}
}
