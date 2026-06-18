package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
