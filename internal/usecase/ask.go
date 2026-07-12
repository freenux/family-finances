package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"family-finances/internal/domain"
)

// Ask AI 对话问数（/ask）：问题 + 最新季度 ContextPack → LLM 单轮回答。
// 不落库、无会话历史（每问独立）、不开放 SQL；refs 违白名单或 can_answer=false
// 一律返回固定"无法回答"文案（铁律 3：不编数）。
type Ask struct {
	builder *ContextPackBuilder
	llm     ReportLLM
	now     func() time.Time
}

func NewAsk(builder *ContextPackBuilder, llm ReportLLM) *Ask {
	return &Ask{builder: builder, llm: llm, now: time.Now}
}

func (uc *Ask) Enabled() bool { return uc.llm.Enabled() }

// AskRef 一条数据依据
type AskRef struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// AskResult 单轮回答
type AskResult struct {
	CanAnswer bool     `json:"can_answer"`
	Answer    string   `json:"answer"`
	Refs      []AskRef `json:"refs"`
	Period    string   `json:"period"` // 数据口径（当前季度）
}

// CannotAnswerText 固定拒答文案
const CannotAnswerText = "这个问题无法用现有的家庭聚合数据回答。我只能基于收支聚合、KPI、资产快照与系统诊断结论作答，不会推测或编造数字。"

const maxQuestionRunes = 200

// Execute 单轮问答
func (uc *Ask) Execute(ctx context.Context, question string) (AskResult, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return AskResult{}, fmt.Errorf("问题不能为空")
	}
	if len([]rune(question)) > maxQuestionRunes {
		return AskResult{}, fmt.Errorf("问题过长（限 %d 字）", maxQuestionRunes)
	}
	if !uc.llm.Enabled() {
		return AskResult{}, ErrLLMDisabled
	}

	p := domain.CurrentQuarter(uc.now())
	pack, err := uc.builder.Build(ctx, p)
	if err != nil {
		return AskResult{}, fmt.Errorf("组装上下文包: %w", err)
	}
	whitelist := pack.RefWhitelist()
	refText := make(map[string]string, len(pack.Findings))
	for _, f := range pack.Findings {
		refText[f.Key] = f.Text
	}

	system := buildAskSystemPrompt()
	user, err := buildAskUserPrompt(pack, question)
	if err != nil {
		return AskResult{}, err
	}
	reply, err := uc.llm.Complete(ctx, system, user)
	if err != nil {
		return AskResult{}, fmt.Errorf("调用 LLM 失败: %w", err)
	}

	parsed, err := parseAskReply(reply)
	if err != nil {
		return AskResult{}, fmt.Errorf("解析 LLM 输出失败: %w (reply=%s)", err, reply)
	}

	res := AskResult{Period: p.Label}
	if !parsed.CanAnswer || strings.TrimSpace(parsed.Answer) == "" {
		res.Answer = CannotAnswerText
		return res, nil
	}
	// refs 校验：任何一条违白名单 → 整个回答降级为拒答（不冒险输出未经溯源的数字）
	for _, ref := range parsed.Refs {
		if !whitelist[ref] {
			res.Answer = CannotAnswerText
			return res, nil
		}
	}
	res.CanAnswer = true
	res.Answer = parsed.Answer
	for _, ref := range parsed.Refs {
		res.Refs = append(res.Refs, AskRef{Key: ref, Text: refText[ref]})
	}
	return res, nil
}

type askReply struct {
	CanAnswer bool     `json:"can_answer"`
	Answer    string   `json:"answer"`
	Refs      []string `json:"refs"`
}

func buildAskSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString("你是家庭财务数据问答助手。你只能基于用户提供的「财务上下文包」JSON 回答问题。\n")
	sb.WriteString("要求：\n")
	sb.WriteString("1. 答案中的每个数字都必须来自上下文包；不能推测、估算或使用外部知识补数。\n")
	sb.WriteString("2. refs 列出支撑答案的 findings[].key（至少一条，只能引用 findings 数组中出现的 key）。\n")
	sb.WriteString("3. 上下文包不足以回答时输出 can_answer=false，不要勉强作答。\n")
	sb.WriteString("4. 严格输出 JSON 对象：{\"can_answer\":true,\"answer\":\"...\",\"refs\":[\"key1\"]}\n")
	sb.WriteString("5. 用简体中文，答案不超过 5 句话。\n")
	return sb.String()
}

func buildAskUserPrompt(pack ContextPack, question string) (string, error) {
	b, err := json.Marshal(pack)
	if err != nil {
		return "", fmt.Errorf("marshal context pack: %w", err)
	}
	return "财务上下文包：\n" + string(b) + "\n\n用户问题：" + question, nil
}

func parseAskReply(reply string) (askReply, error) {
	reply = strings.TrimSpace(reply)
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	reply = strings.TrimSpace(reply)

	var r askReply
	if err := json.Unmarshal([]byte(reply), &r); err != nil {
		return askReply{}, err
	}
	return r, nil
}
