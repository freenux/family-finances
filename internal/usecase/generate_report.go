package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ReportLLM 是 GenerateReport 依赖的窄接口，方便测试注入 fake（做法沿用 ClassifyPending 的
// *llm.Client 直接调用，这里额外抽出接口是为了在不启动真实 HTTP 的情况下做 usecase 测试）。
type ReportLLM interface {
	Enabled() bool
	Complete(ctx context.Context, system, user string) (string, error)
}

// ErrLLMDisabled 未配置 OPENAI_API_KEY 时禁止生成
var ErrLLMDisabled = errors.New("LLM 未启用（未配置 OPENAI_API_KEY）")

// GenerateReport 组装 ContextPack → 调 LLM → 校验 → 落库（reports 表）
type GenerateReport struct {
	builder    *ContextPackBuilder
	reportRepo port.ReportRepo
	llm        ReportLLM
	modelName  string
	now        func() time.Time
}

func NewGenerateReport(builder *ContextPackBuilder, reportRepo port.ReportRepo, llm ReportLLM, modelName string) *GenerateReport {
	return &GenerateReport{builder: builder, reportRepo: reportRepo, llm: llm, modelName: modelName, now: time.Now}
}

func (uc *GenerateReport) Enabled() bool { return uc.llm.Enabled() }

// AIReportContent 是 LLM 输出经校验后的结构，落库为 ai_analysis JSON
type AIReportContent struct {
	Summary    string         `json:"summary"`
	Highlights []AIReportItem `json:"highlights"`
	Risks      []AIReportItem `json:"risks"`
	Advice     []AIReportItem `json:"advice"`
}

type AIReportItem struct {
	Text string   `json:"text"`
	Refs []string `json:"refs"`
}

// KPIDataEnvelope 是落库到 reports.kpi_data 列的结构：kpi + findings + snapshot 打包在一起，
// 因为 reports 表没有单独的 findings/snapshot 列（见 dev-design.md §4）。回看历史财报时
// handler 用同一个类型解码。
type KPIDataEnvelope struct {
	KPI      ContextKPI       `json:"kpi"`
	Findings []Finding        `json:"findings"`
	Snapshot *ContextSnapshot `json:"snapshot"`
}

// Execute 为期间 p 生成一份财报：组包 → prompt → LLM(json_object) → 校验 refs → 落库覆盖同期已有记录
func (uc *GenerateReport) Execute(ctx context.Context, p domain.Period) (domain.AIReport, error) {
	if !uc.llm.Enabled() {
		return domain.AIReport{}, ErrLLMDisabled
	}
	if p.Type != domain.PeriodQuarterly && p.Type != domain.PeriodAnnual {
		return domain.AIReport{}, fmt.Errorf("财报仅支持季度或年度期间")
	}

	pack, err := uc.builder.Build(ctx, p)
	if err != nil {
		return domain.AIReport{}, fmt.Errorf("组装上下文包: %w", err)
	}

	system := buildReportSystemPrompt()
	user, err := buildReportUserPrompt(pack)
	if err != nil {
		return domain.AIReport{}, err
	}

	reply, err := uc.llm.Complete(ctx, system, user)
	if err != nil {
		return domain.AIReport{}, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	content, err := parseAIReportReply(reply)
	if err != nil {
		return domain.AIReport{}, fmt.Errorf("解析 LLM 输出失败: %w (reply=%s)", err, reply)
	}
	content = validateAIReportContent(content, pack.RefWhitelist())
	if strings.TrimSpace(content.Summary) == "" {
		return domain.AIReport{}, fmt.Errorf("LLM 输出缺少 summary，拒绝落库")
	}

	incomeJSON, _ := json.Marshal(pack.Income)
	expenseJSON, _ := json.Marshal(pack.Expense)
	kpiJSON, _ := json.Marshal(KPIDataEnvelope{KPI: pack.KPI, Findings: pack.Findings, Snapshot: pack.Snapshot})
	compareJSON, _ := json.Marshal(pack.Compare)
	analysisJSON, _ := json.Marshal(content)

	now := uc.now()
	report := domain.AIReport{
		ID:          uuid.NewString(),
		Period:      p.Label,
		PeriodType:  p.Type,
		GeneratedAt: now,
		IncomeData:  string(incomeJSON),
		ExpenseData: string(expenseJSON),
		KPIData:     string(kpiJSON),
		Comparison:  string(compareJSON),
		AIPrompt:    system + "\n\n" + user,
		AIAnalysis:  string(analysisJSON),
		AIModel:     uc.modelName,
		Status:      "final",
		IsFrozen:    false,
		CreatedAt:   now,
	}
	if err := uc.reportRepo.Upsert(ctx, &report); err != nil {
		return domain.AIReport{}, fmt.Errorf("落库财报失败: %w", err)
	}
	return report, nil
}

func buildReportSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString("你是一位家庭财务顾问助手。你会收到一份「财务上下文包」JSON，")
	sb.WriteString("其中包含本期收支聚合、KPI、同环比与确定性诊断结论（findings）。\n")
	sb.WriteString("要求：\n")
	sb.WriteString("1. 只解释、叙述、生成行动建议，绝不编造上下文包里没有的数字。\n")
	sb.WriteString("2. 每条 highlights/risks/advice 结论必须给出 refs 字段，")
	sb.WriteString("列出引用的 findings[].key（只能引用上下文包 findings 数组中出现的 key，不要引用其它字段名）。\n")
	sb.WriteString("3. 严格输出 JSON 对象，不要输出其它内容，格式：\n")
	sb.WriteString(`{"summary":"...","highlights":[{"text":"...","refs":["key1"]}],"risks":[...],"advice":[...]}`)
	sb.WriteString("\n4. 用简体中文回答。\n")
	return sb.String()
}

func buildReportUserPrompt(pack ContextPack) (string, error) {
	b, err := json.Marshal(pack)
	if err != nil {
		return "", fmt.Errorf("marshal context pack: %w", err)
	}
	return "财务上下文包：\n" + string(b), nil
}

func parseAIReportReply(reply string) (AIReportContent, error) {
	reply = strings.TrimSpace(reply)
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	reply = strings.TrimSpace(reply)

	var c AIReportContent
	if err := json.Unmarshal([]byte(reply), &c); err != nil {
		return AIReportContent{}, err
	}
	return c, nil
}

// validateAIReportContent 每条结论必须至少有一个 ref，且全部 refs ⊆ whitelist，否则整条丢弃（铁律 3）。
func validateAIReportContent(c AIReportContent, whitelist map[string]bool) AIReportContent {
	return AIReportContent{
		Summary:    c.Summary,
		Highlights: filterValidItems(c.Highlights, whitelist),
		Risks:      filterValidItems(c.Risks, whitelist),
		Advice:     filterValidItems(c.Advice, whitelist),
	}
}

func filterValidItems(items []AIReportItem, whitelist map[string]bool) []AIReportItem {
	out := make([]AIReportItem, 0, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.Text) == "" || len(it.Refs) == 0 {
			continue
		}
		valid := true
		for _, ref := range it.Refs {
			if !whitelist[ref] {
				valid = false
				break
			}
		}
		if valid {
			out = append(out, it)
		}
	}
	return out
}
