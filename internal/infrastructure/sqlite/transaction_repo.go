package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type TransactionRepo struct {
	db *sql.DB
}

func NewTransactionRepo(db *sql.DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) Insert(ctx context.Context, tx domain.Transaction) error {
	_, err := r.db.ExecContext(ctx, insertTxSQL,
		tx.ID, string(tx.Source), nullIfEmpty(tx.ImportBatchID), tx.OccurredAt,
		tx.Counterparty, tx.Description, tx.Note, tx.Amount,
		string(tx.Direction), string(tx.Status), nullIfEmpty(tx.CategoryID),
		tx.RawRow, tx.CreatedAt, tx.UpdatedAt,
	)
	return err
}

const insertTxSQL = `
INSERT INTO transactions
  (id, source, import_batch_id, occurred_at, counterparty, description, note,
   amount, direction, status, category_id, raw_row, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// InsertBatch 把一批候选行写入，逐行检查 imported_transaction_keys 去重，同一事务内提交。
func (r *TransactionRepo) InsertBatch(ctx context.Context, batch domain.ImportBatch, rows []port.ImportRow) (port.ImportResult, error) {
	res := port.ImportResult{}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	insertKey, err := tx.PrepareContext(ctx, `
INSERT INTO imported_transaction_keys (source, transaction_no) VALUES (?, ?)`)
	if err != nil {
		return res, err
	}
	defer insertKey.Close()

	insertTx, err := tx.PrepareContext(ctx, insertTxSQL)
	if err != nil {
		return res, err
	}
	defer insertTx.Close()

	checkKey, err := tx.PrepareContext(ctx, `
SELECT 1 FROM imported_transaction_keys WHERE source = ? AND transaction_no = ?`)
	if err != nil {
		return res, err
	}
	defer checkKey.Close()

	for _, row := range rows {
		if row.TransactionNo == "" {
			res.SkippedInvalid++
			continue
		}
		var one int
		err := checkKey.QueryRowContext(ctx, string(row.Tx.Source), row.TransactionNo).Scan(&one)
		if err == nil {
			res.SkippedDuplicates++
			continue
		}
		if err != sql.ErrNoRows {
			return res, err
		}

		t := row.Tx
		if _, err := insertTx.ExecContext(ctx,
			t.ID, string(t.Source), nullIfEmpty(t.ImportBatchID), t.OccurredAt,
			t.Counterparty, t.Description, t.Note, t.Amount,
			string(t.Direction), string(t.Status), nullIfEmpty(t.CategoryID),
			t.RawRow, t.CreatedAt, t.UpdatedAt,
		); err != nil {
			return res, fmt.Errorf("insert tx: %w", err)
		}
		if _, err := insertKey.ExecContext(ctx, string(row.Tx.Source), row.TransactionNo); err != nil {
			return res, fmt.Errorf("insert key: %w", err)
		}
		res.InsertedRows++
	}

	// 写 import_batches
	_, err = tx.ExecContext(ctx, `
INSERT INTO import_batches
  (id, source, filename, period_start, period_end, total_rows, imported_rows, skipped_rows, pending_rows, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		batch.ID, string(batch.Source), batch.Filename,
		nullIfZeroTime(batch.PeriodStart), nullIfZeroTime(batch.PeriodEnd),
		batch.TotalRows, res.InsertedRows, res.SkippedDuplicates+res.SkippedInvalid, res.PendingCategory,
		batch.CreatedAt,
	)
	if err != nil {
		return res, fmt.Errorf("insert batch: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return res, err
	}
	return res, nil
}

func (r *TransactionRepo) Update(ctx context.Context, id string, patch port.TransactionUpdate) error {
	sets := []string{}
	args := []any{}
	if patch.CategoryID != nil {
		if *patch.CategoryID == "" {
			sets = append(sets, "category_id = NULL")
		} else {
			sets = append(sets, "category_id = ?")
			args = append(args, *patch.CategoryID)
		}
	}
	if patch.Note != nil {
		sets = append(sets, "note = ?")
		args = append(args, *patch.Note)
	}
	if patch.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, string(*patch.Status))
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now())
	args = append(args, id)
	q := "UPDATE transactions SET " + join(sets, ", ") + " WHERE id = ?"
	_, err := r.db.ExecContext(ctx, q, args...)
	return err
}

func (r *TransactionRepo) Get(ctx context.Context, id string) (domain.Transaction, error) {
	row := r.db.QueryRowContext(ctx, selectTxSQL+" WHERE id = ?", id)
	return scanTx(row)
}

func (r *TransactionRepo) List(ctx context.Context, p domain.Period) ([]domain.Transaction, error) {
	rows, err := r.db.QueryContext(ctx, selectTxSQL+`
WHERE occurred_at >= ? AND occurred_at < ?
ORDER BY occurred_at DESC, id DESC`,
		p.Start, p.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxs(rows)
}

func (r *TransactionRepo) ListPendingCategory(ctx context.Context, limit int) ([]domain.Transaction, error) {
	rows, err := r.db.QueryContext(ctx, selectTxSQL+`
WHERE category_id IS NULL AND status = 'pending_review'
ORDER BY occurred_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxs(rows)
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

const selectTxSQL = `
SELECT id, source, COALESCE(import_batch_id,''), occurred_at, COALESCE(counterparty,''),
       COALESCE(description,''), COALESCE(note,''), amount, direction, status, COALESCE(category_id,''),
       COALESCE(raw_row,''), created_at, updated_at
FROM transactions`

type scanner interface {
	Scan(dest ...any) error
}

func scanTx(s scanner) (domain.Transaction, error) {
	var t domain.Transaction
	var src, dir, st string
	if err := s.Scan(&t.ID, &src, &t.ImportBatchID, &t.OccurredAt, &t.Counterparty,
		&t.Description, &t.Note, &t.Amount, &dir, &st, &t.CategoryID, &t.RawRow,
		&t.CreatedAt, &t.UpdatedAt); err != nil {
		return domain.Transaction{}, err
	}
	t.Source = domain.Source(src)
	t.Direction = domain.Direction(dir)
	t.Status = domain.TxStatus(st)
	return t, nil
}

func scanTxs(rows *sql.Rows) ([]domain.Transaction, error) {
	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIfZeroTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func join(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for _, s := range ss[1:] {
		out += sep + s
	}
	return out
}
