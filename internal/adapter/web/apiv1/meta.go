package apiv1

import (
	"net/http"

	"family-finances/internal/domain"
)

type option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type categoryItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type categoryGroup struct {
	GroupID   string         `json:"group_id"`
	GroupName string         `json:"group_name"`
	Type      string         `json:"type"`
	Items     []categoryItem `json:"items"`
}

type specialOption struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type metaResp struct {
	Categories   []categoryGroup `json:"categories"`
	Specials     []specialOption `json:"specials"`
	Accounts     []option        `json:"accounts"`      // 仅可写入的存储值（husband/wife）
	AccountViews []option        `json:"account_views"` // 查询视图切换用，额外含 family
	Sources      []option        `json:"sources"`
	Statuses     []option        `json:"statuses"`
	Directions   []option        `json:"directions"`
	// default_period 依赖 usecase.PeriodNav，属于下一阶段，本阶段不输出。
}

// Meta GET /api/v1/meta：客户端启动时拉一次的 bootstrap。
func (a *API) Meta(w http.ResponseWriter, r *http.Request) {
	cats, err := a.categories.ListAll(r.Context())
	if err != nil {
		a.internalError(w, err)
		return
	}
	specials := []specialOption{} // 空数组而非 null
	if a.specials != nil {
		list, err := a.specials.ListAll(r.Context())
		if err != nil {
			a.internalError(w, err)
			return
		}
		for _, p := range list {
			specials = append(specials, specialOption{ID: p.ID, Name: p.Name, Active: p.IsActive()})
		}
	}
	WriteData(w, metaResp{
		Categories:   groupCategories(cats),
		Specials:     specials,
		Accounts:     accountOptions(false),
		AccountViews: accountOptions(true),
		// csv 对应所有 csv:<模板名>，与 tx_table.js 的 sourceLabel 保持同一说法
		Sources: []option{
			{"alipay", "支付宝"}, {"wechat", "微信"}, {"manual", "手填"}, {"csv", "CSV"},
		},
		Statuses: []option{
			{"pending_review", "待处理"}, {"confirmed", "已确认"}, {"excluded", "已排除"},
		},
		Directions: []option{{"income", "收入"}, {"expense", "支出"}},
	})
}

// groupCategories 一级科目为分组，二级按 parent_id 挂到分组下；保持输入顺序。
// 找不到父分组的二级科目丢弃（与原 tx_table.js 行为一致）。
func groupCategories(cats []domain.Category) []categoryGroup {
	groups := []categoryGroup{}
	index := map[string]int{}
	for _, c := range cats {
		if c.Level == 1 {
			index[c.ID] = len(groups)
			groups = append(groups, categoryGroup{
				GroupID: c.ID, GroupName: c.Name, Type: string(c.Type), Items: []categoryItem{},
			})
		}
	}
	for _, c := range cats {
		if c.Level != 2 {
			continue
		}
		if i, ok := index[c.ParentID]; ok {
			groups[i].Items = append(groups[i].Items, categoryItem{ID: c.ID, Name: c.Name})
		}
	}
	return groups
}

// accountOptions 判据是 IsStorageAccount：family 只是查询视图，不能作为 PATCH 的目标值
func accountOptions(withViews bool) []option {
	out := []option{}
	for _, ac := range []domain.Account{domain.AccountHusband, domain.AccountWife, domain.AccountFamily} {
		if withViews || ac.IsStorageAccount() {
			out = append(out, option{string(ac), ac.Label()})
		}
	}
	return out
}
