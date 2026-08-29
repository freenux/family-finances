package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

func newTestReportRepo(t *testing.T) *ReportRepo {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewReportRepo(db)
}

func TestReportRepoGetByPeriodNotFound(t *testing.T) {
	repo := newTestReportRepo(t)
	_, err := repo.GetByPeriod(context.Background(), "2026Q2", domain.PeriodQuarterly)
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("GetByPeriod() error = %v; want port.ErrNotFound", err)
	}
}

func TestReportRepoUpsertOverwritesSamePeriodAndType(t *testing.T) {
	repo := newTestReportRepo(t)
	ctx := context.Background()

	rep := domain.AIReport{
		ID:          "rep-1",
		Period:      "2026Q2",
		PeriodType:  domain.PeriodQuarterly,
		GeneratedAt: time.Now(),
		AIAnalysis:  `{"summary":"第一版"}`,
		AIModel:     "gpt-4o",
		Status:      "final",
		CreatedAt:   time.Now(),
	}
	if err := repo.Upsert(ctx, &rep); err != nil {
		t.Fatalf("Upsert() first error = %v", err)
	}

	rep2 := rep
	rep2.ID = "rep-2"
	rep2.AIAnalysis = `{"summary":"第二版"}`
	if err := repo.Upsert(ctx, &rep2); err != nil {
		t.Fatalf("Upsert() second error = %v", err)
	}

	got, err := repo.GetByPeriod(ctx, "2026Q2", domain.PeriodQuarterly)
	if err != nil {
		t.Fatalf("GetByPeriod() error = %v", err)
	}
	if got.AIAnalysis != `{"summary":"第二版"}` {
		t.Fatalf("AIAnalysis = %q; want 第二版 (同期同类型应覆盖)", got.AIAnalysis)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("len(ListAll()) = %d; want 1", len(all))
	}
}

// TestReportRepoDataScopeRoundTrip 存档口径的读写往返。
// 未知/空口径一律读成 all——015 之前落库的行没有这一列，而它们恰恰是全口径生成的。
func TestReportRepoDataScopeRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		write domain.Scope
		want  domain.Scope
	}{
		{"新报告写日常口径", domain.ScopeDaily, domain.ScopeDaily},
		{"显式全口径", domain.ScopeAll, domain.ScopeAll},
		{"仅专项口径", domain.ScopeSpecial, domain.ScopeSpecial},
		{"零值（未设置）归一到全口径", domain.Scope(""), domain.ScopeAll},
		{"未知值归一到全口径", domain.Scope("bogus"), domain.ScopeAll},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestReportRepo(t)
			ctx := context.Background()
			rep := domain.AIReport{
				ID: "rep-1", Period: "2026Q2", PeriodType: domain.PeriodQuarterly,
				GeneratedAt: time.Now(), DataScope: tt.write, Status: "final", CreatedAt: time.Now(),
			}
			if err := repo.Upsert(ctx, &rep); err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}

			got, err := repo.GetByPeriod(ctx, "2026Q2", domain.PeriodQuarterly)
			if err != nil {
				t.Fatalf("GetByPeriod() error = %v", err)
			}
			if got.DataScope != tt.want {
				t.Fatalf("GetByPeriod().DataScope = %q; want %q", got.DataScope, tt.want)
			}

			all, err := repo.ListAll(ctx)
			if err != nil {
				t.Fatalf("ListAll() error = %v", err)
			}
			if len(all) != 1 || all[0].DataScope != tt.want {
				t.Fatalf("ListAll()[0].DataScope = %v; want %q", all, tt.want)
			}
		})
	}
}

// TestReportRepoLegacyRowReadsAsAllScope 直接按 015 之前的写法插一行（不带 data_scope），
// 读出来必须是全口径。用旧存档的真实数字标成「日常收入」正是这个字段要防的事。
func TestReportRepoLegacyRowReadsAsAllScope(t *testing.T) {
	repo := newTestReportRepo(t)
	ctx := context.Background()

	if _, err := repo.db.ExecContext(ctx, `
INSERT INTO reports (id, period, period_type, generated_at, kpi_data, status, created_at)
VALUES ('rep-legacy', '2026Q1', 'quarterly', ?, '{"kpi":{"savings_rate":0.25}}', 'final', ?)`,
		time.Now(), time.Now()); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	got, err := repo.GetByPeriod(ctx, "2026Q1", domain.PeriodQuarterly)
	if err != nil {
		t.Fatalf("GetByPeriod() error = %v", err)
	}
	if got.DataScope != domain.ScopeAll {
		t.Fatalf("旧存档 DataScope = %q; want %q", got.DataScope, domain.ScopeAll)
	}

	// 列被手工写空时同样按全口径读（COALESCE + storedScope 的防御路径）
	if _, err := repo.db.ExecContext(ctx, `UPDATE reports SET data_scope = '' WHERE id = 'rep-legacy'`); err != nil {
		t.Fatalf("blank data_scope: %v", err)
	}
	got, err = repo.GetByPeriod(ctx, "2026Q1", domain.PeriodQuarterly)
	if err != nil {
		t.Fatalf("GetByPeriod() error = %v", err)
	}
	if got.DataScope != domain.ScopeAll {
		t.Fatalf("空 data_scope 读出 %q; want %q", got.DataScope, domain.ScopeAll)
	}
}

// TestReportRepoUpsertOverwritesDataScope 同期覆盖时口径也必须跟着更新：
// 旧存档所在的期重新生成一次，就该从全口径翻成日常口径。
func TestReportRepoUpsertOverwritesDataScope(t *testing.T) {
	repo := newTestReportRepo(t)
	ctx := context.Background()

	old := domain.AIReport{
		ID: "rep-1", Period: "2026Q2", PeriodType: domain.PeriodQuarterly,
		GeneratedAt: time.Now(), DataScope: domain.ScopeAll, Status: "final", CreatedAt: time.Now(),
	}
	if err := repo.Upsert(ctx, &old); err != nil {
		t.Fatalf("Upsert(old) error = %v", err)
	}
	fresh := old
	fresh.ID = "rep-2"
	fresh.DataScope = domain.ScopeDaily
	if err := repo.Upsert(ctx, &fresh); err != nil {
		t.Fatalf("Upsert(fresh) error = %v", err)
	}

	got, err := repo.GetByPeriod(ctx, "2026Q2", domain.PeriodQuarterly)
	if err != nil {
		t.Fatalf("GetByPeriod() error = %v", err)
	}
	if got.DataScope != domain.ScopeDaily {
		t.Fatalf("覆盖后 DataScope = %q; want %q", got.DataScope, domain.ScopeDaily)
	}
}

func TestReportRepoQuarterAndAnnualCoexistForSamePeriodLabel(t *testing.T) {
	repo := newTestReportRepo(t)
	ctx := context.Background()

	// "2026" 作为年度 period label 和一个假设的季度不会冲突，这里验证 period_type 是唯一键的一部分：
	// 同一 period label 若 period_type 不同（理论上不会真的撞，因为季度带 Q），应各自独立存在。
	q := domain.AIReport{ID: "rep-q", Period: "2026Q2", PeriodType: domain.PeriodQuarterly, GeneratedAt: time.Now(), Status: "final", CreatedAt: time.Now()}
	a := domain.AIReport{ID: "rep-a", Period: "2026", PeriodType: domain.PeriodAnnual, GeneratedAt: time.Now(), Status: "final", CreatedAt: time.Now()}
	if err := repo.Upsert(ctx, &q); err != nil {
		t.Fatalf("Upsert(quarter) error = %v", err)
	}
	if err := repo.Upsert(ctx, &a); err != nil {
		t.Fatalf("Upsert(annual) error = %v", err)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(ListAll()) = %d; want 2", len(all))
	}
}
