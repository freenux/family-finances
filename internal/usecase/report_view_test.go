package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
)

var reportNow = time.Date(2025, 8, 15, 12, 0, 0, 0, time.Local) // 默认季度 = 2025Q2

func newReportViewUC(repo *fakeTransactionRepo) *ReportView {
	src := NewQueryReport(repo, &fakeCategoryRepo{cats: testCategories()})
	return NewReportView(src, PeriodNav{Now: func() time.Time { return reportNow }})
}

func agg(id string, amt int64) domain.CategoryAggregation {
	return domain.CategoryAggregation{CategoryID: id, Amount: amt}
}

// 现金流三行必须满足 daily + special == all（收入、支出、结余逐列），
// 这是 CLAUDE.md 的红线：/cashflow 的拆分与 ContextPack 都吃这一条。
func TestReportViewCashflowInvariant(t *testing.T) {
	tests := []struct {
		name    string
		daily   []domain.CategoryAggregation
		special []domain.CategoryAggregation
	}{
		{"无专项", []domain.CategoryAggregation{agg("income.salary.husband", 300000), agg("expense.fixed.housing", 80000)}, nil},
		{"有专项支出", []domain.CategoryAggregation{agg("income.salary.husband", 300000), agg("expense.discretion.shopping", 20000)},
			[]domain.CategoryAggregation{agg("expense.fixed.housing", 150000)}},
		{"专项里还有收入（旧车折价）", []domain.CategoryAggregation{agg("income.salary.husband", 300000), agg("expense.fixed.housing", 40000)},
			[]domain.CategoryAggregation{agg("income.salary.husband", 80000), agg("expense.fixed.housing", 250000)}},
		{"全是专项", nil, []domain.CategoryAggregation{agg("expense.fixed.housing", 500000)}},
		{"空周期", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeTransactionRepo{
				periodAgg:  map[string][]domain.CategoryAggregation{"2025Q2": tt.daily},
				specialAgg: map[string][]domain.CategoryAggregation{"2025Q2": tt.special},
			}
			dto, err := newReportViewUC(repo).Execute(context.Background(), "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(dto.Cashflow) != 3 || dto.Cashflow[0].Key != "daily" || dto.Cashflow[1].Key != "special" || dto.Cashflow[2].Key != "all" {
				t.Fatalf("cashflow = %+v; want 固定三行 daily/special/all", dto.Cashflow)
			}
			d, s, a := dto.Cashflow[0], dto.Cashflow[1], dto.Cashflow[2]
			cols := []struct {
				name           string
				daily, special int64
				all            int64
			}{
				{"收入", d.IncomeFen, s.IncomeFen, a.IncomeFen},
				{"支出", d.ExpenseFen, s.ExpenseFen, a.ExpenseFen},
				{"结余", d.SurplusFen, s.SurplusFen, a.SurplusFen},
			}
			for _, c := range cols {
				if c.daily+c.special != c.all {
					t.Errorf("%s：daily(%d) + special(%d) = %d; want all = %d", c.name, c.daily, c.special, c.daily+c.special, c.all)
				}
			}
			for _, r := range dto.Cashflow {
				if r.SurplusFen != r.IncomeFen-r.ExpenseFen {
					t.Errorf("%s 行结余 = %d; want 收入−支出 = %d", r.Key, r.SurplusFen, r.IncomeFen-r.ExpenseFen)
				}
			}
			// 全口径必须等于 QueryReport 的 KPI（不是这里另算一遍）
			if a.ExpenseFen != dto.KPI.TotalExpenseFen || a.SurplusFen != dto.KPI.SurplusFen {
				t.Errorf("all 行 (%d, %d) 与 KPI (%d, %d) 对不上", a.ExpenseFen, a.SurplusFen, dto.KPI.TotalExpenseFen, dto.KPI.SurplusFen)
			}
		})
	}
}

// 告警在日常占比严格大于 35% 时翻转；分母是日常支出而不是全口径。
// 每个用例都带一笔大额专项：若误用全口径做分母，占比会被稀释到阈值以下、告警静默关掉。
func TestReportViewDiscretionWarning(t *testing.T) {
	const dailyTotal = 100000 // 日常支出 1000 元
	tests := []struct {
		name         string
		discretion   int64 // 日常自由裁量（分）
		specialSpend int64
		wantWarn     bool
		wantRatioTxt string
	}{
		{"恰为 35% 不告警（严格大于）", 35000, 0, false, "35.0%"},
		{"35.001% 刚越线就告警", 35001, 0, true, "35.0%"},
		{"30% 不告警", 30000, 0, false, "30.0%"},
		{"装修季：日常 50% 仍告警，专项 15 万不得稀释", 50000, 15000000, true, "50.0%"},
		{"装修季：日常 30% 仍不告警（专项不会凭空制造告警）", 30000, 15000000, false, "30.0%"},
		{"装修季：日常 36% 告警", 36000, 15000000, true, "36.0%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			daily := []domain.CategoryAggregation{
				agg("expense.discretion.shopping", tt.discretion),
				agg("expense.fixed.housing", dailyTotal-tt.discretion),
			}
			var special []domain.CategoryAggregation
			if tt.specialSpend > 0 {
				special = []domain.CategoryAggregation{agg("expense.fixed.housing", tt.specialSpend)}
			}
			repo := &fakeTransactionRepo{
				periodAgg:  map[string][]domain.CategoryAggregation{"2025Q2": daily},
				specialAgg: map[string][]domain.CategoryAggregation{"2025Q2": special},
			}
			dto, err := newReportViewUC(repo).Execute(context.Background(), "quarterly", "2025Q2", "family")
			if err != nil {
				t.Fatal(err)
			}
			k := dto.KPI
			if k.DiscretionWarning != tt.wantWarn {
				t.Errorf("discretion_warning = %v; want %v（ratio=%v，分母必须是日常支出 %d 而不是全口径 %d）",
					k.DiscretionWarning, tt.wantWarn, k.DiscretionRatio, dailyTotal, dailyTotal+tt.specialSpend)
			}
			if k.DiscretionRatioText != tt.wantRatioTxt {
				t.Errorf("discretion_ratio_text = %q; want %q", k.DiscretionRatioText, tt.wantRatioTxt)
			}
			wantRatio := float64(tt.discretion) / dailyTotal
			if diff := k.DiscretionRatio - wantRatio; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("discretion_ratio = %v; want %v", k.DiscretionRatio, wantRatio)
			}
			// 说明文案必须与布尔值一致，客户端只展示不判断
			if tt.wantWarn != strings.Contains(k.DiscretionNote, "超过") {
				t.Errorf("discretion_note = %q 与 warning=%v 不一致", k.DiscretionNote, tt.wantWarn)
			}
			if !strings.Contains(k.DiscretionNote, "35.0%") {
				t.Errorf("discretion_note = %q; want 带阈值 35.0%%", k.DiscretionNote)
			}
		})
	}
}

func TestReportViewPeriodAndAccount(t *testing.T) {
	uc := newReportViewUC(&fakeTransactionRepo{})
	tests := []struct {
		name, typ, period, account string
		wantKey, wantType          string
		wantAcc                    string
		wantErr                    bool
	}{
		{"缺省 = 上一个完整季度（财报不是月度）", "", "", "", "2025Q2", "quarterly", "family", false},
		{"年度缺省 = 去年", "annual", "", "", "2024", "annual", "family", false},
		{"显式季度 + 账户", "quarterly", "2025Q1", "wife", "2025Q1", "quarterly", "wife", false},
		{"type 与 period 对不上 → 退回该 type 的默认周期", "annual", "2025Q1", "", "2024", "annual", "family", false},
		{"非法账户 → family", "", "", "bogus", "2025Q2", "quarterly", "family", false},
		{"period 非法 → ErrInvalidPeriod", "quarterly", "xx", "", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dto, err := uc.Execute(context.Background(), tt.typ, tt.period, tt.account)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidPeriod) {
					t.Fatalf("err = %v; want ErrInvalidPeriod", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if dto.Period.Key != tt.wantKey || dto.Period.Type != tt.wantType {
				t.Errorf("period = %s/%s; want %s/%s", dto.Period.Type, dto.Period.Key, tt.wantType, tt.wantKey)
			}
			if dto.Account != tt.wantAcc {
				t.Errorf("account = %q; want %q", dto.Account, tt.wantAcc)
			}
			if dto.Period.Prev == "" || dto.Period.Next == "" || dto.Period.Label == "" {
				t.Errorf("period 未内嵌 prev/next/label：%+v", dto.Period)
			}
		})
	}
}

func TestReportViewAmountPairs(t *testing.T) {
	repo := &fakeTransactionRepo{
		periodAgg: map[string][]domain.CategoryAggregation{"2025Q2": {
			agg("income.salary.husband", 123456789), agg("expense.fixed.housing", 50)}},
	}
	dto, err := newReportViewUC(repo).Execute(context.Background(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if dto.KPI.TotalIncomeFen != 123456789 || dto.KPI.TotalIncomeText != "1,234,567.89" {
		t.Errorf("total_income = %d / %q; want 123456789 / %q（fen 与 text 成对，text 走 FormatYuan）",
			dto.KPI.TotalIncomeFen, dto.KPI.TotalIncomeText, "1,234,567.89")
	}
	if dto.KPI.TotalExpenseText != "0.50" {
		t.Errorf("total_expense_text = %q; want 0.50", dto.KPI.TotalExpenseText)
	}
}
