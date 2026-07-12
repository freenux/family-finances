package domain

import "testing"

func TestCalcProfileGrade(t *testing.T) {
	cases := []struct {
		name      string
		p         FamilyProfile
		wantGrade string
		wantCap   int
	}{
		// 边界：最低分（0 分 → C1）
		{
			name:      "保守+短年限+不稳定收入 → C1",
			p:         FamilyProfile{RiskAppetite: RiskConservative, Horizon: HorizonShort, IncomeStability: IncomeUnstable, MainAge: 30},
			wantGrade: "C1", wantCap: 20,
		},
		// 边界：负分截断到 0
		{
			name:      "60 岁保守派负分截断 → C1",
			p:         FamilyProfile{RiskAppetite: RiskConservative, Horizon: HorizonShort, IncomeStability: IncomeUnstable, MainAge: 65},
			wantGrade: "C1", wantCap: 20,
		},
		// score = 1（C1 上边界）：保守 0 + 中年限 1 + 不稳定 0，年龄 30
		{
			name:      "score=1 仍是 C1",
			p:         FamilyProfile{RiskAppetite: RiskConservative, Horizon: HorizonMedium, IncomeStability: IncomeUnstable, MainAge: 30},
			wantGrade: "C1", wantCap: 20,
		},
		// score = 2（C2 下边界）：保守 0 + 中 1 + 中 1
		{
			name:      "score=2 进入 C2",
			p:         FamilyProfile{RiskAppetite: RiskConservative, Horizon: HorizonMedium, IncomeStability: IncomeMedium, MainAge: 30},
			wantGrade: "C2", wantCap: 35,
		},
		// score = 4（C3 下边界）：平衡 3 + 中 1 + 不稳定 0
		{
			name:      "score=4 进入 C3",
			p:         FamilyProfile{RiskAppetite: RiskBalanced, Horizon: HorizonMedium, IncomeStability: IncomeUnstable, MainAge: 30},
			wantGrade: "C3", wantCap: 50,
		},
		// score = 5（C3 上边界）：平衡 3 + 中 1 + 中 1
		{
			name:      "score=5 仍是 C3",
			p:         FamilyProfile{RiskAppetite: RiskBalanced, Horizon: HorizonMedium, IncomeStability: IncomeMedium, MainAge: 35},
			wantGrade: "C3", wantCap: 50,
		},
		// score = 6（C4 下边界）：平衡 3 + 长 2 + 中 1
		{
			name:      "score=6 进入 C4",
			p:         FamilyProfile{RiskAppetite: RiskBalanced, Horizon: HorizonLong, IncomeStability: IncomeMedium, MainAge: 30},
			wantGrade: "C4", wantCap: 65,
		},
		// 年龄惩罚跨档：同为进取+长+稳定，40 岁 −1 → 9 → C5；50 岁 −2 → 8 → C5；60 岁 −3 → 7 → C4
		{
			name:      "进取满分 40 岁 → C5",
			p:         FamilyProfile{RiskAppetite: RiskAggressive, Horizon: HorizonLong, IncomeStability: IncomeStable, MainAge: 40},
			wantGrade: "C5", wantCap: 80,
		},
		{
			name:      "进取满分 59 岁 → C5",
			p:         FamilyProfile{RiskAppetite: RiskAggressive, Horizon: HorizonLong, IncomeStability: IncomeStable, MainAge: 59},
			wantGrade: "C5", wantCap: 80,
		},
		{
			name:      "进取满分 60 岁年龄惩罚降档 → C4",
			p:         FamilyProfile{RiskAppetite: RiskAggressive, Horizon: HorizonLong, IncomeStability: IncomeStable, MainAge: 60},
			wantGrade: "C4", wantCap: 65,
		},
		// score = 8（C5 下边界）：进取 6 + 长 2 + 不稳定 0，39 岁无惩罚
		{
			name:      "score=8 进入 C5",
			p:         FamilyProfile{RiskAppetite: RiskAggressive, Horizon: HorizonLong, IncomeStability: IncomeUnstable, MainAge: 39},
			wantGrade: "C5", wantCap: 80,
		},
		// 未知枚举值按最保守处理
		{
			name:      "未知偏好/年限/稳定性按 0 分",
			p:         FamilyProfile{RiskAppetite: "yolo", Horizon: "forever", IncomeStability: "??", MainAge: 30},
			wantGrade: "C1", wantCap: 20,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			grade, cap := CalcProfileGrade(tc.p)
			if grade != tc.wantGrade || cap != tc.wantCap {
				t.Fatalf("CalcProfileGrade() = (%s, %d); want (%s, %d)", grade, cap, tc.wantGrade, tc.wantCap)
			}
		})
	}
}

func TestFinancialGoalYearsLowerBound(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"3", 3}, {"3-5", 3}, {"10+", 10}, {" 2 ", 2}, {"", -1}, {"abc", -1}, {"约5年", -1},
	}
	for _, tc := range cases {
		g := FinancialGoal{YearsToAchieve: tc.in}
		if got := g.YearsLowerBound(); got != tc.want {
			t.Fatalf("YearsLowerBound(%q) = %d; want %d", tc.in, got, tc.want)
		}
	}
}
