package usecase

import (
	"context"

	"family-finances/internal/domain"
)

// 下面这些类型是 /api/v1/meta 的响应形状，同时也是流水页 SSR 首屏嵌入的 meta JSON。
// 跨端共享的数据整形放在 usecase，HTTP 适配器与 SSR handler 都只调 Meta.Execute。

type MetaOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type MetaCategoryItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MetaCategoryGroup struct {
	GroupID   string             `json:"group_id"`
	GroupName string             `json:"group_name"`
	Type      string             `json:"type"`
	Items     []MetaCategoryItem `json:"items"`
}

type MetaSpecial struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// MetaView 客户端 bootstrap：所有下拉选项与三个粒度的默认周期。
type MetaView struct {
	Categories   []MetaCategoryGroup `json:"categories"`
	Specials     []MetaSpecial       `json:"specials"`
	Accounts     []MetaOption        `json:"accounts"`      // 仅可写入的存储值（husband/wife）
	AccountViews []MetaOption        `json:"account_views"` // 查询视图切换用，额外含 family
	Sources      []MetaOption        `json:"sources"`
	Statuses     []MetaOption        `json:"statuses"`
	Directions   []MetaOption        `json:"directions"`
	// 三个粒度各自的默认周期（上一个完整周期），形状与 /periods/nav 一致
	DefaultPeriod map[string]PeriodNavView `json:"default_period"`
}

// MetaSpecialLister 专项列表来源，由 *SpecialView 或专项仓库满足。
type MetaSpecialLister interface {
	ListAll(ctx context.Context) ([]domain.SpecialProject, error)
}

// Meta 装配 MetaView。specials 可为 nil（专项功能未启用：返回空数组）。
type Meta struct {
	cats     LeafCategoryLister
	specials MetaSpecialLister
	nav      PeriodNav
}

func NewMeta(cats LeafCategoryLister, specials MetaSpecialLister, nav PeriodNav) *Meta {
	return &Meta{cats: cats, specials: specials, nav: nav}
}

// Execute 返回完整 bootstrap（不过滤科目方向）。
func (m *Meta) Execute(ctx context.Context) (MetaView, error) {
	return m.ExecuteFor(ctx, "")
}

// ExecuteFor 同 Execute，direction 为 income | expense 时只返回对应 type 的科目分组
// （手填支出只该给支出科目：按方向筛科目是业务规则，不由客户端实现）。
// 其它值（含空串、非法值）一律视为不过滤，与 §5 的 direction=all 及 ParseScope「非法退回默认」同一习惯。
// 只影响 categories，其它字段不变。
func (m *Meta) ExecuteFor(ctx context.Context, direction string) (MetaView, error) {
	var v MetaView
	cats, err := m.cats.ListAll(ctx)
	if err != nil {
		return v, err
	}
	specials := []MetaSpecial{} // 空数组而非 null
	if m.specials != nil {
		list, err := m.specials.ListAll(ctx)
		if err != nil {
			return v, err
		}
		for _, p := range list {
			specials = append(specials, MetaSpecial{ID: p.ID, Name: p.Name, Active: p.IsActive()})
		}
	}
	groups := GroupCategories(cats)
	if direction == string(domain.DirectionIncome) || direction == string(domain.DirectionExpense) {
		kept := make([]MetaCategoryGroup, 0, len(groups))
		for _, g := range groups {
			if g.Type == direction {
				kept = append(kept, g)
			}
		}
		groups = kept
	}
	return MetaView{
		Categories:   groups,
		Specials:     specials,
		Accounts:     accountOptions(false),
		AccountViews: accountOptions(true),
		// csv 对应所有 csv:<模板名>，与 sourceText 的说法一致
		Sources: []MetaOption{
			{"alipay", "支付宝"}, {"wechat", "微信"}, {"manual", "手填"}, {"csv", "CSV"},
		},
		Statuses: []MetaOption{
			{"pending_review", "待处理"}, {"confirmed", "已确认"}, {"excluded", "已排除"},
		},
		Directions: []MetaOption{{"income", "收入"}, {"expense", "支出"}},
		DefaultPeriod: map[string]PeriodNavView{
			string(domain.PeriodMonthly):   m.nav.Nav(m.nav.Default(domain.PeriodMonthly)),
			string(domain.PeriodQuarterly): m.nav.Nav(m.nav.Default(domain.PeriodQuarterly)),
			string(domain.PeriodAnnual):    m.nav.Nav(m.nav.Default(domain.PeriodAnnual)),
		},
	}, nil
}

// GroupCategories 一级科目为分组，二级按 parent_id 挂到分组下；保持输入顺序。
// 找不到父分组的二级科目丢弃。
func GroupCategories(cats []domain.Category) []MetaCategoryGroup {
	groups := []MetaCategoryGroup{}
	index := map[string]int{}
	for _, c := range cats {
		if c.Level == 1 {
			index[c.ID] = len(groups)
			groups = append(groups, MetaCategoryGroup{
				GroupID: c.ID, GroupName: c.Name, Type: string(c.Type), Items: []MetaCategoryItem{},
			})
		}
	}
	for _, c := range cats {
		if c.Level != 2 {
			continue
		}
		if i, ok := index[c.ParentID]; ok {
			groups[i].Items = append(groups[i].Items, MetaCategoryItem{ID: c.ID, Name: c.Name})
		}
	}
	return groups
}

// accountOptions 判据是 IsStorageAccount：family 只是查询视图，不能作为 PATCH 的目标值
func accountOptions(withViews bool) []MetaOption {
	out := []MetaOption{}
	for _, ac := range []domain.Account{domain.AccountHusband, domain.AccountWife, domain.AccountFamily} {
		if withViews || ac.IsStorageAccount() {
			out = append(out, MetaOption{string(ac), ac.Label()})
		}
	}
	return out
}
