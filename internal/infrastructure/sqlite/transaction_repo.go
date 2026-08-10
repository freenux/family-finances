package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
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
		tx.ID, string(tx.Source), string(tx.Account), nullIfEmpty(tx.ImportBatchID), tx.OccurredAt,
		tx.Counterparty, tx.Description, tx.PlatformCategory, tx.Note, tx.Amount,
		string(tx.Direction), string(tx.Status), nullIfEmpty(tx.CategoryID),
		tx.RawRow, tx.CreatedAt, tx.UpdatedAt, tx.Member, nullIfEmpty(tx.SpecialID),
	)
	return err
}

const insertTxSQL = `
INSERT INTO transactions
  (id, source, account, import_batch_id, occurred_at, counterparty, description, platform_category, note,
   amount, direction, status, category_id, raw_row, created_at, updated_at, member, special_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// InsertBatch 把一批候选行写入，逐行检查 imported_transaction_keys 去重，同一事务内提交。
// 去重键是 (source, account, transaction_no)。
func (r *TransactionRepo) InsertBatch(ctx context.Context, batch domain.ImportBatch, rows []port.ImportRow) (port.ImportResult, error) {
	res := port.ImportResult{}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	insertKey, err := tx.PrepareContext(ctx, `
INSERT INTO imported_transaction_keys (source, account, transaction_no) VALUES (?, ?, ?)`)
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
SELECT 1 FROM imported_transaction_keys WHERE source = ? AND account = ? AND transaction_no = ?`)
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
		err := checkKey.QueryRowContext(ctx, string(row.Tx.Source), string(row.Tx.Account), row.TransactionNo).Scan(&one)
		if err == nil {
			res.SkippedDuplicates++
			continue
		}
		if err != sql.ErrNoRows {
			return res, err
		}

		t := row.Tx
		if _, err := insertTx.ExecContext(ctx,
			t.ID, string(t.Source), string(t.Account), nullIfEmpty(t.ImportBatchID), t.OccurredAt,
			t.Counterparty, t.Description, t.PlatformCategory, t.Note, t.Amount,
			string(t.Direction), string(t.Status), nullIfEmpty(t.CategoryID),
			t.RawRow, t.CreatedAt, t.UpdatedAt, t.Member, nullIfEmpty(t.SpecialID),
		); err != nil {
			return res, fmt.Errorf("insert tx: %w", err)
		}
		if _, err := insertKey.ExecContext(ctx, string(row.Tx.Source), string(row.Tx.Account), row.TransactionNo); err != nil {
			return res, fmt.Errorf("insert key: %w", err)
		}
		res.InsertedRows++
	}

	// 写 import_batches
	_, err = tx.ExecContext(ctx, `
INSERT INTO import_batches
  (id, source, account, filename, period_start, period_end, total_rows, imported_rows, skipped_rows, pending_rows, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		batch.ID, string(batch.Source), string(batch.Account), batch.Filename,
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
	if patch.Account != nil {
		sets = append(sets, "account = ?")
		args = append(args, string(*patch.Account))
	}
	if patch.Member != nil {
		sets = append(sets, "member = ?")
		args = append(args, *patch.Member)
	}
	if patch.SpecialID != nil {
		if *patch.SpecialID == "" {
			sets = append(sets, "special_id = NULL")
		} else {
			sets = append(sets, "special_id = ?")
			args = append(args, *patch.SpecialID)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now())
	args = append(args, id)
	q := "UPDATE transactions SET " + join(sets, ", ") + " WHERE id = ?"
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return port.ErrNotFound
	}
	return nil
}

func (r *TransactionRepo) Get(ctx context.Context, id string) (domain.Transaction, error) {
	row := r.db.QueryRowContext(ctx, selectTxSQL+" WHERE id = ?", id)
	return scanTx(row)
}

func (r *TransactionRepo) List(ctx context.Context, p domain.Period, account domain.Account) ([]domain.Transaction, error) {
	q := selectTxSQL + `
WHERE occurred_at >= ? AND occurred_at < ?`
	args := []any{p.Start, p.End}
	if account.IsStorageAccount() {
		q += " AND account = ?"
		args = append(args, string(account))
	}
	q += `
ORDER BY occurred_at DESC, id DESC`
	rows, err := r.db.QueryContext(ctx, q, args...)
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

func (r *TransactionRepo) AggregateByCategory(ctx context.Context, p domain.Period, account domain.Account, scope domain.Scope) ([]domain.CategoryAggregation, error) {
	q := `
SELECT c.id, c.name, COALESCE(c.parent_id,''), COALESCE(SUM(t.amount),0) AS total
FROM categories c
LEFT JOIN transactions t
  ON t.category_id = c.id
  AND t.occurred_at >= ? AND t.occurred_at < ?
  AND t.status = 'confirmed'`
	args := []any{p.Start, p.End}
	if account.IsStorageAccount() {
		q += " AND t.account = ?"
		args = append(args, string(account))
	}
	// 口径过滤必须留在 ON 里：挪到 WHERE 会把无匹配流水的科目整行滤掉
	q += scope.SQLFilter("t.special_id")
	q += `
WHERE c.level = 2
GROUP BY c.id, c.name, c.parent_id
ORDER BY c.sort_order`
	rows, err := r.db.QueryContext(ctx, q, args...)
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

// SumByBuckets 单次范围扫描覆盖全部桶（调用方传入的桶按时间升序、互不重叠），
// 在 Go 侧按桶边界归集，避免每桶一条 SQL（月+季对比一次要 26 个桶）。
func (r *TransactionRepo) SumByBuckets(ctx context.Context, buckets []port.PeriodBucket, direction domain.Direction, account domain.Account, scope domain.Scope) ([]port.PeriodBucket, error) {
	out := make([]port.PeriodBucket, len(buckets))
	for i, b := range buckets {
		out[i] = port.PeriodBucket{Label: b.Label, Start: b.Start, End: b.End}
	}
	if len(buckets) == 0 {
		return out, nil
	}
	q := `
SELECT occurred_at, amount
FROM transactions
WHERE occurred_at >= ? AND occurred_at < ?
  AND direction = ?
  AND status = 'confirmed'`
	args := []any{out[0].Start, out[len(out)-1].End, string(direction)}
	if account.IsStorageAccount() {
		q += " AND account = ?"
		args = append(args, string(account))
	}
	q += scope.SQLFilter("special_id")
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var at time.Time
		var amount int64
		if err := rows.Scan(&at, &amount); err != nil {
			return nil, err
		}
		// 桶升序 → 二分定位第一个 End > at 的桶
		i := sort.Search(len(out), func(i int) bool { return at.Before(out[i].End) })
		if i < len(out) && !at.Before(out[i].Start) {
			out[i].Amount += amount
		}
	}
	return out, rows.Err()
}

func (r *TransactionRepo) TopTransactions(ctx context.Context, p domain.Period, direction domain.Direction, account domain.Account, scope domain.Scope, limit int) ([]port.TopTransaction, error) {
	q := `
SELECT t.id, t.occurred_at, COALESCE(t.counterparty,''), COALESCE(t.description,''), COALESCE(t.note,''),
       t.amount, t.direction, t.account, COALESCE(t.category_id,''), COALESCE(c.name,''),
       COALESCE(t.special_id,''), COALESCE(sp.name,'')
FROM transactions t
LEFT JOIN categories c ON c.id = t.category_id
LEFT JOIN special_projects sp ON sp.id = t.special_id
WHERE t.occurred_at >= ? AND t.occurred_at < ?
  AND t.direction = ?
  AND t.status = 'confirmed'`
	args := []any{p.Start, p.End, string(direction)}
	if account.IsStorageAccount() {
		q += " AND t.account = ?"
		args = append(args, string(account))
	}
	q += scope.SQLFilter("t.special_id")
	q += `
ORDER BY t.amount DESC, t.occurred_at DESC
LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []port.TopTransaction
	for rows.Next() {
		var t port.TopTransaction
		var dir, acc string
		if err := rows.Scan(&t.ID, &t.OccurredAt, &t.Counterparty, &t.Description, &t.Note,
			&t.Amount, &dir, &acc, &t.CategoryID, &t.CategoryName,
			&t.SpecialID, &t.SpecialName); err != nil {
			return nil, err
		}
		t.Direction = domain.Direction(dir)
		t.Account = domain.Account(acc)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListAll 全量流水，按 occurred_at 升序；供 /export 使用
func (r *TransactionRepo) ListAll(ctx context.Context) ([]domain.Transaction, error) {
	rows, err := r.db.QueryContext(ctx, selectTxSQL+`
ORDER BY occurred_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxs(rows)
}

// ListAllImportBatches 全量导入批次，按 created_at 升序；供 /export 使用
func (r *TransactionRepo) ListAllImportBatches(ctx context.Context) ([]domain.ImportBatch, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, source, account, COALESCE(filename,''), period_start, period_end,
       total_rows, imported_rows, skipped_rows, pending_rows, created_at
FROM import_batches
ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ImportBatch
	for rows.Next() {
		var b domain.ImportBatch
		var src, acc string
		var periodStart, periodEnd sql.NullTime
		if err := rows.Scan(&b.ID, &src, &acc, &b.Filename, &periodStart, &periodEnd,
			&b.TotalRows, &b.ImportedRows, &b.SkippedRows, &b.PendingRows, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Source = domain.Source(src)
		b.Account = domain.Account(acc)
		if periodStart.Valid {
			b.PeriodStart = periodStart.Time
		}
		if periodEnd.Valid {
			b.PeriodEnd = periodEnd.Time
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

const selectTxSQL = `
SELECT id, source, account, COALESCE(import_batch_id,''), occurred_at, COALESCE(counterparty,''),
       COALESCE(description,''), COALESCE(platform_category,''), COALESCE(note,''), amount, direction, status, COALESCE(category_id,''),
       COALESCE(raw_row,''), created_at, updated_at, COALESCE(member,''), COALESCE(special_id,'')
FROM transactions`

type scanner interface {
	Scan(dest ...any) error
}

func scanTx(s scanner) (domain.Transaction, error) {
	var t domain.Transaction
	var src, acc, dir, st string
	if err := s.Scan(&t.ID, &src, &acc, &t.ImportBatchID, &t.OccurredAt, &t.Counterparty,
		&t.Description, &t.PlatformCategory, &t.Note, &t.Amount, &dir, &st, &t.CategoryID, &t.RawRow,
		&t.CreatedAt, &t.UpdatedAt, &t.Member, &t.SpecialID); err != nil {
		return domain.Transaction{}, err
	}
	t.Source = domain.Source(src)
	t.Account = domain.Account(acc)
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

// ListForRecurring 周期识别专用：SQL 侧过滤 + 只取必要列，避免 12 个月全列（含 raw_row）扫描
func (r *TransactionRepo) ListForRecurring(ctx context.Context, from, to time.Time, scope domain.Scope) ([]domain.Transaction, error) {
	q := `
SELECT occurred_at, COALESCE(counterparty,''), COALESCE(description,''), amount
FROM transactions
WHERE occurred_at >= ? AND occurred_at < ?
  AND direction = 'expense' AND status = 'confirmed'` +
		scope.SQLFilter("special_id") + `
ORDER BY occurred_at`
	rows, err := r.db.QueryContext(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Transaction
	for rows.Next() {
		t := domain.Transaction{Direction: domain.DirectionExpense, Status: domain.TxStatusConfirmed}
		if err := rows.Scan(&t.OccurredAt, &t.Counterparty, &t.Description, &t.Amount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListMembers 已出现过的成员标注（去重、非空、按字典序）
func (r *TransactionRepo) ListMembers(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT DISTINCT member FROM transactions WHERE member != '' ORDER BY member`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
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
	return strings.Join(ss, sep)
}
