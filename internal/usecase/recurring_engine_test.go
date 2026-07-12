package usecase

import (
	"testing"
	"time"

	"family-finances/internal/domain"
)

func rtx(counterparty, desc string, amountFen int64, date string) domain.Transaction {
	d, _ := time.ParseInLocation("2006-01-02", date, time.Local)
	return domain.Transaction{
		Counterparty: counterparty, Description: desc, Amount: amountFen,
		OccurredAt: d, Direction: domain.DirectionExpense, Status: domain.TxStatusConfirmed,
	}
}

func TestDetectRecurring(t *testing.T) {
	cases := []struct {
		name        string
		txs         []domain.Transaction
		wantCount   int
		wantCadence RecurringCadence
		wantAmount  int64
		wantNext    string
	}{
		{
			name: "月供 3 次识别为 monthly",
			txs: []domain.Transaction{
				rtx("建设银行", "房贷", 800000, "2026-04-15"),
				rtx("建设银行", "房贷", 800000, "2026-05-15"),
				rtx("建设银行", "房贷", 800000, "2026-06-15"),
			},
			wantCount: 1, wantCadence: CadenceMonthly, wantAmount: 800000, wantNext: "2026-07-15",
		},
		{
			name: "月项仅 2 次不识别",
			txs: []domain.Transaction{
				rtx("物业", "物业费", 30000, "2026-05-01"),
				rtx("物业", "物业费", 30000, "2026-06-01"),
			},
			wantCount: 0,
		},
		{
			name: "金额波动超过 5% 拒绝",
			txs: []domain.Transaction{
				rtx("超市", "购物", 100000, "2026-04-10"),
				rtx("超市", "购物", 112000, "2026-05-10"),
				rtx("超市", "购物", 100000, "2026-06-10"),
			},
			wantCount: 0,
		},
		{
			name: "金额波动 ≤5% 接受（边界）",
			txs: []domain.Transaction{
				rtx("话费", "手机充值", 10000, "2026-04-10"),
				rtx("话费", "手机充值", 10200, "2026-05-10"),
				rtx("话费", "手机充值", 10000, "2026-06-10"),
			},
			wantCount: 1, wantCadence: CadenceMonthly, wantAmount: 10066, wantNext: "2026-07-10",
		},
		{
			name: "季付 3 次识别为 quarterly",
			txs: []domain.Transaction{
				rtx("保险公司", "车险分期", 150000, "2025-12-20"),
				rtx("保险公司", "车险分期", 150000, "2026-03-20"),
				rtx("保险公司", "车险分期", 150000, "2026-06-18"),
			},
			wantCount: 1, wantCadence: CadenceQuarterly, wantAmount: 150000, wantNext: "2026-09-18",
		},
		{
			name: "年付 2 次即识别为 yearly",
			txs: []domain.Transaction{
				rtx("云服务", "域名续费", 6500, "2025-06-01"),
				rtx("云服务", "域名续费", 6500, "2026-06-01"),
			},
			wantCount: 1, wantCadence: CadenceYearly, wantAmount: 6500, wantNext: "2027-06-01",
		},
		{
			name: "间隔不规律拒绝",
			txs: []domain.Transaction{
				rtx("餐厅", "聚餐", 20000, "2026-04-01"),
				rtx("餐厅", "聚餐", 20000, "2026-04-20"),
				rtx("餐厅", "聚餐", 20000, "2026-06-15"),
			},
			wantCount: 0,
		},
		{
			name: "无对手方时按描述前缀分组",
			txs: []domain.Transaction{
				rtx("", "视频会员自动续费2026-04", 2500, "2026-04-05"),
				rtx("", "视频会员自动续费2026-05", 2500, "2026-05-05"),
				rtx("", "视频会员自动续费2026-06", 2500, "2026-06-05"),
			},
			wantCount: 1, wantCadence: CadenceMonthly, wantAmount: 2500, wantNext: "2026-07-05",
		},
		{
			name: "非 confirmed 或收入行被忽略",
			txs: []domain.Transaction{
				rtx("房东", "房租", 300000, "2026-04-01"),
				rtx("房东", "房租", 300000, "2026-05-01"),
				{Counterparty: "房东", Description: "房租", Amount: 300000,
					OccurredAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local),
					Direction:  domain.DirectionExpense, Status: domain.TxStatusExcluded},
			},
			wantCount: 0, // 第三条被排除后仅 2 次
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := DetectRecurring(tc.txs)
			if len(items) != tc.wantCount {
				t.Fatalf("DetectRecurring() = %+v; want %d items", items, tc.wantCount)
			}
			if tc.wantCount == 0 {
				return
			}
			it := items[0]
			if it.Cadence != tc.wantCadence || it.AmountFen != tc.wantAmount {
				t.Fatalf("item = %+v; want cadence=%s amount=%d", it, tc.wantCadence, tc.wantAmount)
			}
			if it.NextDate.Format("2006-01-02") != tc.wantNext {
				t.Fatalf("NextDate = %s; want %s", it.NextDate.Format("2006-01-02"), tc.wantNext)
			}
		})
	}
}

func TestProjectCashflow(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.Local)
	items := []RecurringItem{
		{Name: "房贷", Cadence: CadenceMonthly, AmountFen: 800000,
			NextDate: time.Date(2026, 7, 15, 0, 0, 0, 0, time.Local)},
	}
	policies := []domain.InsurancePolicy{
		{InsuredPerson: "男主", InsuranceType: "重疾险", AnnualPremium: 1200000,
			RenewalDate: time.Date(2020, 8, 1, 0, 0, 0, 0, time.Local)}, // 每年 8-01
		{InsuredPerson: "女主", InsuranceType: "寿险", AnnualPremium: 500000,
			RenewalDate: time.Date(2020, 12, 1, 0, 0, 0, 0, time.Local)}, // 90 天外
	}
	months := ProjectCashflow(items, policies, 3000000, now)

	if len(months) != 4 { // 7/8/9/10 月（90 天 = 10-10）
		t.Fatalf("months = %d (%+v); want 4", len(months), months)
	}
	if months[0].Month != "2026-07" || months[0].OutflowFen != 800000 || months[0].NetFen != 2200000 {
		t.Fatalf("month[0] = %+v; want 7 月流出 8000 净 22000", months[0])
	}
	// 8 月：房贷 8000 + 重疾续期 12000
	if months[1].OutflowFen != 2000000 {
		t.Fatalf("month[1] = %+v; want outflow 20000 元", months[1])
	}
	foundInsurance := false
	for _, e := range months[1].Entries {
		if e.Kind == "insurance" && e.AmountFen == 1200000 {
			foundInsurance = true
		}
	}
	if !foundInsurance {
		t.Fatalf("month[1].Entries = %+v; want insurance entry", months[1].Entries)
	}
	// 12-01 的寿险续期在 90 天窗口外，不应出现
	for _, m := range months {
		for _, e := range m.Entries {
			if e.AmountFen == 500000 {
				t.Fatalf("寿险续期不应进入 90 天窗口: %+v", e)
			}
		}
	}
	// 10 月只覆盖到 10-10：15 日的房贷不应计入
	last := months[len(months)-1]
	if last.OutflowFen != 0 {
		t.Fatalf("month[3] = %+v; want no outflow (房贷 10-15 在窗口外)", last)
	}
}
