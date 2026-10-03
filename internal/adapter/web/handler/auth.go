package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"family-finances/internal/adapter/web/apiv1"
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
	tok, _ := a.newTokenExp(now)
	return tok
}

// tokenExpiry 过期时间的唯一来源；newTokenExp 与 expires_at 都从这里算
func tokenExpiry(now time.Time) time.Time { return now.Add(sessionTTL) }

// newTokenExp 同 newToken，并返回编码进 token 的过期时间（秒级精度）
func (a *authManager) newTokenExp(now time.Time) (string, time.Time) {
	expAt := time.Unix(tokenExpiry(now).Unix(), 0)
	exp := strconv.FormatInt(expAt.Unix(), 10)
	return exp + "." + a.sign(exp), expAt
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
	now := time.Now()
	if c, err := r.Cookie(authCookieName); err == nil && a.validToken(c.Value, now) {
		return true
	}
	// 小程序的 wx.request 不可靠地携带 Cookie，补一条 Bearer 通道；token 与 Cookie 同一套签名
	if tok, ok := bearerToken(r); ok {
		return a.validToken(tok, now)
	}
	return false
}

// bearerToken 宽容地解析 Authorization: Bearer <token>：scheme 大小写不敏感，容忍前后空白；
// 格式不对（无头、缺 token、scheme 不是 Bearer）一律视为没有这个头。
func bearerToken(r *http.Request) (string, bool) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, rest, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	tok := strings.TrimSpace(rest)
	return tok, tok != ""
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

// IssueAPIToken 处理 POST /api/v1/auth/token：用 key 换一个 Bearer token（供小程序使用）。
// 必须挂在 RequireAuth 之外，否则没 token 的客户端拿不到 token。
// 与 /auth/login 共用同一个 loginLimiter，否则这里就是绕过网页登录限流的暴力破解后门。
// 鉴权未启用时无需 token：返回空 token 与零值 expires_at，客户端可直接不带头访问。
// 信封复用 apiv1.WriteData/WriteError（handler -> apiv1 单向依赖）。
func (h *Handler) IssueAPIToken(w http.ResponseWriter, r *http.Request) {
	if !h.auth.enabled() {
		apiv1.WriteData(w, map[string]string{"token": "", "expires_at": ""})
		return
	}
	ip := clientIP(r)
	now := time.Now()
	if !h.auth.limiter.allow(ip, now) {
		apiv1.WriteError(w, http.StatusTooManyRequests, "too_many_requests", "尝试次数过多，请 15 分钟后再试")
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		apiv1.WriteError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	if !h.auth.checkKey(body.Key) {
		h.auth.limiter.fail(ip, now)
		apiv1.WriteError(w, http.StatusUnauthorized, "unauthorized", "认证 key 不正确")
		return
	}
	h.auth.limiter.reset(ip)
	tok, expAt := h.auth.newTokenExp(now)
	apiv1.WriteData(w, map[string]string{
		"token":      tok,
		"expires_at": expAt.UTC().Format(time.RFC3339),
	})
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
		if r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/api/v1/") {
			apiv1.WriteError(w, http.StatusUnauthorized, "unauthorized", "未认证或登录已过期，请重新登录")
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
