package apiv1

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"family-finances/internal/usecase"
)

// ListTransactions GET /api/v1/transactions —— 薄壳：query 原样交给 usecase.TxQuery。
// 参数校验与归一（status 缺省、page_size 钳制、sort 白名单、rule_id 只看支出……）全在 usecase，
// 这里不重做，保证归一规则只有一份。
func (a *API) ListTransactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := a.txQuery.Execute(r.Context(), txRequestFromQuery(q))
	if err != nil {
		a.writeUseCaseError(w, err, "规则不存在")
		return
	}
	WriteData(w, res)
}

// txRequestFromQuery 列表与 by-filter 共用的 query → 请求映射，两个接口的筛选参数因此逐字相同
func txRequestFromQuery(q url.Values) usecase.TxQueryRequest {
	return usecase.TxQueryRequest{
		Type: q.Get("type"), Period: q.Get("period"), Account: q.Get("account"),
		Direction: q.Get("direction"), Source: q.Get("source"), Status: q.Get("status"),
		Category: q.Get("category"), Special: q.Get("special"),
		Member: q.Get("member"), Keyword: q.Get("keyword"), RuleID: q.Get("rule_id"),
		Sort: q.Get("sort"), Order: q.Get("order"),
		Page: q.Get("page"), PageSize: q.Get("page_size"),
	}
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

// BatchUpdateTransactionsByFilter PATCH /api/v1/transactions/by-filter?<与列表相同的筛选参数>
// body {"special_id": "..."}（空串 = 归回日常）→ {"data":{"updated":N}}。
// 作用于整个筛选结果（不止当页），排序与分页参数被忽略；筛选归一与 WHERE 都复用列表那一份。
func (a *API) BatchUpdateTransactionsByFilter(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		SpecialID *string `json:"special_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	updated, err := a.txUpdate.AssignSpecialByFilter(r.Context(), txRequestFromQuery(r.URL.Query()), req.SpecialID)
	if err != nil {
		a.writeUseCaseError(w, err, "规则不存在")
		return
	}
	WriteData(w, map[string]int{"updated": updated})
}
