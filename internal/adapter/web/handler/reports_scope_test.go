package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"family-finances/internal/adapter/web"
	"family-finances/internal/domain"
)

// ---- 历史财报存档的口径标注 ----
//
// 口径拆分之后，ContextPack 的收入/支出/环比/结余率全部改成日常口径（剔除专项），
// 页面 chip 也跟着改成「日常收入 / 日常支出 / 日常结余率」。但 reports 表里改动之前
// 存下的报告，其 income_data / expense_data / kpi_data 是全口径算出来的——历史报告不重新
// 生成（要调 LLM、且会改写历史），改为按存档自己的 data_scope 选文案。

// legacyKPIJSON 015 之前落库的 kpi_data：savings_rate 就是全口径结余率，
// 根本没有 savings_rate_all_scope 这个键（那是口径拆分后才加的）。
const legacyKPIJSON = `{"kpi":{"savings_rate":0.2591,"discretion_ratio":0.31}}`

// dailyKPIJSON 口径拆分之后的 kpi_data：两档结余率都在
const dailyKPIJSON = `{"kpi":{"savings_rate":0.8594,"savings_rate_all_scope":0.2591,"discretion_ratio":0.42}}`

// renderReportsPage 用给定的存档渲染 /reports 整页
func renderReportsPage(t *testing.T, rep domain.AIReport) string {
	t.Helper()
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	sel := buildReportViewModel(rep)
	vm := reportsVM{
		pageBase:       pageBase{Title: "AI 财报", Nav: "reports"},
		Selected:       &sel,
		SelectedPeriod: rep.Period,
		PeriodOptions:  []string{rep.Period},
	}
	rec := httptest.NewRecorder()
	if err := renderer.RenderPage(rec, "reports", vm); err != nil {
		t.Fatalf("RenderPage() error = %v", err)
	}
	return rec.Body.String()
}

// chip 渲染出来的 chip 前缀片段。必须连着 `class="chip">` 一起匹配：
// 「收入」是「日常收入」的子串，只找中文会让两档文案互相误判为命中。
func chip(label string) string { return `class="chip">` + label + ` <b>` }

func TestReportsPageLabelsFollowStoredScope(t *testing.T) {
	tests := []struct {
		name    string
		scope   domain.Scope
		kpiJSON string
		want    []string // 必须出现
		notWant []string // 必须不出现
	}{
		{
			name:    "新报告（data_scope=daily）用日常口径文案",
			scope:   domain.ScopeDaily,
			kpiJSON: dailyKPIJSON,
			want: []string{
				chip("日常收入"), chip("日常支出"), chip("日常结余率"),
				chip("自由裁量占日常支出"),
				chip("全口径结余率"), // 日常存档才额外挂这一档
				"85.9%", "25.9%",
				"已剔除装修/购车这类专项开支",
			},
			notWant: []string{
				chip("收入"), chip("支出"), chip("结余率"), chip("自由裁量占比"),
				"生成于口径拆分之前",
			},
		},
		{
			name:    "旧存档（data_scope=all）用全口径文案",
			scope:   domain.ScopeAll,
			kpiJSON: legacyKPIJSON,
			want: []string{
				chip("收入"), chip("支出"), chip("结余率"), chip("自由裁量占比"),
				"25.9%",
				"本报告生成于口径拆分之前", "全口径（含装修/购车这类专项开支）",
			},
			notWant: []string{
				chip("日常收入"), chip("日常支出"), chip("日常结余率"),
				chip("自由裁量占日常支出"),
				chip("全口径结余率"), // 主结余率本身就是全口径，再挂一档是重复
				"已剔除装修/购车这类专项开支",
			},
		},
		{
			name:    "data_scope 为空的旧行同样按全口径渲染",
			scope:   domain.Scope(""),
			kpiJSON: legacyKPIJSON,
			want: []string{
				chip("收入"), chip("支出"), chip("结余率"),
				"本报告生成于口径拆分之前",
			},
			notWant: []string{chip("日常收入"), chip("日常支出"), chip("日常结余率")},
		},
		{
			name:    "口径拆分前的存档即便 kpi_data 里混进了全口径结余率也不重复挂 chip",
			scope:   domain.ScopeAll,
			kpiJSON: dailyKPIJSON,
			want:    []string{chip("结余率"), "本报告生成于口径拆分之前"},
			notWant: []string{chip("全口径结余率")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := renderReportsPage(t, domain.AIReport{
				ID: "rep-1", Period: "2026Q2", PeriodType: domain.PeriodQuarterly,
				DataScope:   tt.scope,
				IncomeData:  `{"total_fen":30000000}`,
				ExpenseData: `{"total_fen":4218000}`,
				KPIData:     tt.kpiJSON,
				AIAnalysis:  `{"summary":"本期结余良好。"}`,
				AIModel:     "test-model", Status: "final",
			})
			for _, w := range tt.want {
				if !strings.Contains(body, w) {
					t.Fatalf("页面缺少 %q\n%s", w, body)
				}
			}
			for _, n := range tt.notWant {
				if strings.Contains(body, n) {
					t.Fatalf("页面不该出现 %q（口径文案串档）\n%s", n, body)
				}
			}
			// 无论哪一档，金额本身都照存储值原样渲染，不做任何换算
			for _, amount := range []string{"300,000.00", "42,180.00"} {
				if !strings.Contains(body, amount) {
					t.Fatalf("页面缺少金额 %q\n%s", amount, body)
				}
			}
		})
	}
}

// TestReportViewModelScopeLabels 视图模型这一层直接钉住口径 → 文案的映射，
// 免得将来有人只改模板、忘了两档要一起改。
func TestReportViewModelScopeLabels(t *testing.T) {
	tests := []struct {
		name           string
		scope          domain.Scope
		wantIncome     string
		wantLegacy     bool
		wantAllScope   bool
		kpiSavingsAll  float64
		wantSavingsFmt string
	}{
		{"daily", domain.ScopeDaily, "日常收入", false, true, 0.2591, "日常结余率"},
		{"daily 且无全口径结余率", domain.ScopeDaily, "日常收入", false, false, 0, "日常结余率"},
		{"all", domain.ScopeAll, "收入", true, false, 0.2591, "结余率"},
		{"空值", domain.Scope(""), "收入", true, false, 0, "结余率"},
		{"未知值", domain.Scope("bogus"), "收入", true, false, 0, "结余率"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kpi := `{"kpi":{"savings_rate":0.5}}`
			if tt.kpiSavingsAll != 0 {
				kpi = `{"kpi":{"savings_rate":0.5,"savings_rate_all_scope":0.2591}}`
			}
			vm := buildReportViewModel(domain.AIReport{DataScope: tt.scope, KPIData: kpi})
			if vm.Labels.Income != tt.wantIncome {
				t.Fatalf("Labels.Income = %q; want %q", vm.Labels.Income, tt.wantIncome)
			}
			if vm.Labels.SavingsRate != tt.wantSavingsFmt {
				t.Fatalf("Labels.SavingsRate = %q; want %q", vm.Labels.SavingsRate, tt.wantSavingsFmt)
			}
			if vm.Labels.Legacy != tt.wantLegacy {
				t.Fatalf("Labels.Legacy = %v; want %v", vm.Labels.Legacy, tt.wantLegacy)
			}
			if vm.ShowAllScopeRate != tt.wantAllScope {
				t.Fatalf("ShowAllScopeRate = %v; want %v", vm.ShowAllScopeRate, tt.wantAllScope)
			}
		})
	}
}
