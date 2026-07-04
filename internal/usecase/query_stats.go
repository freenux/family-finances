package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// StatsView 给 /api/stats 返回的结构，和前端 statsPage() 使用的叶子结构对齐。
type StatsView struct {
	Period          string             `json:"period"`
	Granularity     string             `json:"granularity"` // month|quarter|year
	Direction       string             `json:"direction"`   // income|expense
	Account         string             `json:"account"`     // husband|wife|family
	Total           int64              `json:"total"`
	Categories      []StatsCategorySeg `json:"categories"`
	MonthlyCompare  []StatsBucket      `json:"monthlyCompare"`
	QuarterlyCompare []StatsBucket     `json:"quarterlyCompare"`
	TopTransactions []StatsTopTx       `json:"topTransactions"`
}

type StatsCategorySeg struct {
	CategoryID string `json:"category_id"`
	Name       string `json:"name"`
	Amount     int64  `json:"amount"`
	Color      string `json:"color"`
}

type StatsBucket struct {
	Label  string   `json:"label"`
	Amount int64    `json:"amount"`
	Chain  *float64 `json:"chain"` // 环比：相对上一期的增长率，小数（0.12 = +12%）；基期金额为 0 时为 nil
	YoY    *float64 `json:"yoy"`   // 同比：相对去年同期的增长率，小数；基期金额为 0 时为 nil
}

type StatsTopTx struct {
	ID       string `json:"id"`
	Date     string `json:"date"`      // "05-08"
	Who      string `json:"who"`       // counterparty
	Desc     string `json:"desc"`      // description + note 组合
	Amount   int64  `json:"amount"`
	Category string `json:"category"` // 二级科目名
	Account  string `json:"account"`
}

// 分类颜色：稳定分配，前端用它画饼图 + 分类列表（Stripe 品牌色系，见 DESIGN.md）
var statsPalette = []string{
	"#533afd", "#ea2261", "#1c1e54", "#f96bee", "#9b6829",
	"#665efd", "#64748d", "#4434d4", "#b9b9f9", "#0d253d",
	"#2e2b8c", "#d9b06a",
}

type QueryStats struct {
	txRepo  port.TransactionRepo
	catRepo port.CategoryRepo
}

func NewQueryStats(tx port.TransactionRepo, cat port.CategoryRepo) *QueryStats {
	return &QueryStats{txRepo: tx, catRepo: cat}
}

// Execute 聚合一个周期（月/季/年）×方向×账户的完整统计视图。
// topLimit 传 <=0 时默认 10。
func (uc *QueryStats) Execute(ctx context.Context, p domain.Period, dir domain.Direction, account domain.Account, topLimit int) (StatsView, error) {
	if topLimit <= 0 {
		topLimit = 10
	}
	if dir != domain.DirectionIncome && dir != domain.DirectionExpense {
		return StatsView{}, fmt.Errorf("invalid direction: %s", dir)
	}

	// 1. 分类聚合
	aggs, err := uc.txRepo.AggregateByCategory(ctx, p, account)
	if err != nil {
		return StatsView{}, err
	}
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return StatsView{}, err
	}
	catByID := make(map[string]domain.Category, len(cats))
	for _, c := range cats {
		catByID[c.ID] = c
	}
	// 筛出与 direction 匹配的科目（通过一级组前缀）
	var filtered []domain.CategoryAggregation
	for _, a := range aggs {
		if a.Amount == 0 {
			continue
		}
		parent := a.ParentID
		if dir == domain.DirectionExpense && strings.HasPrefix(parent, "expense.") {
			filtered = append(filtered, a)
		} else if dir == domain.DirectionIncome && strings.HasPrefix(parent, "income.") {
			filtered = append(filtered, a)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Amount > filtered[j].Amount })

	segs := make([]StatsCategorySeg, 0, len(filtered))
	var total int64
	for i, a := range filtered {
		color := statsPalette[i%len(statsPalette)]
		segs = append(segs, StatsCategorySeg{
			CategoryID: a.CategoryID,
			Name:       a.Name,
			Amount:     a.Amount,
			Color:      color,
		})
		total += a.Amount
	}

	// 2. 月度/季度对比桶：显示 N 期；为算同比/环比，额外多查基期。
	//    月度：显示 6 个，YoY 基期取 12 个月前 → 连查 18 个月；
	//    季度：显示 4 个，YoY 基期取 4 个季度前 → 连查 8 个季度。
	const (
		monthShow = 6
		quartShow = 4
	)
	monthlyAll := buildMonthBuckets(p, monthShow+12)
	quarterlyAll := buildQuarterBuckets(p, quartShow+4)
	monthlySummed, err := uc.txRepo.SumByBuckets(ctx, monthlyAll, dir, account)
	if err != nil {
		return StatsView{}, err
	}
	quarterlySummed, err := uc.txRepo.SumByBuckets(ctx, quarterlyAll, dir, account)
	if err != nil {
		return StatsView{}, err
	}
	monthly := bucketsToStatsWithCompare(monthlySummed, monthShow, 12)
	quarterly := bucketsToStatsWithCompare(quarterlySummed, quartShow, 4)

	// 3. Top 流水
	tops, err := uc.txRepo.TopTransactions(ctx, p, dir, account, topLimit)
	if err != nil {
		return StatsView{}, err
	}
	topOut := make([]StatsTopTx, 0, len(tops))
	for _, t := range tops {
		desc := t.Description
		if t.Note != "" {
			if desc != "" {
				desc = desc + "（" + t.Note + "）"
			} else {
				desc = t.Note
			}
		}
		catName := t.CategoryName
		if catName == "" {
			catName = "未分类"
		}
		topOut = append(topOut, StatsTopTx{
			ID:       t.ID,
			Date:     t.OccurredAt.Format("01-02"),
			Who:      firstNonEmpty(t.Counterparty, "—"),
			Desc:     desc,
			Amount:   t.Amount,
			Category: catName,
			Account:  string(t.Account),
		})
	}

	return StatsView{
		Period:           p.Label,
		Granularity:      granLabel(p.Type),
		Direction:        string(dir),
		Account:          string(account),
		Total:            total,
		Categories:       segs,
		MonthlyCompare:   monthly,
		QuarterlyCompare: quarterly,
		TopTransactions:  topOut,
	}, nil
}

func granLabel(t domain.PeriodType) string {
	switch t {
	case domain.PeriodMonthly:
		return "month"
	case domain.PeriodQuarterly:
		return "quarter"
	case domain.PeriodAnnual:
		return "year"
	}
	return string(t)
}

// buildMonthBuckets 返回 n 个月度桶，最后一个对齐到 p 所在的月份。
// 若 p 是季度/年度，则以其 End-1 所在的月份作为末尾。
func buildMonthBuckets(p domain.Period, n int) []port.PeriodBucket {
	last := p.End.AddDate(0, 0, -1)
	lastStart := time.Date(last.Year(), last.Month(), 1, 0, 0, 0, 0, last.Location())
	out := make([]port.PeriodBucket, n)
	for i := 0; i < n; i++ {
		s := lastStart.AddDate(0, -(n - 1 - i), 0)
		e := s.AddDate(0, 1, 0)
		out[i] = port.PeriodBucket{
			Label: fmt.Sprintf("%04d-%02d", s.Year(), s.Month()),
			Start: s, End: e,
		}
	}
	return out
}

// buildQuarterBuckets 返回 n 个季度桶，最后一个对齐到 p 所在的季度。
func buildQuarterBuckets(p domain.Period, n int) []port.PeriodBucket {
	last := p.End.AddDate(0, 0, -1)
	q := (int(last.Month())-1)/3 + 1
	lastStart := time.Date(last.Year(), time.Month((q-1)*3+1), 1, 0, 0, 0, 0, last.Location())
	out := make([]port.PeriodBucket, n)
	for i := 0; i < n; i++ {
		s := lastStart.AddDate(0, -3*(n-1-i), 0)
		e := s.AddDate(0, 3, 0)
		qq := (int(s.Month())-1)/3 + 1
		out[i] = port.PeriodBucket{
			Label: fmt.Sprintf("%dQ%d", s.Year(), qq),
			Start: s, End: e,
		}
	}
	return out
}

// bucketsToStatsWithCompare 从完整序列（长度 = show + yoyOffset）末尾取 show 条，
// 并为每条计算环比（相对前一桶）、同比（相对 yoyOffset 桶之前）。
// 基期金额为 0 时对应的比率为 nil，避免除零与“无穷增长”误导。
func bucketsToStatsWithCompare(bs []port.PeriodBucket, show, yoyOffset int) []StatsBucket {
	n := len(bs)
	if show > n {
		show = n
	}
	out := make([]StatsBucket, show)
	start := n - show
	for i := 0; i < show; i++ {
		idx := start + i
		b := bs[idx]
		seg := StatsBucket{Label: b.Label, Amount: b.Amount}
		if idx-1 >= 0 {
			seg.Chain = pctDelta(b.Amount, bs[idx-1].Amount)
		}
		if idx-yoyOffset >= 0 {
			seg.YoY = pctDelta(b.Amount, bs[idx-yoyOffset].Amount)
		}
		out[i] = seg
	}
	return out
}

// pctDelta 返回 (cur-base)/base，base 为 0 时返回 nil。
func pctDelta(cur, base int64) *float64 {
	if base == 0 {
		return nil
	}
	v := float64(cur-base) / float64(base)
	return &v
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
