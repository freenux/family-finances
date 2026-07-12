package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
)

func newTestAsk(llm *fakeLLM) *Ask {
	txRepo := &fakeTransactionRepo{periodAgg: map[string][]domain.CategoryAggregation{}}
	// 给当前季度一点收入，让 savings_rate finding 存在于白名单
	quarter := domain.CurrentQuarter(time.Now()).Label
	txRepo.periodAgg[quarter] = []domain.CategoryAggregation{
		{CategoryID: "income.salary.husband", Amount: 900000},
	}
	builder := newTestBuilder(txRepo, &fakeAssetSnapshotRepo{})
	return NewAsk(builder, llm)
}

func TestAskValidatesInput(t *testing.T) {
	uc := newTestAsk(&fakeLLM{enabled: true})
	if _, err := uc.Execute(context.Background(), "  "); err == nil {
		t.Fatal("empty question should error")
	}
	if _, err := uc.Execute(context.Background(), strings.Repeat("问", 201)); err == nil {
		t.Fatal("overlong question should error")
	}
}

func TestAskDisabledLLM(t *testing.T) {
	uc := newTestAsk(&fakeLLM{enabled: false})
	if _, err := uc.Execute(context.Background(), "结余率如何"); !errors.Is(err, ErrLLMDisabled) {
		t.Fatalf("error = %v; want ErrLLMDisabled", err)
	}
}

func TestAskCannotAnswerPaths(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{"can_answer=false", `{"can_answer":false,"answer":"","refs":[]}`},
		{"空答案", `{"can_answer":true,"answer":"  ","refs":["savings_rate"]}`},
		{"refs 违白名单", `{"can_answer":true,"answer":"你的结余率是 99%","refs":["made_up_key"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := newTestAsk(&fakeLLM{enabled: true, reply: tc.reply})
			res, err := uc.Execute(context.Background(), "本季结余率如何？")
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if res.CanAnswer || res.Answer != CannotAnswerText {
				t.Fatalf("res = %+v; want fixed cannot-answer text", res)
			}
			if len(res.Refs) != 0 {
				t.Fatalf("refs = %+v; want empty", res.Refs)
			}
		})
	}
}

func TestAskValidAnswerCarriesRefTexts(t *testing.T) {
	reply := `{"can_answer":true,"answer":"本季结余率保持健康水平。","refs":["savings_rate"]}`
	uc := newTestAsk(&fakeLLM{enabled: true, reply: reply})
	res, err := uc.Execute(context.Background(), "本季结余率如何？")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !res.CanAnswer || res.Answer != "本季结余率保持健康水平。" {
		t.Fatalf("res = %+v; want valid answer", res)
	}
	if len(res.Refs) != 1 || res.Refs[0].Key != "savings_rate" || res.Refs[0].Text == "" {
		t.Fatalf("refs = %+v; want savings_rate with text", res.Refs)
	}
	if res.Period == "" {
		t.Fatal("Period empty; want current quarter label")
	}
}
