package apiv1

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

// isNotFound 判定「目标不存在」。sqlite.CategoryRepo.GetRule 现在返回的是 sql.ErrNoRows
// 而不是 port.ErrNotFound（与 port 的约定不一致），两种都认，否则不存在的规则会变成 500。
func isNotFound(err error) bool {
	return errors.Is(err, port.ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

// ListTransactions GET /api/v1/transactions —— 薄壳：query 原样交给 usecase.TxQuery。
// 参数校验与归一（status 缺省、page_size 钳制、sort 白名单、rule_id 只看支出……）全在 usecase，
// 这里不重做，保证归一规则只有一份。
func (a *API) ListTransactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// usecase 对非法 period 返回的是没有哨兵的普通错误，无法与 repo 错误区分；
	// 所以先用 PeriodNav.Resolve 预检一次（只为得到 400，周期的最终归一仍在 usecase）。
	if _, err := a.nav.Resolve(q.Get("type"), q.Get("period")); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "周期格式不正确："+q.Get("period"))
		return
	}
	res, err := a.txQuery.Execute(r.Context(), usecase.TxQueryRequest{
		Type: q.Get("type"), Period: q.Get("period"), Account: q.Get("account"),
		Direction: q.Get("direction"), Source: q.Get("source"), Status: q.Get("status"),
		Category: q.Get("category"), Special: q.Get("special"),
		Member: q.Get("member"), Keyword: q.Get("keyword"), RuleID: q.Get("rule_id"),
		Sort: q.Get("sort"), Order: q.Get("order"),
		Page: q.Get("page"), PageSize: q.Get("page_size"),
	})
	if err != nil {
		switch {
		case isNotFound(err):
			WriteError(w, http.StatusNotFound, "not_found", "规则不存在")
		default:
			a.internalError(w, err)
		}
		return
	}
	WriteData(w, res)
}

// ----- PATCH：与 handler.UpdateTransaction / BatchUpdateTransactions 是孪生实现 -----
//
// 行为必须与 internal/adapter/web/handler/handler.go 里的同名 handler 保持一致
// （body 字段、category_id 非空自动转 confirmed、special_id 空串归回日常、批量单事务）。
// 区别仅在传输层：这里用信封与稳定 code，handler 版用 http.Error 与 204。
// 改规则时两处一起改；待 usecase 层有共享入口后应合并。

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

// 一次批量操作的上限，与 handler 版一致
const maxBatchTxIDs = 1000

func (a *API) UpdateTransaction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		WriteError(w, http.StatusBadRequest, "bad_request", "缺少流水 id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req updateTxReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	patch := port.TransactionUpdate{}
	if req.CategoryID != nil {
		v := *req.CategoryID
		if v != "" {
			ok, err := a.isLeafCategory(r, v)
			if err != nil {
				a.internalError(w, err)
				return
			}
			if !ok {
				WriteError(w, http.StatusBadRequest, "bad_request", "请选择有效的二级分类")
				return
			}
		}
		patch.CategoryID = &v
		// 指定了分类就把状态自动转 confirmed；若同一请求显式给了 status，下面会覆盖它（与 handler 一致）
		if v != "" {
			st := domain.TxStatusConfirmed
			patch.Status = &st
		}
	}
	if req.Note != nil {
		v := *req.Note
		patch.Note = &v
	}
	if req.Status != nil {
		s := domain.TxStatus(*req.Status)
		switch s {
		case domain.TxStatusPendingReview, domain.TxStatusConfirmed, domain.TxStatusExcluded:
		default:
			WriteError(w, http.StatusBadRequest, "bad_request", "状态不合法")
			return
		}
		patch.Status = &s
	}
	if req.Account != nil {
		ac := domain.Account(*req.Account)
		if !ac.IsStorageAccount() {
			WriteError(w, http.StatusBadRequest, "bad_request", "账户不合法")
			return
		}
		patch.Account = &ac
	}
	if req.Member != nil {
		m := strings.TrimSpace(*req.Member)
		if len([]rune(m)) > 20 {
			WriteError(w, http.StatusBadRequest, "bad_request", "成员标注过长（限 20 字）")
			return
		}
		patch.Member = &m
	}
	if req.SpecialID != nil {
		v := strings.TrimSpace(*req.SpecialID)
		if err := a.ensureSpecial(r, v); err != nil {
			WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		patch.SpecialID = &v
	}
	if err := a.tx.Update(r.Context(), id, patch); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			WriteError(w, http.StatusNotFound, "not_found", "流水不存在")
			return
		}
		a.internalError(w, err)
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
	if len(req.IDs) == 0 {
		WriteError(w, http.StatusBadRequest, "bad_request", "请选择要归类的流水")
		return
	}
	if len(req.IDs) > maxBatchTxIDs {
		WriteError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("一次最多处理 %d 条流水", maxBatchTxIDs))
		return
	}
	if req.SpecialID == nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "缺少 special_id")
		return
	}
	specialID := strings.TrimSpace(*req.SpecialID)
	if err := a.ensureSpecial(r, specialID); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ids := make([]string, 0, len(req.IDs))
	for _, id := range req.IDs {
		if id != "" {
			ids = append(ids, id)
		}
	}
	// 单事务一次写完；不存在的 id 由 repo 静默跳过（客户端列表可能已过期）
	updated, err := a.tx.SetSpecialForIDs(r.Context(), ids, specialID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	WriteData(w, map[string]int{"updated": updated})
}

// isLeafCategory category_id 必须是二级科目
func (a *API) isLeafCategory(r *http.Request, id string) (bool, error) {
	cats, err := a.categories.ListAll(r.Context())
	if err != nil {
		return false, err
	}
	for _, c := range cats {
		if c.ID == id && c.Level == 2 {
			return true, nil
		}
	}
	return false, nil
}

// ensureSpecial 空串（清空）直接放行；非空时专项功能必须已启用且专项存在
func (a *API) ensureSpecial(r *http.Request, id string) error {
	if id == "" {
		return nil
	}
	if a.specialCheck == nil {
		return fmt.Errorf("专项功能未启用")
	}
	return a.specialCheck.Ensure(r.Context(), id)
}
