package apiv1

import (
	"encoding/json"
	"net/http"
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
