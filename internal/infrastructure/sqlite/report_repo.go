package sqlite

import (
	"context"
	"database/sql"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type ReportRepo struct {
	db *sql.DB
}

func NewReportRepo(db *sql.DB) *ReportRepo {
	return &ReportRepo{db: db}
}

// Upsert 按 (period, period_type) 唯一键覆盖写入
func (r *ReportRepo) Upsert(ctx context.Context, rep *domain.AIReport) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO reports
  (id, period, period_type, generated_at, income_data, expense_data, kpi_data, comparison,
   data_scope, ai_prompt, ai_analysis, ai_model, status, is_frozen, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(period, period_type) DO UPDATE SET
  id           = excluded.id,
  generated_at = excluded.generated_at,
  income_data  = excluded.income_data,
  expense_data = excluded.expense_data,
  kpi_data     = excluded.kpi_data,
  comparison   = excluded.comparison,
  data_scope   = excluded.data_scope,
  ai_prompt    = excluded.ai_prompt,
  ai_analysis  = excluded.ai_analysis,
  ai_model     = excluded.ai_model,
  status       = excluded.status,
  is_frozen    = excluded.is_frozen`,
		rep.ID, rep.Period, string(rep.PeriodType), rep.GeneratedAt,
		rep.IncomeData, rep.ExpenseData, rep.KPIData, rep.Comparison, string(storedScope(rep.DataScope)),
		rep.AIPrompt, rep.AIAnalysis, rep.AIModel, rep.Status, boolInt(rep.IsFrozen), rep.CreatedAt)
	return err
}

// storedScope 存档口径的归一化：空值/未知值一律当作全口径。
//
// 这里刻意不用 domain.ParseScope——那个是给查询入参用的，空串退回 daily（默认视图要干净）；
// 存档反过来：015 之前落库的行没有这一列，而它们恰恰是全口径生成的，退回 daily
// 就等于把全口径数字标成「日常」，正是这个字段要解决的问题。
func storedScope(s domain.Scope) domain.Scope {
	switch s {
	case domain.ScopeDaily:
		return domain.ScopeDaily
	case domain.ScopeSpecial:
		return domain.ScopeSpecial
	default:
		return domain.ScopeAll
	}
}

// GetByPeriod 未找到时返回 port.ErrNotFound
func (r *ReportRepo) GetByPeriod(ctx context.Context, period string, periodType domain.PeriodType) (*domain.AIReport, error) {
	row := r.db.QueryRowContext(ctx, selectReportSQL+" WHERE period = ? AND period_type = ?", period, string(periodType))
	rep, err := scanReport(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, port.ErrNotFound
		}
		return nil, err
	}
	return &rep, nil
}

// ListAll 全量历史财报，按 generated_at desc
func (r *ReportRepo) ListAll(ctx context.Context) ([]domain.AIReport, error) {
	rows, err := r.db.QueryContext(ctx, selectReportSQL+`
ORDER BY generated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AIReport
	for rows.Next() {
		rep, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

const selectReportSQL = `
SELECT id, period, period_type, generated_at, COALESCE(income_data,''), COALESCE(expense_data,''),
       COALESCE(kpi_data,''), COALESCE(comparison,''), COALESCE(data_scope,''), COALESCE(ai_prompt,''),
       COALESCE(ai_analysis,''), COALESCE(ai_model,''), status, is_frozen, created_at
FROM reports`

func scanReport(s scanner) (domain.AIReport, error) {
	var rep domain.AIReport
	var periodType string
	var dataScope string
	var isFrozen int
	if err := s.Scan(&rep.ID, &rep.Period, &periodType, &rep.GeneratedAt, &rep.IncomeData, &rep.ExpenseData,
		&rep.KPIData, &rep.Comparison, &dataScope, &rep.AIPrompt, &rep.AIAnalysis, &rep.AIModel, &rep.Status,
		&isFrozen, &rep.CreatedAt); err != nil {
		return domain.AIReport{}, err
	}
	rep.PeriodType = domain.PeriodType(periodType)
	// 空值防御：手工写库或将来某次迁移留下的空 data_scope 一律按全口径读
	rep.DataScope = storedScope(domain.Scope(dataScope))
	rep.IsFrozen = isFrozen == 1
	return rep, nil
}
