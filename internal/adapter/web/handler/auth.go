package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const authCookieName = "family_finances_auth"

type authManager struct {
	key         string
	sessionMark string
}

func newAuthManager(key string) *authManager {
	key = strings.TrimSpace(key)
	if key == "" {
		return &authManager{}
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte("family-finances-auth-session-v1"))
	return &authManager{
		key:         key,
		sessionMark: base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
	}
}

func (a *authManager) enabled() bool {
	return a != nil && a.key != ""
}

func (a *authManager) checkKey(input string) bool {
	if !a.enabled() {
		return true
	}
	input = strings.TrimSpace(input)
	return hmac.Equal([]byte(input), []byte(a.key))
}

func (a *authManager) authenticated(r *http.Request) bool {
	if !a.enabled() {
		return true
	}
	c, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(c.Value), []byte(a.sessionMark))
}

func (a *authManager) setSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    a.sessionMark,
		Path:     "/",
		MaxAge:   int((24 * 30 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *authManager) clearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

type authVM struct {
	pageBase
	Error string
	Next  string
}

func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	if h.auth.authenticated(r) {
		http.Redirect(w, r, safeNextURL(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	vm := authVM{
		pageBase: pageBase{Title: "安全认证", Nav: "auth"},
		Next:     safeNextURL(r.URL.Query().Get("next")),
	}
	if err := h.render.RenderPage(w, "auth", vm); err != nil {
		h.serverError(w, err)
	}
}

func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderLoginError(w, r, "表单解析失败")
		return
	}
	if !h.auth.checkKey(r.FormValue("auth_key")) {
		h.renderLoginError(w, r, "认证 key 不正确")
		return
	}
	h.auth.setSession(w, r)
	http.Redirect(w, r, safeNextURL(r.FormValue("next")), http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.auth.clearSession(w, r)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (h *Handler) renderLoginError(w http.ResponseWriter, r *http.Request, msg string) {
	vm := authVM{
		pageBase: pageBase{Title: "安全认证", Nav: "auth"},
		Error:    msg,
		Next:     safeNextURL(r.FormValue("next")),
	}
	w.WriteHeader(http.StatusUnauthorized)
	if err := h.render.RenderPage(w, "auth", vm); err != nil {
		h.serverError(w, err)
	}
}

func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.auth.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "未认证", http.StatusUnauthorized)
			return
		}
		q := url.Values{}
		q.Set("next", r.URL.RequestURI())
		http.Redirect(w, r, "/auth/login?"+q.Encode(), http.StatusSeeOther)
	})
}

func safeNextURL(raw string) string {
	if raw == "" {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	return raw
}
