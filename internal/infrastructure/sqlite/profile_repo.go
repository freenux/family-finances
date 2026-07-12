package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ProfileRepo 家庭画像 + 财务目标/保单只读查询
type ProfileRepo struct {
	db *sql.DB
}

func NewProfileRepo(db *sql.DB) *ProfileRepo {
	return &ProfileRepo{db: db}
}

// Get 读取单行画像；未填写过返回 port.ErrNotFound
func (r *ProfileRepo) Get(ctx context.Context) (*domain.FamilyProfile, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, family_structure, main_age, income_stability, annual_income_fen,
       mortgage_monthly_fen, monthly_expense_fen, emergency_months, risk_appetite, horizon, updated_at
FROM family_profile WHERE id = 'default'`)
	var p domain.FamilyProfile
	var monthlyExpense sql.NullInt64
	if err := row.Scan(&p.ID, &p.FamilyStructure, &p.MainAge, &p.IncomeStability, &p.AnnualIncomeFen,
		&p.MortgageMonthlyFen, &monthlyExpense, &p.EmergencyMonths, &p.RiskAppetite, &p.Horizon, &p.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, port.ErrNotFound
		}
		return nil, err
	}
	if monthlyExpense.Valid {
		v := monthlyExpense.Int64
		p.MonthlyExpenseFen = &v
	}
	return &p, nil
}

// Upsert 覆盖写入单行（id 固定 'default'）
func (r *ProfileRepo) Upsert(ctx context.Context, p *domain.FamilyProfile) error {
	var monthlyExpense any
	if p.MonthlyExpenseFen != nil {
		monthlyExpense = *p.MonthlyExpenseFen
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO family_profile
  (id, family_structure, main_age, income_stability, annual_income_fen,
   mortgage_monthly_fen, monthly_expense_fen, emergency_months, risk_appetite, horizon, updated_at)
VALUES ('default', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  family_structure     = excluded.family_structure,
  main_age             = excluded.main_age,
  income_stability     = excluded.income_stability,
  annual_income_fen    = excluded.annual_income_fen,
  mortgage_monthly_fen = excluded.mortgage_monthly_fen,
  monthly_expense_fen  = excluded.monthly_expense_fen,
  emergency_months     = excluded.emergency_months,
  risk_appetite        = excluded.risk_appetite,
  horizon              = excluded.horizon,
  updated_at           = excluded.updated_at`,
		p.FamilyStructure, p.MainAge, p.IncomeStability, p.AnnualIncomeFen,
		p.MortgageMonthlyFen, monthlyExpense, p.EmergencyMonths, p.RiskAppetite, p.Horizon, p.UpdatedAt)
	return err
}

// ListAllGoals 全量财务目标
func (r *ProfileRepo) ListAllGoals(ctx context.Context) ([]domain.FinancialGoal, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, category, description, target_amount, COALESCE(years_to_achieve,''), priority,
       expected_return, annual_saving, COALESCE(notes,''), sort_order, updated_at,
       COALESCE(target_ym,''), current_amount_fen, COALESCE(linked_codes,'')
FROM financial_goals ORDER BY sort_order, updated_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FinancialGoal
	for rows.Next() {
		var g domain.FinancialGoal
		var annualSaving sql.NullInt64
		var linkedJSON string
		if err := rows.Scan(&g.ID, &g.Category, &g.Description, &g.TargetAmount, &g.YearsToAchieve,
			&g.Priority, &g.ExpectedReturn, &annualSaving, &g.Notes, &g.SortOrder, &g.UpdatedAt,
			&g.TargetYM, &g.CurrentAmountFen, &linkedJSON); err != nil {
			return nil, err
		}
		if annualSaving.Valid {
			v := annualSaving.Int64
			g.AnnualSaving = &v
		}
		if linkedJSON != "" {
			if err := json.Unmarshal([]byte(linkedJSON), &g.LinkedCodes); err != nil {
				return nil, fmt.Errorf("unmarshal linked_codes: %w", err)
			}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpsertGoal 新建/更新财务目标
func (r *ProfileRepo) UpsertGoal(ctx context.Context, g *domain.FinancialGoal) error {
	var linked any
	if len(g.LinkedCodes) > 0 {
		b, err := json.Marshal(g.LinkedCodes)
		if err != nil {
			return fmt.Errorf("marshal linked_codes: %w", err)
		}
		linked = string(b)
	}
	var annualSaving any
	if g.AnnualSaving != nil {
		annualSaving = *g.AnnualSaving
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO financial_goals
  (id, category, description, target_amount, years_to_achieve, priority, expected_return,
   annual_saving, notes, sort_order, updated_at, target_ym, current_amount_fen, linked_codes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  category = excluded.category, description = excluded.description,
  target_amount = excluded.target_amount, years_to_achieve = excluded.years_to_achieve,
  priority = excluded.priority, expected_return = excluded.expected_return,
  annual_saving = excluded.annual_saving, notes = excluded.notes,
  sort_order = excluded.sort_order, updated_at = excluded.updated_at,
  target_ym = excluded.target_ym, current_amount_fen = excluded.current_amount_fen,
  linked_codes = excluded.linked_codes`,
		g.ID, g.Category, g.Description, g.TargetAmount, nullableString(g.YearsToAchieve),
		g.Priority, g.ExpectedReturn, annualSaving, g.Notes, g.SortOrder, g.UpdatedAt,
		nullableString(g.TargetYM), g.CurrentAmountFen, linked)
	return err
}

// DeleteGoal 删除财务目标
func (r *ProfileRepo) DeleteGoal(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM financial_goals WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return port.ErrNotFound
	}
	return nil
}

// ListAllPolicies 全量保单
func (r *ProfileRepo) ListAllPolicies(ctx context.Context) ([]domain.InsurancePolicy, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, insured_person, insurance_type, COALESCE(company_product,''), COALESCE(coverage,''),
       COALESCE(coverage_amount,0), COALESCE(annual_premium,0), renewal_date, COALESCE(notes,''), sort_order, updated_at
FROM insurance_policies ORDER BY sort_order, updated_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.InsurancePolicy
	for rows.Next() {
		var p domain.InsurancePolicy
		var renewal sql.NullTime
		if err := rows.Scan(&p.ID, &p.InsuredPerson, &p.InsuranceType, &p.CompanyProduct, &p.Coverage,
			&p.CoverageAmount, &p.AnnualPremium, &renewal, &p.Notes, &p.SortOrder, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if renewal.Valid {
			p.RenewalDate = renewal.Time
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
