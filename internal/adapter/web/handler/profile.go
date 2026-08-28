package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type profileVM struct {
	pageBase
	P            domain.FamilyProfile
	HasProfile   bool
	Grade        string
	EquityCapPct int
	Error        string
	// 表单回显：元字符串（分转换后）
	AnnualIncomeYuan    string
	MortgageMonthlyYuan string
	MonthlyExpenseYuan  string // 空 = 用流水自动值
}

// Profile GET /profile —— 风险画像表单 + 实时等级卡
func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) {
	p := domain.DefaultFamilyProfile()
	hasProfile := false
	stored, err := h.profileRepo.Get(r.Context())
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		h.serverError(w, err)
		return
	}
	if stored != nil {
		p = *stored
		hasProfile = true
	}
	h.renderProfile(w, r, p, hasProfile, "", http.StatusOK)
}

// SaveProfile POST /profile —— 校验后 upsert 单行
func (h *Handler) SaveProfile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderProfile(w, r, domain.DefaultFamilyProfile(), false, "表单解析失败", http.StatusBadRequest)
		return
	}
	p, err := profileFromForm(r)
	if err != nil {
		h.renderProfile(w, r, p, false, err.Error(), http.StatusBadRequest)
		return
	}
	p.UpdatedAt = time.Now()
	if err := h.profileRepo.Upsert(r.Context(), &p); err != nil {
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "画像已保存。")
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

func (h *Handler) renderProfile(w http.ResponseWriter, r *http.Request, p domain.FamilyProfile, hasProfile bool, errMsg string, status int) {
	grade, cap := domain.CalcProfileGrade(p)
	vm := profileVM{
		pageBase:            pageBase{Title: "风险画像", Nav: "profile", Flash: h.flash.pop(w, r)},
		P:                   p,
		HasProfile:          hasProfile,
		Grade:               grade,
		EquityCapPct:        cap,
		Error:               errMsg,
		AnnualIncomeYuan:    fenToYuanInput(p.AnnualIncomeFen),
		MortgageMonthlyYuan: fenToYuanInput(p.MortgageMonthlyFen),
	}
	if p.MonthlyExpenseFen != nil {
		vm.MonthlyExpenseYuan = fenToYuanInput(*p.MonthlyExpenseFen)
	}
	h.renderPage(w, status, "profile", vm)
}

// profileFromForm 解析并校验表单；返回的 profile 同时用于错误时回显
func profileFromForm(r *http.Request) (domain.FamilyProfile, error) {
	p := domain.DefaultFamilyProfile()
	p.FamilyStructure = strings.TrimSpace(r.FormValue("family_structure"))

	age, err := strconv.Atoi(strings.TrimSpace(r.FormValue("main_age")))
	if err != nil || age < 18 || age > 100 {
		return p, fmt.Errorf("年龄需为 18–100 的整数")
	}
	p.MainAge = age

	switch v := r.FormValue("income_stability"); v {
	case domain.IncomeStable, domain.IncomeMedium, domain.IncomeUnstable:
		p.IncomeStability = v
	default:
		return p, fmt.Errorf("请选择收入稳定性")
	}
	switch v := r.FormValue("risk_appetite"); v {
	case domain.RiskConservative, domain.RiskBalanced, domain.RiskAggressive:
		p.RiskAppetite = v
	default:
		return p, fmt.Errorf("请选择风险偏好")
	}
	switch v := r.FormValue("horizon"); v {
	case domain.HorizonShort, domain.HorizonMedium, domain.HorizonLong:
		p.Horizon = v
	default:
		return p, fmt.Errorf("请选择投资年限")
	}

	if p.AnnualIncomeFen, err = parseNonNegativeYuan(r.FormValue("annual_income"), "家庭年收入"); err != nil {
		return p, err
	}
	if p.MortgageMonthlyFen, err = parseNonNegativeYuan(r.FormValue("mortgage_monthly"), "月供"); err != nil {
		return p, err
	}
	if raw := strings.TrimSpace(r.FormValue("monthly_expense")); raw != "" {
		v, err := parseNonNegativeYuan(raw, "月支出")
		if err != nil {
			return p, err
		}
		p.MonthlyExpenseFen = &v
	} else {
		p.MonthlyExpenseFen = nil
	}

	months, err := strconv.Atoi(strings.TrimSpace(r.FormValue("emergency_months")))
	if err != nil || months < 1 || months > 24 {
		return p, fmt.Errorf("应急金目标月数需为 1–24 的整数")
	}
	p.EmergencyMonths = months
	return p, nil
}

// parseNonNegativeYuan 元 → 分，允许 0 与空（视为 0）
func parseNonNegativeYuan(s, field string) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1e10 {
		return 0, fmt.Errorf("%s金额格式不正确", field)
	}
	return int64(f*100 + 0.5), nil
}

// parseOptionalDate 解析 <input type="date"> 的值；空串 → 零值（表示"未填"/"进行中"）。
// 专项的开始/结束日期与保单续期日共用，label 用于拼中文报错。
func parseOptionalDate(v, label string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", v, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s格式应为 YYYY-MM-DD", label)
	}
	return t, nil
}

// fenToYuanInput 分 → 元字符串（整数元省略小数）
func fenToYuanInput(fen int64) string {
	if fen == 0 {
		return ""
	}
	if fen%100 == 0 {
		return strconv.FormatInt(fen/100, 10)
	}
	return fmt.Sprintf("%d.%02d", fen/100, fen%100)
}
