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
	Form  specialFormVM
	Error string
}

// specialFormVM 右侧表单的预填值。全空 = 新建；Editing=true 时表单 POST 会覆盖同 id 的专项。
// 用预格式化好的字符串而不是 domain 对象，模板才不用为"日期零值要渲染成空串"再加函数。
type specialFormVM struct {
	ID        string
	Name      string
	StartedOn string // YYYY-MM-DD，空 = 未填
	EndedOn   string
	Budget    string // 元
	Note      string
	Editing   bool
}

// specialFormFromProject 已有专项 → 表单预填值
func specialFormFromProject(p domain.SpecialProject) specialFormVM {
	f := specialFormVM{
		ID:      p.ID,
		Name:    p.Name,
		Budget:  fenToYuanInput(p.BudgetFen),
		Note:    p.Note,
		Editing: p.ID != "",
	}
	if !p.StartedOn.IsZero() {
		f.StartedOn = p.StartedOn.Format("2006-01-02")
	}
	if !p.EndedOn.IsZero() {
		f.EndedOn = p.EndedOn.Format("2006-01-02")
	}
	return f
}

// Specials GET /specials —— 专项列表 + 已花费/预算/执行率/跨科目构成 + 新建/编辑表单。
// ?edit=<id> 把该专项填进右侧表单（编辑入口是 SSR 的，不引额外 JS）。
func (h *Handler) Specials(w http.ResponseWriter, r *http.Request) {
	var form specialFormVM
	if id := strings.TrimSpace(r.URL.Query().Get("edit")); id != "" && h.specialRepo != nil {
		p, err := h.specialRepo.Get(r.Context(), id)
		if err != nil && !errors.Is(err, port.ErrNotFound) {
			h.serverError(w, err)
			return
		}
		if err == nil {
			form = specialFormFromProject(p)
		}
	}
	h.renderSpecials(w, r, form, "", http.StatusOK)
}

func (h *Handler) renderSpecials(w http.ResponseWriter, r *http.Request, form specialFormVM, errMsg string, status int) {
	data, err := h.specialView.Load(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := specialsVM{
		pageBase: pageBase{Title: "专项开支", Nav: "specials", Flash: h.flash.pop(w, r)},
		Data:     data,
		Form:     form,
		Error:    errMsg,
	}
	h.renderPage(w, status, "specials", vm)
}

// SaveSpecial POST /specials —— 表单新建/编辑（带 id 即编辑）
func (h *Handler) SaveSpecial(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderSpecials(w, r, specialFormVM{}, "表单解析失败", http.StatusBadRequest)
		return
	}
	p, err := specialFromForm(r)
	if err != nil {
		// 回填用户刚填的内容，报错不该把表单清空
		h.renderSpecials(w, r, specialFormFromRequest(r), err.Error(), http.StatusBadRequest)
		return
	}
	if p.ID == "" {
		p.ID = newID()
		p.CreatedAt = time.Now()
	}
	if err := h.specialView.Upsert(r.Context(), &p); err != nil {
		h.renderSpecials(w, r, specialFormFromRequest(r), err.Error(), http.StatusBadRequest)
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

// specialFormFromRequest 原样回显用户提交的表单（保存失败时用，日期/预算不做解析）
func specialFormFromRequest(r *http.Request) specialFormVM {
	id := strings.TrimSpace(r.FormValue("id"))
	return specialFormVM{
		ID:        id,
		Name:      strings.TrimSpace(r.FormValue("name")),
		StartedOn: strings.TrimSpace(r.FormValue("started_on")),
		EndedOn:   strings.TrimSpace(r.FormValue("ended_on")),
		Budget:    strings.TrimSpace(r.FormValue("budget")),
		Note:      strings.TrimSpace(r.FormValue("note")),
		Editing:   id != "",
	}
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
