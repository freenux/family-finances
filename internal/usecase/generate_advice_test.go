package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"family-finances/internal/domain"
)

func newTestAdviceUC(llm *fakeLLM, reportRepo *fakeReportRepo) *GenerateAdvice {
	txRepo := &fakeTransactionRepo{}
	assetRepo := &fakeAssetSnapshotRepo{}
	_ = assetRepo.Upsert(context.Background(), &domain.AssetSnapshot{
		Period: "2026Q2",
		Data: map[string]int64{
			"asset.cash": 4000000, "asset.equity_cn": 3600000, "asset.equity_global": 2400000,
		},
		NetWorth: 10000000,
	})
	profileRepo := &fakeProfileRepo{profile: &domain.FamilyProfile{
		ID: "default", MainAge: 35, RiskAppetite: domain.RiskBalanced,
		Horizon: domain.HorizonMedium, IncomeStability: domain.IncomeMedium,
		AnnualIncomeFen: 40000000, EmergencyMonths: 6,
	}}
	builder := newTestBuilder(txRepo, assetRepo)
	engine := NewBucketEngine(assetRepo, profileRepo, profileRepo, profileRepo, txRepo)
	return NewGenerateAdvice(builder, engine, reportRepo, llm, "test-model")
}

func TestGenerateAdviceDisabledLLM(t *testing.T) {
	reportRepo := &fakeReportRepo{}
	uc := newTestAdviceUC(&fakeLLM{enabled: false}, reportRepo)
	if uc.Enabled() {
		t.Fatal("Enabled() = true; want false")
	}
	if _, err := uc.Execute(context.Background()); !errors.Is(err, ErrLLMDisabled) {
		t.Fatalf("Execute() error = %v; want ErrLLMDisabled", err)
	}
	if len(reportRepo.saved) != 0 {
		t.Fatalf("saved %d; want 0", len(reportRepo.saved))
	}
}

func TestGenerateAdviceValidatesRefsAndCost(t *testing.T) {
	reply := `{"summary":"权益与海外占比均偏出目标区间，建议分批调整。",` +
		`"explanations":[` +
		`{"text":"权益占比 60% 超出 C3 目标区间上限","refs":["band_equity_share","assume_grade"]},` +
		`{"text":"引用不存在的键，应被丢弃","refs":["made_up_key"]}],` +
		`"actions":[` +
		`{"text":"将部分海外权益转入货基，分 3 个月执行","cost":"可能错过海外市场上涨；产生交易费用","refs":["band_overseas_share"]},` +
		`{"text":"没有代价说明，应被丢弃","cost":"","refs":["band_equity_share"]},` +
		`{"text":"refs 违规，应被丢弃","cost":"某种代价","refs":["nonexistent"]}]}`
	reportRepo := &fakeReportRepo{}
	uc := newTestAdviceUC(&fakeLLM{enabled: true, reply: reply}, reportRepo)

	rep, err := uc.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if rep.PeriodType != PeriodTypeAdvice {
		t.Fatalf("PeriodType = %s; want advice", rep.PeriodType)
	}
	if rep.Period == "" {
		t.Fatal("Period empty; want current quarter label")
	}

	var content AdviceContent
	if err := json.Unmarshal([]byte(rep.AIAnalysis), &content); err != nil {
		t.Fatalf("unmarshal ai_analysis: %v", err)
	}
	if len(content.Explanations) != 1 {
		t.Fatalf("Explanations = %+v; want 1 valid", content.Explanations)
	}
	if len(content.Actions) != 1 || content.Actions[0].Cost == "" {
		t.Fatalf("Actions = %+v; want 1 valid with cost", content.Actions)
	}

	// 系统计算部分随建议落库
	var sys AdviceSystemData
	if err := json.Unmarshal([]byte(rep.KPIData), &sys); err != nil {
		t.Fatalf("unmarshal kpi_data: %v", err)
	}
	if sys.Allocation.Grade != "C3" {
		t.Fatalf("Allocation.Grade = %s; want C3", sys.Allocation.Grade)
	}
	if len(sys.Buckets.Findings) != 4 {
		t.Fatalf("Buckets.Findings = %+v; want 4", sys.Buckets.Findings)
	}
	if len(sys.Allocation.OutOfBand()) == 0 {
		t.Fatal("expected out-of-band entries for equity 60%/overseas 40%")
	}
}

func TestGenerateAdviceRejectsMissingSummary(t *testing.T) {
	reportRepo := &fakeReportRepo{}
	uc := newTestAdviceUC(&fakeLLM{enabled: true, reply: `{"summary":" ","explanations":[],"actions":[]}`}, reportRepo)
	if _, err := uc.Execute(context.Background()); err == nil {
		t.Fatal("Execute() error = nil; want error for empty summary")
	}
	if len(reportRepo.saved) != 0 {
		t.Fatalf("saved %d; want 0", len(reportRepo.saved))
	}
}

func TestAdviceRefWhitelistUnion(t *testing.T) {
	pack := ContextPack{Findings: []Finding{{Key: "savings_rate"}}}
	diag := ComputeBuckets(BucketInputs{})
	alloc := ComputeAllocation(balancedProfile(), true, map[string]int64{})
	wl := adviceRefWhitelist(pack, diag, alloc)
	for _, k := range []string{"savings_rate", "bucket_liquid", "bucket_insurance", "assume_grade", "band_equity_share", "band_gold_share"} {
		if !wl[k] {
			t.Fatalf("whitelist missing %s (got %v)", k, wl)
		}
	}
	if wl["random_key"] {
		t.Fatal("whitelist should not contain random_key")
	}
}
