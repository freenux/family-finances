package domain

import "time"

type Source string

const (
	SourceWechat Source = "wechat"
	SourceAlipay Source = "alipay"
	SourceManual Source = "manual"
)

type Direction string

const (
	DirectionIncome  Direction = "income"
	DirectionExpense Direction = "expense"
)

type TxStatus string

const (
	TxStatusPendingReview TxStatus = "pending_review"
	TxStatusConfirmed     TxStatus = "confirmed"
	TxStatusExcluded      TxStatus = "excluded"
)

// Account 账单归属人。husband/wife 是两个家庭成员的实名枚举；
// family 仅用于查询聚合（合并家庭总账），不会作为存储值写入 transactions.account。
type Account string

const (
	AccountHusband Account = "husband"
	AccountWife    Account = "wife"
	AccountFamily  Account = "family" // 仅查询视图：husband ∪ wife
)

// IsStorageAccount 只有真实成员能写入 DB
func (a Account) IsStorageAccount() bool {
	return a == AccountHusband || a == AccountWife
}

// AccountLabel 给 UI 展示用
func (a Account) Label() string {
	switch a {
	case AccountHusband:
		return "男主"
	case AccountWife:
		return "女主"
	case AccountFamily:
		return "家庭总账"
	}
	return string(a)
}

type Transaction struct {
	ID               string
	Source           Source
	Account          Account
	ImportBatchID    string
	OccurredAt       time.Time
	Counterparty     string
	Description      string
	PlatformCategory string
	Note             string // 用户在流水表中手填的备注
	Amount           int64
	Direction        Direction
	Status           TxStatus
	CategoryID       string
	RawRow           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
