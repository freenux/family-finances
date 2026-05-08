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
