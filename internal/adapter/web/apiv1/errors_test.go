package apiv1

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

// leakText 底层错误里的敏感文本；任何响应体都不得出现它
const leakText = "SQLITE_IOERR /var/lib/family.db SECRET"

// 这组测试钉死「错误分类只看哨兵，不看有没有 %w 包装」：
// repo / usecase 返回一个没包装的裸错误时，必须是 500 且不带细节。
// 旧的 isUserFacing 启发式把「无 Unwrap」当作面向用户的拒绝，会把它误判成 400 并泄露文本。

type bareErrQuerier struct{}

func (bareErrQuerier) Execute(context.Context, usecase.TxQueryRequest) (usecase.TxQueryResult, error) {
	return usecase.TxQueryResult{}, errors.New(leakText)
}
func (bareErrQuerier) ApplyRule(context.Context, string, domain.Period, domain.Account) (int, error) {
	return 0, errors.New(leakText)
}

type bareErrWriter struct{}

func (bareErrWriter) Update(context.Context, string, port.TransactionUpdate) error {
	return errors.New(leakText)
}
func (bareErrWriter) SetSpecialForIDs(context.Context, []string, string) (int, error) {
	return 0, errors.New(leakText)
}

type bareErrCats struct{}

func (bareErrCats) ListAll(context.Context) ([]domain.Category, error) {
	return nil, errors.New(leakText)
}

type bareErrEnsurer struct{}

func (bareErrEnsurer) Ensure(context.Context, string) error { return errors.New(leakText) }

func TestBareRepoErrorsAreInternalAndNeverLeak(t *testing.T) {
	api := New(Deps{
		Categories: bareErrCats{}, SpecialCheck: bareErrEnsurer{},
		TxQuery: bareErrQuerier{}, Tx: bareErrWriter{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	h := api.Routes()
	tests := []struct {
		name, method, path, body string
	}{
		{"列表查询失败", "GET", "/transactions", ""},
		{"规则批量应用失败", "POST", "/rules/r-1/apply", `{"period":"2025-07"}`},
		{"校验分类时科目表读取失败", "PATCH", "/transactions/t1", `{"category_id":"expense.discretion.shopping"}`},
		{"校验专项时遇到 DB 故障", "PATCH", "/transactions/t1", `{"special_id":"sp-1"}`},
		{"单条写库失败", "PATCH", "/transactions/t1", `{"note":"x"}`},
		{"批量校验专项遇到 DB 故障", "PATCH", "/transactions/batch", `{"ids":["a"],"special_id":"sp-1"}`},
		{"批量写库失败", "PATCH", "/transactions/batch", `{"ids":["a"],"special_id":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d; want 500（裸底层错误是内部故障，不是 400）body=%s", rec.Code, rec.Body)
			}
			if code, _ := errCode(t, rec); code != "internal" {
				t.Errorf("code = %q; want internal", code)
			}
			for _, frag := range []string{"SECRET", "SQLITE", "family.db"} {
				if strings.Contains(rec.Body.String(), frag) {
					t.Errorf("响应体泄露了底层错误文本 %q: %s", frag, rec.Body)
				}
			}
		})
	}
}
