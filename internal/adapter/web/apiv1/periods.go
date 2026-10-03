package apiv1

import (
	"net/http"

	"family-finances/internal/domain"
)

// PeriodsNav GET /api/v1/periods/nav?type=&period=
// type 缺省/非法回落 quarterly（不报错）；period 缺省取该粒度的默认周期；
// period 非法 → 400；period 与 type 对不上 → 退回该 type 的默认周期（规则全在 PeriodNav.Resolve）。
func (a *API) PeriodsNav(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := a.nav.Resolve(q.Get("type"), q.Get("period"), domain.PeriodQuarterly)
	if err != nil {
		a.writeUseCaseError(w, err, "")
		return
	}
	WriteData(w, a.nav.Nav(p))
}
