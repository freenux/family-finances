// Package apiv1 是平台无关的 JSON API（手机浏览器 / 微信小程序共用），
// 与 handler 平级：只做 Request -> 依赖 -> DTO，不 import handler。
// 鉴权由 main.go 用 handler.RequireAuth 包住 Routes() 的结果，本包不感知。
package apiv1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"family-finances/internal/domain"
)

// 只声明本包真正用到的方法，由 sqlite 的具体 repo 隐式满足。

type CategoryLister interface {
	ListAll(ctx context.Context) ([]domain.Category, error)
}

type SpecialLister interface {
	ListAll(ctx context.Context) ([]domain.SpecialProject, error)
}

// Deps 构造参数。Specials 可为 nil（专项功能未启用，meta 降级返回空数组）。
type Deps struct {
	Categories CategoryLister
	Specials   SpecialLister
	Log        *slog.Logger
}

type API struct {
	categories CategoryLister
	specials   SpecialLister
	log        *slog.Logger
}

func New(d Deps) *API {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &API{categories: d.Categories, specials: d.Specials, log: log}
}

// Routes 返回 /api/v1 子树的处理器，调用方应挂在 "/api/v1" 下（chi Mount 会剥掉前缀）。
func (a *API) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/meta", a.Meta)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusNotFound, "not_found", "接口不存在")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的请求方法")
	})
	return r
}
