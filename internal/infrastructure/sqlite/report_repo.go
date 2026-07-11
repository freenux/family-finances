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
   ai_prompt, ai_analysis, ai_model, status, is_frozen, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(period, period_type) DO UPDATE SET
  id           = excluded.id,
  generated_at = excluded.generated_at,
  income_data  = excluded.income_data,
  expense_data = excluded.expense_data,
  kpi_data     = excluded.kpi_data,
  comparison   = excluded.comparison,
  ai_prompt    = excluded.ai_prompt,
  ai_analysis  = excluded.ai_analysis,
  ai_model     = excluded.ai_model,
  status       = excluded.status,
  is_frozen    = excluded.is_frozen`,
		rep.ID, rep.Period, string(rep.PeriodType), rep.GeneratedAt,
		rep.IncomeData, rep.ExpenseData, rep.KPIData, rep.Comparison,
		rep.AIPrompt, rep.AIAnalysis, rep.AIModel, rep.Status, boolInt(rep.IsFrozen), rep.CreatedAt)
	return err
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
       COALESCE(kpi_data,''), COALESCE(comparison,''), COALESCE(ai_prompt,''), COALESCE(ai_analysis,''),
       COALESCE(ai_model,''), status, is_frozen, created_at
FROM reports`

func scanReport(s scanner) (domain.AIReport, error) {
	var rep domain.AIReport
	var periodType string
	var isFrozen int
	if err := s.Scan(&rep.ID, &rep.Period, &periodType, &rep.GeneratedAt, &rep.IncomeData, &rep.ExpenseData,
		&rep.KPIData, &rep.Comparison, &rep.AIPrompt, &rep.AIAnalysis, &rep.AIModel, &rep.Status,
		&isFrozen, &rep.CreatedAt); err != nil {
		return domain.AIReport{}, err
	}
	rep.PeriodType = domain.PeriodType(periodType)
	rep.IsFrozen = isFrozen == 1
	return rep, nil
}
