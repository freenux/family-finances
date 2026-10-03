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
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

// 只声明本包真正用到的方法，由 sqlite 的具体 repo 隐式满足。

type CategoryLister interface {
	ListAll(ctx context.Context) ([]domain.Category, error)
}

type SpecialLister interface {
	ListAll(ctx context.Context) ([]domain.SpecialProject, error)
}

// TxQuerier 流水列表与规则批量应用，由 *usecase.TxQuery 满足。
type TxQuerier interface {
	Execute(ctx context.Context, req usecase.TxQueryRequest) (usecase.TxQueryResult, error)
	ApplyRule(ctx context.Context, ruleID string, p domain.Period, acc domain.Account) (int, error)
}

// TxWriter 单条/批量改流水，由 sqlite.TransactionRepo 满足。
type TxWriter interface {
	Update(ctx context.Context, id string, patch port.TransactionUpdate) error
	SetSpecialForIDs(ctx context.Context, ids []string, specialID string) (int, error)
}

// SpecialEnsurer 校验专项存在，由 *usecase.SpecialView 满足。
type SpecialEnsurer interface {
	Ensure(ctx context.Context, id string) error
}

// Deps 构造参数。Specials / SpecialCheck 可为 nil（专项功能未启用：
// meta 降级返回空数组，PATCH 里传非空 special_id 会被拒）。
// Nav 零值可用（用系统时钟）。
type Deps struct {
	Categories   CategoryLister
	Specials     SpecialLister
	SpecialCheck SpecialEnsurer
	TxQuery      TxQuerier
	Tx           TxWriter
	Nav          usecase.PeriodNav
	Log          *slog.Logger
}

type API struct {
	categories   CategoryLister
	specials     SpecialLister
	specialCheck SpecialEnsurer
	txQuery      TxQuerier
	tx           TxWriter
	nav          usecase.PeriodNav
	log          *slog.Logger
}

func New(d Deps) *API {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &API{
		categories: d.Categories, specials: d.Specials, specialCheck: d.SpecialCheck,
		txQuery: d.TxQuery, tx: d.Tx, nav: d.Nav, log: log,
	}
}

// Routes 返回 /api/v1 子树的处理器，调用方应挂在 "/api/v1" 下（chi Mount 会剥掉前缀）。
func (a *API) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/meta", a.Meta)
	r.Get("/periods/nav", a.PeriodsNav)
	r.Get("/transactions", a.ListTransactions)
	// /batch 与 /{id} 都挂在 PATCH 下：同 method 内静态段优先，互不吞噬。
	// 切勿把其中一个改挂到别的 method，否则 chi 会让静态段回退到占位段（见 CLAUDE.md）。
	r.Patch("/transactions/batch", a.BatchUpdateTransactions)
	r.Patch("/transactions/{id}", a.UpdateTransaction)
	r.Post("/rules/{id}/apply", a.ApplyRule)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusNotFound, "not_found", "接口不存在")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的请求方法")
	})
	return r
}
