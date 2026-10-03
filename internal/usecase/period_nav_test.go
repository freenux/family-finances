package usecase

import (
	"errors"
	"testing"
	"time"

	"family-finances/internal/domain"
)

func navAt(y int, m time.Month, d int) PeriodNav {
	return PeriodNav{Now: func() time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.Local) }}
}

func TestPeriodNavDefault(t *testing.T) {
	tests := []struct {
		name string
		now  PeriodNav
		typ  domain.PeriodType
		want string
	}{
		{"季度：2025-08 默认上季度 Q2", navAt(2025, 8, 15), domain.PeriodQuarterly, "2025Q2"},
		{"季度跨年：2025-02 默认 2024Q4", navAt(2025, 2, 1), domain.PeriodQuarterly, "2024Q4"},
		{"月度：8 月默认 7 月", navAt(2025, 8, 15), domain.PeriodMonthly, "2025-07"},
		{"月度跨年：1 月默认去年 12 月", navAt(2025, 1, 3), domain.PeriodMonthly, "2024-12"},
		{"年度：默认去年", navAt(2025, 8, 15), domain.PeriodAnnual, "2024"},
		{"非法 type 按季度处理", navAt(2025, 8, 15), domain.PeriodType("weekly"), "2025Q2"},
		{"空 type 按季度处理", navAt(2025, 8, 15), domain.PeriodType(""), "2025Q2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.now.Default(tt.typ).Label; got != tt.want {
				t.Errorf("Default(%q) = %s; want %s（默认周期是上一个完整周期）", tt.typ, got, tt.want)
			}
		})
	}
}

func TestPeriodNavForRuleViewIsCurrentQuarterNotDefault(t *testing.T) {
	n := navAt(2025, 8, 15)
	if got := n.ForRuleView().Label; got != "2025Q3" {
		t.Errorf("ForRuleView() = %s; want 2025Q3（当前季度，才盖得住当月刚导入的流水）", got)
	}
	if n.ForRuleView().Label == n.Default(domain.PeriodQuarterly).Label {
		t.Errorf("ForRuleView 与 Default 不该相同：Default 是上一个完整季度")
	}
}

// TestPeriodNavResolve 与 handler 的 period_query_test.go 等价（那边一个字没改，这里在 usecase 层再钉一遍）：
// type 与 period 对不上 → 退回该 type 的默认周期，不采用 period 自带的粒度。
func TestPeriodNavResolve(t *testing.T) {
	n := navAt(2025, 8, 15) // 默认：上月 2025-07 / 上季 2025Q2 / 去年 2024
	tests := []struct {
		name, typ, period string
		defaultType       domain.PeriodType
		want              string
		wantType          domain.PeriodType
		wantInvalid       bool
	}{
		{"都没给：取 defaultType 的默认（季度）", "", "", domain.PeriodQuarterly, "2025Q2", domain.PeriodQuarterly, false},
		{"都没给：取 defaultType 的默认（月度）", "", "", domain.PeriodMonthly, "2025-07", domain.PeriodMonthly, false},
		{"只有 type：走该 type 的默认，不看 defaultType", "annual", "", domain.PeriodMonthly, "2024", domain.PeriodAnnual, false},
		{"非法 type 无 period：按季度", "bogus", "", domain.PeriodMonthly, "2025Q2", domain.PeriodQuarterly, false},
		{"type 与 period 匹配：原样采用_月", "monthly", "2026-03", domain.PeriodQuarterly, "2026-03", domain.PeriodMonthly, false},
		{"type 与 period 匹配：原样采用_季", "quarterly", "2026Q1", domain.PeriodMonthly, "2026Q1", domain.PeriodQuarterly, false},
		{"type 与 period 匹配：原样采用_年", "annual", "2024", domain.PeriodMonthly, "2024", domain.PeriodAnnual, false},
		{"只传 period 且与 defaultType 匹配：采用", "", "2026-03", domain.PeriodMonthly, "2026-03", domain.PeriodMonthly, false},
		{"只传 period 但与 defaultType 对不上：退回 defaultType 的默认", "", "2026Q2", domain.PeriodMonthly, "2025-07", domain.PeriodMonthly, false},
		{"period 与显式 type 对不上：退回该 type 的默认而非 defaultType", "annual", "2026Q3", domain.PeriodMonthly, "2024", domain.PeriodAnnual, false},
		{"季度 type 配月度 period：退回季度默认", "quarterly", "2025-03", domain.PeriodMonthly, "2025Q2", domain.PeriodQuarterly, false},
		{"period 前后空白容忍", "", " 2024Q1 ", domain.PeriodQuarterly, "2024Q1", domain.PeriodQuarterly, false},
		{"季度越界：ErrInvalidPeriod", "", "2025Q9", domain.PeriodQuarterly, "", "", true},
		{"乱码 period：ErrInvalidPeriod", "", "abc", domain.PeriodQuarterly, "", "", true},
		{"type 对不上的乱码也是非法，不被默认周期掩盖", "annual", "abc", domain.PeriodQuarterly, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := n.Resolve(tt.typ, tt.period, tt.defaultType)
			if tt.wantInvalid {
				if !errors.Is(err, ErrInvalidPeriod) {
					t.Fatalf("err = %v; want ErrInvalidPeriod", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got.Label != tt.want || got.Type != tt.wantType {
				t.Errorf("Resolve(%q,%q,%s) = %s/%s; want %s/%s", tt.typ, tt.period, tt.defaultType, got.Label, got.Type, tt.want, tt.wantType)
			}
		})
	}
}

func TestPeriodNavResolveForList(t *testing.T) {
	n := navAt(2025, 8, 15)
	tests := []struct {
		name, typ, period, ruleID string
		want                      string
	}{
		{"普通进入流水页：上个月", "", "", "", "2025-07"},
		{"带 rule_id：当前季度", "", "", "r-1", "2025Q3"},
		{"rule_id 空白：按普通处理", "", "", " ", "2025-07"},
		{"rule_id + 显式 period：显式优先", "monthly", "2026-03", "r-1", "2026-03"},
		{"rule_id + 只显式 type：该 type 的默认", "annual", "", "r-1", "2024"},
		{"rule_id + 只显式 period：显式优先", "", "2026-03", "r-1", "2026-03"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := n.ResolveForList(tt.typ, tt.period, tt.ruleID, domain.PeriodMonthly)
			if err != nil || got.Label != tt.want {
				t.Errorf("ResolveForList = (%s, %v); want %s", got.Label, err, tt.want)
			}
		})
	}
}

func TestPeriodNavNav(t *testing.T) {
	n := navAt(2025, 8, 15)
	tests := []struct {
		name  string
		label string
		want  PeriodNavView
	}{
		{"月度跨年向前", "2025-01", PeriodNavView{Type: "monthly", Key: "2025-01", Label: "2025年1月", Prev: "2024-12", Next: "2025-02", HasNext: true}},
		{"季度跨年向前", "2025Q1", PeriodNavView{Type: "quarterly", Key: "2025Q1", Label: "2025年第一季度", Prev: "2024Q4", Next: "2025Q2", HasNext: true}},
		{"季度跨年向后", "2024Q4", PeriodNavView{Type: "quarterly", Key: "2024Q4", Label: "2024年第四季度", Prev: "2024Q3", Next: "2025Q1", HasNext: true}},
		{"当期季度：不让翻到未来", "2025Q3", PeriodNavView{Type: "quarterly", Key: "2025Q3", Label: "2025年第三季度", Prev: "2025Q2", Next: "2025Q4", HasNext: false, IsCurrent: true}},
		{"当月", "2025-08", PeriodNavView{Type: "monthly", Key: "2025-08", Label: "2025年8月", Prev: "2025-07", Next: "2025-09", HasNext: false, IsCurrent: true}},
		{"上月：下一期是当月，可以翻", "2025-07", PeriodNavView{Type: "monthly", Key: "2025-07", Label: "2025年7月", Prev: "2025-06", Next: "2025-08", HasNext: true}},
		{"当年", "2025", PeriodNavView{Type: "annual", Key: "2025", Label: "2025年", Prev: "2024", Next: "2026", HasNext: false, IsCurrent: true}},
		{"去年", "2024", PeriodNavView{Type: "annual", Key: "2024", Label: "2024年", Prev: "2023", Next: "2025", HasNext: true}},
		{"未来周期：同样不可再翻", "2026Q1", PeriodNavView{Type: "quarterly", Key: "2026Q1", Label: "2026年第一季度", Prev: "2025Q4", Next: "2026Q2", HasNext: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := domain.ParsePeriod(tt.label)
			if err != nil {
				t.Fatal(err)
			}
			if got := n.Nav(p); got != tt.want {
				t.Errorf("Nav(%s) = %+v; want %+v", tt.label, got, tt.want)
			}
		})
	}
}
