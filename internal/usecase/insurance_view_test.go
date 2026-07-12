package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
)

var insNow = time.Date(2026, 7, 12, 9, 0, 0, 0, time.Local)

func TestNextRenewal(t *testing.T) {
	today := time.Date(2026, 7, 12, 0, 0, 0, 0, time.Local)
	cases := []struct {
		name    string
		renewal time.Time
		want    string
		wantOK  bool
	}{
		{"今年未到滚到今年", time.Date(2020, 9, 1, 0, 0, 0, 0, time.Local), "2026-09-01", true},
		{"今年已过滚到明年", time.Date(2020, 3, 15, 0, 0, 0, 0, time.Local), "2027-03-15", true},
		{"恰好今天算今天", time.Date(2020, 7, 12, 0, 0, 0, 0, time.Local), "2026-07-12", true},
		{"无续期日", time.Time{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, ok := NextRenewal(domain.InsurancePolicy{RenewalDate: tc.renewal}, today)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if ok && next.Format("2006-01-02") != tc.want {
				t.Fatalf("next = %s; want %s", next.Format("2006-01-02"), tc.want)
			}
		})
	}
}

func TestInsuranceViewRenewalFinding(t *testing.T) {
	cases := []struct {
		name        string
		policies    []domain.InsurancePolicy
		wantFinding bool
		wantSub     string
	}{
		{
			name: "90 天内续期出 finding",
			policies: []domain.InsurancePolicy{
				{ID: "p1", InsuredPerson: "男主", InsuranceType: "重疾险", AnnualPremium: 1200000,
					RenewalDate: time.Date(2020, 8, 1, 0, 0, 0, 0, time.Local)}, // 每年 8-01，20 天内
			},
			wantFinding: true, wantSub: "男主重疾险",
		},
		{
			name: "超过 90 天不提醒",
			policies: []domain.InsurancePolicy{
				{ID: "p1", InsuranceType: "寿险", RenewalDate: time.Date(2020, 12, 30, 0, 0, 0, 0, time.Local)},
			},
			wantFinding: false,
		},
		{
			name:        "无保单不提醒",
			policies:    nil,
			wantFinding: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := NewInsuranceView(&fakeProfileRepo{policies: tc.policies}, &fakeProfileRepo{})
			uc.now = func() time.Time { return insNow }
			findings, err := uc.Findings(context.Background(), domain.Period{})
			if err != nil {
				t.Fatalf("Findings() error = %v", err)
			}
			if tc.wantFinding {
				if len(findings) != 1 || findings[0].Key != "insurance_renewal_due" {
					t.Fatalf("findings = %+v; want insurance_renewal_due", findings)
				}
				if !strings.Contains(findings[0].Text, tc.wantSub) {
					t.Fatalf("text = %q; want substring %q", findings[0].Text, tc.wantSub)
				}
			} else if len(findings) != 0 {
				t.Fatalf("findings = %+v; want none", findings)
			}
		})
	}
}

func TestInsuranceViewLoadTotalsAndCheck(t *testing.T) {
	repo := &fakeProfileRepo{
		profile: &domain.FamilyProfile{AnnualIncomeFen: 40000000},
		policies: []domain.InsurancePolicy{
			{ID: "p1", InsuranceType: "定期寿险", CoverageAmount: 400000000, AnnualPremium: 1000000},
			{ID: "p2", InsuranceType: "重疾险", AnnualPremium: 1500000},
		},
	}
	uc := NewInsuranceView(repo, repo)
	uc.now = func() time.Time { return insNow }
	data, err := uc.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if data.TotalPremiumFen != 2500000 {
		t.Fatalf("TotalPremiumFen = %d; want 2500000", data.TotalPremiumFen)
	}
	if data.Check.Status != BucketOK || !data.Check.LifeCoverageOK {
		t.Fatalf("Check = %+v; want 双十达标", data.Check)
	}
}
