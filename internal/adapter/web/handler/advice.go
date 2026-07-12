package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/usecase"
)

// diagProfileOrDefault 取画像；未填时用缺省画像（等级仅供展示，页面会提示补填）
func diagProfileOrDefault(in usecase.BucketInputs) domain.FamilyProfile {
	if in.Profile != nil {
		return *in.Profile
	}
	return domain.DefaultFamilyProfile()
}

type adviceVM struct {
	pageBase
	LLMEnabled bool
	// 系统计算（即时现算，与生成时可能不同——生成后的存档见 Stored*）
	D          usecase.BucketDiagnosis
	Allocation usecase.AllocationResult
	// 存库的最新一份 AI 建议
	HasStored     bool
	Stored        domain.AIReport
	StoredContent usecase.AdviceContent
	StoredSystem  usecase.AdviceSystemData
	RefText       map[string]string
}

// Advice GET /advice —— 系统计算面板 + 最新存库 AI 建议
func (h *Handler) Advice(w http.ResponseWriter, r *http.Request) {
	in, err := h.bucketEng.LoadInputs(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	diag := usecase.ComputeBuckets(in)
	var snapData map[string]int64
	if in.Snapshot != nil {
		snapData = in.Snapshot.Data
	}
	alloc := usecase.ComputeAllocation(diagProfileOrDefault(in), in.Profile != nil, snapData)

	vm := adviceVM{
		pageBase:   pageBase{Title: "AI 配置建议", Nav: "advice", Flash: h.flash.pop(w, r)},
		LLMEnabled: h.genAdvice.Enabled(),
		D:          diag,
		Allocation: alloc,
	}

	// 最新一份 advice 存档（reports 表 period_type='advice'，ListAll 已按 generated_at desc）
	all, err := h.reportRepo.ListAll(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	for _, rep := range all {
		if rep.PeriodType == usecase.PeriodTypeAdvice {
			vm.HasStored = true
			vm.Stored = rep
			_ = json.Unmarshal([]byte(rep.AIAnalysis), &vm.StoredContent)
			_ = json.Unmarshal([]byte(rep.KPIData), &vm.StoredSystem)
			vm.RefText = adviceRefTexts(vm.StoredSystem)
			break
		}
	}
	h.renderPage(w, http.StatusOK, "advice", vm)
}

// GenerateAdviceSubmit POST /advice/generate —— 同步生成（30s 超时）→ 302 回 GET
func (h *Handler) GenerateAdviceSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := h.genAdvice.Execute(ctx); err != nil {
		h.log.Error("generate advice", "err", err)
		h.flash.set(w, "生成失败："+err.Error())
	} else {
		h.flash.set(w, "AI 配置建议已生成并存档。")
	}
	http.Redirect(w, r, "/advice", http.StatusSeeOther)
}

// adviceRefTexts 把存档系统数据展开成 key → 文本，供 refs 依据小字回显
func adviceRefTexts(sys usecase.AdviceSystemData) map[string]string {
	m := map[string]string{}
	for _, f := range sys.PackFindings {
		m[f.Key] = f.Text
	}
	for _, f := range sys.Buckets.Findings {
		m[f.Key] = f.Text
	}
	for _, a := range sys.Allocation.Assumptions {
		m[a.Key] = a.Text
	}
	for _, b := range sys.Allocation.Bands {
		m[b.Key] = bandRefText(b)
	}
	return m
}

func bandRefText(b usecase.AllocationBand) string {
	if !b.HasCurrent {
		return b.Name + "目标区间 " + pctRange(b.MinPct, b.MaxPct) + "（快照缺失，无现状对照）"
	}
	return b.Name + "目标区间 " + pctRange(b.MinPct, b.MaxPct) + "，当前 " + pctOne(b.CurrentPct)
}

func pctRange(min, max float64) string { return pctOne(min) + "–" + pctOne(max) }

func pctOne(v float64) string { return fmt.Sprintf("%.1f%%", v) }
