package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

var insuranceTypes = []string{"定期寿险", "终身寿险", "重疾险", "医疗险", "意外险", "年金险", "其他"}

type insuranceVM struct {
	pageBase
	Data  usecase.InsuranceViewData
	Types []string
	Error string
}

// Insurance GET /insurance —— 保单表格 + 双十缺口检查面板
func (h *Handler) Insurance(w http.ResponseWriter, r *http.Request) {
	h.renderInsurance(w, r, "", http.StatusOK)
}

func (h *Handler) renderInsurance(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	data, err := h.insView.Load(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	vm := insuranceVM{
		pageBase: pageBase{Title: "保单登记", Nav: "insurance", Flash: h.flash.pop(w, r)},
		Data:     data,
		Types:    insuranceTypes,
		Error:    errMsg,
	}
	h.renderPage(w, status, "insurance", vm)
}

// SaveInsurance POST /api/insurance
func (h *Handler) SaveInsurance(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderInsurance(w, r, "表单解析失败", http.StatusBadRequest)
		return
	}
	p, err := policyFromForm(r)
	if err != nil {
		h.renderInsurance(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	if p.ID == "" {
		p.ID = newID()
	}
	p.UpdatedAt = time.Now()
	if err := h.policyRepo.UpsertPolicy(r.Context(), &p); err != nil {
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "保单已保存。")
	http.Redirect(w, r, "/insurance", http.StatusSeeOther)
}

// DeleteInsurance POST /api/insurance/{id}/delete
func (h *Handler) DeleteInsurance(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	if err := h.policyRepo.DeletePolicy(r.Context(), id); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			http.Error(w, "保单不存在", http.StatusNotFound)
			return
		}
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "保单已删除。")
	http.Redirect(w, r, "/insurance", http.StatusSeeOther)
}

func policyFromForm(r *http.Request) (domain.InsurancePolicy, error) {
	var p domain.InsurancePolicy
	p.ID = strings.TrimSpace(r.FormValue("id"))
	p.InsuredPerson = strings.TrimSpace(r.FormValue("insured_person"))
	if p.InsuredPerson == "" {
		return p, fmt.Errorf("请填写被保险人")
	}
	typeOK := false
	for _, t := range insuranceTypes {
		if r.FormValue("insurance_type") == t {
			typeOK = true
			break
		}
	}
	if !typeOK {
		return p, fmt.Errorf("请选择保险类型")
	}
	p.InsuranceType = r.FormValue("insurance_type")
	p.CompanyProduct = strings.TrimSpace(r.FormValue("company_product"))
	p.Coverage = strings.TrimSpace(r.FormValue("coverage"))

	var err error
	if p.CoverageAmount, err = parseNonNegativeYuan(r.FormValue("coverage_amount"), "保额"); err != nil {
		return p, err
	}
	if p.AnnualPremium, err = parseNonNegativeYuan(r.FormValue("annual_premium"), "年缴保费"); err != nil {
		return p, err
	}
	if p.RenewalDate, err = parseOptionalDate(r.FormValue("renewal_date"), "续期日"); err != nil {
		return p, err
	}
	p.Notes = strings.TrimSpace(r.FormValue("notes"))
	return p, nil
}
