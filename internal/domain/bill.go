package domain

import "time"

// RawBillRow 是从微信/支付宝账单解析出来的统一中间结构。
// 解析器输出它，usecase 据此分类并落地为 Transaction。
type RawBillRow struct {
	Source        Source
	OccurredAt    time.Time
	Counterparty  string
	Description   string
	Amount        int64 // 分，始终为正
	Direction     Direction
	TransactionNo string // 平台交易单号，用于去重

	// 平台原始分类（alipay 的"交易分类"；wepay 的"交易类型"）
	PlatformCategory string

	// 原始行序列化，便于调试或未来审计
	RawRow string
}

// ImportBatch 一次导入的元数据（对应 import_batches 表）
type ImportBatch struct {
	ID           string
	Source       Source
	Filename     string
	PeriodStart  time.Time
	PeriodEnd    time.Time
	TotalRows    int
	ImportedRows int
	SkippedRows  int
	PendingRows  int
	CreatedAt    time.Time
}
