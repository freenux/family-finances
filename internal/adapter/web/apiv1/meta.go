package apiv1

import "net/http"

// Meta GET /api/v1/meta：客户端启动时拉一次的 bootstrap。装配全在 usecase.Meta
// （流水页 SSR 首屏嵌入的是同一份），这里只管 HTTP。
func (a *API) Meta(w http.ResponseWriter, r *http.Request) {
	v, err := a.meta.Execute(r.Context())
	if err != nil {
		a.internalError(w, err)
		return
	}
	WriteData(w, v)
}
