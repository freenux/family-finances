package usecase

import (
	"fmt"

	"family-finances/internal/domain"
)

// 配置区间引擎（确定性）：画像 + 长期桶现状 → 长期桶相关目标区间 + 显式假设列表。
// 输出区间，LLM 只解释（铁律 1）；假设每条一个稳定 key，进入 /advice refs 白名单。

// AllocationBand 一条目标区间与现状对照
type AllocationBand struct {
	Key        string  `json:"key"`  // band_equity_share / band_overseas_share / band_gold_share
	Name       string  `json:"name"` // 中文名
	MinPct     float64 `json:"min_pct"`
	MaxPct     float64 `json:"max_pct"`
	CurrentPct float64 `json:"current_pct"`
	HasCurrent bool    `json:"has_current"` // false = 快照缺失，无现状可对照
	Status     string  `json:"status"`      // within | below | above | unknown
}

// AllocationResult 引擎输出
type AllocationResult struct {
	HasProfile    bool             `json:"has_profile"`
	HasSnapshot   bool             `json:"has_snapshot"`
	Grade         string           `json:"grade"`
	EquityCapPct  int              `json:"equity_cap_pct"`
	InvestableFen int64            `json:"investable_fen"` // 可投资资产（不含房产/公积金）
	LongTermFen   int64            `json:"long_term_fen"`
	Bands         []AllocationBand `json:"bands"`
	Assumptions   []Finding        `json:"assumptions"`
}

// OutOfBand 返回偏出区间的条目（dashboard 再平衡卡片用）
func (r AllocationResult) OutOfBand() []AllocationBand {
	var out []AllocationBand
	for _, b := range r.Bands {
		if b.Status == "below" || b.Status == "above" {
			out = append(out, b)
		}
	}
	return out
}

// 引擎常量（每条对应一个假设 key，修改时同步改 buildAssumptions 文案）
const (
	equityBandWidthPP = 15 // 权益目标区间宽度：[上限−15pp, 上限]
	equityBandFloorPP = 5  // 区间下限不低于 5%
	goldCapPct        = 10 // 黄金 ≤ 长期桶 10%
)

// overseasCapPctByGrade 海外权益占长期桶的上限，随等级放宽
func overseasCapPctByGrade(grade string) float64 {
	switch grade {
	case "C1":
		return 10
	case "C2":
		return 15
	case "C3":
		return 20
	case "C4":
		return 25
	default: // C5
		return 30
	}
}

// ComputeAllocation 是纯函数：profile 为空值时用 DefaultFamilyProfile 的口径由调用方决定；
// snap 传 nil 表示无快照（区间照常输出，现状标记 unknown）。
func ComputeAllocation(profile domain.FamilyProfile, hasProfile bool, snap map[string]int64) AllocationResult {
	grade, cap := domain.CalcProfileGrade(profile)
	res := AllocationResult{
		HasProfile:   hasProfile,
		HasSnapshot:  snap != nil,
		Grade:        grade,
		EquityCapPct: cap,
	}

	var investable, longTerm, equity, global, gold int64
	if snap != nil {
		for _, acc := range domain.AssetCatalog {
			v := snap[acc.Code]
			switch acc.Code {
			case "asset.cash", "asset.mmf", "asset.deposit", "asset.wealth":
				investable += v
			case "asset.equity_cn", "asset.equity_global", "asset.gold":
				investable += v
				longTerm += v
			}
		}
		equity = snap["asset.equity_cn"] + snap["asset.equity_global"]
		global = snap["asset.equity_global"]
		gold = snap["asset.gold"]
	}
	res.InvestableFen = investable
	res.LongTermFen = longTerm

	equityMin := float64(cap - equityBandWidthPP)
	if equityMin < equityBandFloorPP {
		equityMin = equityBandFloorPP
	}
	overseasCap := overseasCapPctByGrade(grade)

	res.Bands = []AllocationBand{
		bandOf("band_equity_share", "权益占可投资资产", equityMin, float64(cap), equity, investable, snap != nil),
		bandOf("band_overseas_share", "海外权益占长期桶", 0, overseasCap, global, longTerm, snap != nil),
		bandOf("band_gold_share", "黄金占长期桶", 0, goldCapPct, gold, longTerm, snap != nil),
	}
	res.Assumptions = buildAssumptions(grade, cap, equityMin, overseasCap)
	return res
}

// bandOf 计算一条区间对照；denominator 为 0 时视为现状 0%（空桶不越界）
func bandOf(key, name string, minPct, maxPct float64, numer, denom int64, hasSnapshot bool) AllocationBand {
	b := AllocationBand{Key: key, Name: name, MinPct: minPct, MaxPct: maxPct}
	if !hasSnapshot {
		b.Status = "unknown"
		return b
	}
	b.HasCurrent = true
	if denom > 0 {
		b.CurrentPct = float64(numer) / float64(denom) * 100
	}
	switch {
	case b.CurrentPct < minPct:
		b.Status = "below"
	case b.CurrentPct > maxPct:
		b.Status = "above"
	default:
		b.Status = "within"
	}
	return b
}

func buildAssumptions(grade string, cap int, equityMin, overseasCap float64) []Finding {
	return []Finding{
		{Key: "assume_grade", Text: fmt.Sprintf(
			"风险等级 %s（权益上限 %d%%）由画像评分推导：偏好 0/3/6 + 年限 0/1/2 + 稳定性 0/1/2 − 年龄惩罚", grade, cap)},
		{Key: "assume_equity_band", Text: fmt.Sprintf(
			"权益目标区间 = [%.0f%%, %d%%]，即上限向下 %dpp、下限不低于 %d%%，分母为可投资资产", equityMin, cap, equityBandWidthPP, equityBandFloorPP)},
		{Key: "assume_investable_scope", Text: "可投资资产 = 现金+货基+定存+理财+权益+黄金，不含自住房产与公积金/养老金"},
		{Key: "assume_overseas_cap", Text: fmt.Sprintf(
			"海外权益 ≤ 长期桶 %.0f%%（随等级 C1→10%% 至 C5→30%%），控制汇率与单一市场风险", overseasCap)},
		{Key: "assume_gold_cap", Text: fmt.Sprintf("黄金及另类 ≤ 长期桶 %d%%，仅作分散与抗通胀配置", goldCapPct)},
	}
}
