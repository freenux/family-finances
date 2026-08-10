package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type SpecialProjectRepo struct {
	db *sql.DB
}

func NewSpecialProjectRepo(db *sql.DB) *SpecialProjectRepo {
	return &SpecialProjectRepo{db: db}
}

const selectSpecialSQL = `
SELECT id, name, started_on, ended_on, budget_fen, COALESCE(note,''), created_at
FROM special_projects`

// ListAll 进行中的排前面，其次按开始日期倒序
func (r *SpecialProjectRepo) ListAll(ctx context.Context) ([]domain.SpecialProject, error) {
	rows, err := r.db.QueryContext(ctx, selectSpecialSQL+`
ORDER BY ended_on IS NOT NULL, started_on DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SpecialProject
	for rows.Next() {
		p, err := scanSpecial(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *SpecialProjectRepo) Get(ctx context.Context, id string) (domain.SpecialProject, error) {
	p, err := scanSpecial(r.db.QueryRowContext(ctx, selectSpecialSQL+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SpecialProject{}, port.ErrNotFound
	}
	return p, err
}

func (r *SpecialProjectRepo) Upsert(ctx context.Context, p *domain.SpecialProject) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO special_projects (id, name, started_on, ended_on, budget_fen, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  started_on = excluded.started_on,
  ended_on = excluded.ended_on,
  budget_fen = excluded.budget_fen,
  note = excluded.note`,
		p.ID, p.Name, nullIfZeroTime(p.StartedOn), nullIfZeroTime(p.EndedOn),
		p.BudgetFen, p.Note, p.CreatedAt)
	return err
}

// Delete 删除专项；已挂在该专项上的流水通过 special_id 置空归回日常
func (r *SpecialProjectRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE transactions SET special_id = NULL, updated_at = ? WHERE special_id = ?`,
		time.Now(), id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM special_projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return port.ErrNotFound
	}
	return tx.Commit()
}

func (r *SpecialProjectRepo) SumByProject(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT special_id, COALESCE(SUM(amount),0)
FROM transactions
WHERE special_id IS NOT NULL AND status = 'confirmed'
GROUP BY special_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSumByProject(rows)
}

func (r *SpecialProjectRepo) SumByProjectInPeriod(ctx context.Context, p domain.Period, account domain.Account) (map[string]int64, error) {
	q := `
SELECT special_id, COALESCE(SUM(amount),0)
FROM transactions
WHERE special_id IS NOT NULL AND status = 'confirmed'
  AND occurred_at >= ? AND occurred_at < ?`
	args := []any{p.Start, p.End}
	if account.IsStorageAccount() {
		q += " AND account = ?"
		args = append(args, string(account))
	}
	q += " GROUP BY special_id"
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSumByProject(rows)
}

func scanSumByProject(rows *sql.Rows) (map[string]int64, error) {
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var amount int64
		if err := rows.Scan(&id, &amount); err != nil {
			return nil, err
		}
		out[id] = amount
	}
	return out, rows.Err()
}

// SumByCategoryForProject 专项内部的跨科目构成（一个装修会横跨多个科目）
func (r *SpecialProjectRepo) SumByCategoryForProject(ctx context.Context, projectID string) ([]domain.CategoryAggregation, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT COALESCE(t.category_id,''), COALESCE(c.name,'未分类'), COALESCE(c.parent_id,''), COALESCE(SUM(t.amount),0) AS total
FROM transactions t
LEFT JOIN categories c ON c.id = t.category_id
WHERE t.special_id = ? AND t.status = 'confirmed'
GROUP BY t.category_id, c.name, c.parent_id
HAVING total != 0
ORDER BY total DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CategoryAggregation
	for rows.Next() {
		var a domain.CategoryAggregation
		if err := rows.Scan(&a.CategoryID, &a.Name, &a.ParentID, &a.Amount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanSpecial(s scanner) (domain.SpecialProject, error) {
	var p domain.SpecialProject
	var startedOn, endedOn sql.NullTime
	if err := s.Scan(&p.ID, &p.Name, &startedOn, &endedOn, &p.BudgetFen, &p.Note, &p.CreatedAt); err != nil {
		return domain.SpecialProject{}, err
	}
	if startedOn.Valid {
		p.StartedOn = startedOn.Time
	}
	if endedOn.Valid {
		p.EndedOn = endedOn.Time
	}
	return p, nil
}

var _ port.SpecialProjectRepo = (*SpecialProjectRepo)(nil)
