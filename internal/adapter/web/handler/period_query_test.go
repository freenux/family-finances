package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"family-finances/internal/domain"
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
