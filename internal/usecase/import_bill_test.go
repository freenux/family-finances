package usecase

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

func TestImportBillImportsSkipRulesAsExcludedButSkipsIncome(t *testing.T) {
	txRepo := &captureTransactionRepo{}
	ruleRepo := &stubRuleRepo{rules: []domain.CategoryRule{
		{
			Pattern:     "转账",
			PatternType: "exact",
			Field:       "platform_category",
			IsActive:    true,
		},
	}}
	triggered := false
	uc := NewImportBill(txRepo, ruleRepo).WithTrigger(func() { triggered = true })

	res, err := uc.Execute(context.Background(), ImportBillInput{
		Source:   domain.SourceAlipay,
		Account:  domain.AccountHusband,
		Filename: "alipay.csv",
		Reader:   gb18030Reader(minimalAlipayCSV()),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if res.TotalRows != 3 || res.InsertedRows != 2 || res.SkippedInvalid != 1 || res.PendingCategory != 1 {
		t.Fatalf("Execute() result = %+v; want total=3 inserted=2 skippedInvalid=1 pending=1", res)
	}
	if got := res.EarliestOccurredAt.Format("2006-01"); got != "2026-05" {
		t.Fatalf("EarliestOccurredAt month = %s; want 2026-05", got)
	}
	if !triggered {
		t.Fatal("Execute() did not trigger pending classifier")
	}
	if len(txRepo.rows) != 2 {
		t.Fatalf("captured %d rows; want 2", len(txRepo.rows))
	}
	if got := txRepo.rows[0].Tx.Status; got != domain.TxStatusExcluded {
		t.Fatalf("skip-rule row status = %s; want excluded", got)
	}
	if got := txRepo.rows[1].Tx.Status; got != domain.TxStatusPendingReview {
		t.Fatalf("uncategorized expense row status = %s; want pending_review", got)
	}
	for _, row := range txRepo.rows {
		if row.Tx.Direction == domain.DirectionIncome {
			t.Fatalf("income row was imported: %+v", row.Tx)
		}
	}
}

func minimalAlipayCSV() string {
	return strings.Join([]string{
		"交易时间,交易分类,交易对方,对方账号,商品说明,收/支,金额,收/付款方式,交易状态,交易订单号,商家订单号,备注",
		"2026-05-01 10:00:00,转账,张三,,转账给张三,支出,100.00,余额,交易成功,expense-skip-1,,",
		"2026-05-02 10:00:00,工资,公司,,工资,收入,200.00,余额,交易成功,income-1,,",
		"2026-05-03 10:00:00,餐饮,饭店,,午餐,支出,30.00,余额,交易成功,expense-pending-1,,",
	}, "\n")
}

func gb18030Reader(s string) io.Reader {
	encoded, _, err := transform.String(simplifiedchinese.GB18030.NewEncoder(), s)
	if err != nil {
		panic(err)
	}
	return strings.NewReader(encoded)
}

type captureTransactionRepo struct {
	rows []port.ImportRow
}

func (r *captureTransactionRepo) Insert(context.Context, domain.Transaction) error {
	return nil
}

func (r *captureTransactionRepo) InsertBatch(_ context.Context, _ domain.ImportBatch, rows []port.ImportRow) (port.ImportResult, error) {
	r.rows = append([]port.ImportRow(nil), rows...)
	return port.ImportResult{InsertedRows: len(rows)}, nil
}

func (r *captureTransactionRepo) Update(context.Context, string, port.TransactionUpdate) error {
	return nil
}

func (r *captureTransactionRepo) Get(context.Context, string) (domain.Transaction, error) {
	return domain.Transaction{}, nil
}

func (r *captureTransactionRepo) List(context.Context, domain.Period, domain.Account) ([]domain.Transaction, error) {
	return nil, nil
}

func (r *captureTransactionRepo) ListPendingCategory(context.Context, int) ([]domain.Transaction, error) {
	return nil, nil
}

func (r *captureTransactionRepo) AggregateByCategory(context.Context, domain.Period, domain.Account, domain.Scope) ([]domain.CategoryAggregation, error) {
	return nil, nil
}

func (r *captureTransactionRepo) SumByBuckets(context.Context, []port.PeriodBucket, domain.Direction, domain.Account, domain.Scope) ([]port.PeriodBucket, error) {
	return nil, nil
}

func (r *captureTransactionRepo) TopTransactions(context.Context, domain.Period, domain.Direction, domain.Account, domain.Scope, int) ([]port.TopTransaction, error) {
	return nil, nil
}

func (r *captureTransactionRepo) ListAll(context.Context) ([]domain.Transaction, error) {
	return nil, nil
}

func (r *captureTransactionRepo) ListAllImportBatches(context.Context) ([]domain.ImportBatch, error) {
	return nil, nil
}

func (r *captureTransactionRepo) ListMembers(context.Context) ([]string, error) {
	return nil, nil
}

func (r *captureTransactionRepo) ListForRecurring(context.Context, time.Time, time.Time, domain.Scope) ([]domain.Transaction, error) {
	return nil, nil
}

type stubRuleRepo struct {
	rules []domain.CategoryRule
}

func (r *stubRuleRepo) ListRules(context.Context) ([]domain.CategoryRule, error) {
	return r.rules, nil
}

func (r *stubRuleRepo) ListActiveRules(context.Context) ([]domain.CategoryRule, error) {
	return r.rules, nil
}

func (r *stubRuleRepo) GetRule(context.Context, string) (domain.CategoryRule, error) {
	return domain.CategoryRule{}, nil
}

func (r *stubRuleRepo) InsertRule(context.Context, domain.CategoryRule) error {
	return nil
}

func (r *stubRuleRepo) UpdateRule(context.Context, domain.CategoryRule) error {
	return nil
}

func (r *stubRuleRepo) SetRuleActive(context.Context, string, bool) error {
	return nil
}

func (r *stubRuleRepo) DeleteRule(context.Context, string) error {
	return nil
}

var _ port.TransactionRepo = (*captureTransactionRepo)(nil)
var _ port.CategoryRuleRepo = (*stubRuleRepo)(nil)
