package bill

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"family-finances/internal/domain"
)

func singleMapping() CSVMapping {
	return CSVMapping{
		SkipRows: 0, TimeCol: 0, DateFormat: "2006-01-02",
		AmountMode: "single", AmountCol: 1,
		DebitCol: -1, CreditCol: -1,
		DescCols: []int{2}, CounterpartyCol: 3,
	}
}

func TestGenericCSVSingleMode(t *testing.T) {
	csvData := "日期,金额,说明,对方\n" +
		"2026-05-10,123.45,买菜,菜市场\n" +
		"2026-05-11,-3000,工资,公司\n" + // 负数 = 收入
		"合计,999,,\n" // 时间列无法解析 → 跳过
	p := NewGenericCSVParser(singleMapping(), "银行流水")
	rows, err := p.Parse(strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d (%+v); want 2", len(rows), rows)
	}
	if rows[0].Amount != 12345 || rows[0].Direction != domain.DirectionExpense {
		t.Fatalf("row0 = %+v; want 12345 分支出", rows[0])
	}
	if rows[0].Counterparty != "菜市场" || rows[0].Description != "买菜" {
		t.Fatalf("row0 fields = %+v", rows[0])
	}
	if rows[1].Amount != 300000 || rows[1].Direction != domain.DirectionIncome {
		t.Fatalf("row1 = %+v; want 300000 分收入", rows[1])
	}
	if rows[0].TransactionNo == "" || rows[0].TransactionNo == rows[1].TransactionNo {
		t.Fatalf("TransactionNo 应非空且互异: %q %q", rows[0].TransactionNo, rows[1].TransactionNo)
	}
	if string(rows[0].Source) != "csv:银行流水" {
		t.Fatalf("Source = %s; want csv:银行流水", rows[0].Source)
	}
}

func TestGenericCSVDebitCreditMode(t *testing.T) {
	m := CSVMapping{
		SkipRows: 1, TimeCol: 0, DateFormat: "2006/01/02",
		AmountMode: "debit_credit", AmountCol: -1, DebitCol: 1, CreditCol: 2,
		DescCols: []int{3, 4}, CounterpartyCol: -1,
	}
	csvData := "某银行对账单\n" + // skip_rows=1
		"日期,支出,收入,摘要,附言\n" +
		"2026/05/10,\"¥1,234.50\",,餐费,团队聚餐\n" +
		"2026/05/12,,5000.00,退款,\n"
	p := NewGenericCSVParser(m, "")
	rows, err := p.Parse(strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v; want 2", rows)
	}
	if rows[0].Amount != 123450 || rows[0].Direction != domain.DirectionExpense {
		t.Fatalf("row0 = %+v; want 123450 分支出（千位逗号+¥ 前缀）", rows[0])
	}
	if rows[0].Description != "餐费 团队聚餐" {
		t.Fatalf("desc = %q; want 多列拼接", rows[0].Description)
	}
	if rows[1].Direction != domain.DirectionIncome || rows[1].Amount != 500000 {
		t.Fatalf("row1 = %+v; want 500000 分收入", rows[1])
	}
	if string(rows[1].Source) != "csv:generic" {
		t.Fatalf("Source = %s; want csv:generic（空模板名兜底）", rows[1].Source)
	}
}

func TestGenericCSVGB18030(t *testing.T) {
	utf8Data := "日期,金额,说明,对方\n2026-05-10,88.00,早餐豆浆,楼下早点铺\n"
	gbkData, _, err := transform.String(simplifiedchinese.GB18030.NewEncoder(), utf8Data)
	if err != nil {
		t.Fatalf("encode gb18030: %v", err)
	}
	p := NewGenericCSVParser(singleMapping(), "gbk 模板")
	rows, err := p.Parse(strings.NewReader(gbkData))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Counterparty != "楼下早点铺" || rows[0].Description != "早餐豆浆" {
		t.Fatalf("rows = %+v; want GB18030 中文解码正确", rows)
	}
}

func TestGenericCSVUTF8BOM(t *testing.T) {
	csvData := "\xEF\xBB\xBF日期,金额,说明,对方\n2026-05-10,10.00,x,y\n"
	rows, err := NewGenericCSVParser(singleMapping(), "t").Parse(strings.NewReader(csvData))
	if err != nil || len(rows) != 1 {
		t.Fatalf("Parse() = %v, %v; want 1 row（BOM 去除）", rows, err)
	}
}

func TestGenericCSVValidation(t *testing.T) {
	cases := []struct {
		name string
		m    CSVMapping
	}{
		{"缺时间列", CSVMapping{TimeCol: -1, DateFormat: "2006-01-02", AmountMode: "single", AmountCol: 1}},
		{"缺日期格式", CSVMapping{TimeCol: 0, AmountMode: "single", AmountCol: 1}},
		{"single 缺金额列", CSVMapping{TimeCol: 0, DateFormat: "2006-01-02", AmountMode: "single", AmountCol: -1}},
		{"debit_credit 两列全缺", CSVMapping{TimeCol: 0, DateFormat: "2006-01-02", AmountMode: "debit_credit", DebitCol: -1, CreditCol: -1}},
		{"未知模式", CSVMapping{TimeCol: 0, DateFormat: "2006-01-02", AmountMode: "both", AmountCol: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.m.Validate(); err == nil {
				t.Fatal("Validate() = nil; want error")
			}
		})
	}
}

func TestGenericCSVNoValidRows(t *testing.T) {
	p := NewGenericCSVParser(singleMapping(), "t")
	if _, err := p.Parse(strings.NewReader("日期,金额,说明,对方\nabc,def,,\n")); err == nil {
		t.Fatal("Parse() = nil; want no-valid-rows error")
	}
}
