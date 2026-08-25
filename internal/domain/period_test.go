package domain

import (
	"testing"
	"time"
)

// TestPeriodPrevious 覆盖三种粒度的 Previous()，重点是跨年/跨季边界。
func TestPeriodPrevious(t *testing.T) {
	tests := []struct {
		name      string
		label     string
		wantLabel string
		wantType  PeriodType
		wantStart time.Time
	}{
		{
			name:      "月度_普通_月内",
			label:     "2026-07",
			wantLabel: "2026-06",
			wantType:  PeriodMonthly,
			wantStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local),
		},
		{
			name:      "月度_跨年_1月的上一期是去年12月",
			label:     "2026-01",
			wantLabel: "2025-12",
			wantType:  PeriodMonthly,
			wantStart: time.Date(2025, 12, 1, 0, 0, 0, 0, time.Local),
		},
		{
			name:      "季度_普通_季内",
			label:     "2026Q3",
			wantLabel: "2026Q2",
			wantType:  PeriodQuarterly,
			wantStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local),
		},
		{
			name:      "季度_跨年_Q1的上一期是去年Q4",
			label:     "2026Q1",
			wantLabel: "2025Q4",
			wantType:  PeriodQuarterly,
			wantStart: time.Date(2025, 10, 1, 0, 0, 0, 0, time.Local),
		},
		{
			name:      "年度",
			label:     "2026",
			wantLabel: "2025",
			wantType:  PeriodAnnual,
			wantStart: time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParsePeriod(tt.label)
			if err != nil {
				t.Fatalf("ParsePeriod(%q) 失败: %v", tt.label, err)
			}
			prev := p.Previous()
			if prev.Label != tt.wantLabel {
				t.Fatalf("Previous().Label = %q; want %q", prev.Label, tt.wantLabel)
			}
			if prev.Type != tt.wantType {
				t.Fatalf("Previous().Type = %q; want %q", prev.Type, tt.wantType)
			}
			if !prev.Start.Equal(tt.wantStart) {
				t.Fatalf("Previous().Start = %v; want %v", prev.Start, tt.wantStart)
			}
			// End 独占，上一期的 End 必须严丝合缝接上本期的 Start，否则聚合会漏一天或重一天。
			if !prev.End.Equal(p.Start) {
				t.Fatalf("Previous().End = %v; want == 本期 Start %v", prev.End, p.Start)
			}
		})
	}
}

// TestPeriodPreviousChain 连续 Previous() 两次跨年，确认季度序列正确回卷（Q1→去年Q4→去年Q3）。
func TestPeriodPreviousChain(t *testing.T) {
	p, err := ParsePeriod("2026Q1")
	if err != nil {
		t.Fatalf("ParsePeriod 失败: %v", err)
	}
	first := p.Previous()
	if first.Label != "2025Q4" {
		t.Fatalf("第一次 Previous().Label = %q; want 2025Q4", first.Label)
	}
	second := first.Previous()
	if second.Label != "2025Q3" {
		t.Fatalf("第二次 Previous().Label = %q; want 2025Q3", second.Label)
	}
}

// TestCurrentYear 对齐 CurrentMonth/CurrentQuarter 的既有约定。
func TestCurrentYear(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 30, 0, 0, time.Local)
	p := CurrentYear(now)
	if p.Label != "2026" {
		t.Fatalf("Label = %q; want 2026", p.Label)
	}
	if p.Type != PeriodAnnual {
		t.Fatalf("Type = %q; want annual", p.Type)
	}
	wantStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	wantEnd := time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local)
	if !p.Start.Equal(wantStart) || !p.End.Equal(wantEnd) {
		t.Fatalf("Start/End = %v/%v; want %v/%v", p.Start, p.End, wantStart, wantEnd)
	}
}

// TestCurrentPeriodsPreviousIsBeforeNow 三个粒度的"当期.Previous()"应严格早于 now 所在的当期起点，
// 这是"上一个完整周期"作为默认值的语义基础：不完整的当期永远不会被选中。
func TestCurrentPeriodsPreviousIsBeforeNow(t *testing.T) {
	now := time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local) // 刻意选年初，最容易踩跨年 bug
	monthPrev := CurrentMonth(now).Previous()
	if monthPrev.Label != "2025-12" {
		t.Fatalf("monthPrev.Label = %q; want 2025-12", monthPrev.Label)
	}
	quarterPrev := CurrentQuarter(now).Previous()
	if quarterPrev.Label != "2025Q4" {
		t.Fatalf("quarterPrev.Label = %q; want 2025Q4", quarterPrev.Label)
	}
	yearPrev := CurrentYear(now).Previous()
	if yearPrev.Label != "2025" {
		t.Fatalf("yearPrev.Label = %q; want 2025", yearPrev.Label)
	}
}
