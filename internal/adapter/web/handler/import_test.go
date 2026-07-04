package handler

import (
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

func TestImportRedirectURLUsesEarliestImportedMonth(t *testing.T) {
	got := importRedirectURL(domain.AccountWife, port.ImportResult{
		EarliestOccurredAt: time.Date(2026, 5, 20, 12, 0, 0, 0, time.Local),
	})
	want := "/transactions?account=wife&period=2026-05&type=monthly"
	if got != want {
		t.Fatalf("importRedirectURL() = %q; want %q", got, want)
	}
}

func TestImportRedirectURLWithoutImportedRowsFallsBackToDefaultPeriod(t *testing.T) {
	got := importRedirectURL(domain.AccountHusband, port.ImportResult{})
	want := "/transactions?account=husband"
	if got != want {
		t.Fatalf("importRedirectURL() = %q; want %q", got, want)
	}
}

func TestTransactionsRedirectURLUsesOccurredMonth(t *testing.T) {
	got := transactionsRedirectURL(domain.AccountHusband, time.Date(2024, 12, 1, 8, 30, 0, 0, time.Local))
	want := "/transactions?account=husband&period=2024-12&type=monthly"
	if got != want {
		t.Fatalf("transactionsRedirectURL() = %q; want %q", got, want)
	}
}

func TestParseAmountToFen(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "normal", in: "12.34", want: 1234},
		{name: "integer", in: "100", want: 10000},
		{name: "with spaces", in: " 5.5 ", want: 550},
		{name: "rounding", in: "19.999", want: 2000},
		{name: "zero", in: "0", wantErr: true},
		{name: "negative", in: "-3", wantErr: true},
		{name: "nan", in: "NaN", wantErr: true},
		{name: "inf", in: "Inf", wantErr: true},
		{name: "negative inf", in: "-Inf", wantErr: true},
		{name: "overflow", in: "1e30", wantErr: true},
		{name: "not a number", in: "abc", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAmountToFen(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAmountToFen(%q) = %d; want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAmountToFen(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseAmountToFen(%q) = %d; want %d", tt.in, got, tt.want)
			}
		})
	}
}
