package usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ---- SpecialView.Upsert 的入参校验 ----
//
// 这些分支是 /specials 表单唯一的服务端防线（前端只有一个 required），
// 报错文案会原样回显到页面上，所以连文案一起钉住。

func TestSpecialViewUpsertValidation(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.Local) }

	tests := []struct {
		name    string
		in      domain.SpecialProject
		wantErr string                                  // 空 = 应当写入成功
		check   func(*testing.T, domain.SpecialProject) // 成功时对落库内容的额外断言
	}{
		{
			name:    "名称必填",
			in:      domain.SpecialProject{ID: "sp-1"},
			wantErr: "请填写专项名称",
		},
		{
			name:    "只有空白的名称等同于空",
			in:      domain.SpecialProject{ID: "sp-1", Name: "   \t "},
			wantErr: "请填写专项名称",
		},
		{
			name:    "名称超长（41 字）",
			in:      domain.SpecialProject{ID: "sp-1", Name: strings.Repeat("装", 41)},
			wantErr: "专项名称过长（限 40 字）",
		},
		{
			name: "名称 40 字：卡在上限内，放行",
			in:   domain.SpecialProject{ID: "sp-1", Name: strings.Repeat("装", 40)},
		},
		{
			name:    "预算为负",
			in:      domain.SpecialProject{ID: "sp-1", Name: "装修", BudgetFen: -1},
			wantErr: "预算不能为负数",
		},
		{
			name: "预算为 0：合法，表示不设预算只做归集",
			in:   domain.SpecialProject{ID: "sp-1", Name: "装修", BudgetFen: 0},
		},
		{
			name: "结束早于开始",
			in: domain.SpecialProject{ID: "sp-1", Name: "装修",
				StartedOn: day(2026, 4, 1), EndedOn: day(2026, 3, 31)},
			wantErr: "结束日期不能早于开始日期",
		},
		{
			name: "结束等于开始：当天完事，放行",
			in: domain.SpecialProject{ID: "sp-1", Name: "装修",
				StartedOn: day(2026, 4, 1), EndedOn: day(2026, 4, 1)},
		},
		{
			name: "只填结束日期（开始为零值）：不比较，放行",
			in:   domain.SpecialProject{ID: "sp-1", Name: "装修", EndedOn: day(2026, 3, 31)},
		},
		{
			name: "只填开始日期（进行中）：不比较，放行",
			in:   domain.SpecialProject{ID: "sp-1", Name: "装修", StartedOn: day(2026, 4, 1)},
		},
		{
			name:    "缺 ID（调用方忘了生成）",
			in:      domain.SpecialProject{Name: "装修"},
			wantErr: "缺少专项 ID",
		},
		{
			name: "正常写入：名称与备注两头的空白被 trim",
			in: domain.SpecialProject{ID: "sp-1", Name: "  2026 老房装修 ", Note: "  含家电  ",
				StartedOn: day(2026, 4, 1), BudgetFen: 18000000},
			check: func(t *testing.T, got domain.SpecialProject) {
				if got.Name != "2026 老房装修" {
					t.Fatalf("落库 Name = %q; want %q", got.Name, "2026 老房装修")
				}
				if got.Note != "含家电" {
					t.Fatalf("落库 Note = %q; want %q", got.Note, "含家电")
				}
				if got.BudgetFen != 18000000 {
					t.Fatalf("落库 BudgetFen = %d; want 18000000", got.BudgetFen)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeSpecialProjectRepo{}
			uc := NewSpecialView(repo)

			p := tt.in
			err := uc.Upsert(context.Background(), &p)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Upsert() error = nil; want %q", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("Upsert() error = %q; want %q", err.Error(), tt.wantErr)
				}
				// 校验没过就绝不能落库，否则页面报着错、库里已经多了一条
				if len(repo.upserted) != 0 {
					t.Fatalf("校验失败却写库了: %+v", repo.upserted)
				}
				return
			}

			if err != nil {
				t.Fatalf("Upsert() error = %v; want nil", err)
			}
			if len(repo.upserted) != 1 {
				t.Fatalf("落库 %d 条; want 1", len(repo.upserted))
			}
			if tt.check != nil {
				tt.check(t, repo.upserted[0])
			}
		})
	}
}

// TestSpecialViewLoadNetNegative 净额为负（退款/变卖多过投入）时如实往上传，
// 不 clamp 到 0；执行率跟着为负，模板靠 NetRefunded 决定不画进度条。
func TestSpecialViewLoadNetNegative(t *testing.T) {
	const spCar = "sp-car"
	repo := &fakeSpecialProjectRepo{
		projects: []domain.SpecialProject{{ID: spCar, Name: "换车", BudgetFen: 300000}},
		spent:    map[string]int64{spCar: -50000},
		breakdown: map[string][]domain.CategoryAggregation{
			spCar: {{CategoryID: "expense.family.home_maintenance", Name: "房屋维护", Amount: -50000}},
		},
	}

	data, err := NewSpecialView(repo).Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(data.Rows) != 1 {
		t.Fatalf("len(Rows) = %d; want 1", len(data.Rows))
	}
	row := data.Rows[0]
	if row.SpentFen != -50000 {
		t.Fatalf("SpentFen = %d; want -50000（负净额不该被 clamp）", row.SpentFen)
	}
	if !row.NetRefunded() {
		t.Fatal("NetRefunded() = false; want true（净额为负）")
	}
	if row.Ratio >= 0 {
		t.Fatalf("Ratio = %v; want 负数", row.Ratio)
	}
	if row.Status != "ok" {
		t.Fatalf("Status = %q; want ok（净额为负当然没超预算）", row.Status)
	}
	if data.TotalSpentFen != -50000 {
		t.Fatalf("TotalSpentFen = %d; want -50000", data.TotalSpentFen)
	}
}

// ---- SpecialRow 的花费拆解：条数 / 毛额 / 冲抵 ----

// TestSpecialViewLoadSpendBreakdown 页面要能分开三件事：
//  1. 净额（原本就有）；
//  2. 「支出 − 冲抵 = 净」的算式，否则被退款压低的数字无从对账；
//  3. 「真的没有流水」与「有流水但净额冲平成 0」——两者金额都是 0，
//     Go 的 map 零值把它们抹平过，用户挂了两笔却被告知"还没有流水归入"。
func TestSpecialViewLoadSpendBreakdown(t *testing.T) {
	const sp = "sp-1"

	tests := []struct {
		name      string
		spend     port.SpecialSpend
		wantSpent int64
		wantGross int64
		wantOff   int64
		wantCount int
		wantHasTx bool
	}{
		{
			name:      "普通专项：只有支出，没有冲抵",
			spend:     port.SpecialSpend{GrossSpentFen: 130000, OffsetFen: 0, NetSpentFen: 130000, TxCount: 3},
			wantSpent: 130000, wantGross: 130000, wantOff: 0, wantCount: 3, wantHasTx: true,
		},
		{
			name:      "部分退款：净额 = 毛额 − 冲抵",
			spend:     port.SpecialSpend{GrossSpentFen: 140000, OffsetFen: 20000, NetSpentFen: 120000, TxCount: 4},
			wantSpent: 120000, wantGross: 140000, wantOff: 20000, wantCount: 4, wantHasTx: true,
		},
		{
			name:      "全额退款冲平：净额 0 但确实挂了两笔——不能说成「还没有流水」",
			spend:     port.SpecialSpend{GrossSpentFen: 60000, OffsetFen: 60000, NetSpentFen: 0, TxCount: 2},
			wantSpent: 0, wantGross: 60000, wantOff: 60000, wantCount: 2, wantHasTx: true,
		},
		{
			name:      "真的没有流水：条数为 0",
			spend:     port.SpecialSpend{},
			wantSpent: 0, wantGross: 0, wantOff: 0, wantCount: 0, wantHasTx: false,
		},
		{
			name:      "退回的比花掉的多：净额为负",
			spend:     port.SpecialSpend{GrossSpentFen: 200000, OffsetFen: 250000, NetSpentFen: -50000, TxCount: 2},
			wantSpent: -50000, wantGross: 200000, wantOff: 250000, wantCount: 2, wantHasTx: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeSpecialProjectRepo{
				projects:    []domain.SpecialProject{{ID: sp, Name: "2026 老房装修", BudgetFen: 180000}},
				spendDetail: map[string]port.SpecialSpend{sp: tt.spend},
			}
			data, err := NewSpecialView(repo).Load(context.Background())
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(data.Rows) != 1 {
				t.Fatalf("len(Rows) = %d; want 1", len(data.Rows))
			}
			row := data.Rows[0]
			if row.SpentFen != tt.wantSpent {
				t.Fatalf("SpentFen = %d; want %d", row.SpentFen, tt.wantSpent)
			}
			if row.GrossSpentFen != tt.wantGross {
				t.Fatalf("GrossSpentFen = %d; want %d", row.GrossSpentFen, tt.wantGross)
			}
			if row.OffsetFen != tt.wantOff {
				t.Fatalf("OffsetFen = %d; want %d", row.OffsetFen, tt.wantOff)
			}
			if row.SpentFen != row.GrossSpentFen-row.OffsetFen {
				t.Fatalf("净额 %d != 毛额 %d − 冲抵 %d（页面上那行算式会对不上）",
					row.SpentFen, row.GrossSpentFen, row.OffsetFen)
			}
			if row.TxCount != tt.wantCount {
				t.Fatalf("TxCount = %d; want %d", row.TxCount, tt.wantCount)
			}
			if row.HasTransactions() != tt.wantHasTx {
				t.Fatalf("HasTransactions() = %v; want %v", row.HasTransactions(), tt.wantHasTx)
			}
		})
	}
}

// TestSpecialViewLoadNoNPlusOne 构成表必须一次查回来：按专项逐个查是 N+1，
// 实测 /specials 每多一个专项 +17ms。查询次数不能随专项个数增长。
func TestSpecialViewLoadNoNPlusOne(t *testing.T) {
	for _, n := range []int{0, 1, 3, 10} {
		t.Run(fmt.Sprintf("%d 个专项", n), func(t *testing.T) {
			repo := &fakeSpecialProjectRepo{breakdown: map[string][]domain.CategoryAggregation{}}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("sp-%02d", i)
				repo.projects = append(repo.projects, domain.SpecialProject{ID: id, Name: id})
				repo.breakdown[id] = []domain.CategoryAggregation{
					{CategoryID: "expense.discretion.shopping", Name: "购物消费", Amount: int64(1000 * (i + 1))},
				}
			}
			data, err := NewSpecialView(repo).Load(context.Background())
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if repo.breakdownCalls != 1 {
				t.Fatalf("构成查询发了 %d 次; want 1（%d 个专项也只该查一次）", repo.breakdownCalls, n)
			}
			if len(data.Rows) != n {
				t.Fatalf("len(Rows) = %d; want %d", len(data.Rows), n)
			}
			// 分组结果要按专项 id 正确落到各自的行上，别串行
			for i, row := range data.Rows {
				want := int64(1000 * (i + 1))
				if len(row.Breakdown) != 1 || row.Breakdown[0].Amount != want {
					t.Fatalf("专项 %s 的构成 = %+v; want 单项 %d", row.Project.ID, row.Breakdown, want)
				}
			}
		})
	}
}
