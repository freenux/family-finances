package usecase

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"family-finances/internal/domain"
)

// MaxAmountFen 单笔金额上限：100 亿元（分），防 int64 溢出与垃圾数据。
const MaxAmountFen int64 = 10_000_000_000 * 100

// TxInserter 写入单条流水，由 sqlite.TransactionRepo 满足。
type TxInserter interface {
	Insert(ctx context.Context, tx domain.Transaction) error
}

// NewTransactionInput 手填记账的入参。全部是原始字符串/分，校验与装配都在 CreateTransaction。
// 金额只收分：元转分是一处容易两端各写一遍的推导，SSR 表单走 ParseYuanToFen，
// /api/v1 客户端直接传分，换算因此只有这一处。
type NewTransactionInput struct {
	OccurredAt   string // 见 ParseOccurredAt
	Account      string // husband | wife
	Direction    string // income | expense
	AmountFen    int64  // 分，必须 > 0
	CategoryID   string // 可空；非空必须是二级科目
	Member       string
	Counterparty string
	Description  string
	Note         string
}

// CreateTransaction 手填记账用例：SSR 表单与 /api/v1 共用，适配器只管 HTTP。
// 校验失败返回 ErrInvalidInput（Error() 是面向用户的中文），其余是内部故障。
type CreateTransaction struct {
	tx    TxInserter
	cats  LeafCategoryLister
	newID func() string
	now   func() time.Time
}

func NewCreateTransaction(tx TxInserter, cats LeafCategoryLister) *CreateTransaction {
	return &CreateTransaction{tx: tx, cats: cats, newID: uuid.NewString, now: time.Now}
}

// Execute 校验并落库，返回写入的流水（适配器要用它的 ID、账户、发生时间做响应/跳转）。
// 校验顺序：时间 → 账户 → 方向 → 金额 → 科目。
// 状态恒为 confirmed（手填就是用户亲手确认的，页面文案也是「录入一条已确认流水」）；
// 来源恒为 manual。
func (uc *CreateTransaction) Execute(ctx context.Context, in NewTransactionInput) (domain.Transaction, error) {
	occurredAt, err := ParseOccurredAt(in.OccurredAt)
	if err != nil {
		return domain.Transaction{}, err
	}
	acc := domain.Account(in.Account)
	if !acc.IsStorageAccount() {
		return domain.Transaction{}, newUserError(ErrInvalidInput, "请选择账户归属（男主/女主）")
	}
	dir := domain.Direction(in.Direction)
	if dir != domain.DirectionIncome && dir != domain.DirectionExpense {
		return domain.Transaction{}, newUserError(ErrInvalidInput, "请选择收支方向")
	}
	if in.AmountFen <= 0 {
		return domain.Transaction{}, newUserError(ErrInvalidInput, "金额必须为正数")
	}
	if in.AmountFen > MaxAmountFen {
		return domain.Transaction{}, newUserError(ErrInvalidInput, "金额超出可接受范围")
	}
	if in.CategoryID != "" {
		if err := ensureLeafCategory(ctx, uc.cats, in.CategoryID); err != nil {
			return domain.Transaction{}, err
		}
	}

	now := uc.now()
	tx := domain.Transaction{
		ID:           uc.newID(),
		Source:       domain.SourceManual,
		Account:      acc,
		Member:       strings.TrimSpace(in.Member),
		OccurredAt:   occurredAt,
		Counterparty: strings.TrimSpace(in.Counterparty),
		Description:  strings.TrimSpace(in.Description),
		Note:         strings.TrimSpace(in.Note),
		Amount:       in.AmountFen,
		Direction:    dir,
		Status:       domain.TxStatusConfirmed,
		CategoryID:   in.CategoryID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := uc.tx.Insert(ctx, tx); err != nil {
		return domain.Transaction{}, err // 内部故障，适配器负责不外泄细节
	}
	return tx, nil
}

// ParseOccurredAt 解析发生时间，三种写法都按服务器本地时区理解（无时区信息时）：
// "2006-01-02T15:04"（SSR datetime-local 表单）、"2006-01-02 15:04"（与响应里 occurred_text 同形）、
// RFC3339（与响应里 occurred_at 同形，带时区时保留其偏移）。
func ParseOccurredAt(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, newUserError(ErrInvalidInput, "日期时间格式不正确")
}

// ParseYuanToFen 把用户输入的元金额（"12.34"）转成分，是元→分的唯一实现。
// 纯十进制字符串解析，整条链路不出现 float；第三位小数四舍五入（半数进位），
// 与账单解析器「元乘 100 再 +0.5」同向（19.999 → 2000）。
// 只接受 [+-]整数[.小数]；科学计数法、千分位、NaN/Inf 一律「金额格式不正确」。
// 返回的分保证 > 0 且 ≤ MaxAmountFen，否则分别报「金额必须为正数」「金额超出可接受范围」。
func ParseYuanToFen(s string) (int64, error) {
	bad := newUserError(ErrInvalidInput, "金额格式不正确")
	s = strings.TrimSpace(s)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	intPart, fracPart, _ := strings.Cut(s, ".")
	if intPart == "" && fracPart == "" {
		return 0, bad
	}
	for _, part := range []string{intPart, fracPart} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, bad
			}
		}
	}
	// 整数部分超过 11 位（> 100 亿元）直接判越界，避免累加溢出
	trimmed := strings.TrimLeft(intPart, "0")
	if len(trimmed) > 11 {
		if neg {
			return 0, newUserError(ErrInvalidInput, "金额必须为正数")
		}
		return 0, newUserError(ErrInvalidInput, "金额超出可接受范围")
	}
	var yuan int64
	for _, c := range trimmed {
		yuan = yuan*10 + int64(c-'0')
	}
	for len(fracPart) < 3 {
		fracPart += "0"
	}
	fen := yuan*100 + int64(fracPart[0]-'0')*10 + int64(fracPart[1]-'0')
	if fracPart[2] >= '5' {
		fen++
	}
	switch {
	case neg || fen <= 0:
		return 0, newUserError(ErrInvalidInput, "金额必须为正数")
	case fen > MaxAmountFen:
		return 0, newUserError(ErrInvalidInput, "金额超出可接受范围")
	}
	return fen, nil
}
