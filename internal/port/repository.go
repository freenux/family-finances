package port

import (
	"context"
	"errors"
	"time"

	"family-finances/internal/domain"
)

// ErrNotFound 目标记录不存在（例如按 id 更新时无匹配行）
var ErrNotFound = errors.New("record not found")

// TransactionUpdate 流水人工修正内容
type TransactionUpdate struct {
	CategoryID *string // nil 表示不修改；空字符串表示清空
	Note       *string
	Status     *domain.TxStatus
	Account    *domain.Account
	Member     *string
	SpecialID  *string // nil 表示不修改；空字符串表示清空（归回日常）
}

// ImportResult 一次账单导入的结果
type ImportResult struct {
	TotalRows          int
	InsertedRows       int
	SkippedDuplicates  int
	SkippedInvalid     int
	PendingCategory    int
	EarliestOccurredAt time.Time
}

// ImportRow 导入时的候选行：Transaction 主体 + 平台交易号（用于去重）
type ImportRow struct {
	Tx            domain.Transaction
	TransactionNo string
}

// TopTransaction 大额流水条目（statistics 页 Top-N）
type TopTransaction struct {
	ID           string
	OccurredAt   time.Time
	Counterparty string
	Description  string
	Note         string
	Amount       int64
	Direction    domain.Direction
	Account      domain.Account
	CategoryID   string
	CategoryName string
	SpecialID    string // 空 = 日常开支
	SpecialName  string
}

// PeriodBucket 单个周期（月或季）的聚合点，用于月度/季度对比条
type PeriodBucket struct {
	Label  string // "2026-05" 或 "2026Q2"
	Start  time.Time
	End    time.Time
	Amount int64
}

type TransactionRepo interface {
	Insert(ctx context.Context, tx domain.Transaction) error
	InsertBatch(ctx context.Context, batch domain.ImportBatch, rows []ImportRow) (ImportResult, error)
	Update(ctx context.Context, id string, patch TransactionUpdate) error
	Get(ctx context.Context, id string) (domain.Transaction, error)
	List(ctx context.Context, p domain.Period, account domain.Account) ([]domain.Transaction, error)
	ListPendingCategory(ctx context.Context, limit int) ([]domain.Transaction, error)
	// AggregateByCategory 按二级科目聚合；scope 决定算不算专项开支
	// （daily 剔除专项、special 只看专项、all 全口径）。
	AggregateByCategory(ctx context.Context, p domain.Period, account domain.Account, scope domain.Scope) ([]domain.CategoryAggregation, error)
	// SumByBuckets 按给定的周期桶返回 [{label, amount}]，方向过滤，状态='confirmed'。
	// 桶必须按时间升序且互不重叠（实现依赖此约定做单次扫描归桶）。
	//
	// 一次调用同时给出两组同源桶：daily（未挂专项）与 special（挂了专项），长度、顺序、
	// Label 与入参一致，可按下标对齐；"全部"口径 = 两者逐桶相加。只要日常基线的调用方
	// 取第一个返回值即可——分两次按 scope 查等于把同一段范围扫两遍。
	SumByBuckets(ctx context.Context, buckets []PeriodBucket, direction domain.Direction, account domain.Account) (daily, special []PeriodBucket, err error)
	// SetSpecialForIDs 批量把一组流水归入专项（specialID 空串 = 归回日常），
	// 必须在单个事务里一次写完；返回真正被改到的行数，不存在的 id 静默跳过。
	SetSpecialForIDs(ctx context.Context, ids []string, specialID string) (int, error)
	// TopTransactions 周期内按 |amount| desc 的前 N 条；account=family 合并统计
	TopTransactions(ctx context.Context, p domain.Period, direction domain.Direction, account domain.Account, scope domain.Scope, limit int) ([]TopTransaction, error)
	// ListAll 全量流水，按 occurred_at 升序；供 /export 使用
	ListAll(ctx context.Context) ([]domain.Transaction, error)
	// ListAllImportBatches 全量导入批次；供 /export 使用
	ListAllImportBatches(ctx context.Context) ([]domain.ImportBatch, error)
	// ListMembers 已出现过的成员标注去重列表（datalist 记忆用）
	ListMembers(ctx context.Context) ([]string, error)
	// ListForRecurring 周期识别专用精简查询：direction='expense' AND status='confirmed'，
	// 只取 occurred_at / counterparty / description / amount 必要列（不拉 raw_row 等大字段），
	// 返回的 Transaction 仅这四个字段 + Direction/Status 有值。scope 同上。
	ListForRecurring(ctx context.Context, from, to time.Time, scope domain.Scope) ([]domain.Transaction, error)
}

// SpecialProjectRepo 专项开支项目（special_projects 表）
type SpecialProjectRepo interface {
	ListAll(ctx context.Context) ([]domain.SpecialProject, error)
	// Get 未找到时返回 ErrNotFound
	Get(ctx context.Context, id string) (domain.SpecialProject, error)
	// Upsert 按 id 覆盖写入
	Upsert(ctx context.Context, p *domain.SpecialProject) error
	Delete(ctx context.Context, id string) error
	// 下面三个求和一律是「净额」口径：挂在专项上的收入（退款、退货返现、卖旧车抵扣换车）
	// 从支出里减掉，而不是加进已花费。净额可能为负（退款大于支出），实现不得 clamp 到 0。

	// SumByProject 每个专项的花费统计（专项 id → SpecialSpend），只算 status='confirmed'；
	// 有流水的专项一定出现在返回值里，哪怕净额被冲平成 0
	SumByProject(ctx context.Context) (map[string]SpecialSpend, error)
	// SumByProjectInPeriod 周期内每个专项的净花费（专项 id → 分），供季/年报拆行；
	// account 为非存储值（family）时不做账户过滤
	SumByProjectInPeriod(ctx context.Context, p domain.Period, account domain.Account) (map[string]int64, error)
	// SumByCategoryForAllProjects 全部专项内部的跨科目构成（净额），专项 id → 构成表，
	// 每张表按金额降序、只含净额非零的科目——被全额退款冲平的科目不再列出。
	// 一次 GROUP BY (special_id, category_id) 取回全部，不要退化成按专项逐个查。
	SumByCategoryForAllProjects(ctx context.Context) (map[string][]domain.CategoryAggregation, error)
}

// SpecialSpend 单个专项的花费统计。满足 NetSpentFen = GrossSpentFen - OffsetFen。
type SpecialSpend struct {
	// GrossSpentFen 支出毛额：挂在专项上的 direction='expense' 流水合计
	GrossSpentFen int64
	// OffsetFen 冲抵额：挂在专项上的 direction='income' 流水合计（退款、退货返现、卖旧车）
	OffsetFen int64
	// NetSpentFen 净额 = 毛额 − 冲抵，可能为负（退回的比花掉的多），不得 clamp 到 0
	NetSpentFen int64
	// TxCount 归入该专项的 confirmed 流水条数（收支都算）。
	// 页面靠它区分"真的没有流水"和"有流水但净额被冲平为 0"——只看金额零值分不出这两种。
	TxCount int
}

type CategoryRepo interface {
	ListAll(ctx context.Context) ([]domain.Category, error)
	ListByType(ctx context.Context, t domain.CategoryType) ([]domain.Category, error)
}

type CategoryRuleRepo interface {
	ListRules(ctx context.Context) ([]domain.CategoryRule, error)
	ListActiveRules(ctx context.Context) ([]domain.CategoryRule, error)
	GetRule(ctx context.Context, id string) (domain.CategoryRule, error)
	InsertRule(ctx context.Context, rule domain.CategoryRule) error
	UpdateRule(ctx context.Context, rule domain.CategoryRule) error
	SetRuleActive(ctx context.Context, id string, active bool) error
	DeleteRule(ctx context.Context, id string) error
}

// AssetSnapshotRepo 资产快照（asset_snapshots 表）
type AssetSnapshotRepo interface {
	// Upsert 按 period 唯一键覆盖写入
	Upsert(ctx context.Context, snap *domain.AssetSnapshot) error
	// GetByPeriod 未找到时返回 ErrNotFound
	GetByPeriod(ctx context.Context, period string) (*domain.AssetSnapshot, error)
	// ListByPeriodAsc 返回最近 limit 个快照，按 period 升序排列（供曲线绘制）
	ListByPeriodAsc(ctx context.Context, limit int) ([]domain.AssetSnapshot, error)
	// ListAll 全量快照，按 period 升序；供 /export 使用
	ListAll(ctx context.Context) ([]domain.AssetSnapshot, error)
}

// FamilyProfileRepo 家庭风险画像（family_profile 单行表）
type FamilyProfileRepo interface {
	// Get 未填写过时返回 ErrNotFound
	Get(ctx context.Context) (*domain.FamilyProfile, error)
	// Upsert 覆盖写入单行（id 固定 'default'）
	Upsert(ctx context.Context, p *domain.FamilyProfile) error
}

// FinancialGoalRepo 财务目标（financial_goals 表）
type FinancialGoalRepo interface {
	ListAllGoals(ctx context.Context) ([]domain.FinancialGoal, error)
	UpsertGoal(ctx context.Context, g *domain.FinancialGoal) error
	DeleteGoal(ctx context.Context, id string) error
}

// InsurancePolicyRepo 保单（insurance_policies 表）
type InsurancePolicyRepo interface {
	ListAllPolicies(ctx context.Context) ([]domain.InsurancePolicy, error)
	UpsertPolicy(ctx context.Context, p *domain.InsurancePolicy) error
	DeletePolicy(ctx context.Context, id string) error
}

// ImportTemplateRepo CSV 导入映射模板（import_templates 表）
type ImportTemplateRepo interface {
	ListTemplates(ctx context.Context) ([]domain.ImportTemplate, error)
	// SaveTemplate 按 name 唯一键覆盖
	SaveTemplate(ctx context.Context, t *domain.ImportTemplate) error
}

// DigestRepo 摘要推送设置（digest_settings 单行表）
type DigestRepo interface {
	// GetSettings 未配置过时返回 ErrNotFound
	GetSettings(ctx context.Context) (*domain.DigestSettings, error)
	UpsertSettings(ctx context.Context, s *domain.DigestSettings) error
	MarkSent(ctx context.Context, at time.Time) error
}

// BudgetRepo 季度预算（budgets 表）
type BudgetRepo interface {
	ListBudgets(ctx context.Context) ([]domain.Budget, error)
	// ReplaceBudgets 整表覆盖：amounts 里 >0 的科目 upsert，未出现或 =0 的科目删除
	ReplaceBudgets(ctx context.Context, amounts map[string]int64) error
}

// ReportRepo AI 季/年财报（reports 表）
type ReportRepo interface {
	// Upsert 按 (period, period_type) 唯一键覆盖写入
	Upsert(ctx context.Context, report *domain.AIReport) error
	// GetByPeriod 未找到时返回 ErrNotFound
	GetByPeriod(ctx context.Context, period string, periodType domain.PeriodType) (*domain.AIReport, error)
	// ListAll 全量历史财报，按 generated_at desc
	ListAll(ctx context.Context) ([]domain.AIReport, error)
}
