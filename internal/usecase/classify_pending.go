package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"family-finances/internal/adapter/llm"
	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ClassifyPending 用 LLM 把仍未分类的 pending_review 行补上分类。
// 没有 API key 时 ClassifyPending.Enabled()=false，不做任何事。
type ClassifyPending struct {
	txRepo  port.TransactionRepo
	catRepo port.CategoryRepo
	llm     *llm.Client
	log     *slog.Logger
	trigger chan struct{}
}

func NewClassifyPending(tx port.TransactionRepo, cat port.CategoryRepo, c *llm.Client, log *slog.Logger) *ClassifyPending {
	return &ClassifyPending{
		txRepo:  tx,
		catRepo: cat,
		llm:     c,
		log:     log,
		trigger: make(chan struct{}, 1),
	}
}

func (uc *ClassifyPending) Enabled() bool { return uc.llm.Enabled() }

// Trigger 非阻塞地唤醒后台循环立即跑一轮
func (uc *ClassifyPending) Trigger() {
	select {
	case uc.trigger <- struct{}{}:
	default:
	}
}

// Run 阻塞：定时 + Trigger 驱动，直到 ctx 结束
func (uc *ClassifyPending) Run(ctx context.Context, interval time.Duration, batch int) {
	if !uc.Enabled() {
		uc.log.Info("llm classifier disabled (no OPENAI_API_KEY)")
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	uc.log.Info("llm classifier started", "interval", interval, "batch", batch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-uc.trigger:
		}
		if err := uc.runOnce(ctx, batch); err != nil {
			uc.log.Warn("classify round", "err", err)
		}
	}
}

func (uc *ClassifyPending) runOnce(ctx context.Context, batch int) error {
	rows, err := uc.txRepo.ListPendingCategory(ctx, batch)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return err
	}
	// 只保留二级科目作为 LLM 的可选项
	var options []domain.Category
	for _, c := range cats {
		if c.Level == 2 {
			options = append(options, c)
		}
	}
	validIDs := make(map[string]bool, len(options))
	for _, c := range options {
		validIDs[c.ID] = true
	}

	system := buildSystemPrompt(options)
	user := buildUserPrompt(rows)

	reply, err := uc.llm.Complete(ctx, system, user)
	if err != nil {
		return err
	}
	assignments, err := parseLLMReply(reply)
	if err != nil {
		return fmt.Errorf("parse llm reply: %w (reply=%s)", err, reply)
	}

	for _, a := range assignments {
		if !validIDs[a.CategoryID] {
			uc.log.Warn("llm returned invalid category_id", "id", a.ID, "cat", a.CategoryID)
			continue
		}
		cat := a.CategoryID
		status := domain.TxStatusConfirmed
		if err := uc.txRepo.Update(ctx, a.ID, port.TransactionUpdate{
			CategoryID: &cat,
			Status:     &status,
		}); err != nil {
			uc.log.Warn("persist llm classification", "id", a.ID, "err", err)
		}
	}
	uc.log.Info("llm classified", "rows", len(rows), "assigned", len(assignments))
	return nil
}

func buildSystemPrompt(options []domain.Category) string {
	var sb strings.Builder
	sb.WriteString("你是一个家庭账单分类助手。根据每笔交易的对方、商品说明，为其选择最合适的二级科目。\n")
	sb.WriteString("只能从下列 category_id 中选择，不要编造新的：\n")
	for _, c := range options {
		sb.WriteString("- ")
		sb.WriteString(c.ID)
		sb.WriteString(" (")
		sb.WriteString(c.Name)
		sb.WriteString(")\n")
	}
	sb.WriteString("\n输出 JSON 对象 {\"assignments\":[{\"id\":\"...\",\"category_id\":\"...\"}]}，不要输出其它内容。\n")
	sb.WriteString("如果实在无法判断，category_id 留空字符串。\n")
	return sb.String()
}

type llmRow struct {
	ID           string `json:"id"`
	Counterparty string `json:"counterparty"`
	Description  string `json:"description"`
	Direction    string `json:"direction"`
}

func buildUserPrompt(rows []domain.Transaction) string {
	payload := make([]llmRow, 0, len(rows))
	for _, r := range rows {
		payload = append(payload, llmRow{
			ID:           r.ID,
			Counterparty: r.Counterparty,
			Description:  r.Description,
			Direction:    string(r.Direction),
		})
	}
	b, _ := json.Marshal(payload)
	return "请为以下交易分类:\n" + string(b)
}

type llmAssignment struct {
	ID         string `json:"id"`
	CategoryID string `json:"category_id"`
}

type llmReply struct {
	Assignments []llmAssignment `json:"assignments"`
}

func parseLLMReply(reply string) ([]llmAssignment, error) {
	reply = strings.TrimSpace(reply)
	// 兼容 ```json …``` 包裹
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	reply = strings.TrimSpace(reply)

	var r llmReply
	if err := json.Unmarshal([]byte(reply), &r); err != nil {
		return nil, err
	}
	return r.Assignments, nil
}
