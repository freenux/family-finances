package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"family-finances/internal/domain"
)

func newTestBuilder(txRepo *fakeTransactionRepo, assetRepo *fakeAssetSnapshotRepo) *ContextPackBuilder {
	catRepo := &fakeCategoryRepo{cats: testCategories()}
	qr := NewQueryReport(txRepo, catRepo)
	return NewContextPackBuilder(qr, assetRepo, txRepo)
}

func TestGenerateReportDisabledLLM(t *testing.T) {
	builder := newTestBuilder(&fakeTransactionRepo{}, &fakeAssetSnapshotRepo{})
	reportRepo := &fakeReportRepo{}
	uc := NewGenerateReport(builder, reportRepo, &fakeLLM{enabled: false}, "test-model")

	if uc.Enabled() {
		t.Fatal("Enabled() = true; want false when llm disabled")
	}
	p, _ := domain.ParsePeriod("2026Q2")
	_, err := uc.Execute(context.Background(), p)
	if !errors.Is(err, ErrLLMDisabled) {
		t.Fatalf("Execute() error = %v; want ErrLLMDisabled", err)
	}
	if len(reportRepo.saved) != 0 {
		t.Fatalf("saved %d reports; want 0", len(reportRepo.saved))
	}
}

func TestGenerateReportDropsInvalidRefs(t *testing.T) {
	txRepo := &fakeTransactionRepo{periodAgg: map[string][]domain.CategoryAggregation{
		"2026Q2": {
			{CategoryID: "income.salary.husband", Amount: 900000},
			{CategoryID: "expense.discretion.shopping", Amount: 200000},
			{CategoryID: "expense.fixed.housing", Amount: 100000},
		},
		"2026Q1": {
			{CategoryID: "income.salary.husband", Amount: 880000},
			{CategoryID: "expense.discretion.shopping", Amount: 150000},
			{CategoryID: "expense.fixed.housing", Amount: 100000},
		},
	}}
	builder := newTestBuilder(txRepo, &fakeAssetSnapshotRepo{})
	reportRepo := &fakeReportRepo{}

	reply := `{"summary":"本期结余良好，购物支出有所上升。",` +
		`"highlights":[{"text":"结余率保持在健康区间","refs":["savings_rate"]}],` +
		`"risks":[{"text":"引用了不存在的键，应被丢弃","refs":["not_a_real_key"]},` +
		`{"text":"没有 refs，应被丢弃","refs":[]}],` +
		`"advice":[{"text":"购物支出连续上升，建议设预算","refs":["expense_top_change_expense.discretion.shopping"]}]}`
	llm := &fakeLLM{enabled: true, reply: reply}
	uc := NewGenerateReport(builder, reportRepo, llm, "test-model")

	p, _ := domain.ParsePeriod("2026Q2")
	rep, err := uc.Execute(context.Background(), p)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if rep.Period != "2026Q2" || rep.Status != "final" || rep.AIModel != "test-model" {
		t.Fatalf("unexpected report meta: %+v", rep)
	}
	// 新报告必须自报口径：包里 income/expense/kpi/compare 都是日常口径，
	// 不标上就会被页面当成口径拆分之前的全口径旧存档来渲染
	if rep.DataScope != domain.ScopeDaily {
		t.Fatalf("DataScope = %q; want %q", rep.DataScope, domain.ScopeDaily)
	}

	var content AIReportContent
	if err := json.Unmarshal([]byte(rep.AIAnalysis), &content); err != nil {
		t.Fatalf("unmarshal ai_analysis: %v", err)
	}
	if len(content.Highlights) != 1 {
		t.Fatalf("Highlights = %+v; want 1 valid item", content.Highlights)
	}
	if len(content.Risks) != 0 {
		t.Fatalf("Risks = %+v; want all dropped (invalid refs)", content.Risks)
	}
	if len(content.Advice) != 1 {
		t.Fatalf("Advice = %+v; want 1 valid item", content.Advice)
	}

	if len(reportRepo.saved) != 1 {
		t.Fatalf("saved %d reports; want 1", len(reportRepo.saved))
	}

	// 同期再生成一次应覆盖（Upsert 语义），不追加新记录
	llm.reply = `{"summary":"第二次生成","highlights":[],"risks":[],"advice":[]}`
	if _, err := uc.Execute(context.Background(), p); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if len(reportRepo.saved) != 2 {
		// fakeReportRepo.Upsert 是 append-only 的假实现，这里只验证两次都成功写入且都能通过 GetByPeriod 取到最新一条
		t.Fatalf("saved %d reports after second generate; want 2 (fake repo appends)", len(reportRepo.saved))
	}
	latest, err := reportRepo.GetByPeriod(context.Background(), "2026Q2", domain.PeriodQuarterly)
	if err != nil {
		t.Fatalf("GetByPeriod() error = %v", err)
	}
	var latestContent AIReportContent
	if err := json.Unmarshal([]byte(latest.AIAnalysis), &latestContent); err != nil {
		t.Fatalf("unmarshal latest ai_analysis: %v", err)
	}
	if latestContent.Summary != "第二次生成" {
		t.Fatalf("GetByPeriod() latest summary = %q; want 第二次生成", latestContent.Summary)
	}
}

func TestGenerateReportRejectsMissingSummary(t *testing.T) {
	txRepo := &fakeTransactionRepo{periodAgg: map[string][]domain.CategoryAggregation{
		"2026Q2": {{CategoryID: "income.salary.husband", Amount: 900000}},
	}}
	builder := newTestBuilder(txRepo, &fakeAssetSnapshotRepo{})
	reportRepo := &fakeReportRepo{}
	llm := &fakeLLM{enabled: true, reply: `{"summary":"","highlights":[],"risks":[],"advice":[]}`}
	uc := NewGenerateReport(builder, reportRepo, llm, "test-model")

	p, _ := domain.ParsePeriod("2026Q2")
	if _, err := uc.Execute(context.Background(), p); err == nil {
		t.Fatal("Execute() error = nil; want error for empty summary")
	}
	if len(reportRepo.saved) != 0 {
		t.Fatalf("saved %d reports; want 0", len(reportRepo.saved))
	}
}

func TestGenerateReportRejectsMalformedReply(t *testing.T) {
	builder := newTestBuilder(&fakeTransactionRepo{}, &fakeAssetSnapshotRepo{})
	reportRepo := &fakeReportRepo{}
	llm := &fakeLLM{enabled: true, reply: `not json`}
	uc := NewGenerateReport(builder, reportRepo, llm, "test-model")

	p, _ := domain.ParsePeriod("2026Q2")
	if _, err := uc.Execute(context.Background(), p); err == nil {
		t.Fatal("Execute() error = nil; want parse error")
	}
}
