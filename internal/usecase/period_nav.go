package usecase

import (
	"fmt"
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

// Resolve 解析 query 参数：period 显式给了就以它为准（type 被忽略，由标签形状决定），
// 没给就用 Default(type)。
func (n PeriodNav) Resolve(typeStr, periodStr string) (domain.Period, error) {
	if strings.TrimSpace(periodStr) == "" {
		return n.Default(ParsePeriodType(typeStr)), nil
	}
	p, err := domain.ParsePeriod(periodStr)
	if err != nil {
		return domain.Period{}, fmt.Errorf("周期格式不正确: %w", err)
	}
	return p, nil
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
