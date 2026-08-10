package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"family-finances/internal/adapter/web"
	"family-finances/internal/domain"
	"family-finances/internal/port"
	"family-finances/internal/usecase"
)

// ---- HTTP 层：专项归类（单条 / 批量）与统计口径入参 ----

// ---- 替身 ----

// stubTxRepo 只实现被测端点用到的方法，其余返回零值。
type stubTxRepo struct {
	existing     map[string]bool                     // 存在的流水 id
	patches      map[string][]port.TransactionUpdate // id → 收到的 patch 序列
	aggScope     domain.Scope                        // 最后一次 AggregateByCategory 收到的口径
	bucketScopes []domain.Scope                      // SumByBuckets 收到的口径序列
	topScopes    []domain.Scope                      // TopTransactions 收到的口径序列
}

func newStubTxRepo(ids ...string) *stubTxRepo {
	r := &stubTxRepo{existing: map[string]bool{}, patches: map[string][]port.TransactionUpdate{}}
	for _, id := range ids {
		r.existing[id] = true
	}
	return r
}

func (r *stubTxRepo) Insert(context.Context, domain.Transaction) error { return nil }
func (r *stubTxRepo) InsertBatch(context.Context, domain.ImportBatch, []port.ImportRow) (port.ImportResult, error) {
	return port.ImportResult{}, nil
}

func (r *stubTxRepo) Update(_ context.Context, id string, patch port.TransactionUpdate) error {
	if !r.existing[id] {
		return port.ErrNotFound
	}
	r.patches[id] = append(r.patches[id], patch)
	return nil
}

func (r *stubTxRepo) Get(context.Context, string) (domain.Transaction, error) {
	return domain.Transaction{}, port.ErrNotFound
}
func (r *stubTxRepo) List(context.Context, domain.Period, domain.Account) ([]domain.Transaction, error) {
	return nil, nil
}
func (r *stubTxRepo) ListPendingCategory(context.Context, int) ([]domain.Transaction, error) {
	return nil, nil
}

func (r *stubTxRepo) AggregateByCategory(_ context.Context, _ domain.Period, _ domain.Account, scope domain.Scope) ([]domain.CategoryAggregation, error) {
	r.aggScope = scope
	// 三种口径给三个可区分的金额，方便断言"口径确实透传下去了"
	amounts := map[domain.Scope]int64{domain.ScopeDaily: 1000, domain.ScopeAll: 9000, domain.ScopeSpecial: 8000}
	return []domain.CategoryAggregation{
		{CategoryID: "expense.discretion.shopping", Name: "购物消费", ParentID: "expense.discretion", Amount: amounts[scope]},
	}, nil
}

func (r *stubTxRepo) SumByBuckets(_ context.Context, buckets []port.PeriodBucket, _ domain.Direction, _ domain.Account, scope domain.Scope) ([]port.PeriodBucket, error) {
	r.bucketScopes = append(r.bucketScopes, scope)
	out := make([]port.PeriodBucket, len(buckets))
	copy(out, buckets)
	return out, nil
}

func (r *stubTxRepo) TopTransactions(_ context.Context, _ domain.Period, _ domain.Direction, _ domain.Account, scope domain.Scope, _ int) ([]port.TopTransaction, error) {
	r.topScopes = append(r.topScopes, scope)
	return nil, nil
}

func (r *stubTxRepo) ListAll(context.Context) ([]domain.Transaction, error) { return nil, nil }
func (r *stubTxRepo) ListAllImportBatches(context.Context) ([]domain.ImportBatch, error) {
	return nil, nil
}
func (r *stubTxRepo) ListMembers(context.Context) ([]string, error) { return nil, nil }
func (r *stubTxRepo) ListForRecurring(context.Context, time.Time, time.Time, domain.Scope) ([]domain.Transaction, error) {
	return nil, nil
}

// stubSpecialRepo 只关心 Get 能否命中（handler 用它校验专项是否存在）
type stubSpecialRepo struct {
	projects []domain.SpecialProject
}

func (r *stubSpecialRepo) ListAll(context.Context) ([]domain.SpecialProject, error) {
	return r.projects, nil
}
func (r *stubSpecialRepo) Get(_ context.Context, id string) (domain.SpecialProject, error) {
	for _, p := range r.projects {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.SpecialProject{}, port.ErrNotFound
}
func (r *stubSpecialRepo) Upsert(context.Context, *domain.SpecialProject) error { return nil }
func (r *stubSpecialRepo) Delete(context.Context, string) error                 { return nil }
func (r *stubSpecialRepo) SumByProject(context.Context) (map[string]int64, error) {
	return map[string]int64{}, nil
}
func (r *stubSpecialRepo) SumByProjectInPeriod(context.Context, domain.Period, domain.Account) (map[string]int64, error) {
	return map[string]int64{}, nil
}
func (r *stubSpecialRepo) SumByCategoryForProject(context.Context, string) ([]domain.CategoryAggregation, error) {
	return nil, nil
}

// stubCatRepo 一棵最小科目树，够 QueryStats 组装饼图
type stubCatRepo struct{}

func (stubCatRepo) ListAll(context.Context) ([]domain.Category, error) {
	return []domain.Category{
		{ID: "expense.discretion", Level: 1, Name: "自由裁量支出", Type: domain.CategoryTypeExpense},
		{ID: "expense.discretion.shopping", ParentID: "expense.discretion", Level: 2, Name: "购物消费", Type: domain.CategoryTypeExpense},
	}, nil
}
func (stubCatRepo) ListByType(_ context.Context, t domain.CategoryType) ([]domain.Category, error) {
	all, _ := stubCatRepo{}.ListAll(context.Background())
	var out []domain.Category
	for _, c := range all {
		if c.Type == t {
			out = append(out, c)
		}
	}
	return out, nil
}

var (
	_ port.TransactionRepo    = (*stubTxRepo)(nil)
	_ port.SpecialProjectRepo = (*stubSpecialRepo)(nil)
	_ port.CategoryRepo       = stubCatRepo{}
)

// newSpecialTestHandler 组一个只带专项相关依赖的 Handler
func newSpecialTestHandler(txRepo *stubTxRepo, projects ...domain.SpecialProject) *Handler {
	return &Handler{
		txRepo:      txRepo,
		catRepo:     stubCatRepo{},
		specialRepo: &stubSpecialRepo{projects: projects},
		queryStats:  usecase.NewQueryStats(txRepo, stubCatRepo{}),
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// patchTxRequest 带 chi 路由参数的 PATCH /api/transactions/{id}
func patchTxRequest(id, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/api/transactions/"+id, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// ---- E1. PATCH /api/transactions/{id} 的 special_id ----

func TestUpdateTransactionSpecialID(t *testing.T) {
	const reno = "sp-reno"

	tests := []struct {
		name       string
		txID       string
		body       string
		wantStatus int
		wantBody   string // 响应体应包含的片段（错误场景）
		// wantPatch 期望落到 repo 的 SpecialID 值；nil 表示不该有 patch
		wantPatch *string
	}{
		{
			name: "正常归入专项", txID: "tx-1", body: `{"special_id":"sp-reno"}`,
			wantStatus: http.StatusNoContent, wantPatch: strptr(reno),
		},
		{
			name: "空字符串清空（归回日常）", txID: "tx-1", body: `{"special_id":""}`,
			wantStatus: http.StatusNoContent, wantPatch: strptr(""),
		},
		{
			name: "首尾空白被 trim", txID: "tx-1", body: `{"special_id":"  sp-reno  "}`,
			wantStatus: http.StatusNoContent, wantPatch: strptr(reno),
		},
		{
			name: "不存在的专项 → 400", txID: "tx-1", body: `{"special_id":"sp-ghost"}`,
			wantStatus: http.StatusBadRequest, wantBody: "专项不存在",
		},
		{
			name: "不存在的流水 → 404", txID: "tx-missing", body: `{"special_id":"sp-reno"}`,
			wantStatus: http.StatusNotFound, wantBody: "流水不存在",
		},
		{
			name: "非法 JSON → 400", txID: "tx-1", body: `{`,
			wantStatus: http.StatusBadRequest, wantBody: "invalid body",
		},
		{
			name: "不带 special_id 字段时不动专项", txID: "tx-1", body: `{"note":"改备注"}`,
			wantStatus: http.StatusNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txRepo := newStubTxRepo("tx-1")
			h := newSpecialTestHandler(txRepo, domain.SpecialProject{ID: reno, Name: "2026 老房装修"})

			rec := httptest.NewRecorder()
			h.UpdateTransaction(rec, patchTxRequest(tt.txID, tt.body))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d; want %d（body=%s）", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q; want 含 %q", rec.Body.String(), tt.wantBody)
			}

			patches := txRepo.patches[tt.txID]
			if tt.wantPatch == nil {
				for _, p := range patches {
					if p.SpecialID != nil {
						t.Fatalf("不该带 SpecialID 的请求却下发了 patch: %v", *p.SpecialID)
					}
				}
				return
			}
			if len(patches) != 1 {
				t.Fatalf("收到 %d 个 patch; want 1", len(patches))
			}
			if patches[0].SpecialID == nil {
				t.Fatal("patch.SpecialID = nil; want 非空指针")
			}
			if *patches[0].SpecialID != *tt.wantPatch {
				t.Fatalf("patch.SpecialID = %q; want %q", *patches[0].SpecialID, *tt.wantPatch)
			}
		})
	}
}

// TestUpdateTransactionSpecialWithoutRepo 专项仓库没注入时应给 400 而不是 panic
func TestUpdateTransactionSpecialWithoutRepo(t *testing.T) {
	txRepo := newStubTxRepo("tx-1")
	h := &Handler{txRepo: txRepo, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	rec := httptest.NewRecorder()
	h.UpdateTransaction(rec, patchTxRequest("tx-1", `{"special_id":"sp-reno"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400（body=%s）", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "专项功能未启用") {
		t.Fatalf("body = %q; want 含 专项功能未启用", rec.Body.String())
	}
}

func strptr(s string) *string { return &s }

// ---- E2. PATCH /api/transactions/batch ----

func TestBatchUpdateTransactions(t *testing.T) {
	const reno = "sp-reno"

	manyIDs := func(n int) []string {
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, "tx-1")
		}
		return out
	}
	mustJSON := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}

	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantBody    string
		wantUpdated int // 200 时响应里的 updated
	}{
		{
			name:       "正常批量归入",
			body:       `{"ids":["tx-1","tx-2","tx-3"],"special_id":"sp-reno"}`,
			wantStatus: http.StatusOK, wantUpdated: 3,
		},
		{
			name:       "批量清空（归回日常）",
			body:       `{"ids":["tx-1","tx-2"],"special_id":""}`,
			wantStatus: http.StatusOK, wantUpdated: 2,
		},
		{
			name:       "含不存在的流水：跳过但不整体失败",
			body:       `{"ids":["tx-1","tx-missing","tx-2"],"special_id":"sp-reno"}`,
			wantStatus: http.StatusOK, wantUpdated: 2,
		},
		{
			name:       "含空字符串 id：跳过",
			body:       `{"ids":["tx-1","","tx-2"],"special_id":"sp-reno"}`,
			wantStatus: http.StatusOK, wantUpdated: 2,
		},
		{
			name:       "空 ids → 400",
			body:       `{"ids":[],"special_id":"sp-reno"}`,
			wantStatus: http.StatusBadRequest, wantBody: "请选择要归类的流水",
		},
		{
			name:       "缺 ids 字段 → 400",
			body:       `{"special_id":"sp-reno"}`,
			wantStatus: http.StatusBadRequest, wantBody: "请选择要归类的流水",
		},
		{
			name:       "超过上限 1000 → 400",
			body:       mustJSON(map[string]any{"ids": manyIDs(maxBatchTxIDs + 1), "special_id": reno}),
			wantStatus: http.StatusBadRequest, wantBody: "一次最多处理 1000 条流水",
		},
		{
			name:       "恰好 1000 条 → 放行",
			body:       mustJSON(map[string]any{"ids": manyIDs(maxBatchTxIDs), "special_id": reno}),
			wantStatus: http.StatusOK, wantUpdated: maxBatchTxIDs,
		},
		{
			name:       "缺 special_id → 400",
			body:       `{"ids":["tx-1"]}`,
			wantStatus: http.StatusBadRequest, wantBody: "缺少 special_id",
		},
		{
			name:       "不存在的专项 → 400",
			body:       `{"ids":["tx-1"],"special_id":"sp-ghost"}`,
			wantStatus: http.StatusBadRequest, wantBody: "专项不存在",
		},
		{
			name:       "非法 JSON → 400",
			body:       `not json`,
			wantStatus: http.StatusBadRequest, wantBody: "invalid body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txRepo := newStubTxRepo("tx-1", "tx-2", "tx-3")
			h := newSpecialTestHandler(txRepo, domain.SpecialProject{ID: reno, Name: "2026 老房装修"})

			req := httptest.NewRequest(http.MethodPatch, "/api/transactions/batch", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.BatchUpdateTransactions(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d; want %d（body=%s）", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q; want 含 %q", rec.Body.String(), tt.wantBody)
			}
			if tt.wantStatus != http.StatusOK {
				if len(txRepo.patches) != 0 {
					t.Fatalf("请求被拒绝却仍写了 %d 条流水", len(txRepo.patches))
				}
				return
			}
			var resp struct {
				Updated int `json:"updated"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("解析响应失败: %v（body=%s）", err, rec.Body.String())
			}
			if resp.Updated != tt.wantUpdated {
				t.Fatalf("updated = %d; want %d", resp.Updated, tt.wantUpdated)
			}
		})
	}
}

// ---- E3. GET /api/stats?scope= ----

func TestStatsAPIScope(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantScope domain.Scope
	}{
		{"缺省落到 daily", "", domain.ScopeDaily},
		{"空串落到 daily", "&scope=", domain.ScopeDaily},
		{"非法值落到 daily", "&scope=garbage", domain.ScopeDaily},
		{"大小写不匹配也落到 daily", "&scope=DAILY", domain.ScopeDaily},
		{"all 生效", "&scope=all", domain.ScopeAll},
		{"special 生效", "&scope=special", domain.ScopeSpecial},
	}

	// 饼图金额随口径变化（stub 里 daily=1000 / all=9000 / special=8000），
	// 用它反查 scope 有没有真的透传到聚合层。
	wantTotal := map[domain.Scope]int64{domain.ScopeDaily: 1000, domain.ScopeAll: 9000, domain.ScopeSpecial: 8000}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txRepo := newStubTxRepo()
			h := newSpecialTestHandler(txRepo)

			req := httptest.NewRequest(http.MethodGet,
				"/api/stats?granularity=month&period=2026-05&direction=expense&account=family"+tt.query, nil)
			rec := httptest.NewRecorder()
			h.StatsAPI(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200（body=%s）", rec.Code, rec.Body.String())
			}
			var view usecase.StatsView
			if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
				t.Fatalf("解析响应失败: %v", err)
			}
			if view.Scope != string(tt.wantScope) {
				t.Fatalf("view.scope = %q; want %q", view.Scope, tt.wantScope)
			}
			if txRepo.aggScope != tt.wantScope {
				t.Fatalf("AggregateByCategory 收到口径 %q; want %q", txRepo.aggScope, tt.wantScope)
			}
			if view.Total != wantTotal[tt.wantScope] {
				t.Fatalf("total = %d; want %d（口径没有真的透传到聚合层）", view.Total, wantTotal[tt.wantScope])
			}

			// 对比条恒为日常口径 + 专项叠加段；Top 榜单恒为全口径。
			// 这两条是刻意定死的，不跟随入参 scope。
			for _, s := range txRepo.bucketScopes {
				if s != domain.ScopeDaily && s != domain.ScopeSpecial {
					t.Fatalf("SumByBuckets 收到口径 %q; want daily 或 special", s)
				}
			}
			if len(txRepo.topScopes) != 1 || txRepo.topScopes[0] != domain.ScopeAll {
				t.Fatalf("TopTransactions 收到口径 %v; want [all]", txRepo.topScopes)
			}
		})
	}
}

// TestStatsAPIInvalidPeriod 周期非法仍应 400（口径解析不能吞掉这个校验）
func TestStatsAPIInvalidPeriod(t *testing.T) {
	h := newSpecialTestHandler(newStubTxRepo())
	req := httptest.NewRequest(http.MethodGet, "/api/stats?period=not-a-period&scope=all", nil)
	rec := httptest.NewRecorder()
	h.StatsAPI(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}

// ---- E4. /specials 编辑入口（表单预填）----

func TestSpecialFormFromProject(t *testing.T) {
	tests := []struct {
		name string
		in   domain.SpecialProject
		want specialFormVM
	}{
		{
			name: "完整字段",
			in: domain.SpecialProject{
				ID: "sp-reno", Name: "2026 老房装修",
				StartedOn: time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local),
				EndedOn:   time.Date(2026, 8, 31, 0, 0, 0, 0, time.Local),
				BudgetFen: 18000000, Note: "含家电",
			},
			want: specialFormVM{
				ID: "sp-reno", Name: "2026 老房装修",
				StartedOn: "2026-04-01", EndedOn: "2026-08-31",
				Budget: "180000", Note: "含家电", Editing: true,
			},
		},
		{
			name: "进行中（结束日期零值）渲染成空串，而不是 0001-01-01",
			in:   domain.SpecialProject{ID: "sp-car", Name: "换车", BudgetFen: 30000050},
			want: specialFormVM{ID: "sp-car", Name: "换车", Budget: "300000.50", Editing: true},
		},
		{
			name: "零值 = 新建，Editing 为 false",
			in:   domain.SpecialProject{},
			want: specialFormVM{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := specialFormFromProject(tt.in); got != tt.want {
				t.Fatalf("specialFormFromProject() = %+v; want %+v", got, tt.want)
			}
		})
	}
}

// TestSpecialsPageEditPrefill GET /specials?edit=<id> 必须把该专项填进右侧表单，
// 尤其是 hidden id——它为空时 POST 会新建一个同名专项而不是覆盖（编辑入口原本不可达）。
func TestSpecialsPageEditPrefill(t *testing.T) {
	renderer, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	repo := &stubSpecialRepo{projects: []domain.SpecialProject{{
		ID: "sp-reno", Name: "2026 老房装修",
		StartedOn: time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local),
		BudgetFen: 18000000, Note: "含家电",
	}}}
	h := &Handler{
		render:      renderer,
		specialRepo: repo,
		specialView: usecase.NewSpecialView(repo),
		flash:       newFlashStore(),
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	tests := []struct {
		name     string
		url      string
		wantHTML []string
		notHTML  []string
	}{
		{
			name: "带 edit 参数：表单预填",
			url:  "/specials?edit=sp-reno",
			wantHTML: []string{
				`name="id" value="sp-reno"`,
				`name="name" required placeholder="如：2026 老房装修" value="2026 老房装修"`,
				`name="started_on" value="2026-04-01"`,
				`name="budget" inputmode="decimal" placeholder="180000" value="180000"`,
				"编辑专项", "取消编辑", "保存修改",
			},
		},
		{
			name:     "不带参数：空表单（新建）",
			url:      "/specials",
			wantHTML: []string{`name="id" value=""`, "新建专项", "保存专项", `href="/specials?edit=sp-reno#special-form"`},
			notHTML:  []string{"取消编辑"},
		},
		{
			name:     "edit 指向不存在的专项：退回新建表单，不报错",
			url:      "/specials?edit=no-such",
			wantHTML: []string{`name="id" value=""`, "新建专项"},
			notHTML:  []string{"取消编辑"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.Specials(rec, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", rec.Code)
			}
			body := rec.Body.String()
			for _, want := range tt.wantHTML {
				if !strings.Contains(body, want) {
					t.Fatalf("页面缺少 %q", want)
				}
			}
			for _, no := range tt.notHTML {
				if strings.Contains(body, no) {
					t.Fatalf("页面不该出现 %q", no)
				}
			}
		})
	}
}

// ---- E5. 路由不打架 ----

// TestBatchRouteDoesNotCollideWithIDRoute chi 的 trie 里 {id} 占位段会"吞掉"同级字面量段，
// 是这个仓库踩过的坑（见 CLAUDE.md）。/api/transactions/batch 与 /api/transactions/{id}
// 同为 PATCH，必须由静态段优先命中批量端点。这里把 main.go 的注册顺序原样搬过来锁住。
func TestBatchRouteDoesNotCollideWithIDRoute(t *testing.T) {
	var hit string
	r := chi.NewRouter()
	r.Patch("/api/transactions/batch", func(w http.ResponseWriter, r *http.Request) {
		hit = "batch"
		w.WriteHeader(http.StatusOK)
	})
	r.Patch("/api/transactions/{id}", func(w http.ResponseWriter, r *http.Request) {
		hit = "id:" + chi.URLParam(r, "id")
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name string
		path string
		want string
	}{
		{"批量端点命中静态段", "/api/transactions/batch", "batch"},
		{"普通 id 仍命中占位段", "/api/transactions/tx-1", "id:tx-1"},
		{"看起来像 batch 的 id 不受影响", "/api/transactions/batch-2", "id:batch-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit = ""
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, tt.path, strings.NewReader("{}")))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", rec.Code)
			}
			if hit != tt.want {
				t.Fatalf("命中 %q; want %q", hit, tt.want)
			}
		})
	}
}
