package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type AssetSnapshotRepo struct {
	db *sql.DB
}

func NewAssetSnapshotRepo(db *sql.DB) *AssetSnapshotRepo {
	return &AssetSnapshotRepo{db: db}
}

// Upsert 按 period 唯一键覆盖写入
func (r *AssetSnapshotRepo) Upsert(ctx context.Context, snap *domain.AssetSnapshot) error {
	dataJSON, err := json.Marshal(snap.Data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO asset_snapshots (id, period, snapshot_date, data, net_worth, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(period) DO UPDATE SET
  snapshot_date = excluded.snapshot_date,
  data          = excluded.data,
  net_worth     = excluded.net_worth`,
		snap.ID, snap.Period, snap.SnapshotDate, string(dataJSON), snap.NetWorth, snap.CreatedAt)
	return err
}

// GetByPeriod 未找到时返回 port.ErrNotFound
func (r *AssetSnapshotRepo) GetByPeriod(ctx context.Context, period string) (*domain.AssetSnapshot, error) {
	row := r.db.QueryRowContext(ctx, selectSnapshotSQL+" WHERE period = ?", period)
	snap, err := scanSnapshot(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, port.ErrNotFound
		}
		return nil, err
	}
	return &snap, nil
}

// ListByPeriodAsc 返回最近 limit 个快照，按 period 升序排列（供曲线绘制）
func (r *AssetSnapshotRepo) ListByPeriodAsc(ctx context.Context, limit int) ([]domain.AssetSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, selectSnapshotSQL+`
ORDER BY period DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanSnapshots(rows)
	if err != nil {
		return nil, err
	}
	// 反转为升序
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ListAll 全量快照，按 period 升序；供 /export 使用
func (r *AssetSnapshotRepo) ListAll(ctx context.Context) ([]domain.AssetSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, selectSnapshotSQL+`
ORDER BY period ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSnapshots(rows)
}

const selectSnapshotSQL = `
SELECT id, period, snapshot_date, data, net_worth, created_at
FROM asset_snapshots`

func scanSnapshot(s scanner) (domain.AssetSnapshot, error) {
	var snap domain.AssetSnapshot
	var dataJSON string
	if err := s.Scan(&snap.ID, &snap.Period, &snap.SnapshotDate, &dataJSON, &snap.NetWorth, &snap.CreatedAt); err != nil {
		return domain.AssetSnapshot{}, err
	}
	data := map[string]int64{}
	if dataJSON != "" {
		if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
			return domain.AssetSnapshot{}, fmt.Errorf("unmarshal data: %w", err)
		}
	}
	snap.Data = data
	return snap, nil
}

func scanSnapshots(rows *sql.Rows) ([]domain.AssetSnapshot, error) {
	var out []domain.AssetSnapshot
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}
