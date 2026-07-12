package sqlite

import (
	"context"
	"database/sql"

	"family-finances/internal/domain"
)

type ImportTemplateRepo struct {
	db *sql.DB
}

func NewImportTemplateRepo(db *sql.DB) *ImportTemplateRepo {
	return &ImportTemplateRepo{db: db}
}

func (r *ImportTemplateRepo) ListTemplates(ctx context.Context) ([]domain.ImportTemplate, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, name, mapping, created_at FROM import_templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ImportTemplate
	for rows.Next() {
		var t domain.ImportTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.Mapping, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ImportTemplateRepo) SaveTemplate(ctx context.Context, t *domain.ImportTemplate) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO import_templates (id, name, mapping, created_at) VALUES (?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET mapping = excluded.mapping`,
		t.ID, t.Name, t.Mapping, t.CreatedAt)
	return err
}
