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

type Transaction struct {
	ID            string
	Source        Source
	ImportBatchID string
	OccurredAt    time.Time
	Counterparty  string
	Description   string
	Amount        int64
	Direction     Direction
	Status        TxStatus
	CategoryID    string
	RawRow        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
