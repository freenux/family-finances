package apiv1

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"family-finances/internal/usecase"
)

// ListTransactions GET /api/v1/transactions —— 薄壳：query 原样交给 usecase.TxQuery。
// 参数校验与归一（status 缺省、page_size 钳制、sort 白名单、rule_id 只看支出……）全在 usecase，
// 这里不重做，保证归一规则只有一份。
func (a *API) ListTransactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := a.txQuery.Execute(r.Context(), usecase.TxQueryRequest{
		Type: q.Get("type"), Period: q.Get("period"), Account: q.Get("account"),
		Direction: q.Get("direction"), Source: q.Get("source"), Status: q.Get("status"),
		Category: q.Get("category"), Special: q.Get("special"),
		Member: q.Get("member"), Keyword: q.Get("keyword"), RuleID: q.Get("rule_id"),
		Sort: q.Get("sort"), Order: q.Get("order"),
		Page: q.Get("page"), PageSize: q.Get("page_size"),
	})
	if err != nil {
		a.writeUseCaseError(w, err, "规则不存在")
		return
	}
	WriteData(w, res)
}

// ----- PATCH：校验与装配全在 usecase.UpdateTransaction（与 SSR handler 共用），这里只管 HTTP -----

type updateTxReq struct {
	CategoryID *string `json:"category_id"`
	Note       *string `json:"note"`
	Status     *string `json:"status"`
	Account    *string `json:"account"`
	Member     *string `json:"member"`
	SpecialID  *string `json:"special_id"` // 空字符串 = 归回日常
}

type batchUpdateTxReq struct {
	IDs       []string `json:"ids"`
	SpecialID *string  `json:"special_id"` // 空字符串 = 批量归回日常
}

// 批量上限，常量本体在 usecase（与 SSR handler 共用）
const maxBatchTxIDs = usecase.MaxBatchTxIDs

func (a *API) UpdateTransaction(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req updateTxReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	if err := a.txUpdate.Update(r.Context(), chi.URLParam(r, "id"), usecase.TxPatch(req)); err != nil {
		a.writeUseCaseError(w, err, "流水不存在")
		return
	}
	WriteData(w, map[string]bool{"ok": true})
}

func (a *API) BatchUpdateTransactions(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var req batchUpdateTxReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	updated, err := a.txUpdate.AssignSpecial(r.Context(), req.IDs, req.SpecialID)
	if err != nil {
		a.writeUseCaseError(w, err, "流水不存在")
		return
	}
	WriteData(w, map[string]int{"updated": updated})
}
