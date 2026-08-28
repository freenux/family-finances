package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"family-finances/internal/adapter/web"
	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

// TestParsePeriodFromQueryDefaults 覆盖三个页面各自的默认粒度/默认周期——当期还没走完，
// 数字有误导性，所以默认值统一取「上一个完整周期」而不是当期：
//
//	现金流表（Dashboard/PartialReport）默认粒度 quarterly；
//	收支流水（ListTransactions/ListTransactionsAPI）默认粒度 monthly。
//
// 同时覆盖 URL 显式指定 period 时不能被默认值覆盖——需求 1/2 里仪表盘→流水页的双击跳转
// 全靠这一点才能把周期带过去。
func TestParsePeriodFromQueryDefaults(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		query       string // 不含前导 '?'
		defaultType domain.PeriodType
		wantLabel   string
		wantType    domain.PeriodType
	}{
		{
			name:        "现金流表页默认_无任何参数_取上季度",
			query:       "",
			defaultType: domain.PeriodQuarterly,
			wantLabel:   domain.CurrentQuarter(now).Previous().Label,
			wantType:    domain.PeriodQuarterly,
		},
		{
			name:        "收支流水页默认_无任何参数_取上月",
			query:       "",
			defaultType: domain.PeriodMonthly,
			wantLabel:   domain.CurrentMonth(now).Previous().Label,
			wantType:    domain.PeriodMonthly,
		},
		{
			name:        "URL指定type=annual但未指定period_取上一年",
			query:       "type=annual",
			defaultType: domain.PeriodQuarterly, // URL 的 type 优先于调用方默认粒度
			wantLabel:   domain.CurrentYear(now).Previous().Label,
			wantType:    domain.PeriodAnnual,
		},
		{
			name:        "URL显式period与type都给出_不被默认值覆盖_月度",
			query:       "type=monthly&period=2026-03",
			defaultType: domain.PeriodQuarterly, // 即便调用方默认是季度，显式指定也必须原样保留
			wantLabel:   "2026-03",
			wantType:    domain.PeriodMonthly,
		},
		{
			name:        "URL显式period与type都给出_不被默认值覆盖_季度",
			query:       "type=quarterly&period=2026Q1",
			defaultType: domain.PeriodMonthly,
			wantLabel:   "2026Q1",
			wantType:    domain.PeriodQuarterly,
		},
		{
			name:        "URL显式period与type都给出_不被默认值覆盖_年度",
			query:       "type=annual&period=2024",
			defaultType: domain.PeriodMonthly,
			wantLabel:   "2024",
			wantType:    domain.PeriodAnnual,
		},
		{
			name:        "只传period不传type_period格式与defaultType匹配_直接采用",
			query:       "period=2026-03",
			defaultType: domain.PeriodMonthly,
			wantLabel:   "2026-03",
			wantType:    domain.PeriodMonthly,
		},
		{
			name:        "只传period不传type_period格式与defaultType不匹配_退回defaultType的默认周期",
			query:       "period=2026Q2", // 季度格式的 label，但 defaultType 是 monthly
			defaultType: domain.PeriodMonthly,
			wantLabel:   domain.CurrentMonth(now).Previous().Label,
			wantType:    domain.PeriodMonthly,
		},
		{
			name:        "period与显式type不匹配_退回该type的默认周期而非defaultType",
			query:       "type=annual&period=2026Q3",
			defaultType: domain.PeriodMonthly,
			wantLabel:   domain.CurrentYear(now).Previous().Label,
			wantType:    domain.PeriodAnnual,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := "/whatever"
			if tt.query != "" {
				target += "?" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			p, err := parsePeriodFromQuery(req, tt.defaultType)
			if err != nil {
				t.Fatalf("parsePeriodFromQuery 失败: %v", err)
			}
			if p.Label != tt.wantLabel {
				t.Fatalf("Label = %q; want %q", p.Label, tt.wantLabel)
			}
			if p.Type != tt.wantType {
				t.Fatalf("Type = %q; want %q", p.Type, tt.wantType)
			}
		})
	}
}

// TestStatsAPIDefaultPeriod 覆盖 StatsAPI 独立维护的那份「粒度短别名 → 默认周期」逻辑，
// 确认它现在也统一走 defaultPeriodFor、取上一个完整周期，且 URL 显式 period 不被覆盖。
func TestStatsAPIDefaultPeriod(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		query     string
		wantLabel string
	}{
		{"缺省粒度(month)_取上月", "", domain.CurrentMonth(now).Previous().Label},
		{"granularity=month_显式_取上月", "granularity=month", domain.CurrentMonth(now).Previous().Label},
		{"granularity=quarter_取上季度", "granularity=quarter", domain.CurrentQuarter(now).Previous().Label},
		{"granularity=year_取上一年", "granularity=year", domain.CurrentYear(now).Previous().Label},
		{"显式period不被覆盖", "granularity=quarter&period=2020Q1", "2020Q1"},
	}

	h := newSpecialTestHandler(newStubTxRepo())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := "/api/stats?direction=expense&account=family"
			if tt.query != "" {
				target += "&" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			rec := httptest.NewRecorder()
			h.StatsAPI(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200（body=%s）", rec.Code, rec.Body.String())
			}
			var view struct {
				Period string `json:"period"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
				t.Fatalf("解析响应失败: %v", err)
			}
			if view.Period != tt.wantLabel {
				t.Fatalf("period = %q; want %q", view.Period, tt.wantLabel)
			}
		})
	}
}

// ---- 缺陷 8：AI 财报页 / 资产快照页的默认周期必须和其它页面对齐 ----

// stubReportRepo 记录被问到的期间；不返回任何存档（走"尚无财报"分支）
type stubReportRepo struct {
	askedPeriods []string
}

func (r *stubReportRepo) Upsert(context.Context, *domain.AIReport) error { return nil }
func (r *stubReportRepo) GetByPeriod(_ context.Context, period string, _ domain.PeriodType) (*domain.AIReport, error) {
	r.askedPeriods = append(r.askedPeriods, period)
	return nil, port.ErrNotFound
}
func (r *stubReportRepo) ListAll(context.Context) ([]domain.AIReport, error) { return nil, nil }

// stubReportLLM 只需要回答 Enabled()（Reports 页面用它决定按钮是否可点）
type stubReportLLM struct{}

func (stubReportLLM) Enabled() bool { return false }
func (stubReportLLM) Complete(context.Context, string, string) (string, error) {
	return "", nil
}

// stubAssetRepo 记录被问到的期间；没有任何快照
type stubAssetRepo struct {
	askedPeriods []string
}

func (r *stubAssetRepo) Upsert(context.Context, *domain.AssetSnapshot) error { return nil }
func (r *stubAssetRepo) GetByPeriod(_ context.Context, period string) (*domain.AssetSnapshot, error) {
	r.askedPeriods = append(r.askedPeriods, period)
	return nil, port.ErrNotFound
}
func (r *stubAssetRepo) ListByPeriodAsc(context.Context, int) ([]domain.AssetSnapshot, error) {
	return nil, nil
}
func (r *stubAssetRepo) ListAll(context.Context) ([]domain.AssetSnapshot, error) { return nil, nil }

var (
	_ port.ReportRepo        = (*stubReportRepo)(nil)
	_ port.AssetSnapshotRepo = (*stubAssetRepo)(nil)
	_ usecase.ReportLLM      = stubReportLLM{}
)

// TestReportsDefaultPeriodMatchesOtherPages 修复前 /reports 默认当前季度，而现金流表
// 已经默认「上一个完整季度」：现金流表显示 2026Q2，点进 AI 财报却默认 2026Q3。
func TestReportsDefaultPeriodMatchesOtherPages(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		url       string
		wantAsked string
	}{
		{"不带 period：取上一个完整季度", "/reports", domain.CurrentQuarter(now).Previous().Label},
		{"显式 period 不被覆盖", "/reports?period=2020Q1", "2020Q1"},
		{"显式年度 period 不被覆盖", "/reports?period=2024", "2024"},
	}
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repRepo := &stubReportRepo{}
			h := &Handler{
				render:     renderer,
				reportRepo: repRepo,
				genReport:  usecase.NewGenerateReport(nil, repRepo, stubReportLLM{}, "test"),
				flash:      newFlashStore(),
				log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			rec := httptest.NewRecorder()
			h.Reports(rec, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200（body=%s）", rec.Code, rec.Body.String())
			}
			if len(repRepo.askedPeriods) != 1 || repRepo.askedPeriods[0] != tt.wantAsked {
				t.Fatalf("查询的期间 = %v; want [%s]", repRepo.askedPeriods, tt.wantAsked)
			}
		})
	}
}

// TestReportPeriodOptionsContainDefaultAndCurrent 下拉里既要有默认周期（排第一，
// 和页面缺省选中一致），也不能把仍在进行中的当前季度/年度弄丢。
func TestReportPeriodOptionsContainDefaultAndCurrent(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.Local)
	opts := reportPeriodOptions(now, "2026Q2")

	if len(opts) == 0 || opts[0] != defaultPeriodFor(domain.PeriodQuarterly, now).Label {
		t.Fatalf("opts[0] = %v; want 默认周期 %s", opts, defaultPeriodFor(domain.PeriodQuarterly, now).Label)
	}
	must := []string{
		defaultPeriodFor(domain.PeriodQuarterly, now).Label, // 上一个完整季度
		domain.CurrentQuarter(now).Label,                    // 进行中的当前季度仍可选
		defaultPeriodFor(domain.PeriodAnnual, now).Label,    // 上一个完整年度
		strconv.Itoa(now.Year()),                            // 当前年度
	}
	for _, want := range must {
		if !slices.Contains(opts, want) {
			t.Fatalf("下拉缺少 %s；opts = %v", want, opts)
		}
	}
	// 选中项一定在列表里（即使是很久以前的期间）
	old := reportPeriodOptions(now, "2019Q3")
	if !slices.Contains(old, "2019Q3") {
		t.Fatalf("selected 未被补进下拉；opts = %v", old)
	}
	// 不重复
	seen := map[string]bool{}
	for _, o := range opts {
		if seen[o] {
			t.Fatalf("下拉出现重复项 %s；opts = %v", o, opts)
		}
		seen[o] = true
	}
}

// TestAssetsDefaultPeriodMatchesReports 修复前 /assets 默认当前季度，而财报按
// 「上一个完整季度」去查快照：快照存进 2026Q3，报告查 2026Q2，净资产环比直接消失。
func TestAssetsDefaultPeriodMatchesReports(t *testing.T) {
	now := time.Now()
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	tests := []struct {
		name       string
		url        string
		wantPeriod string
	}{
		{"不带 period：取上一个完整季度", "/assets", domain.CurrentQuarter(now).Previous().Label},
		{"显式 period 不被覆盖", "/assets?period=2020Q1", "2020Q1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assetRepo := &stubAssetRepo{}
			h := &Handler{
				render:   renderer,
				assetSvc: usecase.NewAssetSnapshotService(assetRepo),
				flash:    newFlashStore(),
				log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			rec := httptest.NewRecorder()
			h.Assets(rec, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200（body=%s）", rec.Code, rec.Body.String())
			}
			if len(assetRepo.askedPeriods) == 0 || assetRepo.askedPeriods[0] != tt.wantPeriod {
				t.Fatalf("查询的期间 = %v; want 首个是 %s", assetRepo.askedPeriods, tt.wantPeriod)
			}
		})
	}
}

// TestAssetsAndReportsAgreeOnDefaultPeriod 两个页面的默认周期必须是同一个：
// 这正是"快照存 Q3、报告查 Q2"那条 bug 的根因。
func TestAssetsAndReportsAgreeOnDefaultPeriod(t *testing.T) {
	now := time.Now()
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	assetRepo := &stubAssetRepo{}
	repRepo := &stubReportRepo{}
	h := &Handler{
		render:     renderer,
		assetSvc:   usecase.NewAssetSnapshotService(assetRepo),
		reportRepo: repRepo,
		genReport:  usecase.NewGenerateReport(nil, repRepo, stubReportLLM{}, "test"),
		flash:      newFlashStore(),
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h.Assets(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets", nil))
	h.Reports(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/reports", nil))

	if len(assetRepo.askedPeriods) == 0 || len(repRepo.askedPeriods) == 0 {
		t.Fatalf("两个页面都该查一次库：assets=%v reports=%v", assetRepo.askedPeriods, repRepo.askedPeriods)
	}
	if assetRepo.askedPeriods[0] != repRepo.askedPeriods[0] {
		t.Fatalf("资产快照页默认 %s，财报页默认 %s——快照会存进一个报告不去查的季度",
			assetRepo.askedPeriods[0], repRepo.askedPeriods[0])
	}
	if want := defaultPeriodFor(domain.PeriodQuarterly, now).Label; assetRepo.askedPeriods[0] != want {
		t.Fatalf("默认周期 = %s; want %s（defaultPeriodFor）", assetRepo.askedPeriods[0], want)
	}
}

// ---- 缺陷 9：从「分类规则」页跳过来时的默认周期 ----

// TestTxListPeriodForRule 不带 type/period 的 ?rule_id= 跳转必须落到一个能覆盖住
// 待处理流水的周期。默认「上个月」会把当月刚导入的流水全排除，页面于是告诉用户
// "这条规则没匹配到任何流水"。注意也不能只把粒度换成季度——defaultPeriodFor 给的是
// 上一个完整季度，同样盖不住当月。
func TestTxListPeriodForRule(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		query     string
		wantType  domain.PeriodType
		wantLabel string
	}{
		{"普通进入流水页：仍是上个月", "", domain.PeriodMonthly, domain.CurrentMonth(now).Previous().Label},
		{"带 rule_id：改用当前季度（覆盖当月）", "rule_id=r-1", domain.PeriodQuarterly, domain.CurrentQuarter(now).Label},
		{"带 rule_id 但空白：仍按普通处理", "rule_id=%20", domain.PeriodMonthly, domain.CurrentMonth(now).Previous().Label},
		{"带 rule_id 且显式 period：显式的优先", "rule_id=r-1&type=monthly&period=2026-03", domain.PeriodMonthly, "2026-03"},
		{"带 rule_id 且只显式 type：按显式粒度的默认周期", "rule_id=r-1&type=annual", domain.PeriodAnnual, domain.CurrentYear(now).Previous().Label},
		{"带 rule_id 且只显式 period：显式的优先", "rule_id=r-1&period=2026-03", domain.PeriodMonthly, "2026-03"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := "/transactions"
			if tt.query != "" {
				target += "?" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			p, err := txListPeriod(req)
			if err != nil {
				t.Fatalf("txListPeriod 失败: %v", err)
			}
			if p.Type != tt.wantType {
				t.Fatalf("Type = %q; want %q", p.Type, tt.wantType)
			}
			if p.Label != tt.wantLabel {
				t.Fatalf("Label = %q; want %q", p.Label, tt.wantLabel)
			}
		})
	}
}

// TestRuleDefaultPeriodCoversToday 性质断言（而非字面量）：带 rule_id 时的默认周期
// 必须覆盖"现在"——刚导入、待分类的流水就落在当下，否则规则页跳过来永远是空列表。
func TestRuleDefaultPeriodCoversToday(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/transactions?rule_id=r-1", nil)
	p, err := txListPeriod(req)
	if err != nil {
		t.Fatalf("txListPeriod 失败: %v", err)
	}
	now := time.Now()
	if now.Before(p.Start) || !now.Before(p.End) {
		t.Fatalf("默认周期 %s [%s, %s) 不覆盖当前时刻——当月刚导入的流水会被排除",
			p.Label, p.Start, p.End)
	}
}

// TestListTransactionsWithRuleIDLoadsCurrentQuarter 端到端：GET /transactions?rule_id=…
// 真正向仓库要的必须是覆盖当下的那个周期。
func TestListTransactionsWithRuleIDLoadsCurrentQuarter(t *testing.T) {
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	now := time.Now()
	tests := []struct {
		name      string
		url       string
		wantLabel string
	}{
		{"带 rule_id", "/transactions?rule_id=r-1", domain.CurrentQuarter(now).Label},
		{"不带 rule_id", "/transactions", domain.CurrentMonth(now).Previous().Label},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txRepo := newStubTxRepo()
			h := &Handler{
				render:      renderer,
				txRepo:      txRepo,
				catRepo:     stubCatRepo{},
				ruleRepo:    stubRuleRepo{},
				specialView: usecase.NewSpecialView(&stubSpecialRepo{}),
				flash:       newFlashStore(),
				log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			rec := httptest.NewRecorder()
			h.ListTransactions(rec, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200（body=%s）", rec.Code, rec.Body.String())
			}
			if len(txRepo.listPeriods) != 1 || txRepo.listPeriods[0].Label != tt.wantLabel {
				t.Fatalf("List 收到周期 %v; want [%s]", txRepo.listPeriods, tt.wantLabel)
			}
		})
	}
}

// stubRuleRepo 规则仓库替身：任何 id 都能取到一条规则
type stubRuleRepo struct{}

func (stubRuleRepo) ListRules(context.Context) ([]domain.CategoryRule, error)       { return nil, nil }
func (stubRuleRepo) ListActiveRules(context.Context) ([]domain.CategoryRule, error) { return nil, nil }
func (stubRuleRepo) GetRule(_ context.Context, id string) (domain.CategoryRule, error) {
	return domain.CategoryRule{ID: id, Pattern: "山姆", PatternType: "contains", Field: "counterparty"}, nil
}
func (stubRuleRepo) InsertRule(context.Context, domain.CategoryRule) error { return nil }
func (stubRuleRepo) UpdateRule(context.Context, domain.CategoryRule) error { return nil }
func (stubRuleRepo) SetRuleActive(context.Context, string, bool) error     { return nil }
func (stubRuleRepo) DeleteRule(context.Context, string) error              { return nil }

var _ port.CategoryRuleRepo = stubRuleRepo{}
