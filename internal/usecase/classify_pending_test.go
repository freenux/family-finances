package usecase

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"family-finances/internal/adapter/llm"
	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// classifyTxRepo 记录 Update 收到的 patch，其余方法沿用 fakeTransactionRepo
type classifyTxRepo struct {
	*fakeTransactionRepo
	pending []domain.Transaction
	patches map[string]port.TransactionUpdate
}

func (r *classifyTxRepo) ListPendingCategory(_ context.Context, limit int) ([]domain.Transaction, error) {
	if limit < len(r.pending) {
		return r.pending[:limit], nil
	}
	return r.pending, nil
}

func (r *classifyTxRepo) Update(_ context.Context, id string, patch port.TransactionUpdate) error {
	r.patches[id] = patch
	return nil
}

var _ port.TransactionRepo = (*classifyTxRepo)(nil)

// stubLLMServer 一个 OpenAI 兼容的假上游，固定回一段 content
func stubLLMServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestClassifyPendingKeepsSpecialID LLM 兜底分类只补「分类 + 状态」，
// 绝不能顺手把用户手工标好的专项归属冲掉。
//
// port.TransactionUpdate 的 patch 语义是「nil = 不修改，空字符串才是清空」，
// 所以这里断言 patch.SpecialID 必须是 nil：将来谁把它改成 &tx.SpecialID 之类的
// "顺手带上"，未分类的专项流水就会在下一轮 LLM 兜底里被踢回日常。
func TestClassifyPendingKeepsSpecialID(t *testing.T) {
	srv := stubLLMServer(t, `{"assignments":[{"id":"tx-1","category_id":"expense.discretion.shopping"}]}`)

	repo := &classifyTxRepo{
		fakeTransactionRepo: &fakeTransactionRepo{},
		patches:             map[string]port.TransactionUpdate{},
		pending: []domain.Transaction{{
			ID: "tx-1", Counterparty: "某某建材", Description: "瓷砖",
			Direction: domain.DirectionExpense, Status: domain.TxStatusPendingReview,
			SpecialID: "sp-reno", // 用户已经把它归到装修专项，只是还没分类
		}},
	}
	uc := NewClassifyPending(
		repo,
		&fakeCategoryRepo{cats: testCategories()},
		llm.NewClient(llm.Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"}),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	pending, assigned, err := uc.runOnce(context.Background(), 20)
	if err != nil {
		t.Fatalf("runOnce() error = %v", err)
	}
	if pending != 1 || assigned != 1 {
		t.Fatalf("runOnce() = (pending=%d, assigned=%d); want (1, 1)", pending, assigned)
	}

	patch, ok := repo.patches["tx-1"]
	if !ok {
		t.Fatalf("没有对 tx-1 下发 patch（收到的：%v）", repo.patches)
	}
	if patch.SpecialID != nil {
		t.Fatalf("patch.SpecialID = %q; want nil（LLM 分类不该碰专项归属）", *patch.SpecialID)
	}
	if patch.CategoryID == nil || *patch.CategoryID != "expense.discretion.shopping" {
		t.Fatalf("patch.CategoryID = %v; want expense.discretion.shopping", patch.CategoryID)
	}
	if patch.Status == nil || *patch.Status != domain.TxStatusConfirmed {
		t.Fatalf("patch.Status = %v; want confirmed", patch.Status)
	}
	// 备注 / 账户 / 成员同理，都不归 LLM 管
	if patch.Note != nil || patch.Account != nil || patch.Member != nil {
		t.Fatalf("patch 多带了不该改的字段: %+v", patch)
	}
}
