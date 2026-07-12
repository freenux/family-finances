package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

var digestModules = []struct{ Key, Label string }{
	{"expense_compare", "支出环比"},
	{"top_categories", "Top 3 支出科目"},
	{"pending_count", "待确认流水数"},
	{"llm_note", "AI 一句话观察（需配置 key）"},
}

type digestVM struct {
	pageBase
	S              domain.DigestSettings
	Modules        []struct{ Key, Label string }
	EmailAvailable bool
	Error          string
}

// DigestSettingsPage GET /settings/digest
func (h *Handler) DigestSettingsPage(w http.ResponseWriter, r *http.Request) {
	h.renderDigestSettings(w, r, "", http.StatusOK)
}

func (h *Handler) renderDigestSettings(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	s := domain.DefaultDigestSettings()
	stored, err := h.digestRepo.GetSettings(r.Context())
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		h.serverError(w, err)
		return
	}
	if stored != nil {
		s = *stored
	}
	vm := digestVM{
		pageBase:       pageBase{Title: "推送设置", Nav: "digest", Flash: h.flash.pop(w, r)},
		S:              s,
		Modules:        digestModules,
		EmailAvailable: h.digestSvc != nil && h.digestSenderAvailable(domain.DigestChannelEmail),
		Error:          errMsg,
	}
	h.renderPage(w, status, "digest_settings", vm)
}

func (h *Handler) digestSenderAvailable(channel string) bool {
	return h.digestSender != nil && h.digestSender.Available(channel)
}

// SaveDigestSettings POST /settings/digest
func (h *Handler) SaveDigestSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderDigestSettings(w, r, "表单解析失败", http.StatusBadRequest)
		return
	}
	s := domain.DefaultDigestSettings()
	if stored, err := h.digestRepo.GetSettings(r.Context()); err == nil && stored != nil {
		s = *stored // 保留 last_sent_at
	}
	s.Enabled = r.FormValue("enabled") == "1"
	switch v := r.FormValue("cadence"); v {
	case domain.DigestWeekly, domain.DigestQuarterly:
		s.Cadence = v
	default:
		h.renderDigestSettings(w, r, "请选择推送频率", http.StatusBadRequest)
		return
	}
	switch v := r.FormValue("channel"); v {
	case domain.DigestChannelEmail, domain.DigestChannelWebhook:
		s.Channel = v
	default:
		h.renderDigestSettings(w, r, "请选择推送通道", http.StatusBadRequest)
		return
	}
	s.Target = strings.TrimSpace(r.FormValue("target"))
	if s.Channel == domain.DigestChannelWebhook && s.Target != "" {
		u, err := url.Parse(s.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			h.renderDigestSettings(w, r, "webhook URL 必须是 http(s) 地址", http.StatusBadRequest)
			return
		}
	}
	if s.Enabled && s.Target == "" {
		h.renderDigestSettings(w, r, "启用推送需填写收件地址 / webhook URL", http.StatusBadRequest)
		return
	}
	s.Modules = nil
	validModule := map[string]bool{}
	for _, m := range digestModules {
		validModule[m.Key] = true
	}
	for _, m := range r.Form["modules"] {
		if !validModule[m] {
			h.renderDigestSettings(w, r, "非法模块: "+m, http.StatusBadRequest)
			return
		}
		s.Modules = append(s.Modules, m)
	}

	if err := h.digestRepo.UpsertSettings(r.Context(), &s); err != nil {
		h.serverError(w, err)
		return
	}
	h.flash.set(w, "推送设置已保存。")
	http.Redirect(w, r, "/settings/digest", http.StatusSeeOther)
}

// DigestTestSend POST /api/digest/test —— 立即发送一封测试（不更新 last_sent_at）
func (h *Handler) DigestTestSend(w http.ResponseWriter, r *http.Request) {
	if err := h.digestSvc.SendNow(r.Context(), false); err != nil {
		h.flash.set(w, "测试发送失败："+err.Error())
	} else {
		h.flash.set(w, "测试摘要已发送。")
	}
	http.Redirect(w, r, "/settings/digest", http.StatusSeeOther)
}
