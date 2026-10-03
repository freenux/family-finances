package apiv1

import (
	"net/http"
)

// PeriodsNav GET /api/v1/periods/nav?type=&period=
// type 缺省/非法回落 quarterly（不报错）；period 缺省取该粒度的默认周期；period 非法 → 400。
func (a *API) PeriodsNav(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := a.nav.Resolve(q.Get("type"), q.Get("period"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "周期格式不正确："+q.Get("period"))
		return
	}
	WriteData(w, a.nav.Nav(p))
}
