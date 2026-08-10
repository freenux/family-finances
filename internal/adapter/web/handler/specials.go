package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

type specialsVM struct {
	pageBase
	Data  usecase.SpecialViewData
	Error string
}

// Specials GET /specials —— 专项列表 + 已花费/预算/执行率/跨科目构成 + 新建表单
func (h *Handler) Specials(w http.ResponseWriter, r *http.Request) {
	h.renderSpecials(w, r, "", http.StatusOK)
}

func (h *Handler) renderSpecials(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	data, err := h.specialView.Load(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := specialsVM{
		pageBase: pageBase{Title: "专项开支", Nav: "specials", Flash: h.flash.pop(w, r)},
		Data:     data,
		Error:    errMsg,
	}
	h.renderPage(w, status, "specials", vm)
}

// SaveSpecial POST /specials —— 表单新建/编辑（带 id 即编辑）
func (h *Handler) SaveSpecial(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderSpecials(w, r, "表单解析失败", http.StatusBadRequest)
		return
	}
	p, err := specialFromForm(r)
	if err != nil {
		h.renderSpecials(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	if p.ID == "" {
		p.ID = newID()
		p.CreatedAt = time.Now()
	}
	if err := h.specialView.Upsert(r.Context(), &p); err != nil {
		h.renderSpecials(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	h.flash.set(w, "专项已保存。")
	http.Redirect(w, r, "/specials", http.StatusSeeOther)
}

// DeleteSpecial POST /api/specials/{id}/delete —— 删除专项，原有流水归回日常
func (h *Handler) DeleteSpecial(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	if err := h.specialView.Delete(r.Context(), id); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			http.Error(w, "专项不存在", http.StatusNotFound)
			return
		}
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "专项已删除，原本挂在上面的流水已归回日常。")
	http.Redirect(w, r, "/specials", http.StatusSeeOther)
}

func specialFromForm(r *http.Request) (domain.SpecialProject, error) {
	var p domain.SpecialProject
	p.ID = strings.TrimSpace(r.FormValue("id"))
	p.Name = strings.TrimSpace(r.FormValue("name"))
	p.Note = strings.TrimSpace(r.FormValue("note"))

	budget, err := parseNonNegativeYuan(r.FormValue("budget"), "预算")
	if err != nil {
		return p, err
	}
	p.BudgetFen = budget

	if p.StartedOn, err = parseOptionalDate(r.FormValue("started_on"), "开始日期"); err != nil {
		return p, err
	}
	if p.EndedOn, err = parseOptionalDate(r.FormValue("ended_on"), "结束日期"); err != nil {
		return p, err
	}
	return p, nil
}

// parseOptionalDate 空串 → 零值（表示"未填"/"进行中"）
func parseOptionalDate(v, label string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", v, time.Local)
	if err != nil {
		return time.Time{}, errors.New(label + "格式应为 YYYY-MM-DD")
	}
	return t, nil
}
