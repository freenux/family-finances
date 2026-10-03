package apiv1

import (
	"encoding/json"
	"errors"
	"net/http"

	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

// 信封工具。唯一实现在此；handler 单向 import apiv1 复用（apiv1 不得 import handler）。

// WriteData 成功信封 {"data": ...}
func WriteData(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// WriteError 失败信封 {"error":{"code","message"}}。
// code 是稳定短串：bad_request / unauthorized / not_found / internal；message 面向用户，简体中文。
func WriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// internalError 细节只进日志，不把 SQL / 文件路径等内部信息回给客户端
func (a *API) internalError(w http.ResponseWriter, err error) {
	a.log.Error("apiv1 internal error", "err", err)
	WriteError(w, http.StatusInternalServerError, "internal", "服务器内部错误，请稍后重试")
}

// writeUseCaseError 把 usecase 返回的错误映射成状态码，是各 handler 唯一的映射入口：
//
//   - usecase.ErrInvalidPeriod / ErrRuleNotApplicable / ErrInvalidInput → 400 bad_request，
//     message 用 usecase 给的那句中文（它本来就是面向用户的解释）；
//   - port.ErrNotFound → 404 not_found，message 由调用方按端点给（"规则不存在" / "流水不存在"）；
//   - 其余一律 500 internal，细节只进日志。
//
// 一律用 errors.Is 判定哨兵，不看错误文本、不看有没有 %w 包装——repo 直接返回的裸驱动错误
// 因此只会落进 500，不会被误判成 400 而泄露底层文本。
func (a *API) writeUseCaseError(w http.ResponseWriter, err error, notFoundMsg string) {
	switch {
	case errors.Is(err, usecase.ErrInvalidPeriod),
		errors.Is(err, usecase.ErrRuleNotApplicable),
		errors.Is(err, usecase.ErrInvalidInput):
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, port.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", notFoundMsg)
	default:
		a.internalError(w, err)
	}
}
