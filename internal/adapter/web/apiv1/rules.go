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
	p, err := a.nav.Resolve(req.Type, req.Period)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "周期格式不正确："+req.Period)
		return
	}
	n, err := a.txQuery.ApplyRule(r.Context(), id, p, parseAccount(req.Account))
	if err != nil {
		switch {
		case isNotFound(err):
			WriteError(w, http.StatusNotFound, "not_found", "规则不存在")
		case isUserFacing(err):
			// 死规则 / 无目标科目：usecase 给的中文就是面向用户的解释，原样回显
			WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		default:
			a.internalError(w, err)
		}
		return
	}
	WriteData(w, map[string]int{"updated": n})
}

// isUserFacing 判定 usecase.ApplyRule 返回的是「面向用户的业务拒绝」而非内部故障。
// usecase 没有为这几种拒绝定义哨兵，而基础设施错误（begin/exec 等）总是 %w 包装了底层错误，
// 业务拒绝则是无包装的 fmt.Errorf——据此区分，不做字符串匹配。
// 脆弱点：repo 若直接 return 裸驱动错误会被误判为 400；根治办法是 usecase 加哨兵错误。
func isUserFacing(err error) bool {
	type unwrapper interface{ Unwrap() error }
	_, wrapped := err.(unwrapper)
	return !wrapped
}

// parseAccount 未知/空值一律 family（查询视图），与 usecase、handler 里的同名逻辑一致
func parseAccount(s string) domain.Account {
	switch domain.Account(s) {
	case domain.AccountHusband:
		return domain.AccountHusband
	case domain.AccountWife:
		return domain.AccountWife
	default:
		return domain.AccountFamily
	}
}
