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

// netAmountSQL 专项已花费一律用「净额」：支出加、收入减。
//
// 挂到专项上的收入就是装修退款、退货返现、卖旧车抵扣换车这类冲抵成本的钱，
// 用户主动把它归到专项上，意思就是"这笔要从项目成本里扣掉"。裸的 SUM(amount)
// 会把退款当成又花了一笔，"忽略收入"则会静默丢弃用户标注的数据——两者都是错的。
// 净额可能为负（退款大于支出），如实返回，不要 clamp 到 0。
//
// 三处求和（SumByProject / SumByProjectInPeriod / SumByCategoryForProject）共用这个
// 表达式，要改就三处一起改，否则同一个专项在不同页面上会对不上账；
// 为此三条 SQL 都把 transactions 别名成 t。
const netAmountSQL = `SUM(CASE WHEN t.direction = 'income' THEN -t.amount ELSE t.amount END)`

// grossSpentSQL / offsetSQL 把净额再拆成"支出 − 冲抵"两半，让页面能把
// 「已花费（净）」这一个数字的来历摊开（¥支出 − ¥冲抵 = ¥净），
// 否则用户看到一个被退款压低的数字无从对账。三者恒满足 net = gross - offset。
const (
	grossSpentSQL = `SUM(CASE WHEN t.direction = 'income' THEN 0 ELSE t.amount END)`
	offsetSQL     = `SUM(CASE WHEN t.direction = 'income' THEN t.amount ELSE 0 END)`
)

// SumByProject 每个专项的历史花费统计（见 netAmountSQL：收入抵扣支出）。
// 毛额/冲抵/条数与净额在同一次 GROUP BY 里一起取回，不额外发 COUNT 查询。
func (r *SpecialProjectRepo) SumByProject(ctx context.Context) (map[string]port.SpecialSpend, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT t.special_id, COALESCE(`+grossSpentSQL+`,0), COALESCE(`+offsetSQL+`,0), COUNT(*)
FROM transactions t
WHERE t.special_id IS NOT NULL AND t.status = 'confirmed'
GROUP BY t.special_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]port.SpecialSpend{}
	for rows.Next() {
		var id string
		var s port.SpecialSpend
		if err := rows.Scan(&id, &s.GrossSpentFen, &s.OffsetFen, &s.TxCount); err != nil {
			return nil, err
		}
		s.NetSpentFen = s.GrossSpentFen - s.OffsetFen
		out[id] = s
	}
	return out, rows.Err()
}

// SumByProjectInPeriod 周期内每个专项的净花费（见 netAmountSQL：收入抵扣支出）
func (r *SpecialProjectRepo) SumByProjectInPeriod(ctx context.Context, p domain.Period, account domain.Account) (map[string]int64, error) {
	q := `
SELECT t.special_id, COALESCE(` + netAmountSQL + `,0)
FROM transactions t
WHERE t.special_id IS NOT NULL AND t.status = 'confirmed'
  AND t.occurred_at >= ? AND t.occurred_at < ?`
	args := []any{p.Start, p.End}
	if account.IsStorageAccount() {
		q += " AND t.account = ?"
		args = append(args, string(account))
	}
	q += " GROUP BY t.special_id"
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

// SumByCategoryForAllProjects 每个专项内部的跨科目构成（一个装修会横跨多个科目）。
// 同样是净额（见 netAmountSQL）：某科目被全额退款后净额为 0，会被 HAVING 滤掉不再列出。
//
// 一次 GROUP BY (special_id, category_id) 取回全部专项，调用方按 id 索引：/specials 页
// 每个专项各查一次是 N+1（实测每多一个专项 +17ms），与本文件 SumByProject 的
// GROUP BY t.special_id + map 是同一套范式。
func (r *SpecialProjectRepo) SumByCategoryForAllProjects(ctx context.Context) (map[string][]domain.CategoryAggregation, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT t.special_id, COALESCE(t.category_id,''), COALESCE(c.name,'未分类'), COALESCE(c.parent_id,''), COALESCE(`+netAmountSQL+`,0) AS total
FROM transactions t
LEFT JOIN categories c ON c.id = t.category_id
WHERE t.special_id IS NOT NULL AND t.status = 'confirmed'
GROUP BY t.special_id, t.category_id, c.name, c.parent_id
HAVING total != 0
ORDER BY t.special_id, total DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]domain.CategoryAggregation{}
	for rows.Next() {
		var projectID string
		var a domain.CategoryAggregation
		if err := rows.Scan(&projectID, &a.CategoryID, &a.Name, &a.ParentID, &a.Amount); err != nil {
			return nil, err
		}
		out[projectID] = append(out[projectID], a)
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
