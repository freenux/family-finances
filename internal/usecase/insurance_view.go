package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// InsuranceView 保单页用例：列表 + 年保费合计 + 双十诊断 + 续期提醒 finding
type InsuranceView struct {
	policyRepo  port.InsurancePolicyRepo
	profileRepo port.FamilyProfileRepo
	now         func() time.Time
}

func NewInsuranceView(policyRepo port.InsurancePolicyRepo, profileRepo port.FamilyProfileRepo) *InsuranceView {
	return &InsuranceView{policyRepo: policyRepo, profileRepo: profileRepo, now: time.Now}
}

// PolicyRenewal 一条即将续期的保单
type PolicyRenewal struct {
	Policy   domain.InsurancePolicy
	DueInDay int // 距续期日天数（0 = 今天）
}

// InsuranceViewData 页面数据
type InsuranceViewData struct {
	Policies         []domain.InsurancePolicy
	TotalPremiumFen  int64
	Check            InsuranceCheck // 复用 bucket_engine 的双十诊断
	RenewalsDueSoon  []PolicyRenewal
	RenewalWindowDay int
}

const renewalWindowDays = 90

// Load 组装页面数据
func (uc *InsuranceView) Load(ctx context.Context) (InsuranceViewData, error) {
	policies, err := uc.policyRepo.ListAllPolicies(ctx)
	if err != nil {
		return InsuranceViewData{}, err
	}
	var annualIncome int64
	if profile, err := uc.profileRepo.Get(ctx); err == nil && profile != nil {
		annualIncome = profile.AnnualIncomeFen
	} else if err != nil && err != port.ErrNotFound {
		return InsuranceViewData{}, err
	}

	data := InsuranceViewData{
		Policies:         policies,
		Check:            diagnoseInsurance(policies, annualIncome),
		RenewalsDueSoon:  renewalsDueWithin(policies, uc.now(), renewalWindowDays),
		RenewalWindowDay: renewalWindowDays,
	}
	for _, p := range policies {
		data.TotalPremiumFen += p.AnnualPremium
	}
	return data, nil
}

// renewalsDueWithin 续期日在 [now, now+days] 内的保单（renewal_date 按年滚动到下一次到期）
func renewalsDueWithin(policies []domain.InsurancePolicy, now time.Time, days int) []PolicyRenewal {
	var out []PolicyRenewal
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	deadline := today.AddDate(0, 0, days)
	for _, p := range policies {
		next, ok := NextRenewal(p, today)
		if !ok {
			continue
		}
		if !next.After(deadline) {
			out = append(out, PolicyRenewal{Policy: p, DueInDay: int(next.Sub(today).Hours() / 24)})
		}
	}
	return out
}

// NextRenewal 保单下一个缴费/续期日：renewal_date 按年滚动到 >= today 的最近一次。
// 无 renewal_date 返回 ok=false。8.3 现金流时间线复用。
func NextRenewal(p domain.InsurancePolicy, today time.Time) (time.Time, bool) {
	if p.RenewalDate.IsZero() {
		return time.Time{}, false
	}
	next := time.Date(today.Year(), p.RenewalDate.Month(), p.RenewalDate.Day(), 0, 0, 0, 0, today.Location())
	if next.Before(today) {
		next = next.AddDate(1, 0, 0)
	}
	return next, true
}

// Findings 续期提醒 finding（ContextPack FindingSource）
func (uc *InsuranceView) Findings(ctx context.Context, _ domain.Period) ([]Finding, error) {
	policies, err := uc.policyRepo.ListAllPolicies(ctx)
	if err != nil {
		return nil, err
	}
	due := renewalsDueWithin(policies, uc.now(), renewalWindowDays)
	if len(due) == 0 {
		return nil, nil
	}
	var names []string
	var total int64
	for _, d := range due {
		label := d.Policy.InsuranceType
		if d.Policy.InsuredPerson != "" {
			label = d.Policy.InsuredPerson + label
		}
		names = append(names, fmt.Sprintf("%s（%d 天内）", label, d.DueInDay))
		total += d.Policy.AnnualPremium
	}
	return []Finding{{
		Key: "insurance_renewal_due",
		Text: fmt.Sprintf("未来 %d 天内有 %d 张保单待续期：%s，涉及保费合计 %s 元",
			renewalWindowDays, len(due), strings.Join(names, "、"), formatYuanFen(total)),
	}}, nil
}
