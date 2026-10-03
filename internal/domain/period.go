package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type PeriodType string

const (
	PeriodMonthly   PeriodType = "monthly"
	PeriodQuarterly PeriodType = "quarterly"
	PeriodAnnual    PeriodType = "annual"
)

type Period struct {
	Label string // "2025Q3" or "2025"
	Type  PeriodType
	Start time.Time
	End   time.Time // 独占，即 < End
}

// ParsePeriod 接受 "2025Q3" / "2025" / "2025-08"
func ParsePeriod(label string) (Period, error) {
	label = strings.TrimSpace(label)
	if len(label) == 0 {
		return Period{}, fmt.Errorf("empty period label")
	}
	if strings.Contains(label, "Q") {
		parts := strings.Split(label, "Q")
		if len(parts) != 2 {
			return Period{}, fmt.Errorf("invalid quarter label: %s", label)
		}
		year, err := strconv.Atoi(parts[0])
		if err != nil {
			return Period{}, fmt.Errorf("invalid year: %w", err)
		}
		q, err := strconv.Atoi(parts[1])
		if err != nil || q < 1 || q > 4 {
			return Period{}, fmt.Errorf("invalid quarter: %s", parts[1])
		}
		startMonth := time.Month((q-1)*3 + 1)
		start := time.Date(year, startMonth, 1, 0, 0, 0, 0, time.Local)
		end := start.AddDate(0, 3, 0)
		return Period{Label: label, Type: PeriodQuarterly, Start: start, End: end}, nil
	}
	if strings.Contains(label, "-") {
		parts := strings.Split(label, "-")
		if len(parts) != 2 {
			return Period{}, fmt.Errorf("invalid month label: %s", label)
		}
		year, err := strconv.Atoi(parts[0])
		if err != nil {
			return Period{}, fmt.Errorf("invalid year: %w", err)
		}
		m, err := strconv.Atoi(parts[1])
		if err != nil || m < 1 || m > 12 {
			return Period{}, fmt.Errorf("invalid month: %s", parts[1])
		}
		start := time.Date(year, time.Month(m), 1, 0, 0, 0, 0, time.Local)
		end := start.AddDate(0, 1, 0)
		return Period{Label: label, Type: PeriodMonthly, Start: start, End: end}, nil
	}
	year, err := strconv.Atoi(label)
	if err != nil {
		return Period{}, fmt.Errorf("invalid year: %w", err)
	}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(1, 0, 0)
	return Period{Label: label, Type: PeriodAnnual, Start: start, End: end}, nil
}

// CurrentMonth 返回当前月的 Period
func CurrentMonth(now time.Time) Period {
	label := fmt.Sprintf("%04d-%02d", now.Year(), now.Month())
	p, _ := ParsePeriod(label)
	return p
}

// CurrentQuarter 返回当前季度的 Period
func CurrentQuarter(now time.Time) Period {
	q := (int(now.Month())-1)/3 + 1
	label := fmt.Sprintf("%dQ%d", now.Year(), q)
	p, _ := ParsePeriod(label)
	return p
}

// CurrentYear 返回当前年度的 Period
func CurrentYear(now time.Time) Period {
	label := strconv.Itoa(now.Year())
	p, _ := ParsePeriod(label)
	return p
}

// PreviousPeriod 返回上一期
func (p Period) Previous() Period {
	switch p.Type {
	case PeriodMonthly:
		prevStart := p.Start.AddDate(0, -1, 0)
		label := fmt.Sprintf("%04d-%02d", prevStart.Year(), prevStart.Month())
		return Period{Label: label, Type: PeriodMonthly, Start: prevStart, End: p.Start}
	case PeriodQuarterly:
		prevStart := p.Start.AddDate(0, -3, 0)
		q := (int(prevStart.Month())-1)/3 + 1
		label := fmt.Sprintf("%dQ%d", prevStart.Year(), q)
		return Period{Label: label, Type: PeriodQuarterly, Start: prevStart, End: p.Start}
	case PeriodAnnual:
		prevStart := p.Start.AddDate(-1, 0, 0)
		label := fmt.Sprintf("%d", prevStart.Year())
		return Period{Label: label, Type: PeriodAnnual, Start: prevStart, End: p.Start}
	}
	return Period{}
}

// Next 返回下一期（Shift(1)）
func (p Period) Next() Period { return p.Shift(1) }

// Shift 平移 n 期（n 可为负），跨年自动进位；零值或未知 Type 返回零值 Period。
// Shift(-1) 与 Previous() 完全等价。
func (p Period) Shift(n int) Period {
	if p.Start.IsZero() {
		return Period{}
	}
	var start time.Time
	var label string
	switch p.Type {
	case PeriodMonthly:
		start = p.Start.AddDate(0, n, 0)
		label = fmt.Sprintf("%04d-%02d", start.Year(), start.Month())
		return Period{Label: label, Type: PeriodMonthly, Start: start, End: start.AddDate(0, 1, 0)}
	case PeriodQuarterly:
		start = p.Start.AddDate(0, 3*n, 0)
		q := (int(start.Month())-1)/3 + 1
		label = fmt.Sprintf("%dQ%d", start.Year(), q)
		return Period{Label: label, Type: PeriodQuarterly, Start: start, End: start.AddDate(0, 3, 0)}
	case PeriodAnnual:
		start = p.Start.AddDate(n, 0, 0)
		label = fmt.Sprintf("%d", start.Year())
		return Period{Label: label, Type: PeriodAnnual, Start: start, End: start.AddDate(1, 0, 0)}
	}
	return Period{}
}

// DisplayLabel 中文可读标签：2025年7月 / 2025年第三季度 / 2025年
func (p Period) DisplayLabel() string {
	if p.Start.IsZero() {
		return ""
	}
	switch p.Type {
	case PeriodMonthly:
		return fmt.Sprintf("%d年%d月", p.Start.Year(), int(p.Start.Month()))
	case PeriodQuarterly:
		q := (int(p.Start.Month())-1)/3 + 1
		return fmt.Sprintf("%d年第%s季度", p.Start.Year(), [...]string{"", "一", "二", "三", "四"}[q])
	case PeriodAnnual:
		return fmt.Sprintf("%d年", p.Start.Year())
	}
	return p.Label
}
