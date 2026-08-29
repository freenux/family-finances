package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"family-finances/internal/domain"
)

type assetsVM struct {
	pageBase
	Catalog         []domain.AssetAccount
	CurrentPeriod   string
	PrevPeriodLabel string
	NextPeriodLabel string
	HasCurrent      bool
	HasPrevSnapshot bool
	PrevNetWorth    int64
	CurrentDataJSON string
	CurveJSON       string
}

// Assets GET /assets?period=2026Q2；period 缺省为上一个完整季度
func (h *Handler) Assets(w http.ResponseWriter, r *http.Request) {
	label := r.URL.Query().Get("period")
	if label == "" {
		// 必须和财报/现金流表同一套默认（defaultPeriodFor）：否则快照存进 2026Q3，
		// 而财报按 2026Q2 去查快照，净资产环比与活钱覆盖月数直接消失。
		label = defaultPeriodFor(domain.PeriodQuarterly, time.Now()).Label
	}
	view, err := h.assetSvc.SnapshotView(r.Context(), label)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data := map[string]int64{}
	if view.Current != nil {
		data = view.Current.Data
	}
	var prevNetWorth int64
	if view.Previous != nil {
		prevNetWorth = view.Previous.NetWorth
	}

	dataJSON, _ := json.Marshal(data)
	curveJSON, _ := json.Marshal(view.Curve)

	vm := assetsVM{
		pageBase:        pageBase{Title: "资产快照", Nav: "assets", Flash: h.flash.pop(w, r)},
		Catalog:         domain.AssetCatalog,
		CurrentPeriod:   view.Period.Label,
		PrevPeriodLabel: view.Period.Previous().Label,
		NextPeriodLabel: nextQuarterLabel(view.Period),
		HasCurrent:      view.Current != nil,
		HasPrevSnapshot: view.Previous != nil,
		PrevNetWorth:    prevNetWorth,
		CurrentDataJSON: string(dataJSON),
		CurveJSON:       string(curveJSON),
	}
	h.renderPage(w, http.StatusOK, "assets", vm)
}

// nextQuarterLabel 返回 p 的下一季度 label（domain.Period 只提供 Previous，这里补一个 Next）
func nextQuarterLabel(p domain.Period) string {
	next := p.Start.AddDate(0, 3, 0)
	q := (int(next.Month())-1)/3 + 1
	return fmt.Sprintf("%dQ%d", next.Year(), q)
}

// ----- API -----

type assetPutReq struct {
	Data map[string]int64 `json:"data"`
}

// SaveAssetSnapshot PUT /api/assets/{period}
func (h *Handler) SaveAssetSnapshot(w http.ResponseWriter, r *http.Request) {
	period := chiURLParam(r, "period")
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req assetPutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	snap, err := h.assetSvc.SaveSnapshot(r.Context(), period, req.Data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var totalAssets, totalLiab int64
	for _, acc := range domain.AssetCatalog {
		v := snap.Data[acc.Code]
		if acc.Group == "asset" {
			totalAssets += v
		} else {
			totalLiab += v
		}
	}
	writeJSON(w, map[string]any{
		"net_worth": snap.NetWorth,
		"totals": map[string]int64{
			"assets":      totalAssets,
			"liabilities": totalLiab,
		},
	})
}

// PrevAssetSnapshot GET /api/assets/{period}/prev；返回 period 的上一季度快照数据，供"从上季复制"使用
func (h *Handler) PrevAssetSnapshot(w http.ResponseWriter, r *http.Request) {
	period := chiURLParam(r, "period")
	p, err := domain.ParsePeriod(period)
	if err != nil || p.Type != domain.PeriodQuarterly {
		http.Error(w, "非法的期间", http.StatusBadRequest)
		return
	}
	data, found, err := h.assetSvc.GetData(r.Context(), p.Previous().Label)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !found {
		data = map[string]int64{}
	}
	writeJSON(w, map[string]any{"data": data, "found": found, "period": p.Previous().Label})
}
