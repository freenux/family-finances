package handler

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
)

type failingCatRepo struct{}

func (failingCatRepo) ListAll(context.Context) ([]domain.Category, error) {
	return nil, errors.New("SQLITE_IOERR SECRET")
}
func (failingCatRepo) ListByType(context.Context, domain.CategoryType) ([]domain.Category, error) {
	return nil, errors.New("SQLITE_IOERR SECRET")
}

var _ port.CategoryRepo = failingCatRepo{}

// 校验叶子科目时科目表读取失败是内部故障：500 且不回底层文本
// （旧实现回 400 并原样回显 err.Error()）。
func TestUpdateTransactionCategoryFaultIs500(t *testing.T) {
	h := &Handler{
		txRepo:  newStubTxRepo("tx-1"),
		catRepo: failingCatRepo{},
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rec := httptest.NewRecorder()
	h.UpdateTransaction(rec, patchTxRequest("tx-1", `{"category_id":"expense.discretion.shopping"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("响应体泄露底层错误: %q", rec.Body.String())
	}
}
