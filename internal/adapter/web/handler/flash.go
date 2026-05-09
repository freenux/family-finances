package handler

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// flashStore 用 cookie 存一次性消息（导入完成回显）。
// 简化实现：明文存消息，读后清空。无跨请求安全需求。
type flashStore struct{}

const flashCookieName = "ff_flash"

func newFlashStore() *flashStore { return &flashStore{} }

func (s *flashStore) set(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookieName,
		Value:    url.QueryEscape(msg),
		Path:     "/",
		HttpOnly: true,
		MaxAge:   60,
	})
}

func (s *flashStore) pop(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookieName)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	v, _ := url.QueryUnescape(c.Value)
	return v
}

// chiURLParam 给 handler 用，避免 handler 直接 import chi
func chiURLParam(r *http.Request, key string) string { return chi.URLParam(r, key) }
