package apiv1

import (
	"errors"
	"net/http"
)

// Report GET /api/v1/report?type=&period=&account= —— 薄壳：query 原样交给 usecase.ReportView。
// type 缺省 quarterly（财报视图是季/年口径，不是月度）；周期解析与口径合成都在 usecase。
func (a *API) Report(w http.ResponseWriter, r *http.Request) {
	if a.report == nil {
		a.internalError(w, errors.New("财报未装配"))
		return
	}
	q := r.URL.Query()
	v, err := a.report.Execute(r.Context(), q.Get("type"), q.Get("period"), q.Get("account"))
	if err != nil {
		a.writeUseCaseError(w, err, "")
		return
	}
	WriteData(w, v)
}
