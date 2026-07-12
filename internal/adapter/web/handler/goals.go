package handler

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

type goalsVM struct {
	pageBase
	Data    usecase.GoalViewData
	Catalog []domain.AssetAccount // linked_codes 复选用（仅资产科目）
	Error   string
}

// Goals GET /goals —— 目标列表 + 进度 + 月供 + 新建表单
func (h *Handler) Goals(w http.ResponseWriter, r *http.Request) {
	h.renderGoals(w, r, "", http.StatusOK)
}

func (h *Handler) renderGoals(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	data, err := h.goalView.Load(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	var assetCatalog []domain.AssetAccount
	for _, a := range domain.AssetCatalog {
		if a.Group == "asset" {
			assetCatalog = append(assetCatalog, a)
		}
	}
	vm := goalsVM{
		pageBase: pageBase{Title: "财务目标", Nav: "goals", Flash: h.flash.pop(w, r)},
		Data:     data,
		Catalog:  assetCatalog,
		Error:    errMsg,
	}
	h.renderPage(w, status, "goals", vm)
}

var ymPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// SaveGoal POST /api/goals —— 表单新建/更新（带 id 即更新）
func (h *Handler) SaveGoal(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderGoals(w, r, "表单解析失败", http.StatusBadRequest)
		return
	}
	g, err := goalFromForm(r)
	if err != nil {
		h.renderGoals(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	if g.ID == "" {
		g.ID = newID()
	}
	g.UpdatedAt = time.Now()
	if err := h.goalRepo.UpsertGoal(r.Context(), &g); err != nil {
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "目标已保存。")
	http.Redirect(w, r, "/goals", http.StatusSeeOther)
}

// DeleteGoal POST /api/goals/{id}/delete
func (h *Handler) DeleteGoal(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	if err := h.goalRepo.DeleteGoal(r.Context(), id); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			http.Error(w, "目标不存在", http.StatusNotFound)
			return
		}
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "目标已删除。")
	http.Redirect(w, r, "/goals", http.StatusSeeOther)
}

func goalFromForm(r *http.Request) (domain.FinancialGoal, error) {
	var g domain.FinancialGoal
	g.ID = strings.TrimSpace(r.FormValue("id"))
	g.Description = strings.TrimSpace(r.FormValue("description"))
	if g.Description == "" {
		return g, fmt.Errorf("请填写目标名称")
	}
	g.Category = strings.TrimSpace(r.FormValue("category"))
	if g.Category == "" {
		g.Category = "其他"
	}

	target, err := parseNonNegativeYuan(r.FormValue("target_amount"), "目标金额")
	if err != nil {
		return g, err
	}
	if target <= 0 {
		return g, fmt.Errorf("目标金额必须大于 0")
	}
	g.TargetAmount = target

	g.TargetYM = strings.TrimSpace(r.FormValue("target_ym"))
	if g.TargetYM != "" && !ymPattern.MatchString(g.TargetYM) {
		return g, fmt.Errorf("目标年月格式应为 YYYY-MM")
	}
	g.YearsToAchieve = strings.TrimSpace(r.FormValue("years_to_achieve"))

	if g.CurrentAmountFen, err = parseNonNegativeYuan(r.FormValue("current_amount"), "当前金额"); err != nil {
		return g, err
	}

	codeSet := domain.AssetCodeSet()
	for _, code := range r.Form["linked_codes"] {
		if !codeSet[code] {
			return g, fmt.Errorf("非法的关联科目: %s", code)
		}
		g.LinkedCodes = append(g.LinkedCodes, code)
	}

	switch v := r.FormValue("priority"); v {
	case "高", "中", "低":
		g.Priority = v
	default:
		g.Priority = "中"
	}
	g.Notes = strings.TrimSpace(r.FormValue("notes"))
	return g, nil
}
