package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authCookieName = "family_finances_auth"
	sessionTTL     = 30 * 24 * time.Hour
)

type authManager struct {
	key     string
	limiter *loginLimiter
}

func newAuthManager(key string) *authManager {
	key = strings.TrimSpace(key)
	if key == "" {
		return &authManager{}
	}
	return &authManager{key: key, limiter: newLoginLimiter()}
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

// sign 对 payload 做 HMAC-SHA256 签名，v2 前缀与旧版固定 token 区分开
func (a *authManager) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(a.key))
	_, _ = mac.Write([]byte("family-finances-auth-session-v2|" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// newToken 生成带过期时间的会话 token：exp.sig
func (a *authManager) newToken(now time.Time) string {
	exp := strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	return exp + "." + a.sign(exp)
}

func (a *authManager) validToken(v string, now time.Time) bool {
	exp, sig, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(a.sign(exp))) {
		return false
	}
	t, err := strconv.ParseInt(exp, 10, 64)
	return err == nil && now.Unix() < t
}

func (a *authManager) authenticated(r *http.Request) bool {
	if !a.enabled() {
		return true
	}
	c, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	return a.validToken(c.Value, time.Now())
}

func (a *authManager) setSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    a.newToken(time.Now()),
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
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

// ----- 登录失败限流 -----

const (
	loginFailLimit  = 5
	loginFailWindow = 15 * time.Minute
	loginMaxEntries = 4096
)

// loginLimiter 按来源 IP 记登录失败次数，窗口内超限直接拒绝。
// 部署在反代后时所有请求共享反代 IP，会退化为全局限流——对家庭应用可接受，
// 且仍然有效阻断口令爆破。
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*failRecord
}

type failRecord struct {
	count       int
	windowStart time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: make(map[string]*failRecord)}
}

func (l *loginLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.fails[ip]
	if !ok || now.Sub(rec.windowStart) > loginFailWindow {
		return true
	}
	return rec.count < loginFailLimit
}

func (l *loginLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.fails[ip]
	if !ok || now.Sub(rec.windowStart) > loginFailWindow {
		if len(l.fails) >= loginMaxEntries {
			l.evictExpiredLocked(now)
		}
		l.fails[ip] = &failRecord{count: 1, windowStart: now}
		return
	}
	rec.count++
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

func (l *loginLimiter) evictExpiredLocked(now time.Time) {
	for k, rec := range l.fails {
		if now.Sub(rec.windowStart) > loginFailWindow {
			delete(l.fails, k)
		}
	}
	// 全是活跃记录时兜底清空，防止 map 无限增长
	if len(l.fails) >= loginMaxEntries {
		l.fails = make(map[string]*failRecord)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ----- Handlers -----

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
	h.renderPage(w, http.StatusOK, "auth", vm)
}

func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderLoginError(w, r, "表单解析失败", http.StatusBadRequest)
		return
	}
	ip := clientIP(r)
	now := time.Now()
	if h.auth.enabled() && !h.auth.limiter.allow(ip, now) {
		h.renderLoginError(w, r, "尝试次数过多，请 15 分钟后再试", http.StatusTooManyRequests)
		return
	}
	if !h.auth.checkKey(r.FormValue("auth_key")) {
		if h.auth.enabled() {
			h.auth.limiter.fail(ip, now)
		}
		h.renderLoginError(w, r, "认证 key 不正确", http.StatusUnauthorized)
		return
	}
	if h.auth.enabled() {
		h.auth.limiter.reset(ip)
	}
	h.auth.setSession(w, r)
	http.Redirect(w, r, safeNextURL(r.FormValue("next")), http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.auth.clearSession(w, r)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (h *Handler) renderLoginError(w http.ResponseWriter, r *http.Request, msg string, status int) {
	vm := authVM{
		pageBase: pageBase{Title: "安全认证", Nav: "auth"},
		Error:    msg,
		Next:     safeNextURL(r.FormValue("next")),
	}
	h.renderPage(w, status, "auth", vm)
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
	// 浏览器会把 Location 里的 \ 归一化成 /，因此 /\evil.com 等价于 //evil.com，必须一并拒绝
	if raw == "" || strings.ContainsAny(raw, "\\") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	return raw
}
