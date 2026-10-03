package usecase

import (
	"strings"
	"time"

	"family-finances/internal/domain"
)

// PeriodNav 周期规则的服务端唯一来源：默认周期、前后翻页、中文标签。
// 客户端不做任何日期运算，只用这里给出的 key。
type PeriodNav struct {
	Now func() time.Time // nil 时用 time.Now，便于测试注入
}

// PeriodNavView 周期导航视图（内嵌在每个带周期的响应里）
type PeriodNavView struct {
	Type      string `json:"type"`
	Key       string `json:"key"`
	Label     string `json:"label"`
	Prev      string `json:"prev"`
	Next      string `json:"next"`
	HasNext   bool   `json:"has_next"`
	IsCurrent bool   `json:"is_current"`
}

func (n PeriodNav) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

// Default 默认周期：上一个完整周期（当期没走完，环比同比都会失真）。
// annual → 去年，monthly → 上月，quarterly 以及任何非法 type → 上季度。
func (n PeriodNav) Default(t domain.PeriodType) domain.Period {
	now := n.now()
	switch t {
	case domain.PeriodAnnual:
		return domain.CurrentYear(now).Previous()
	case domain.PeriodMonthly:
		return domain.CurrentMonth(now).Previous()
	default:
		return domain.CurrentQuarter(now).Previous()
	}
}

// ForRuleView 「分类规则」页点「查看流水」时用的周期：当前季度。
// 不能复用 Default：它给的是上一个完整季度，盖不住当月刚导入、待核对的那批流水，
// 页面会误报「这条规则没匹配到任何流水」。
func (n PeriodNav) ForRuleView() domain.Period {
	return domain.CurrentQuarter(n.now())
}

// ParsePeriodType 把 query 里的 type 解析成 PeriodType；空串与非法值一律按季度
func ParsePeriodType(s string) domain.PeriodType {
	switch domain.PeriodType(strings.TrimSpace(s)) {
	case domain.PeriodMonthly:
		return domain.PeriodMonthly
	case domain.PeriodAnnual:
		return domain.PeriodAnnual
	default:
		return domain.PeriodQuarterly
	}
}

// Resolve 解析 query 参数里的 type / period，是 SSR 与 /api/v1 共用的唯一规则：
//
//   - type 为空 → 用 defaultType；type 非法 → 按季度。
//   - period 为空 → 该 type 的默认周期（Default）。
//   - period 非法（解析失败）→ ErrInvalidPeriod。
//   - period 合法但与 type 对不上（如 type=annual&period=2026Q3）→ 退回该 type 的默认周期，
//     而不是采用 period 自带的粒度。
//
// 最后一条的理由：这是 handler 既有、并由 period_query_test.go 钉住的行为；而且更稳妥——
// type 与 period 对不上是客户端的 bug（典型场景：用户把「季」切成「年」，旧 label 还是 2026Q1），
// 悄悄改变请求的粒度，比退回该粒度的默认周期更糟：用户点的是「年」，看到的却是一个季度。
func (n PeriodNav) Resolve(typeStr, periodStr string, defaultType domain.PeriodType) (domain.Period, error) {
	t := ParsePeriodType(typeStr)
	if strings.TrimSpace(typeStr) == "" {
		t = ParsePeriodType(string(defaultType))
	}
	label := strings.TrimSpace(periodStr)
	if label == "" {
		return n.Default(t), nil
	}
	p, err := domain.ParsePeriod(label)
	if err != nil {
		return domain.Period{}, newUserError(ErrInvalidPeriod, "周期格式不正确："+label)
	}
	if p.Type != t {
		return n.Default(t), nil
	}
	return p, nil
}

// ResolveForList 流水列表页的周期：Resolve 加上 ?rule_id= 的例外——带 ruleID 且 URL 里既没有
// type 也没有 period 时用当前季度（原因见 ForRuleView）。URL 显式给了 type 或 period 一律以显式为准。
func (n PeriodNav) ResolveForList(typeStr, periodStr, ruleID string, defaultType domain.PeriodType) (domain.Period, error) {
	if strings.TrimSpace(ruleID) != "" && periodStr == "" && typeStr == "" {
		return n.ForRuleView(), nil
	}
	return n.Resolve(typeStr, periodStr, defaultType)
}

// Nav 生成导航视图。HasNext 在当期（及未来期）上为 false：不让用户翻到未来。
func (n PeriodNav) Nav(p domain.Period) PeriodNavView {
	now := n.now()
	next := p.Next()
	return PeriodNavView{
		Type:      string(p.Type),
		Key:       p.Label,
		Label:     p.DisplayLabel(),
		Prev:      p.Previous().Label,
		Next:      next.Label,
		HasNext:   !next.Start.IsZero() && !next.Start.After(now),
		IsCurrent: !p.Start.IsZero() && !now.Before(p.Start) && now.Before(p.End),
	}
}
