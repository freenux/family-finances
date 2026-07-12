package handler

import (
	"encoding/json"
	"net/http"

	"family-finances/internal/domain"
	"family-finances/internal/usecase"
)

type budgetVM struct {
	pageBase
	Data     usecase.BudgetViewData
	RowsJSON string // 给 Alpine 就地编辑用
}

type budgetRowJSON struct {
	CategoryID string `json:"category_id"`
	Name       string `json:"name"`
	GroupName  string `json:"group_name"`
	BudgetFen  int64  `json:"budget_fen"`
	SpentFen   int64  `json:"spent_fen"`
	Status     string `json:"status"`
}

// Budget GET /budget —— 预算表 + 周期交易 + 90 天现金流
func (h *Handler) Budget(w http.ResponseWriter, r *http.Request) {
	data, err := h.budgetView.Load(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	rows := make([]budgetRowJSON, 0, len(data.Rows))
	for _, row := range data.Rows {
		rows = append(rows, budgetRowJSON{
			CategoryID: row.CategoryID, Name: row.Name, GroupName: row.GroupName,
			BudgetFen: row.BudgetFen, SpentFen: row.SpentFen, Status: row.Status,
		})
	}
	rowsJSON, _ := json.Marshal(rows)
	vm := budgetVM{
		pageBase: pageBase{Title: "预算与周期", Nav: "budget", Flash: h.flash.pop(w, r)},
		Data:     data,
		RowsJSON: string(rowsJSON),
	}
	h.renderPage(w, http.StatusOK, "budget", vm)
}

type budgetsPutReq struct {
	Amounts map[string]int64 `json:"amounts"` // category_id -> quarter_amount_fen
}

// SaveBudgets PUT /api/budgets —— 整表提交
func (h *Handler) SaveBudgets(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req budgetsPutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// 校验：必须是已知的二级支出科目，金额非负
	cats, err := h.catRepo.ListAll(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	valid := map[string]bool{}
	for _, c := range cats {
		if c.Level == 2 && c.Type == domain.CategoryTypeExpense {
			valid[c.ID] = true
		}
	}
	for catID, amount := range req.Amounts {
		if !valid[catID] {
			http.Error(w, "非法的科目: "+catID, http.StatusBadRequest)
			return
		}
		if amount < 0 {
			http.Error(w, "预算不能为负: "+catID, http.StatusBadRequest)
			return
		}
	}
	if err := h.budgetRepo.ReplaceBudgets(r.Context(), req.Amounts); err != nil {
		h.serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
