package domain

import "time"

// ImportTemplate 通用 CSV 导入的映射模板（import_templates 表）。
// Mapping 为 adapter/bill.CSVMapping 的 JSON 序列化（domain 不依赖 adapter，存原文）。
type ImportTemplate struct {
	ID        string
	Name      string
	Mapping   string // JSON
	CreatedAt time.Time
}
