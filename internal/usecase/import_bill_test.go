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

// TestImportBillTransferCategories 钉住迁移 016 之后导入的落点：
// 命中往来科目的行（不论收支方向）一律以 excluded 落地并计进 TransferRows；
// 未命中往来科目的收入行仍然不导入。
//
// 为什么方向也要一起测：报销到账/收回借款是收入方向，它们是"垫付出去/借出去"
// 那半边的对手方，不放进来两头就抵不平，一笔垫付会被永远记成支出。
func TestImportBillTransferCategories(t *testing.T) {
	tests := []struct {
		name          string
		wantStatus    domain.TxStatus
		wantCategory  string
		wantDirection domain.Direction
	}{
		{"支出方向的转账落不计收支", domain.TxStatusExcluded, "transfer.internal", domain.DirectionExpense},
		{"收入方向的报销到账也进来且不计收支", domain.TxStatusExcluded, "transfer.reimburse", domain.DirectionIncome},
	}

	txRepo := &captureTransactionRepo{}
	ruleRepo := &stubRuleRepo{rules: []domain.CategoryRule{
		{Pattern: "报销", PatternType: "contains", Field: "any", CategoryID: "transfer.reimburse", Priority: 85, IsActive: true},
		{Pattern: "转账", PatternType: "exact", Field: "platform_category", CategoryID: "transfer.internal", Priority: 90, IsActive: true},
	}}
	uc := NewImportBill(txRepo, ruleRepo)

	res, err := uc.Execute(context.Background(), ImportBillInput{
		Source:   domain.SourceAlipay,
		Account:  domain.AccountHusband,
		Filename: "alipay.csv",
		Reader:   gb18030Reader(transferAlipayCSV()),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// 4 行：转账(支出) / 报销到账(收入) / 工资(收入，非往来，应丢弃) / 午餐(支出，待核对)
	if res.TotalRows != 4 || res.InsertedRows != 3 || res.SkippedInvalid != 1 {
		t.Fatalf("Execute() result = %+v; want total=4 inserted=3 skippedInvalid=1（工资是非往来收入，不导入）", res)
	}
	if res.TransferRows != 2 {
		t.Fatalf("TransferRows = %d; want 2（转账 + 报销到账都以不计收支入库）", res.TransferRows)
	}
	if res.PendingCategory != 1 {
		t.Fatalf("PendingCategory = %d; want 1（只有午餐那条未命中规则）", res.PendingCategory)
	}
	if len(txRepo.rows) != 3 {
		t.Fatalf("captured %d rows; want 3", len(txRepo.rows))
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := txRepo.rows[i].Tx
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %v; want %v —— 往来行落 confirmed 会被 SumByBuckets/TopTransactions/ListForRecurring 当成真支出", got.Status, tt.wantStatus)
			}
			if got.CategoryID != tt.wantCategory {
				t.Errorf("CategoryID = %v; want %v", got.CategoryID, tt.wantCategory)
			}
			if got.Direction != tt.wantDirection {
				t.Errorf("Direction = %v; want %v", got.Direction, tt.wantDirection)
			}
		})
	}

	for _, row := range txRepo.rows {
		if row.Tx.Direction == domain.DirectionIncome && !domain.IsTransferCategory(row.Tx.CategoryID) {
			t.Fatalf("非往来的收入行被导入了: %+v", row.Tx)
		}
	}
}

func transferAlipayCSV() string {
	return strings.Join([]string{
		"交易时间,交易分类,交易对方,对方账号,商品说明,收/支,金额,收/付款方式,交易状态,交易订单号,商家订单号,备注",
		"2026-05-01 10:00:00,转账,张三,,转账给张三,支出,100.00,余额,交易成功,transfer-1,,",
		"2026-05-02 10:00:00,转账,公司财务,,差旅报销,收入,880.00,余额,交易成功,reimburse-1,,",
		"2026-05-03 10:00:00,工资,公司,,工资,收入,20000.00,余额,交易成功,salary-1,,",
		"2026-05-04 10:00:00,餐饮,饭店,,午餐,支出,30.00,余额,交易成功,pending-1,,",
	}, "\n")
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

func (r *captureTransactionRepo) SumByBuckets(context.Context, []port.PeriodBucket, domain.Direction, domain.Account) ([]port.PeriodBucket, []port.PeriodBucket, error) {
	return nil, nil, nil
}

func (r *captureTransactionRepo) SetSpecialForIDs(context.Context, []string, string) (int, error) {
	return 0, nil
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
