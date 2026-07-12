package domain

import "time"

// 风险偏好
const (
	RiskConservative = "conservative" // 保守
	RiskBalanced     = "balanced"     // 平衡
	RiskAggressive   = "aggressive"   // 进取
)

// 收入稳定性
const (
	IncomeStable   = "stable"   // 稳定（体制内/长约）
	IncomeMedium   = "medium"   // 一般
	IncomeUnstable = "unstable" // 不稳定（自由职业/周期行业）
)

// 投资年限
const (
	HorizonShort  = "short"  // < 3 年
	HorizonMedium = "medium" // 3–10 年
	HorizonLong   = "long"   // > 10 年
)

// FamilyProfile 家庭风险画像（family_profile 单行表，id 固定 'default'）
type FamilyProfile struct {
	ID                 string
	FamilyStructure    string // 家庭结构：单身/夫妻/夫妻+子女/三代同堂（自由文本存储）
	MainAge            int    // 主要收入者年龄
	IncomeStability    string // IncomeStable | IncomeMedium | IncomeUnstable
	AnnualIncomeFen    int64  // 家庭年收入（分）
	MortgageMonthlyFen int64  // 月供（分）
	MonthlyExpenseFen  *int64 // 月支出（分）；nil = 用流水自动折算
	EmergencyMonths    int    // 应急金目标月数
	RiskAppetite       string // RiskConservative | RiskBalanced | RiskAggressive
	Horizon            string // HorizonShort | HorizonMedium | HorizonLong
	UpdatedAt          time.Time
}

// DefaultFamilyProfile 未填画像时的缺省值
func DefaultFamilyProfile() FamilyProfile {
	return FamilyProfile{
		ID:              "default",
		MainAge:         35,
		IncomeStability: IncomeMedium,
		EmergencyMonths: 6,
		RiskAppetite:    RiskBalanced,
		Horizon:         HorizonMedium,
	}
}

// CalcProfileGrade 是纯函数：由画像计算风险等级 C1..C5 与权益占比上限（%）。
// 评分规则（docs/dev-design.md §6）：
//
//	偏好  conservative=0 / balanced=3 / aggressive=6
//	年限  short=0 / medium=1 / long=2
//	稳定性 unstable=0 / medium=1 / stable=2
//	年龄惩罚 <40:0，40–49:-1，50–59:-2，>=60:-3
//
// 总分（截断到 >=0）映射：0–1→C1，2–3→C2，4–5→C3，6–7→C4，>=8→C5。
// equityCapPct：C1=20，C2=35，C3=50，C4=65，C5=80（权益占可投资资产上限）。
func CalcProfileGrade(p FamilyProfile) (grade string, equityCapPct int) {
	score := 0
	switch p.RiskAppetite {
	case RiskAggressive:
		score += 6
	case RiskBalanced:
		score += 3
	default: // conservative 或未知值按最保守处理
	}
	switch p.Horizon {
	case HorizonLong:
		score += 2
	case HorizonMedium:
		score += 1
	default: // short
	}
	switch p.IncomeStability {
	case IncomeStable:
		score += 2
	case IncomeMedium:
		score += 1
	default: // unstable
	}
	switch {
	case p.MainAge >= 60:
		score -= 3
	case p.MainAge >= 50:
		score -= 2
	case p.MainAge >= 40:
		score -= 1
	}
	if score < 0 {
		score = 0
	}

	switch {
	case score <= 1:
		return "C1", 20
	case score <= 3:
		return "C2", 35
	case score <= 5:
		return "C3", 50
	case score <= 7:
		return "C4", 65
	default:
		return "C5", 80
	}
}
