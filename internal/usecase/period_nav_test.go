package usecase

import (
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

func TestPeriodNavResolve(t *testing.T) {
	n := navAt(2025, 8, 15)
	tests := []struct {
		name, typ, period string
		want              string
		wantErr           bool
	}{
		{"显式 period 优先于 type", "annual", "2025-03", "2025-03", false},
		{"只有 type：走默认", "monthly", "", "2025-07", false},
		{"都没给：季度默认", "", "", "2025Q2", false},
		{"非法 type 无 period：季度默认", "bogus", "", "2025Q2", false},
		{"period 前后空白容忍", "", " 2024Q1 ", "2024Q1", false},
		{"非法 period 报错", "", "2025Q9", "", true},
		{"乱码 period 报错", "", "abc", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := n.Resolve(tt.typ, tt.period)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v; wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.Label != tt.want {
				t.Errorf("Resolve(%q,%q) = %s; want %s", tt.typ, tt.period, got.Label, tt.want)
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
