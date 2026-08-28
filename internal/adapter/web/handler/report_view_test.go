package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"family-finances/internal/adapter/web"
	"family-finances/internal/domain"
)

// renderReportView 用给定的报表数据渲染 report_view 片段（kpi_cards + report_tables）
func renderReportView(t *testing.T, rep domain.ReportData) string {
	t.Helper()
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	p, err := domain.ParsePeriod("2026Q2")
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	rep.Period = p
	vm := dashboardVM{
		pageBase: pageBase{Period: p, Account: domain.AccountFamily},
		Report:   rep,
	}
	rec := httptest.NewRecorder()
	if err := renderer.RenderPartial(rec, "report_view", vm); err != nil {
		t.Fatalf("RenderPartial() error = %v", err)
	}
	return rec.Body.String()
}

// renovationReport 装修季的报表数据：日常支出 ¥42,180，专项 ¥186,000，合计 ¥228,180。
// 专项里还有一笔 ¥8,000 的收入（旧车折价/材料退款），所以按专项拆出来的净额合计
// 是 ¥178,000 而不是 ¥186,000——模板里那句"净额"说明就是为它准备的。
func renovationReport() domain.ReportData {
	return domain.ReportData{
		IncomeGroups: []domain.CategoryGroupAggregation{{
			GroupID: "income.salary", GroupName: "工资收入", Subtotal: 30800000,
			Items: []domain.CategoryAggregation{{CategoryID: "income.salary.husband", Name: "男主工资", Amount: 30800000}},
		}},
		ExpenseGroups: []domain.CategoryGroupAggregation{{
			GroupID: "expense.discretion", GroupName: "自由裁量支出", Subtotal: 22818000,
			Items: []domain.CategoryAggregation{{CategoryID: "expense.discretion.shopping", Name: "购物消费", Amount: 22818000}},
		}},
		// 按专项是净额：装修 ¥170,000 + 换车 ¥8,000 = ¥178,000
		// = 专项支出 ¥186,000 − 专项收入 ¥8,000，两边对得上账
		SpecialByProject: []domain.SpecialAggregation{
			{SpecialID: "sp-reno", Name: "老房装修", Amount: 17000000},
			{SpecialID: "sp-car", Name: "2026 换车", Amount: 800000},
		},
		KPI: domain.ReportKPI{
			TotalIncome: 30800000, DailyIncome: 30000000, SpecialIncome: 800000,
			TotalExpense: 22818000, DailyExpense: 4218000, SpecialExpense: 18600000,
			Surplus: 7982000, SurplusRate: 0.2591,
			DailySurplus: 25782000, DailySurplusRate: 0.8594,
			DiscretionRatio: 0.42, DiscretionWarning: true,
		},
	}
}

// TestReportViewShowsScopeSplit 缺陷 7：ReportData/ReportKPI 里那六个按口径拆开的字段
// 全部算了却没有任何模板消费。报表必须把支出拆成三行（日常 / 专项 / 合计），
// 专项行按专项展开，且 TotalExpense 语义不变。
func TestReportViewShowsScopeSplit(t *testing.T) {
	body := renderReportView(t, renovationReport())

	wants := []struct {
		name string
		sub  string
	}{
		{"日常支出行", "其中：日常支出"},
		{"日常支出金额", "42,180.00"},
		{"专项支出行", "其中：专项支出"},
		{"专项支出金额", "186,000.00"},
		{"支出合计仍是全口径", "228,180.00"},
		{"支出合计行文案未变", "支出合计 (B)"},
		{"专项按项目展开：老房装修", "老房装修"},
		{"专项按项目展开：换车", "2026 换车"},
		{"专项行可展开", "<details>"},
		{"净额口径写清楚", "净额"},
		{"专项收入拆行", "其中：专项收入"},
		{"日常收入拆行", "其中：日常收入"},
		{"收入合计仍是全口径", "308,000.00"},
	}
	for _, w := range wants {
		t.Run(w.name, func(t *testing.T) {
			if !strings.Contains(body, w.sub) {
				t.Fatalf("报表缺少 %q（%s）\n%s", w.sub, w.name, body)
			}
		})
	}

	// 每个专项都要能链回自己的编辑页——这就是"行上必须带专项 id"的原因
	for _, href := range []string{`href="/specials?edit=sp-reno#special-form"`, `href="/specials?edit=sp-car#special-form"`} {
		if !strings.Contains(body, href) {
			t.Fatalf("专项行缺少链接 %s\n%s", href, body)
		}
	}
}

// TestReportViewShowsDailyExpenseDenominator 缺陷 3：自由裁量占比的分子分母都换成了
// 日常口径，但文案还写着「自由裁量占比」，且 DailyExpense 从不渲染——用户没法核对。
func TestReportViewShowsDailyExpenseDenominator(t *testing.T) {
	body := renderReportView(t, renovationReport())

	if !strings.Contains(body, "自由裁量占日常支出") {
		t.Fatalf("KPI 文案没写明口径（仍是含糊的「自由裁量占比」）\n%s", body)
	}
	if !strings.Contains(body, "分母＝日常支出 42,180.00") {
		t.Fatalf("没把分母（日常支出）显示出来，用户无法反推核对\n%s", body)
	}
	if !strings.Contains(body, "42.0%") {
		t.Fatalf("自由裁量占比数值缺失\n%s", body)
	}
	// 两档结余都要给，并说明各自回答什么问题
	for _, sub := range []string{"全口径结余率", "真实进出", "日常结余", "剔除专项", "25.9%", "85.9%"} {
		if !strings.Contains(body, sub) {
			t.Fatalf("结余卡片缺少 %q\n%s", sub, body)
		}
	}
}

// TestReportViewWithoutSpecialKeepsOldLayout 没有专项时不加拆行噪音：老的展示不被破坏，
// 但日常支出这个分母仍然要在 KPI 卡片上能读到。
func TestReportViewWithoutSpecialKeepsOldLayout(t *testing.T) {
	rep := domain.ReportData{
		IncomeGroups: []domain.CategoryGroupAggregation{{
			GroupID: "income.salary", GroupName: "工资收入", Subtotal: 30000000,
			Items: []domain.CategoryAggregation{{CategoryID: "income.salary.husband", Name: "男主工资", Amount: 30000000}},
		}},
		ExpenseGroups: []domain.CategoryGroupAggregation{{
			GroupID: "expense.discretion", GroupName: "自由裁量支出", Subtotal: 4218000,
			Items: []domain.CategoryAggregation{{CategoryID: "expense.discretion.shopping", Name: "购物消费", Amount: 4218000}},
		}},
		KPI: domain.ReportKPI{
			TotalIncome: 30000000, DailyIncome: 30000000,
			TotalExpense: 4218000, DailyExpense: 4218000,
			Surplus: 25782000, SurplusRate: 0.8594,
			DailySurplus: 25782000, DailySurplusRate: 0.8594,
			DiscretionRatio: 1, DiscretionWarning: true,
		},
	}
	body := renderReportView(t, rep)

	for _, no := range []string{"其中：日常支出", "其中：专项支出", "其中：专项收入", "<details>"} {
		if strings.Contains(body, no) {
			t.Fatalf("无专项时不该出现拆行 %q\n%s", no, body)
		}
	}
	for _, want := range []string{"支出合计 (B)", "42,180.00", "全部为日常支出 42,180.00", "分母＝日常支出 42,180.00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("无专项时缺少 %q\n%s", want, body)
		}
	}
}

// TestReportViewSpecialWithoutProjectBreakdown 未注入专项仓库（SpecialByProject 为空）
// 时仍要拆出金额行，只是不可展开——不能因为拿不到明细就把整行藏起来。
func TestReportViewSpecialWithoutProjectBreakdown(t *testing.T) {
	rep := renovationReport()
	rep.SpecialByProject = nil
	body := renderReportView(t, rep)

	if !strings.Contains(body, "其中：专项支出") || !strings.Contains(body, "186,000.00") {
		t.Fatalf("拿不到按专项明细时仍应显示专项合计行\n%s", body)
	}
	if strings.Contains(body, "<details>") {
		t.Fatalf("没有明细却渲染了可展开控件\n%s", body)
	}
}
