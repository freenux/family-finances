package apiv1

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"family-finances/internal/domain"
)

type applyRuleReq struct {
	Type    string `json:"type"`
	Period  string `json:"period"`
	Account string `json:"account"`
}

// ApplyRule POST /api/v1/rules/{id}/apply —— 把规则一次性应用到周期+账户内命中的支出流水。
func (a *API) ApplyRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req applyRuleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	p, err := a.nav.Resolve(req.Type, req.Period, domain.PeriodQuarterly)
	if err != nil {
		a.writeUseCaseError(w, err, "")
		return
	}
	n, err := a.txQuery.ApplyRule(r.Context(), id, p, domain.ParseAccount(req.Account))
	if err != nil {
		a.writeUseCaseError(w, err, "规则不存在")
		return
	}
	WriteData(w, map[string]int{"updated": n})
}
