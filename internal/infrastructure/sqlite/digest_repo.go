package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type DigestRepo struct {
	db *sql.DB
}

func NewDigestRepo(db *sql.DB) *DigestRepo {
	return &DigestRepo{db: db}
}

func (r *DigestRepo) GetSettings(ctx context.Context) (*domain.DigestSettings, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, enabled, cadence, channel, target, modules, last_sent_at
FROM digest_settings WHERE id = 'default'`)
	var s domain.DigestSettings
	var enabled int
	var modulesJSON string
	var lastSent sql.NullTime
	if err := row.Scan(&s.ID, &enabled, &s.Cadence, &s.Channel, &s.Target, &modulesJSON, &lastSent); err != nil {
		if err == sql.ErrNoRows {
			return nil, port.ErrNotFound
		}
		return nil, err
	}
	s.Enabled = enabled == 1
	if lastSent.Valid {
		s.LastSentAt = lastSent.Time
	}
	if modulesJSON != "" {
		if err := json.Unmarshal([]byte(modulesJSON), &s.Modules); err != nil {
			return nil, fmt.Errorf("unmarshal modules: %w", err)
		}
	}
	return &s, nil
}

func (r *DigestRepo) UpsertSettings(ctx context.Context, s *domain.DigestSettings) error {
	modulesJSON, err := json.Marshal(s.Modules)
	if err != nil {
		return fmt.Errorf("marshal modules: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO digest_settings (id, enabled, cadence, channel, target, modules, last_sent_at)
VALUES ('default', ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  enabled = excluded.enabled, cadence = excluded.cadence, channel = excluded.channel,
  target = excluded.target, modules = excluded.modules`,
		boolInt(s.Enabled), s.Cadence, s.Channel, s.Target, string(modulesJSON), nullIfZeroTime(s.LastSentAt))
	return err
}

func (r *DigestRepo) MarkSent(ctx context.Context, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE digest_settings SET last_sent_at = ? WHERE id = 'default'`, at)
	return err
}
