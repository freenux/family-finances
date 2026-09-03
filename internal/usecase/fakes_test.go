package usecase

import (
	"context"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// ---- fakeCategoryRepo ----

type fakeCategoryRepo struct {
	cats []domain.Category
}

func (f *fakeCategoryRepo) ListAll(context.Context) ([]domain.Category, error) { return f.cats, nil }

func (f *fakeCategoryRepo) ListByType(_ context.Context, t domain.CategoryType) ([]domain.Category, error) {
	var out []domain.Category
	for _, c := range f.cats {
		if c.Type == t {
			out = append(out, c)
		}
	}
	return out, nil
}

// testCategories 一套最小的收支科目树，income.salary/expense.discretion/expense.fixed 各一个二级科目
func testCategories() []domain.Category {
	return []domain.Category{
		{ID: "income.salary", Level: 1, Name: "工资收入", Type: domain.CategoryTypeIncome},
		{ID: "income.salary.husband", ParentID: "income.salary", Level: 2, Name: "男主工资", Type: domain.CategoryTypeIncome},
		{ID: "expense.discretion", Level: 1, Name: "自由裁量支出", Type: domain.CategoryTypeExpense},
		{ID: "expense.discretion.shopping", ParentID: "expense.discretion", Level: 2, Name: "购物消费", Type: domain.CategoryTypeExpense},
		{ID: "expense.fixed", Level: 1, Name: "固定刚性支出", Type: domain.CategoryTypeExpense},
		{ID: "expense.fixed.housing", ParentID: "expense.fixed", Level: 2, Name: "居住成本", Type: domain.CategoryTypeExpense},
		// 往来科目：它虽然是 level=2，却必须被挡在 LLM 白名单之外
		{ID: "transfer", Level: 1, Name: "资金往来 · 不计收支", Type: domain.CategoryTypeTransfer},
		{ID: "transfer.loan", ParentID: "transfer", Level: 2, Name: "借出借入还款", Type: domain.CategoryTypeTransfer},
	}
}

// ---- fakeTransactionRepo ----

type fakeTransactionRepo struct {
	periodAgg  map[string][]domain.CategoryAggregation
	bucketAmts map[string]int64 // bucket label -> amount，供 SumByBuckets 用
	allTxs     []domain.Transaction
	allBatches []domain.ImportBatch
	// 专项口径（domain.ScopeSpecial）的数据；不设 = 夹具里没有专项流水。
	// periodAgg / bucketAmts 是日常口径，全口径 = 两者之和。
	specialAgg     map[string][]domain.CategoryAggregation
	specialBuckets map[string]int64
	// incomeBuckets 可选：收入方向的日常桶金额。不设时收入与支出共用 bucketAmts
	// （老夹具的行为）；要测"结余率连胜"这类收入 ≠ 支出的场景才需要它。
	incomeBuckets map[string]int64
	// bucketCalls SumByBuckets 被调用的次数（一次返回日常+专项两组，不该按口径各查一遍）
	bucketCalls int
	// batchIDs / batchSpecialID 记录 SetSpecialForIDs 收到的入参
	batchIDs       []string
	batchSpecialID string
	// tops / topScopes：TopTransactions 的返回值与收到的口径序列。
	// tops 按口径分组，用来验"Top 榜单跟随 scope"。
	tops      map[domain.Scope][]port.TopTransaction
	topScopes []domain.Scope
}

// sumAggs 合并同科目金额，用于拼出全口径（日常 + 专项）
func sumAggs(a, b []domain.CategoryAggregation) []domain.CategoryAggregation {
	if len(b) == 0 {
		return a
	}
	out := append([]domain.CategoryAggregation(nil), a...)
	for _, sp := range b {
		found := false
		for i := range out {
			if out[i].CategoryID == sp.CategoryID {
				out[i].Amount += sp.Amount
				found = true
				break
			}
		}
		if !found {
			out = append(out, sp)
		}
	}
	return out
}

func (f *fakeTransactionRepo) Insert(context.Context, domain.Transaction) error { return nil }

func (f *fakeTransactionRepo) InsertBatch(context.Context, domain.ImportBatch, []port.ImportRow) (port.ImportResult, error) {
	return port.ImportResult{}, nil
}

func (f *fakeTransactionRepo) Update(context.Context, string, port.TransactionUpdate) error {
	return nil
}

func (f *fakeTransactionRepo) Get(context.Context, string) (domain.Transaction, error) {
	return domain.Transaction{}, nil
}

func (f *fakeTransactionRepo) List(context.Context, domain.Period, domain.Account) ([]domain.Transaction, error) {
	return nil, nil
}

func (f *fakeTransactionRepo) ListPendingCategory(context.Context, int) ([]domain.Transaction, error) {
	return nil, nil
}

func (f *fakeTransactionRepo) AggregateByCategory(_ context.Context, p domain.Period, _ domain.Account, scope domain.Scope) ([]domain.CategoryAggregation, error) {
	switch scope {
	case domain.ScopeSpecial:
		return f.specialAgg[p.Label], nil
	case domain.ScopeAll:
		return sumAggs(f.periodAgg[p.Label], f.specialAgg[p.Label]), nil
	default: // ScopeDaily
		return f.periodAgg[p.Label], nil
	}
}

// SumByBuckets 一次返回日常/专项两组同源桶，与真实 repo 的约定一致；
// bucketCalls 记录被调用次数，好断言"月+季各一条查询、不会按口径各查一遍"。
func (f *fakeTransactionRepo) SumByBuckets(_ context.Context, buckets []port.PeriodBucket, dir domain.Direction, _ domain.Account) (daily, special []port.PeriodBucket, err error) {
	f.bucketCalls++
	amts := f.bucketAmts
	if dir == domain.DirectionIncome && f.incomeBuckets != nil {
		amts = f.incomeBuckets
	}
	daily = make([]port.PeriodBucket, len(buckets))
	special = make([]port.PeriodBucket, len(buckets))
	copy(daily, buckets)
	copy(special, buckets)
	for i := range buckets {
		daily[i].Amount = amts[buckets[i].Label]
		special[i].Amount = f.specialBuckets[buckets[i].Label]
	}
	return daily, special, nil
}

func (f *fakeTransactionRepo) SetSpecialForIDs(_ context.Context, ids []string, specialID string) (int, error) {
	f.batchSpecialID = specialID
	f.batchIDs = append(f.batchIDs, ids...)
	return len(ids), nil
}

func (f *fakeTransactionRepo) TopTransactions(_ context.Context, _ domain.Period, _ domain.Direction, _ domain.Account, scope domain.Scope, limit int) ([]port.TopTransaction, error) {
	f.topScopes = append(f.topScopes, scope)
	rows := f.tops[scope]
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (f *fakeTransactionRepo) ListAll(context.Context) ([]domain.Transaction, error) {
	return f.allTxs, nil
}

func (f *fakeTransactionRepo) ListAllImportBatches(context.Context) ([]domain.ImportBatch, error) {
	return f.allBatches, nil
}

func (f *fakeTransactionRepo) ListMembers(context.Context) ([]string, error) { return nil, nil }

func (f *fakeTransactionRepo) ListForRecurring(_ context.Context, from, to time.Time, scope domain.Scope) ([]domain.Transaction, error) {
	var out []domain.Transaction
	for _, t := range f.allTxs {
		if scope == domain.ScopeDaily && t.SpecialID != "" {
			continue
		}
		if scope == domain.ScopeSpecial && t.SpecialID == "" {
			continue
		}
		if t.Direction == domain.DirectionExpense && t.Status == domain.TxStatusConfirmed &&
			!t.OccurredAt.Before(from) && t.OccurredAt.Before(to) {
			out = append(out, domain.Transaction{
				OccurredAt: t.OccurredAt, Counterparty: t.Counterparty,
				Description: t.Description, Amount: t.Amount,
				Direction: t.Direction, Status: t.Status,
			})
		}
	}
	return out, nil
}

// ---- fakeSpecialProjectRepo ----

// fakeSpecialProjectRepo 专项仓库替身。spent / inPeriod / breakdown 都直接给值，
// 口径本身的正确性由 sqlite 层的真库测试负责，这里只验 usecase 的拼装逻辑。
type fakeSpecialProjectRepo struct {
	projects []domain.SpecialProject
	spent    map[string]int64            // 专项 id → 历史已花费（净额，简写夹具用）
	inPeriod map[string]map[string]int64 // 周期 label → 专项 id → 金额
	// spendDetail 需要显式给毛额/冲抵/条数时用；同 id 会覆盖 spent 折算出来的值
	spendDetail    map[string]port.SpecialSpend
	breakdown      map[string][]domain.CategoryAggregation
	breakdownCalls int // SumByCategoryForAllProjects 被调用次数（钉住不再按专项 N+1）
	deleted        []string
	upserted       []domain.SpecialProject
}

func (f *fakeSpecialProjectRepo) ListAll(context.Context) ([]domain.SpecialProject, error) {
	return f.projects, nil
}

func (f *fakeSpecialProjectRepo) Get(_ context.Context, id string) (domain.SpecialProject, error) {
	for _, p := range f.projects {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.SpecialProject{}, port.ErrNotFound
}

func (f *fakeSpecialProjectRepo) Upsert(_ context.Context, p *domain.SpecialProject) error {
	f.upserted = append(f.upserted, *p)
	for i := range f.projects {
		if f.projects[i].ID == p.ID {
			f.projects[i] = *p
			return nil
		}
	}
	f.projects = append(f.projects, *p)
	return nil
}

func (f *fakeSpecialProjectRepo) Delete(_ context.Context, id string) error {
	for i := range f.projects {
		if f.projects[i].ID == id {
			f.projects = append(f.projects[:i], f.projects[i+1:]...)
			f.deleted = append(f.deleted, id)
			return nil
		}
	}
	return port.ErrNotFound
}

func (f *fakeSpecialProjectRepo) SumByProject(context.Context) (map[string]port.SpecialSpend, error) {
	out := make(map[string]port.SpecialSpend, len(f.spent))
	for id, net := range f.spent {
		// 夹具只给净额时，按"没有冲抵"补齐毛额与条数，保持 net = gross - offset
		s := port.SpecialSpend{GrossSpentFen: net, NetSpentFen: net, TxCount: 1}
		if extra, ok := f.spendDetail[id]; ok {
			s = extra
		}
		out[id] = s
	}
	for id, s := range f.spendDetail {
		if _, ok := out[id]; !ok {
			out[id] = s
		}
	}
	return out, nil
}

func (f *fakeSpecialProjectRepo) SumByProjectInPeriod(_ context.Context, p domain.Period, _ domain.Account) (map[string]int64, error) {
	if f.inPeriod == nil {
		return map[string]int64{}, nil
	}
	return f.inPeriod[p.Label], nil
}

func (f *fakeSpecialProjectRepo) SumByCategoryForAllProjects(context.Context) (map[string][]domain.CategoryAggregation, error) {
	f.breakdownCalls++
	return f.breakdown, nil
}

var _ port.SpecialProjectRepo = (*fakeSpecialProjectRepo)(nil)

// ---- fakeAssetSnapshotRepo ----

type fakeAssetSnapshotRepo struct {
	byPeriod map[string]domain.AssetSnapshot
}

func (f *fakeAssetSnapshotRepo) Upsert(_ context.Context, snap *domain.AssetSnapshot) error {
	if f.byPeriod == nil {
		f.byPeriod = map[string]domain.AssetSnapshot{}
	}
	f.byPeriod[snap.Period] = *snap
	return nil
}

func (f *fakeAssetSnapshotRepo) GetByPeriod(_ context.Context, period string) (*domain.AssetSnapshot, error) {
	s, ok := f.byPeriod[period]
	if !ok {
		return nil, port.ErrNotFound
	}
	cp := s
	return &cp, nil
}

func (f *fakeAssetSnapshotRepo) ListByPeriodAsc(_ context.Context, limit int) ([]domain.AssetSnapshot, error) {
	var out []domain.AssetSnapshot
	for _, s := range f.byPeriod {
		out = append(out, s)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeAssetSnapshotRepo) ListAll(_ context.Context) ([]domain.AssetSnapshot, error) {
	var out []domain.AssetSnapshot
	for _, s := range f.byPeriod {
		out = append(out, s)
	}
	return out, nil
}

// ---- fakeReportRepo ----

type fakeReportRepo struct {
	saved []domain.AIReport
}

func (f *fakeReportRepo) Upsert(_ context.Context, r *domain.AIReport) error {
	f.saved = append(f.saved, *r)
	return nil
}

func (f *fakeReportRepo) GetByPeriod(_ context.Context, period string, pt domain.PeriodType) (*domain.AIReport, error) {
	for i := len(f.saved) - 1; i >= 0; i-- {
		if f.saved[i].Period == period && f.saved[i].PeriodType == pt {
			r := f.saved[i]
			return &r, nil
		}
	}
	return nil, port.ErrNotFound
}

func (f *fakeReportRepo) ListAll(context.Context) ([]domain.AIReport, error) { return f.saved, nil }

// ---- fakeCategoryRuleRepo ----

type fakeCategoryRuleRepo struct {
	rules []domain.CategoryRule
}

func (f *fakeCategoryRuleRepo) ListRules(context.Context) ([]domain.CategoryRule, error) {
	return f.rules, nil
}
func (f *fakeCategoryRuleRepo) ListActiveRules(context.Context) ([]domain.CategoryRule, error) {
	return f.rules, nil
}
func (f *fakeCategoryRuleRepo) GetRule(context.Context, string) (domain.CategoryRule, error) {
	return domain.CategoryRule{}, port.ErrNotFound
}
func (f *fakeCategoryRuleRepo) InsertRule(context.Context, domain.CategoryRule) error { return nil }
func (f *fakeCategoryRuleRepo) UpdateRule(context.Context, domain.CategoryRule) error { return nil }
func (f *fakeCategoryRuleRepo) SetRuleActive(context.Context, string, bool) error     { return nil }
func (f *fakeCategoryRuleRepo) DeleteRule(context.Context, string) error              { return nil }

// ---- fakeLLM ----

type fakeLLM struct {
	enabled bool
	reply   string
	err     error
}

func (f *fakeLLM) Enabled() bool { return f.enabled }

func (f *fakeLLM) Complete(context.Context, string, string) (string, error) {
	return f.reply, f.err
}

var (
	_ port.CategoryRepo      = (*fakeCategoryRepo)(nil)
	_ port.TransactionRepo   = (*fakeTransactionRepo)(nil)
	_ port.AssetSnapshotRepo = (*fakeAssetSnapshotRepo)(nil)
	_ port.ReportRepo        = (*fakeReportRepo)(nil)
	_ port.CategoryRuleRepo  = (*fakeCategoryRuleRepo)(nil)
	_ ReportLLM              = (*fakeLLM)(nil)
)

// ---- fakeProfileRepo（画像 + 目标 + 保单三合一）----

type fakeProfileRepo struct {
	profile  *domain.FamilyProfile
	goals    []domain.FinancialGoal
	policies []domain.InsurancePolicy
}

func (f *fakeProfileRepo) Get(context.Context) (*domain.FamilyProfile, error) {
	if f.profile == nil {
		return nil, port.ErrNotFound
	}
	cp := *f.profile
	return &cp, nil
}

func (f *fakeProfileRepo) Upsert(_ context.Context, p *domain.FamilyProfile) error {
	cp := *p
	f.profile = &cp
	return nil
}

func (f *fakeProfileRepo) ListAllGoals(context.Context) ([]domain.FinancialGoal, error) {
	return f.goals, nil
}

func (f *fakeProfileRepo) UpsertGoal(_ context.Context, g *domain.FinancialGoal) error {
	for i := range f.goals {
		if f.goals[i].ID == g.ID {
			f.goals[i] = *g
			return nil
		}
	}
	f.goals = append(f.goals, *g)
	return nil
}

func (f *fakeProfileRepo) DeleteGoal(_ context.Context, id string) error {
	for i := range f.goals {
		if f.goals[i].ID == id {
			f.goals = append(f.goals[:i], f.goals[i+1:]...)
			return nil
		}
	}
	return port.ErrNotFound
}

func (f *fakeProfileRepo) ListAllPolicies(context.Context) ([]domain.InsurancePolicy, error) {
	return f.policies, nil
}

func (f *fakeProfileRepo) UpsertPolicy(_ context.Context, p *domain.InsurancePolicy) error {
	for i := range f.policies {
		if f.policies[i].ID == p.ID {
			f.policies[i] = *p
			return nil
		}
	}
	f.policies = append(f.policies, *p)
	return nil
}

func (f *fakeProfileRepo) DeletePolicy(_ context.Context, id string) error {
	for i := range f.policies {
		if f.policies[i].ID == id {
			f.policies = append(f.policies[:i], f.policies[i+1:]...)
			return nil
		}
	}
	return port.ErrNotFound
}

var (
	_ port.FamilyProfileRepo   = (*fakeProfileRepo)(nil)
	_ port.FinancialGoalRepo   = (*fakeProfileRepo)(nil)
	_ port.InsurancePolicyRepo = (*fakeProfileRepo)(nil)
)
