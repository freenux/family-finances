package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// AssetSnapshotService 资产快照录入与查询（/assets 页面用例）
type AssetSnapshotService struct {
	repo port.AssetSnapshotRepo
	now  func() time.Time
}

func NewAssetSnapshotService(repo port.AssetSnapshotRepo) *AssetSnapshotService {
	return &AssetSnapshotService{repo: repo, now: time.Now}
}

// ValidateAssetData 校验 data 里的每个 code 都在 AssetCatalog 白名单内
func ValidateAssetData(data map[string]int64) error {
	set := domain.AssetCodeSet()
	for code := range data {
		if !set[code] {
			return fmt.Errorf("未知资产科目: %s", code)
		}
	}
	return nil
}

// ComputeNetWorth 净资产 = 资产合计 − 负债合计（未在 data 中出现的科目按 0 计）
func ComputeNetWorth(data map[string]int64) int64 {
	var net int64
	for _, acc := range domain.AssetCatalog {
		v := data[acc.Code]
		if acc.Group == "asset" {
			net += v
		} else {
			net -= v
		}
	}
	return net
}

// SaveSnapshot 校验 period（仅季度）与 code 白名单 → 计算 NetWorth → Upsert
func (s *AssetSnapshotService) SaveSnapshot(ctx context.Context, periodLabel string, data map[string]int64) (domain.AssetSnapshot, error) {
	p, err := domain.ParsePeriod(periodLabel)
	if err != nil {
		return domain.AssetSnapshot{}, fmt.Errorf("非法的期间: %w", err)
	}
	if p.Type != domain.PeriodQuarterly {
		return domain.AssetSnapshot{}, fmt.Errorf("资产快照仅支持季度期间")
	}
	if err := ValidateAssetData(data); err != nil {
		return domain.AssetSnapshot{}, err
	}

	id := uuid.NewString()
	if existing, err := s.repo.GetByPeriod(ctx, p.Label); err == nil && existing != nil {
		id = existing.ID
	} else if err != nil && err != port.ErrNotFound {
		return domain.AssetSnapshot{}, err
	}

	snap := domain.AssetSnapshot{
		ID:           id,
		Period:       p.Label,
		SnapshotDate: s.now(),
		Data:         data,
		NetWorth:     ComputeNetWorth(data),
		CreatedAt:    s.now(),
	}
	if err := s.repo.Upsert(ctx, &snap); err != nil {
		return domain.AssetSnapshot{}, err
	}
	return snap, nil
}

// NetWorthPoint 净值曲线上的一个点
type NetWorthPoint struct {
	Period   string `json:"period"`
	NetWorth int64  `json:"net_worth"`
}

// SnapshotView 页面展示所需的数据：当季快照（可为空）、上季快照（供"从上季复制"与环比）、
// 近 curveLimit 季净值序列
type SnapshotView struct {
	Period   domain.Period
	Current  *domain.AssetSnapshot
	Previous *domain.AssetSnapshot
	Curve    []NetWorthPoint
}

const defaultCurveQuarters = 12

// SnapshotView 返回期间 periodLabel 的快照视图
func (s *AssetSnapshotService) SnapshotView(ctx context.Context, periodLabel string) (SnapshotView, error) {
	p, err := domain.ParsePeriod(periodLabel)
	if err != nil {
		return SnapshotView{}, fmt.Errorf("非法的期间: %w", err)
	}
	if p.Type != domain.PeriodQuarterly {
		return SnapshotView{}, fmt.Errorf("资产快照仅支持季度期间")
	}

	cur, err := s.getOrNil(ctx, p.Label)
	if err != nil {
		return SnapshotView{}, err
	}
	prev, err := s.getOrNil(ctx, p.Previous().Label)
	if err != nil {
		return SnapshotView{}, err
	}
	snaps, err := s.repo.ListByPeriodAsc(ctx, defaultCurveQuarters)
	if err != nil {
		return SnapshotView{}, err
	}
	curve := make([]NetWorthPoint, 0, len(snaps))
	for _, sn := range snaps {
		curve = append(curve, NetWorthPoint{Period: sn.Period, NetWorth: sn.NetWorth})
	}

	return SnapshotView{Period: p, Current: cur, Previous: prev, Curve: curve}, nil
}

// GetData 返回某季度快照的科目余额（code -> 分）；found=false 表示该季度尚无快照。
// 供"从上季复制"等只需要原始数据、不需要完整校验的场景使用。
func (s *AssetSnapshotService) GetData(ctx context.Context, periodLabel string) (data map[string]int64, found bool, err error) {
	snap, err := s.getOrNil(ctx, periodLabel)
	if err != nil {
		return nil, false, err
	}
	if snap == nil {
		return nil, false, nil
	}
	return snap.Data, true, nil
}

func (s *AssetSnapshotService) getOrNil(ctx context.Context, period string) (*domain.AssetSnapshot, error) {
	snap, err := s.repo.GetByPeriod(ctx, period)
	if err != nil {
		if err == port.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return snap, nil
}
