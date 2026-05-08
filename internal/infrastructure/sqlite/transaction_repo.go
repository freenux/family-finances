package sqlite

import (
	"context"
	"database/sql"

	"family-finances/internal/domain"
)

type TransactionRepo struct {
	db *sql.DB
}

func NewTransactionRepo(db *sql.DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) Insert(ctx context.Context, tx domain.Transaction) error {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO transactions
          (id, source, import_batch_id, occurred_at, counterparty, description,
           amount, direction, status, category_id, raw_row, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tx.ID, string(tx.Source), nullIfEmpty(tx.ImportBatchID), tx.OccurredAt,
		tx.Counterparty, tx.Description, tx.Amount,
		string(tx.Direction), string(tx.Status), nullIfEmpty(tx.CategoryID),
		tx.RawRow, tx.CreatedAt, tx.UpdatedAt,
	)
	return err
}

func (r *TransactionRepo) List(ctx context.Context, p domain.Period) ([]domain.Transaction, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, source, COALESCE(import_batch_id,''), occurred_at, COALESCE(counterparty,''),
               COALESCE(description,''), amount, direction, status, COALESCE(category_id,''),
               COALESCE(raw_row,''), created_at, updated_at
        FROM transactions
        WHERE occurred_at >= ? AND occurred_at < ?
        ORDER BY occurred_at DESC, id DESC`,
		p.Start, p.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Transaction
	for rows.Next() {
		var t domain.Transaction
		var src, dir, st string
		if err := rows.Scan(&t.ID, &src, &t.ImportBatchID, &t.OccurredAt, &t.Counterparty,
			&t.Description, &t.Amount, &dir, &st, &t.CategoryID, &t.RawRow,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Source = domain.Source(src)
		t.Direction = domain.Direction(dir)
		t.Status = domain.TxStatus(st)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TransactionRepo) AggregateByCategory(ctx context.Context, p domain.Period) ([]domain.CategoryAggregation, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT c.id, c.name, COALESCE(c.parent_id,''), COALESCE(SUM(t.amount),0) AS total
        FROM categories c
        LEFT JOIN transactions t
          ON t.category_id = c.id
          AND t.occurred_at >= ? AND t.occurred_at < ?
          AND t.status = 'confirmed'
        WHERE c.level = 2
        GROUP BY c.id, c.name, c.parent_id
        ORDER BY c.sort_order`,
		p.Start, p.End)
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

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
