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
