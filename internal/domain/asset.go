package domain

import "time"

// AssetAccount 是资产/负债科目目录条目。目录为代码内常量（不进 categories 表——
// categories 与交易/规则耦合，资产科目无交易语义）。
type AssetAccount struct {
	Code  string
	Name  string
	Group string // "asset" | "liability"
	Sort  int
}

// AssetCatalog 资产/负债科目目录。权益细分为境内/海外/黄金，为 M2 长期桶内配置诊断预留，
// 避免日后迁移数据。
var AssetCatalog = []AssetAccount{
	{"asset.cash", "现金及活期存款", "asset", 1},
	{"asset.mmf", "货币基金（余额宝/零钱通）", "asset", 2},
	{"asset.deposit", "定期存款", "asset", 3},
	{"asset.wealth", "银行理财/债券基金", "asset", 4},
	{"asset.equity_cn", "境内股票及偏股基金", "asset", 5},
	{"asset.equity_global", "海外权益（QDII等）", "asset", 6},
	{"asset.gold", "黄金及另类", "asset", 7},
	{"asset.pension", "公积金/养老金账户", "asset", 8},
	{"asset.house", "自住房产（估值）", "asset", 9},
	{"liability.mortgage", "房贷余额", "liability", 10},
	{"liability.consumer", "信用卡/消费贷", "liability", 11},
}

// AssetCodeSet 返回目录中所有合法 code 的集合，用于校验。
func AssetCodeSet() map[string]bool {
	set := make(map[string]bool, len(AssetCatalog))
	for _, a := range AssetCatalog {
		set[a.Code] = true
	}
	return set
}

// AssetSnapshot 一次季度资产快照
type AssetSnapshot struct {
	ID           string
	Period       string // 仅季度，如 "2026Q2"
	SnapshotDate time.Time
	Data         map[string]int64 // code -> 分；只允许 AssetCatalog 中的 code
	NetWorth     int64            // 资产合计 − 负债合计
	CreatedAt    time.Time
}
