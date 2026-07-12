package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"family-finances/internal/domain"
)

type BudgetRepo struct {
	db *sql.DB
}

func NewBudgetRepo(db *sql.DB) *BudgetRepo {
	return &BudgetRepo{db: db}
}

func (r *BudgetRepo) ListBudgets(ctx context.Context) ([]domain.Budget, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, category_id, quarter_amount, updated_at FROM budgets ORDER BY category_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Budget
	for rows.Next() {
		var b domain.Budget
		if err := rows.Scan(&b.ID, &b.CategoryID, &b.QuarterAmount, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ReplaceBudgets 整表覆盖（事务内）：>0 upsert，未出现或 =0 删除
func (r *BudgetRepo) ReplaceBudgets(ctx context.Context, amounts map[string]int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM budgets`); err != nil {
		return err
	}
	now := time.Now()
	for catID, amount := range amounts {
		if amount <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO budgets (id, category_id, quarter_amount, updated_at) VALUES (?, ?, ?, ?)`,
			uuid.NewString(), catID, amount, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
