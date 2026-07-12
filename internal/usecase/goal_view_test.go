package usecase

import (
	"context"
	"testing"
	"time"

	"family-finances/internal/domain"
)

func TestGoalViewOverload(t *testing.T) {
	cases := []struct {
		name           string
		goals          []domain.FinancialGoal
		bucketAmts     map[string]int64 // 桶 label → 金额；q0..q3
		wantOverloaded bool
		wantMonthly    int64
	}{
		{
			name: "月供合计超过月均结余 → 过载",
			goals: []domain.FinancialGoal{
				{ID: "g1", TargetAmount: 12000000, TargetYM: "2027-07"}, // 12 个月内存 12 万 → 月供 1 万
			},
			// 收入桶为 0，支出桶为 0 → 月均结余 0 → 过载
			bucketAmts:     map[string]int64{},
			wantOverloaded: true,
			wantMonthly:    1000000,
		},
		{
			name:           "无目标不过载",
			goals:          nil,
			bucketAmts:     map[string]int64{},
			wantOverloaded: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			txRepo := &fakeTransactionRepo{bucketAmts: tc.bucketAmts}
			profileRepo := &fakeProfileRepo{goals: tc.goals}
			uc := NewGoalView(profileRepo, &fakeAssetSnapshotRepo{}, txRepo)
			uc.now = func() time.Time { return time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local) }

			data, err := uc.Load(context.Background())
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if data.Overloaded != tc.wantOverloaded {
				t.Fatalf("Overloaded = %v; want %v (%+v)", data.Overloaded, tc.wantOverloaded, data)
			}
			if data.TotalMonthlyFen != tc.wantMonthly {
				t.Fatalf("TotalMonthlyFen = %d; want %d", data.TotalMonthlyFen, tc.wantMonthly)
			}
		})
	}
}

func TestGoalViewLinkedCurrentFromLatestSnapshot(t *testing.T) {
	assetRepo := &fakeAssetSnapshotRepo{}
	_ = assetRepo.Upsert(context.Background(), &domain.AssetSnapshot{
		Period: "2026Q2", Data: map[string]int64{"asset.deposit": 8000000},
	})
	profileRepo := &fakeProfileRepo{goals: []domain.FinancialGoal{
		{ID: "g1", TargetAmount: 10000000, CurrentAmountFen: 1, LinkedCodes: []string{"asset.deposit"}},
	}}
	uc := NewGoalView(profileRepo, assetRepo, &fakeTransactionRepo{})
	data, err := uc.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(data.Goals) != 1 || !data.Goals[0].Progress.FromLinked || data.Goals[0].Progress.CurrentFen != 8000000 {
		t.Fatalf("goals = %+v; want linked current 8000000", data.Goals)
	}
}

func TestGoalViewFindingsIntegratesIntoContextPack(t *testing.T) {
	// 集成测试：goal_overload finding 经 AddFindingSource 进入 ContextPack.Findings 与 refs 白名单
	txRepo := &fakeTransactionRepo{}
	assetRepo := &fakeAssetSnapshotRepo{}
	profileRepo := &fakeProfileRepo{goals: []domain.FinancialGoal{
		{ID: "g1", TargetAmount: 12000000, TargetYM: "2027-07"},
	}}
	gv := NewGoalView(profileRepo, assetRepo, txRepo)
	gv.now = func() time.Time { return time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local) }

	builder := newTestBuilder(txRepo, assetRepo).AddFindingSource(gv.Findings)
	p, _ := domain.ParsePeriod("2026Q2")
	pack, err := builder.Build(context.Background(), p)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !pack.RefWhitelist()["goal_overload"] {
		t.Fatalf("findings = %+v; want goal_overload in whitelist", pack.Findings)
	}
}
