package usecase

import (
	"context"
	"testing"
)

func TestComputeNetWorth(t *testing.T) {
	cases := []struct {
		name string
		data map[string]int64
		want int64
	}{
		{"空快照净值为0", map[string]int64{}, 0},
		{
			"资产减负债",
			map[string]int64{
				"asset.cash":         5800000,
				"asset.house":        320000000,
				"liability.mortgage": 158000000,
				"liability.consumer": 2400000,
			},
			5800000 + 320000000 - 158000000 - 2400000,
		},
		{
			"未在目录中出现的科目缺省为0，不影响计算",
			map[string]int64{"asset.cash": 100},
			100,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputeNetWorth(tc.data); got != tc.want {
				t.Fatalf("ComputeNetWorth(%v) = %d; want %d", tc.data, got, tc.want)
			}
		})
	}
}

func TestValidateAssetData(t *testing.T) {
	cases := []struct {
		name    string
		data    map[string]int64
		wantErr bool
	}{
		{"合法 code", map[string]int64{"asset.cash": 100, "liability.mortgage": 200}, false},
		{"空 data 合法", map[string]int64{}, false},
		{"非法 code 报错", map[string]int64{"asset.crypto": 100}, true},
		{"拼写错误报错", map[string]int64{"asset.Cash": 100}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAssetData(tc.data)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateAssetData(%v) error = %v; wantErr %v", tc.data, err, tc.wantErr)
			}
		})
	}
}

func TestSaveSnapshotRejectsNonQuarterPeriod(t *testing.T) {
	svc := NewAssetSnapshotService(&fakeAssetSnapshotRepo{})
	cases := []string{"2026", "2026-05"}
	for _, label := range cases {
		if _, err := svc.SaveSnapshot(context.Background(), label, map[string]int64{"asset.cash": 100}); err == nil {
			t.Fatalf("SaveSnapshot(%q) error = nil; want error for non-quarter period", label)
		}
	}
}

func TestSaveSnapshotRejectsInvalidCode(t *testing.T) {
	svc := NewAssetSnapshotService(&fakeAssetSnapshotRepo{})
	if _, err := svc.SaveSnapshot(context.Background(), "2026Q2", map[string]int64{"asset.crypto": 100}); err == nil {
		t.Fatal("SaveSnapshot() error = nil; want error for unknown code")
	}
}

func TestSaveSnapshotUpsertIsIdempotent(t *testing.T) {
	repo := &fakeAssetSnapshotRepo{}
	svc := NewAssetSnapshotService(repo)

	first, err := svc.SaveSnapshot(context.Background(), "2026Q2", map[string]int64{"asset.cash": 100000})
	if err != nil {
		t.Fatalf("SaveSnapshot() first error = %v", err)
	}
	if first.NetWorth != 100000 {
		t.Fatalf("NetWorth = %d; want 100000", first.NetWorth)
	}

	second, err := svc.SaveSnapshot(context.Background(), "2026Q2", map[string]int64{"asset.cash": 200000})
	if err != nil {
		t.Fatalf("SaveSnapshot() second error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("ID changed across同期重复保存: %q vs %q; want same id (覆盖语义)", first.ID, second.ID)
	}
	if second.NetWorth != 200000 {
		t.Fatalf("NetWorth after overwrite = %d; want 200000", second.NetWorth)
	}
	if len(repo.byPeriod) != 1 {
		t.Fatalf("stored periods = %d; want 1 (同季重复保存应覆盖而非新增)", len(repo.byPeriod))
	}
}

func TestSnapshotViewComparesCurrentAndPrevious(t *testing.T) {
	repo := &fakeAssetSnapshotRepo{}
	svc := NewAssetSnapshotService(repo)
	ctx := context.Background()

	if _, err := svc.SaveSnapshot(ctx, "2026Q1", map[string]int64{"asset.cash": 100000}); err != nil {
		t.Fatalf("save Q1: %v", err)
	}
	if _, err := svc.SaveSnapshot(ctx, "2026Q2", map[string]int64{"asset.cash": 150000}); err != nil {
		t.Fatalf("save Q2: %v", err)
	}

	view, err := svc.SnapshotView(ctx, "2026Q2")
	if err != nil {
		t.Fatalf("SnapshotView() error = %v", err)
	}
	if view.Current == nil || view.Current.NetWorth != 150000 {
		t.Fatalf("Current = %+v; want net worth 150000", view.Current)
	}
	if view.Previous == nil || view.Previous.NetWorth != 100000 {
		t.Fatalf("Previous = %+v; want net worth 100000", view.Previous)
	}
}

func TestSnapshotViewNoDataYieldsNilCurrentNoError(t *testing.T) {
	svc := NewAssetSnapshotService(&fakeAssetSnapshotRepo{})
	view, err := svc.SnapshotView(context.Background(), "2026Q2")
	if err != nil {
		t.Fatalf("SnapshotView() error = %v", err)
	}
	if view.Current != nil || view.Previous != nil {
		t.Fatalf("view = %+v; want both nil when no snapshots saved", view)
	}
}
