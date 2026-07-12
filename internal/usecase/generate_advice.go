package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// GenerateAdvice：ContextPack + 四笔钱诊断 + 配置区间引擎输出 → LLM 只解释并生成行动选项
// （每项含代价说明与 refs）→ 校验后落库 reports（period_type='advice'）。
// 输出范式（铁律 1）：系统计算 → AI 解释 → 可选动作及代价。
type GenerateAdvice struct {
	builder    *ContextPackBuilder
	bucketEng  *BucketEngine
	reportRepo port.ReportRepo
	llm        ReportLLM
	modelName  string
	now        func() time.Time
}

func NewGenerateAdvice(builder *ContextPackBuilder, bucketEng *BucketEngine, reportRepo port.ReportRepo,
	llm ReportLLM, modelName string) *GenerateAdvice {
	return &GenerateAdvice{
		builder: builder, bucketEng: bucketEng, reportRepo: reportRepo,
		llm: llm, modelName: modelName, now: time.Now,
	}
}

func (uc *GenerateAdvice) Enabled() bool { return uc.llm.Enabled() }

// PeriodTypeAdvice 落库 reports.period_type 的取值；period 用生成时的季度 label
const PeriodTypeAdvice domain.PeriodType = "advice"

// AdviceContent 是 LLM 输出经校验后的结构（落库 ai_analysis）
type AdviceContent struct {
	Summary      string         `json:"summary"`
	Explanations []AIReportItem `json:"explanations"` // 对系统结论的解释
	Actions      []AdviceAction `json:"actions"`      // 可选动作，必须带代价说明
}

type AdviceAction struct {
	Text string   `json:"text"`
	Cost string   `json:"cost"` // 代价/权衡说明，缺失则整条丢弃
	Refs []string `json:"refs"`
}

// AdviceSystemData 是"系统计算"部分，随建议一起落库（kpi_data 列），页面渲染存库结果
type AdviceSystemData struct {
	Buckets      BucketDiagnosis  `json:"buckets"`
	Allocation   AllocationResult `json:"allocation"`
	PackFindings []Finding        `json:"pack_findings"` // ContextPack findings，供 refs 文本回显
}

// Execute 生成一份建议并落库；period 取当前季度
func (uc *GenerateAdvice) Execute(ctx context.Context) (domain.AIReport, error) {
	if !uc.llm.Enabled() {
		return domain.AIReport{}, ErrLLMDisabled
	}

	p := domain.CurrentQuarter(uc.now())
	pack, err := uc.builder.Build(ctx, p)
	if err != nil {
		return domain.AIReport{}, fmt.Errorf("组装上下文包: %w", err)
	}
	in, err := uc.bucketEng.LoadInputs(ctx)
	if err != nil {
		return domain.AIReport{}, fmt.Errorf("加载四笔钱输入: %w", err)
	}
	diag := ComputeBuckets(in)
	profile := domain.DefaultFamilyProfile()
	if in.Profile != nil {
		profile = *in.Profile
	}
	var snapData map[string]int64
	if in.Snapshot != nil {
		snapData = in.Snapshot.Data
	}
	alloc := ComputeAllocation(profile, in.Profile != nil, snapData)

	whitelist := adviceRefWhitelist(pack, diag, alloc)

	system := buildAdviceSystemPrompt()
	user, err := buildAdviceUserPrompt(pack, diag, alloc)
	if err != nil {
		return domain.AIReport{}, err
	}
	reply, err := uc.llm.Complete(ctx, system, user)
	if err != nil {
		return domain.AIReport{}, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	content, err := parseAdviceReply(reply)
	if err != nil {
		return domain.AIReport{}, fmt.Errorf("解析 LLM 输出失败: %w (reply=%s)", err, reply)
	}
	content = validateAdviceContent(content, whitelist)
	if strings.TrimSpace(content.Summary) == "" {
		return domain.AIReport{}, fmt.Errorf("LLM 输出缺少 summary，拒绝落库")
	}

	sysJSON, _ := json.Marshal(AdviceSystemData{Buckets: diag, Allocation: alloc, PackFindings: pack.Findings})
	incomeJSON, _ := json.Marshal(pack.Income)
	expenseJSON, _ := json.Marshal(pack.Expense)
	compareJSON, _ := json.Marshal(pack.Compare)
	analysisJSON, _ := json.Marshal(content)

	now := uc.now()
	report := domain.AIReport{
		ID:          uuid.NewString(),
		Period:      p.Label,
		PeriodType:  PeriodTypeAdvice,
		GeneratedAt: now,
		IncomeData:  string(incomeJSON),
		ExpenseData: string(expenseJSON),
		KPIData:     string(sysJSON),
		Comparison:  string(compareJSON),
		AIPrompt:    system + "\n\n" + user,
		AIAnalysis:  string(analysisJSON),
		AIModel:     uc.modelName,
		Status:      "final",
		CreatedAt:   now,
	}
	if err := uc.reportRepo.Upsert(ctx, &report); err != nil {
		return domain.AIReport{}, fmt.Errorf("落库建议失败: %w", err)
	}
	return report, nil
}

// adviceRefWhitelist = ContextPack findings ∪ 桶诊断 findings ∪ 引擎假设 ∪ 区间 key
func adviceRefWhitelist(pack ContextPack, diag BucketDiagnosis, alloc AllocationResult) map[string]bool {
	set := pack.RefWhitelist()
	for _, f := range diag.Findings {
		set[f.Key] = true
	}
	for _, a := range alloc.Assumptions {
		set[a.Key] = true
	}
	for _, b := range alloc.Bands {
		set[b.Key] = true
	}
	return set
}

func buildAdviceSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString("你是一位家庭资产配置顾问助手。你会收到系统确定性计算的结果：")
	sb.WriteString("财务上下文包（findings）、四笔钱分层诊断（bucket_* 键）、配置区间引擎输出（band_* 键与 assume_* 假设）。\n")
	sb.WriteString("要求：\n")
	sb.WriteString("1. 只解释系统结论并给出可选动作，绝不编造系统输出之外的数字或区间。\n")
	sb.WriteString("2. explanations 每条给 refs（引用 findings/bucket_*/band_*/assume_* 的 key）。\n")
	sb.WriteString("3. actions 每条必须包含 cost 字段，说明该动作的代价或权衡（如流动性下降、税费、时间成本），并给 refs。\n")
	sb.WriteString("4. 严格输出 JSON 对象：\n")
	sb.WriteString(`{"summary":"...","explanations":[{"text":"...","refs":["key1"]}],"actions":[{"text":"...","cost":"...","refs":["key2"]}]}`)
	sb.WriteString("\n5. 用简体中文回答。\n")
	return sb.String()
}

func buildAdviceUserPrompt(pack ContextPack, diag BucketDiagnosis, alloc AllocationResult) (string, error) {
	payload := map[string]any{
		"context_pack": pack,
		"buckets":      diag,
		"allocation":   alloc,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal advice payload: %w", err)
	}
	return "系统计算结果：\n" + string(b), nil
}

func parseAdviceReply(reply string) (AdviceContent, error) {
	reply = strings.TrimSpace(reply)
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	reply = strings.TrimSpace(reply)

	var c AdviceContent
	if err := json.Unmarshal([]byte(reply), &c); err != nil {
		return AdviceContent{}, err
	}
	return c, nil
}

// validateAdviceContent：explanations 校验同财报；actions 额外要求 cost 非空（铁律 3 + 输出范式）
func validateAdviceContent(c AdviceContent, whitelist map[string]bool) AdviceContent {
	out := AdviceContent{
		Summary:      c.Summary,
		Explanations: filterValidItems(c.Explanations, whitelist),
	}
	for _, a := range c.Actions {
		if strings.TrimSpace(a.Text) == "" || strings.TrimSpace(a.Cost) == "" || len(a.Refs) == 0 {
			continue
		}
		valid := true
		for _, ref := range a.Refs {
			if !whitelist[ref] {
				valid = false
				break
			}
		}
		if valid {
			out.Actions = append(out.Actions, a)
		}
	}
	if out.Actions == nil {
		out.Actions = []AdviceAction{}
	}
	return out
}
