package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"family-finances/internal/adapter/web"
)

func TestRequireAuthDisabledAllowsRequest(t *testing.T) {
	h := &Handler{auth: newAuthManager("")}
	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	rec := httptest.NewRecorder()

	h.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRequireAuthRedirectsPageWhenMissingSession(t *testing.T) {
	h := &Handler{auth: newAuthManager("secret-key")}
	req := httptest.NewRequest(http.MethodGet, "/transactions?period=2026-05", nil)
	rec := httptest.NewRecorder()

	h.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not run")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusSeeOther)
	}
	want := "/auth/login?next=%2Ftransactions%3Fperiod%3D2026-05"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q; want %q", got, want)
	}
}

func TestRequireAuthRejectsAPIWhenMissingSession(t *testing.T) {
	h := &Handler{auth: newAuthManager("secret-key")}
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rec := httptest.NewRecorder()

	h.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not run")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestLoginSubmitSetsSessionCookie(t *testing.T) {
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	h := &Handler{render: renderer, auth: newAuthManager("secret-key")}
	form := url.Values{}
	form.Set("auth_key", "secret-key")
	form.Set("next", "/transactions")
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.LoginSubmit(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/transactions" {
		t.Fatalf("Location = %q; want /transactions", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != authCookieName {
		t.Fatalf("session cookie not set: %#v", cookies)
	}
}

func TestLoginSubmitRejectsWrongKey(t *testing.T) {
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	h := &Handler{render: renderer, auth: newAuthManager("secret-key")}
	form := url.Values{}
	form.Set("auth_key", "wrong")
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.LoginSubmit(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusUnauthorized)
	}
	if strings.Contains(rec.Body.String(), "secret-key") {
		t.Fatal("response leaked configured auth key")
	}
}

func TestSessionToken(t *testing.T) {
	a := newAuthManager("secret-key")
	now := time.Now()

	valid := a.newToken(now)
	if !a.validToken(valid, now) {
		t.Fatal("fresh token should be valid")
	}
	if !a.validToken(valid, now.Add(sessionTTL-time.Minute)) {
		t.Fatal("token should be valid just before TTL")
	}
	if a.validToken(valid, now.Add(sessionTTL+time.Minute)) {
		t.Fatal("token should expire after TTL")
	}
	if a.validToken(valid+"x", now) {
		t.Fatal("tampered signature should be rejected")
	}
	_, sig, _ := strings.Cut(valid, ".")
	if a.validToken("9999999999."+sig, now) {
		t.Fatal("forged expiry with reused signature should be rejected")
	}
	if a.validToken("garbage", now) {
		t.Fatal("malformed token should be rejected")
	}
	other := newAuthManager("other-key")
	if other.validToken(valid, now) {
		t.Fatal("token signed with a different key should be rejected")
	}
}

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	ip := "192.0.2.1"

	for i := 0; i < loginFailLimit; i++ {
		if !l.allow(ip, now) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		l.fail(ip, now)
	}
	if l.allow(ip, now) {
		t.Fatal("should be blocked after reaching fail limit")
	}
	if !l.allow("192.0.2.2", now) {
		t.Fatal("other ip should not be affected")
	}
	if !l.allow(ip, now.Add(loginFailWindow+time.Second)) {
		t.Fatal("should be allowed again after window expires")
	}
	l.reset(ip)
	if !l.allow(ip, now) {
		t.Fatal("should be allowed after reset")
	}
}

func TestLoginSubmitRateLimited(t *testing.T) {
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	h := &Handler{render: renderer, auth: newAuthManager("secret-key")}

	post := func(key string) *httptest.ResponseRecorder {
		form := url.Values{}
		form.Set("auth_key", key)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.0.2.9:12345"
		rec := httptest.NewRecorder()
		h.LoginSubmit(rec, req)
		return rec
	}

	for i := 0; i < loginFailLimit; i++ {
		if rec := post("wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d; want 401", i+1, rec.Code)
		}
	}
	if rec := post("secret-key"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d; want 429 (blocked even with correct key)", rec.Code)
	}
}

func TestSafeNextURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "/"},
		{name: "relative path", in: "/transactions?period=2026-05", want: "/transactions?period=2026-05"},
		{name: "absolute url", in: "https://example.com", want: "/"},
		{name: "protocol relative", in: "//example.com", want: "/"},
		{name: "backslash protocol relative", in: `/\example.com`, want: "/"},
		{name: "backslash anywhere", in: `/a\b`, want: "/"},
		{name: "relative without slash", in: "transactions", want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeNextURL(tt.in); got != tt.want {
				t.Fatalf("safeNextURL(%q) = %q; want %q", tt.in, got, tt.want)
			}
		})
	}
}

// okNext 是被 RequireAuth 包住的下游，放行时返回 204
var okNext = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

func TestRequireAuthBearerAndCookie(t *testing.T) {
	a := newAuthManager("secret-key")
	h := &Handler{auth: a}
	now := time.Now()
	valid := a.newToken(now)
	expired := a.newToken(now.Add(-sessionTTL - time.Hour)) // 签发于 31 天前，exp 已过
	exp := strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	forged := exp + "." + newAuthManager("other-key").sign(exp) // 用别的 key 伪造签名

	tests := []struct {
		name   string
		header string
		cookie string
		want   int
		why    string
	}{
		{"有效 Bearer", "Bearer " + valid, "", 204, "token 与 Cookie 同一套签名，有效即放行"},
		{"scheme 小写", "bearer " + valid, "", 204, "scheme 大小写不敏感"},
		{"前后空白", "  Bearer   " + valid + "  ", "", 204, "容忍前后空白"},
		{"过期 Bearer", "Bearer " + expired, "", 401, "exp 已过必须拒绝"},
		{"伪造签名", "Bearer " + forged, "", 401, "签名不是本服务 key 算出的必须拒绝"},
		{"空头", "", "", 401, "没有任何凭据"},
		{"只有 Bearer", "Bearer", "", 401, "缺 token 当作没有这个头"},
		{"只有 Bearer 加空格", "Bearer   ", "", 401, "token 为空当作没有这个头"},
		{"scheme 写错", "Basic " + valid, "", 401, "非 Bearer scheme 不认"},
		{"裸 token 无 scheme", valid, "", 401, "没有 scheme 不认"},
		{"Cookie 仍然有效", "", valid, 204, "回归：Cookie 路径不受影响"},
		{"Cookie 无效但 Bearer 有效", "Bearer " + valid, "garbage", 204, "Cookie 不中才看 Bearer"},
		{"Cookie 过期", "", expired, 401, "回归：Cookie 过期仍被拒"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: authCookieName, Value: tt.cookie})
			}
			rec := httptest.NewRecorder()
			h.RequireAuth(okNext).ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d; want %d（%s）", rec.Code, tt.want, tt.why)
			}
			if tt.want == 401 && !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
				t.Fatalf("body = %q; want v1 错误信封含 unauthorized（/api/v1 的 401 要给客户端可解析的 JSON）", rec.Body.String())
			}
		})
	}
}

func TestRequireAuthBareAPIV1Path(t *testing.T) {
	h := &Handler{auth: newAuthManager("secret-key")}
	rec := httptest.NewRecorder()
	h.RequireAuth(okNext).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1", nil))
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("status/body = %d %q; want 401 JSON 信封（正好 /api/v1 也要走 v1 分支）", rec.Code, rec.Body)
	}
}

func TestRequireAuthDisabledIgnoresBearer(t *testing.T) {
	h := &Handler{auth: newAuthManager("")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
	req.Header.Set("Authorization", "Bearer junk")
	rec := httptest.NewRecorder()
	h.RequireAuth(okNext).ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d; want 204（鉴权未启用时一律放行）", rec.Code)
	}
}

func postToken(h *Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	h.IssueAPIToken(rec, req)
	return rec
}

func TestIssueAPIToken(t *testing.T) {
	tests := []struct {
		name     string
		key      string // Handler 的 key
		body     string
		want     int
		wantCode string
	}{
		{"key 正确", "secret-key", `{"key":"secret-key"}`, 200, ""},
		{"key 错误", "secret-key", `{"key":"nope"}`, 401, "unauthorized"},
		{"body 不是 JSON", "secret-key", `not json`, 400, "bad_request"},
		{"鉴权未启用", "", `{}`, 200, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{auth: newAuthManager(tt.key)}
			rec := postToken(h, tt.body)
			if rec.Code != tt.want {
				t.Fatalf("status = %d; want %d; body=%s", rec.Code, tt.want, rec.Body)
			}
			var resp struct {
				Data  map[string]string `json:"data"`
				Error map[string]string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("响应不是 JSON: %v", err)
			}
			if tt.wantCode != "" {
				if resp.Error["code"] != tt.wantCode || resp.Error["message"] == "" {
					t.Fatalf("error = %v; want code=%s 且带 message", resp.Error, tt.wantCode)
				}
				return
			}
			if tt.key == "" {
				if resp.Data["token"] != "" {
					t.Fatalf("token = %q; want 空（未启用鉴权无需 token）", resp.Data["token"])
				}
				return
			}
			tok := resp.Data["token"]
			if !h.auth.validToken(tok, time.Now()) {
				t.Fatalf("签发的 token %q 无法通过 validToken", tok)
			}
			gotExp, err := time.Parse(time.RFC3339, resp.Data["expires_at"])
			if err != nil {
				t.Fatalf("expires_at = %q; want RFC3339: %v", resp.Data["expires_at"], err)
			}
			encoded, _, _ := strings.Cut(tok, ".")
			sec, _ := strconv.ParseInt(encoded, 10, 64)
			if gotExp.Unix() != sec {
				t.Fatalf("expires_at = %d; want %d（必须与 token 内编码的 exp 一致）", gotExp.Unix(), sec)
			}
			// 签出来的 token 真能当 Bearer 过 RequireAuth
			req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			r2 := httptest.NewRecorder()
			h.RequireAuth(okNext).ServeHTTP(r2, req)
			if r2.Code != 204 {
				t.Fatalf("用签发的 token 访问 status = %d; want 204", r2.Code)
			}
		})
	}
}

func TestIssueAPITokenRateLimited(t *testing.T) {
	h := &Handler{auth: newAuthManager("secret-key")}
	for i := 0; i < loginFailLimit; i++ {
		if rec := postToken(h, `{"key":"bad"}`); rec.Code != 401 {
			t.Fatalf("第 %d 次失败 status = %d; want 401", i+1, rec.Code)
		}
	}
	// 限流触发后，即使 key 正确也必须被拒，否则限流形同虚设
	if rec := postToken(h, `{"key":"secret-key"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超限后 status = %d; want 429（与 /auth/login 共用 loginLimiter，不能成为绕过限流的后门）", rec.Code)
	}
	// 网页登录入口共享同一计数：同 IP 在网页登录同样被限
	form := url.Values{"auth_key": {"secret-key"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "10.0.0.1:9999"
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	h.render = renderer
	rec := httptest.NewRecorder()
	h.LoginSubmit(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("网页登录 status = %d; want 429（两个入口共用同一份失败计数）", rec.Code)
	}
}
