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
