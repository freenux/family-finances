package usecase

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ---- fakes ----

type fakeDigestRepo struct {
	settings *domain.DigestSettings
	sentAt   time.Time
}

func (f *fakeDigestRepo) GetSettings(context.Context) (*domain.DigestSettings, error) {
	if f.settings == nil {
		return nil, port.ErrNotFound
	}
	cp := *f.settings
	return &cp, nil
}

func (f *fakeDigestRepo) UpsertSettings(_ context.Context, s *domain.DigestSettings) error {
	cp := *s
	f.settings = &cp
	return nil
}

func (f *fakeDigestRepo) MarkSent(_ context.Context, at time.Time) error {
	f.sentAt = at
	if f.settings != nil {
		f.settings.LastSentAt = at
	}
	return nil
}

type fakeSender struct {
	available bool
	sent      []DigestContent
	channels  []string
}

func (f *fakeSender) Available(string) bool { return f.available }

func (f *fakeSender) Send(_ context.Context, channel, _ string, c DigestContent) error {
	f.channels = append(f.channels, channel)
	f.sent = append(f.sent, c)
	return nil
}

var (
	_ port.DigestRepo = (*fakeDigestRepo)(nil)
	_ DigestSender    = (*fakeSender)(nil)
)

// ---- digestDue ----

func TestDigestDue(t *testing.T) {
	// 2026-07-12 是周日；本周一 = 2026-07-06；当前季度 = 2026Q3（07-01 起）
	sunday := time.Date(2026, 7, 12, 10, 0, 0, 0, time.Local)
	mondayEarly := time.Date(2026, 7, 6, 7, 0, 0, 0, time.Local)
	mondayLate := time.Date(2026, 7, 6, 9, 0, 0, 0, time.Local)

	cases := []struct {
		name string
		s    domain.DigestSettings
		now  time.Time
		want bool
	}{
		{"未启用不发", domain.DigestSettings{Enabled: false, Cadence: "weekly"}, sunday, false},
		{"weekly 周一 08:00 前不发", domain.DigestSettings{Enabled: true, Cadence: "weekly"}, mondayEarly, false},
		{"weekly 周一 08:00 后首次发", domain.DigestSettings{Enabled: true, Cadence: "weekly"}, mondayLate, true},
		{"weekly 本周已发过不重发",
			domain.DigestSettings{Enabled: true, Cadence: "weekly",
				LastSentAt: time.Date(2026, 7, 6, 8, 30, 0, 0, time.Local)}, sunday, false},
		{"weekly 上周发的本周该发",
			domain.DigestSettings{Enabled: true, Cadence: "weekly",
				LastSentAt: time.Date(2026, 6, 29, 9, 0, 0, 0, time.Local)}, sunday, true},
		{"quarterly 本季未发该发", domain.DigestSettings{Enabled: true, Cadence: "quarterly"}, sunday, true},
		{"quarterly 本季已发不重发",
			domain.DigestSettings{Enabled: true, Cadence: "quarterly",
				LastSentAt: time.Date(2026, 7, 1, 9, 0, 0, 0, time.Local)}, sunday, false},
		{"quarterly 上季发的本季该发",
			domain.DigestSettings{Enabled: true, Cadence: "quarterly",
				LastSentAt: time.Date(2026, 4, 1, 9, 0, 0, 0, time.Local)}, sunday, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := digestDue(tc.s, tc.now); got != tc.want {
				t.Fatalf("digestDue() = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestStartOfWeek(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026-07-12", "2026-07-06"}, // 周日 → 上周一
		{"2026-07-06", "2026-07-06"}, // 周一 → 当天
		{"2026-07-08", "2026-07-06"}, // 周三
	}
	for _, tc := range cases {
		in, _ := time.ParseInLocation("2006-01-02", tc.in, time.Local)
		if got := startOfWeek(in).Format("2006-01-02"); got != tc.want {
			t.Fatalf("startOfWeek(%s) = %s; want %s", tc.in, got, tc.want)
		}
	}
}

// ---- Build / SendNow ----

func newTestDigest(txRepo *fakeTransactionRepo, repo *fakeDigestRepo, sender *fakeSender, llm *fakeLLM) *DigestService {
	uc := NewDigestService(repo, txRepo, &fakeCategoryRepo{cats: testCategories()}, llm, sender,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	uc.now = func() time.Time { return time.Date(2026, 7, 12, 10, 0, 0, 0, time.Local) }
	return uc
}

func TestDigestBuildSections(t *testing.T) {
	quarter := domain.CurrentQuarter(time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)).Label
	txRepo := &fakeTransactionRepo{
		periodAgg: map[string][]domain.CategoryAggregation{
			quarter: {
				{CategoryID: "expense.discretion.shopping", Name: "购物消费", Amount: 30000},
				{CategoryID: "expense.fixed.housing", Name: "居住成本", Amount: 90000},
				{CategoryID: "income.salary.husband", Name: "男主工资", Amount: 500000},
			},
		},
	}
	uc := newTestDigest(txRepo, &fakeDigestRepo{}, &fakeSender{}, &fakeLLM{enabled: false})

	c, err := uc.Build(context.Background(), domain.DigestQuarterly, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !strings.Contains(c.Subject, "季报") {
		t.Fatalf("Subject = %q; want 季报", c.Subject)
	}
	// Top3 按金额降序且不含收入科目
	if !strings.Contains(c.Text, "1. 居住成本") || !strings.Contains(c.Text, "2. 购物消费") {
		t.Fatalf("Text = %q; want top categories sorted", c.Text)
	}
	if strings.Contains(c.Text, "男主工资") {
		t.Fatalf("Text 不应包含收入科目: %q", c.Text)
	}
	if !strings.Contains(c.HTML, "<h3") || !strings.Contains(c.HTML, "居住成本") {
		t.Fatalf("HTML 渲染缺失: %q", c.HTML)
	}
	// llm 未启用时无 AI 观察
	if strings.Contains(c.Text, "AI 观察") {
		t.Fatalf("llm disabled 时不应有 AI 观察: %q", c.Text)
	}
}

func TestDigestSendNow(t *testing.T) {
	repo := &fakeDigestRepo{settings: &domain.DigestSettings{
		ID: "default", Enabled: true, Cadence: domain.DigestWeekly,
		Channel: domain.DigestChannelWebhook, Target: "https://example.com/hook",
	}}
	sender := &fakeSender{available: true}
	uc := newTestDigest(&fakeTransactionRepo{}, repo, sender, &fakeLLM{enabled: false})

	if err := uc.SendNow(context.Background(), true); err != nil {
		t.Fatalf("SendNow() error = %v", err)
	}
	if len(sender.sent) != 1 || sender.channels[0] != domain.DigestChannelWebhook {
		t.Fatalf("sender = %+v; want 1 webhook send", sender)
	}
	if repo.sentAt.IsZero() {
		t.Fatal("MarkSent 未被调用")
	}

	// 通道不可用时报错
	sender.available = false
	if err := uc.SendNow(context.Background(), false); err == nil {
		t.Fatal("SendNow() = nil; want channel unavailable error")
	}

	// 未填 target 报错
	repo.settings.Target = ""
	sender.available = true
	if err := uc.SendNow(context.Background(), false); err == nil {
		t.Fatal("SendNow() = nil; want missing target error")
	}
}
