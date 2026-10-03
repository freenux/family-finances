package domain

import "testing"

func mustPeriod(t *testing.T, label string) Period {
	t.Helper()
	p, err := ParsePeriod(label)
	if err != nil {
		t.Fatalf("ParsePeriod(%q): %v", label, err)
	}
	return p
}

func TestPeriodShift(t *testing.T) {
	tests := []struct {
		name  string
		from  string
		n     int
		want  string
		start string
	}{
		{"月度跨年向前", "2025-01", -1, "2024-12", ""},
		{"月度跨年向后", "2025-12", 1, "2026-01", ""},
		{"季度跨年向前", "2025Q1", -1, "2024Q4", ""},
		{"季度跨年向后", "2025Q4", 1, "2026Q1", ""},
		{"季度跨多期", "2025Q3", -5, "2024Q2", ""},
		{"年度前移", "2025", -1, "2024", ""},
		{"年度后移", "2025", 2, "2027", ""},
		{"零步不变", "2025Q2", 0, "2025Q2", ""},
		{"月度大跨度", "2025-03", 14, "2026-05", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustPeriod(t, tt.from).Shift(tt.n)
			if got.Label != tt.want {
				t.Fatalf("%s Shift(%d).Label = %q; want %q（跨年必须正确进位）", tt.from, tt.n, got.Label, tt.want)
			}
			want := mustPeriod(t, tt.want)
			if !got.Start.Equal(want.Start) || !got.End.Equal(want.End) || got.Type != want.Type {
				t.Fatalf("Shift 结果的 Start/End/Type = %v/%v/%v; want 与 ParsePeriod(%q) 一致 %v/%v/%v",
					got.Start, got.End, got.Type, tt.want, want.Start, want.End, want.Type)
			}
		})
	}
}

func TestPeriodShiftMinusOneEqualsPrevious(t *testing.T) {
	for _, label := range []string{"2025-01", "2025-07", "2025Q1", "2025Q3", "2025", "2000"} {
		p := mustPeriod(t, label)
		if got, want := p.Shift(-1), p.Previous(); got != want {
			t.Errorf("%s: Shift(-1) = %+v; want 与 Previous() 完全一致 %+v", label, got, want)
		}
		if got, want := p.Next(), p.Shift(1); got != want {
			t.Errorf("%s: Next() = %+v; want 等于 Shift(1) %+v", label, got, want)
		}
	}
}

func TestPeriodShiftZeroValue(t *testing.T) {
	tests := []struct {
		name string
		p    Period
	}{
		{"零值", Period{}},
		{"未知 Type", Period{Label: "x", Type: "weekly", Start: mustPeriod(t, "2025").Start}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Shift(1); got != (Period{}) {
				t.Errorf("Shift(1) = %+v; want 零值 Period（不 panic）", got)
			}
			if got := tt.p.Next(); got != (Period{}) {
				t.Errorf("Next() = %+v; want 零值 Period", got)
			}
		})
	}
}

func TestPeriodDisplayLabel(t *testing.T) {
	tests := []struct{ label, want string }{
		{"2025-07", "2025年7月"},
		{"2025-12", "2025年12月"},
		{"2025Q1", "2025年第一季度"},
		{"2025Q3", "2025年第三季度"},
		{"2025Q4", "2025年第四季度"},
		{"2025", "2025年"},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			if got := mustPeriod(t, tt.label).DisplayLabel(); got != tt.want {
				t.Errorf("DisplayLabel(%s) = %q; want %q", tt.label, got, tt.want)
			}
		})
	}
	if got := (Period{}).DisplayLabel(); got != "" {
		t.Errorf("零值 DisplayLabel = %q; want 空串", got)
	}
}
