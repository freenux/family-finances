package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"family-finances/internal/usecase"
)

type askVM struct {
	pageBase
	LLMEnabled bool
	Presets    []string
}

var askPresets = []string{
	"本季结余率怎么样？",
	"支出里变化最大的是哪一类？",
	"活钱能覆盖几个月支出？",
	"有没有预算超支的科目？",
	"净资产比上季度变化了多少？",
}

// AskPage GET /ask —— 聊天页（预设问题 chips + 输入框）
func (h *Handler) AskPage(w http.ResponseWriter, r *http.Request) {
	vm := askVM{
		pageBase:   pageBase{Title: "AI 问数", Nav: "ask"},
		LLMEnabled: h.ask.Enabled(),
		Presets:    askPresets,
	}
	h.renderPage(w, http.StatusOK, "ask", vm)
}

type askReq struct {
	Question string `json:"question"`
}

// AskAPI POST /api/ask —— 同步单轮问答（30s 超时）
func (h *Handler) AskAPI(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req askReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := h.ask.Execute(ctx, req.Question)
	if err != nil {
		if errors.Is(err, usecase.ErrLLMDisabled) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		// 输入类错误（空/过长）返回 400，其余 500
		if req.Question == "" || len([]rune(req.Question)) > 200 {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.log.Error("ask", "err", err)
		http.Error(w, "问答失败，请稍后重试", http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}
