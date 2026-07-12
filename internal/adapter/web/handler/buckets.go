package handler

import (
	"net/http"

	"family-finances/internal/usecase"
)

type bucketsVM struct {
	pageBase
	D          usecase.BucketDiagnosis
	Allocation usecase.AllocationResult
}

// Buckets GET /buckets —— 四笔钱分层诊断（确定性计算，页面即算即渲染）
func (h *Handler) Buckets(w http.ResponseWriter, r *http.Request) {
	in, err := h.bucketEng.LoadInputs(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	diag := usecase.ComputeBuckets(in)
	profile := diagProfileOrDefault(in)
	var snapData map[string]int64
	if in.Snapshot != nil {
		snapData = in.Snapshot.Data
	}
	alloc := usecase.ComputeAllocation(profile, in.Profile != nil, snapData)

	vm := bucketsVM{
		pageBase:   pageBase{Title: "四笔钱诊断", Nav: "buckets", Flash: h.flash.pop(w, r)},
		D:          diag,
		Allocation: alloc,
	}
	h.renderPage(w, http.StatusOK, "buckets", vm)
}
