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
	// 无进展退避：LLM 对剩余行始终给不出答案时，别每个 tick 都重发同一批（空烧 token）。
	// Trigger（新导入）会立刻解除退避。
	const idleBackoff = 30 * time.Minute
	var idleUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Now().Before(idleUntil) {
				continue
			}
		case <-uc.trigger:
			idleUntil = time.Time{}
		}
		pending, assigned, err := uc.runOnce(ctx, batch)
		if err != nil {
			uc.log.Warn("classify round", "err", err)
			continue
		}
		if pending > 0 && assigned == 0 {
			idleUntil = time.Now().Add(idleBackoff)
			uc.log.Info("llm classifier idle backoff", "pending", pending, "until", idleUntil.Format(time.RFC3339))
		}
	}
}

// runOnce 跑一轮分类，返回（本轮 pending 行数, 实际写回的分类数, err）
func (uc *ClassifyPending) runOnce(ctx context.Context, batch int) (pending, assigned int, err error) {
	rows, err := uc.txRepo.ListPendingCategory(ctx, batch)
	if err != nil {
		return 0, 0, err
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return len(rows), 0, err
	}
	// 只保留二级科目作为 LLM 的可选项。
	//
	// 往来科目（转账/借还款/报销垫付）故意排除在外：模型只拿得到
	// counterparty/description/direction，没有金额和时间，判断"这是不是借款"
	// 证据不足；而误判的代价是一笔真支出以 excluded 落地、从所有报表里消失，
	// 比分错科目严重得多。往来只靠规则 + 人工。
	var options []domain.Category
	for _, c := range cats {
		if c.Level == 2 && !domain.IsTransferCategory(c.ID) {
			options = append(options, c)
		}
	}
	validIDs := make(map[string]bool, len(options))
	for _, c := range options {
		validIDs[c.ID] = true
	}

	// 只允许写回本轮送给 LLM 的行，防止模型幻觉/账单内容注入指令去改写其它流水
	batchIDs := make(map[string]bool, len(rows))
	for _, r := range rows {
		batchIDs[r.ID] = true
	}

	system := buildSystemPrompt(options)
	user := buildUserPrompt(rows)

	reply, err := uc.llm.Complete(ctx, system, user)
	if err != nil {
		return len(rows), 0, err
	}
	assignments, err := parseLLMReply(reply)
	if err != nil {
		return len(rows), 0, fmt.Errorf("parse llm reply: %w (reply=%s)", err, reply)
	}

	for _, a := range assignments {
		if a.CategoryID == "" {
			// 模型明确表示无法判断，留给人工
			continue
		}
		if !batchIDs[a.ID] {
			uc.log.Warn("llm returned id outside batch", "id", a.ID)
			continue
		}
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
			continue
		}
		assigned++
	}
	uc.log.Info("llm classified", "rows", len(rows), "assigned", assigned)
	return len(rows), assigned, nil
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
