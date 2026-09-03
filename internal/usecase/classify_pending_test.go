package usecase

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestClassifyPendingExcludesTransferCategories 往来科目不进 LLM 白名单。
//
// 模型只拿得到 counterparty/description/direction——没有金额也没有时间，
// 判断"这是不是借款/报销"证据不足。而误判的代价不对称：一笔真支出被答成
// transfer.* 就会以 excluded 落地，从季报、仪表盘、预算、四笔钱里一起消失，
// 比分错科目严重得多。往来只靠规则 + 人工。
func TestClassifyPendingExcludesTransferCategories(t *testing.T) {
	var gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPrompt = string(body)
		w.Header().Set("Content-Type", "application/json")
		// 模型硬答一个往来科目：即使它猜中了白名单外的 id，也必须被丢弃
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{
					"role":    "assistant",
					"content": `{"assignments":[{"id":"tx-1","category_id":"transfer.loan"}]}`,
				}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	repo := &classifyTxRepo{
		fakeTransactionRepo: &fakeTransactionRepo{},
		patches:             map[string]port.TransactionUpdate{},
		pending: []domain.Transaction{{
			ID: "tx-1", Counterparty: "张三", Description: "借款",
			Direction: domain.DirectionExpense, Status: domain.TxStatusPendingReview,
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
	if pending != 1 || assigned != 0 {
		t.Fatalf("runOnce() = (pending=%d, assigned=%d); want (1, 0)——往来科目不该被接受", pending, assigned)
	}
	if len(repo.patches) != 0 {
		t.Fatalf("下发了 patch %v; want 一个都没有", repo.patches)
	}
	if strings.Contains(gotPrompt, "transfer.loan") {
		t.Errorf("prompt 里出现了 transfer.loan；want 往来科目根本不该作为候选项给模型看到")
	}
	if !strings.Contains(gotPrompt, "expense.discretion.shopping") {
		t.Errorf("prompt 里没有普通二级科目，白名单可能被过度收窄了")
	}
}
