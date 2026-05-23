package sqlite

import (
	"context"
	"database/sql"

	"family-finances/internal/domain"
)

type CategoryRepo struct {
	db *sql.DB
}

func NewCategoryRepo(db *sql.DB) *CategoryRepo {
	return &CategoryRepo{db: db}
}

func (r *CategoryRepo) ListAll(ctx context.Context) ([]domain.Category, error) {
	return r.query(ctx, `
        SELECT id, COALESCE(parent_id,''), level, name, COALESCE(group_emoji,''), type, sort_order
        FROM categories ORDER BY sort_order`)
}

func (r *CategoryRepo) ListByType(ctx context.Context, t domain.CategoryType) ([]domain.Category, error) {
	return r.query(ctx, `
        SELECT id, COALESCE(parent_id,''), level, name, COALESCE(group_emoji,''), type, sort_order
        FROM categories WHERE type = ? ORDER BY sort_order`, string(t))
}

func (r *CategoryRepo) query(ctx context.Context, q string, args ...any) ([]domain.Category, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Category
	for rows.Next() {
		var c domain.Category
		var t string
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Level, &c.Name, &c.GroupEmoji, &t, &c.SortOrder); err != nil {
			return nil, err
		}
		c.Type = domain.CategoryType(t)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CategoryRepo) ListRules(ctx context.Context) ([]domain.CategoryRule, error) {
	return r.queryRules(ctx, `
SELECT id, pattern, pattern_type, field, COALESCE(category_id, ''), priority, source, is_active, created_at
FROM category_rules
ORDER BY is_active DESC, priority ASC, created_at DESC`)
}

func (r *CategoryRepo) ListActiveRules(ctx context.Context) ([]domain.CategoryRule, error) {
	return r.queryRules(ctx, `
SELECT id, pattern, pattern_type, field, COALESCE(category_id, ''), priority, source, is_active, created_at
FROM category_rules
WHERE is_active = 1
ORDER BY priority ASC, created_at DESC`)
}

func (r *CategoryRepo) GetRule(ctx context.Context, id string) (domain.CategoryRule, error) {
	rows, err := r.queryRules(ctx, `
SELECT id, pattern, pattern_type, field, COALESCE(category_id, ''), priority, source, is_active, created_at
FROM category_rules
WHERE id = ?`, id)
	if err != nil {
		return domain.CategoryRule{}, err
	}
	if len(rows) == 0 {
		return domain.CategoryRule{}, sql.ErrNoRows
	}
	return rows[0], nil
}

func (r *CategoryRepo) InsertRule(ctx context.Context, rule domain.CategoryRule) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO category_rules (id, pattern, pattern_type, field, category_id, priority, source, is_active, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.Pattern, rule.PatternType, rule.Field, nullableString(rule.CategoryID),
		rule.Priority, rule.Source, boolInt(rule.IsActive), rule.CreatedAt)
	return err
}

func (r *CategoryRepo) UpdateRule(ctx context.Context, rule domain.CategoryRule) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE category_rules
SET pattern = ?, pattern_type = ?, field = ?, category_id = ?, priority = ?
WHERE id = ?`,
		rule.Pattern, rule.PatternType, rule.Field, nullableString(rule.CategoryID), rule.Priority, rule.ID)
	return err
}

func (r *CategoryRepo) SetRuleActive(ctx context.Context, id string, active bool) error {
	_, err := r.db.ExecContext(ctx, `UPDATE category_rules SET is_active = ? WHERE id = ?`, boolInt(active), id)
	return err
}

func (r *CategoryRepo) DeleteRule(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM category_rules WHERE id = ?`, id)
	return err
}

func (r *CategoryRepo) queryRules(ctx context.Context, q string, args ...any) ([]domain.CategoryRule, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CategoryRule
	for rows.Next() {
		var rule domain.CategoryRule
		var active int
		if err := rows.Scan(
			&rule.ID, &rule.Pattern, &rule.PatternType, &rule.Field, &rule.CategoryID,
			&rule.Priority, &rule.Source, &active, &rule.CreatedAt,
		); err != nil {
			return nil, err
		}
		rule.IsActive = active == 1
		out = append(out, rule)
	}
	return out, rows.Err()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
