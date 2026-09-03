package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"sort"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// Digest 周报/季报摘要：确定性组装 + 可选 LLM 一句话观察 + 定时发送。

// DigestContent 渲染结果（HTML 内联样式 + 纯文本两版）
type DigestContent struct {
	Subject string
	HTML    string
	Text    string
}

// DigestSender 发送器（adapter/notify 实现）
type DigestSender interface {
	// Available 该通道当前是否可用（email 需 SMTP 配置）
	Available(channel string) bool
	Send(ctx context.Context, channel, target string, c DigestContent) error
}

// DigestService 摘要组装与调度
type DigestService struct {
	digestRepo port.DigestRepo
	txRepo     port.TransactionRepo
	catRepo    port.CategoryRepo
	llm        ReportLLM
	sender     DigestSender
	log        *slog.Logger
	now        func() time.Time
}

func NewDigestService(digestRepo port.DigestRepo, txRepo port.TransactionRepo, catRepo port.CategoryRepo,
	llm ReportLLM, sender DigestSender, log *slog.Logger) *DigestService {
	return &DigestService{
		digestRepo: digestRepo, txRepo: txRepo, catRepo: catRepo,
		llm: llm, sender: sender, log: log, now: time.Now,
	}
}

// Build 组装一期摘要（cadence: weekly / quarterly）
func (uc *DigestService) Build(ctx context.Context, cadence string, modules []string) (DigestContent, error) {
	now := uc.now()
	cur, prev, periodName := digestWindows(cadence, now)

	enabled := func(m string) bool {
		for _, v := range modules {
			if v == m {
				return true
			}
		}
		return len(modules) == 0 // 未选模块 = 全开
	}

	var sections []digestSection

	if enabled("expense_compare") {
		curExp, err := uc.sumExpense(ctx, cur)
		if err != nil {
			return DigestContent{}, err
		}
		prevExp, err := uc.sumExpense(ctx, prev)
		if err != nil {
			return DigestContent{}, err
		}
		delta := curExp - prevExp
		sign := ""
		if delta > 0 {
			sign = "+"
		}
		sections = append(sections, digestSection{
			Title: fmt.Sprintf("本%s支出 vs 上%s", periodName, periodName),
			Lines: []string{fmt.Sprintf("本%s支出 %s 元，上%s %s 元，变化 %s%s 元",
				periodName, formatYuanFen(curExp), periodName, formatYuanFen(prevExp), sign, formatYuanFen(delta))},
		})
	}

	if enabled("top_categories") {
		top, err := uc.topExpenseCategories(ctx, cur, 3)
		if err != nil {
			return DigestContent{}, err
		}
		lines := make([]string, 0, len(top))
		for i, t := range top {
			lines = append(lines, fmt.Sprintf("%d. %s：%s 元", i+1, t.Name, formatYuanFen(t.Amount)))
		}
		if len(lines) == 0 {
			lines = []string{"本期无已入账支出"}
		}
		sections = append(sections, digestSection{Title: "Top 3 支出科目", Lines: lines})
	}

	if enabled("pending_count") {
		pending, err := uc.txRepo.ListPendingCategory(ctx, 500)
		if err != nil {
			return DigestContent{}, err
		}
		sections = append(sections, digestSection{
			Title: "待核对流水",
			Lines: []string{fmt.Sprintf("还有 %d 条流水待分类/核对", len(pending))},
		})
	}

	if enabled("llm_note") && uc.llm.Enabled() {
		if note := uc.llmNote(ctx, sections); note != "" {
			sections = append(sections, digestSection{Title: "AI 观察", Lines: []string{note}})
		}
	}

	subject := fmt.Sprintf("家庭财务%s报 · %s", periodName, cur.Label)
	return renderDigest(subject, sections), nil
}

type digestSection struct {
	Title string
	Lines []string
}

// digestWindows 当期与上期窗口。weekly = 本周一起（自然周），quarterly = 当前季度。
func digestWindows(cadence string, now time.Time) (cur, prev domain.Period, periodName string) {
	if cadence == domain.DigestQuarterly {
		cur = domain.CurrentQuarter(now)
		return cur, cur.Previous(), "季"
	}
	monday := startOfWeek(now)
	cur = domain.Period{Label: monday.Format("2006-01-02") + " 周", Start: monday, End: monday.AddDate(0, 0, 7)}
	prevMonday := monday.AddDate(0, 0, -7)
	prev = domain.Period{Label: prevMonday.Format("2006-01-02") + " 周", Start: prevMonday, End: monday}
	return cur, prev, "周"
}

// startOfWeek 本周一 00:00
func startOfWeek(now time.Time) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekday := int(day.Weekday())
	if weekday == 0 {
		weekday = 7 // 周日算第 7 天
	}
	return day.AddDate(0, 0, -(weekday - 1))
}

func (uc *DigestService) sumExpense(ctx context.Context, p domain.Period) (int64, error) {
	buckets := []port.PeriodBucket{{Label: p.Label, Start: p.Start, End: p.End}}
	// 日常口径：摘要要避免"这周花了 12 万"这种一次性专项造成的误报警
	out, _, err := uc.txRepo.SumByBuckets(ctx, buckets, domain.DirectionExpense, domain.AccountFamily)
	if err != nil {
		return 0, err
	}
	return out[0].Amount, nil
}

type digestTopCat struct {
	Name   string
	Amount int64
}

func (uc *DigestService) topExpenseCategories(ctx context.Context, p domain.Period, n int) ([]digestTopCat, error) {
	aggs, err := uc.txRepo.AggregateByCategory(ctx, p, domain.AccountFamily, domain.ScopeDaily)
	if err != nil {
		return nil, err
	}
	var out []digestTopCat
	for _, a := range aggs {
		if strings.HasPrefix(a.CategoryID, "expense.") && a.Amount > 0 {
			out = append(out, digestTopCat{Name: a.Name, Amount: a.Amount})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// llmNote 一句话观察（失败或输出异常直接跳过，不阻塞摘要）
func (uc *DigestService) llmNote(ctx context.Context, sections []digestSection) string {
	var sb strings.Builder
	for _, s := range sections {
		sb.WriteString(s.Title + "：" + strings.Join(s.Lines, "；") + "\n")
	}
	system := "你是家庭财务助手。基于给定的摘要数据，输出一句简体中文观察（不超过 40 字），" +
		"不得引入摘要之外的数字。严格输出 JSON：{\"observation\":\"...\"}"
	reply, err := uc.llm.Complete(ctx, system, "摘要数据：\n"+sb.String())
	if err != nil {
		uc.log.Warn("digest llm note", "err", err)
		return ""
	}
	reply = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(reply, "```json"), "```"), "```"))
	var out struct {
		Observation string `json:"observation"`
	}
	if err := json.Unmarshal([]byte(reply), &out); err != nil {
		uc.log.Warn("digest llm note parse", "err", err)
		return ""
	}
	return strings.TrimSpace(out.Observation)
}

// renderDigest 渲染 HTML（内联样式）与纯文本两版
func renderDigest(subject string, sections []digestSection) DigestContent {
	var text strings.Builder
	text.WriteString(subject + "\n\n")
	var htm strings.Builder
	htm.WriteString(`<div style="font-family:-apple-system,'PingFang SC',sans-serif;max-width:560px;margin:0 auto;color:#0d253d">`)
	htm.WriteString(`<h2 style="font-weight:400;font-size:20px">` + html.EscapeString(subject) + `</h2>`)
	for _, s := range sections {
		text.WriteString("## " + s.Title + "\n")
		htm.WriteString(`<h3 style="font-size:14px;margin:18px 0 6px">` + html.EscapeString(s.Title) + `</h3><ul style="margin:0;padding-left:18px;font-size:13px;line-height:1.8;color:#273951">`)
		for _, line := range s.Lines {
			text.WriteString("- " + line + "\n")
			htm.WriteString(`<li>` + html.EscapeString(line) + `</li>`)
		}
		text.WriteString("\n")
		htm.WriteString(`</ul>`)
	}
	footer := "由家庭财务系统自动生成，仅作参考。"
	text.WriteString("--\n" + footer + "\n")
	htm.WriteString(`<p style="margin-top:20px;font-size:11px;color:#64748d">` + html.EscapeString(footer) + `</p></div>`)
	return DigestContent{Subject: subject, HTML: htm.String(), Text: text.String()}
}

// ---- 调度 ----

// digestDue 纯函数：是否到发送时点。
// weekly：周一 08:00 后的首个检查点，且本周未发过；quarterly：季初首日 00:00 后，且本季未发过。
func digestDue(s domain.DigestSettings, now time.Time) bool {
	if !s.Enabled {
		return false
	}
	var threshold time.Time
	if s.Cadence == domain.DigestQuarterly {
		threshold = domain.CurrentQuarter(now).Start
	} else {
		threshold = startOfWeek(now).Add(8 * time.Hour)
	}
	if now.Before(threshold) {
		return false
	}
	return s.LastSentAt.Before(threshold)
}

// SendNow 立即组装并发送一期（测试按钮与调度共用）
func (uc *DigestService) SendNow(ctx context.Context, markSent bool) error {
	settings, err := uc.digestRepo.GetSettings(ctx)
	if err != nil {
		if err == port.ErrNotFound {
			return fmt.Errorf("尚未保存推送设置")
		}
		return err
	}
	if settings.Target == "" {
		return fmt.Errorf("未填写收件地址 / webhook URL")
	}
	if !uc.sender.Available(settings.Channel) {
		return fmt.Errorf("通道 %s 不可用（email 需配置 SMTP_HOST/SMTP_FROM）", settings.Channel)
	}
	content, err := uc.Build(ctx, settings.Cadence, settings.Modules)
	if err != nil {
		return fmt.Errorf("组装摘要失败: %w", err)
	}
	if err := uc.sender.Send(ctx, settings.Channel, settings.Target, content); err != nil {
		return fmt.Errorf("发送失败: %w", err)
	}
	if markSent {
		return uc.digestRepo.MarkSent(ctx, uc.now())
	}
	return nil
}

// Run 每小时检查一次 due（参考 ClassifyPending 的循环模式）
func (uc *DigestService) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	uc.log.Info("digest scheduler started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		settings, err := uc.digestRepo.GetSettings(ctx)
		if err != nil {
			if err != port.ErrNotFound {
				uc.log.Warn("digest settings", "err", err)
			}
			continue
		}
		if !digestDue(*settings, uc.now()) {
			continue
		}
		if err := uc.SendNow(ctx, true); err != nil {
			uc.log.Warn("digest send", "err", err)
		} else {
			uc.log.Info("digest sent", "cadence", settings.Cadence, "channel", settings.Channel)
		}
	}
}
