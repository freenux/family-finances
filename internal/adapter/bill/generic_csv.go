package bill

import (
	"crypto/sha1"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"family-finances/internal/domain"
)

// CSVMapping 通用 CSV 导入的列映射（列一律用下标，-1 = 不使用）。
// AmountMode:
//
//	single       金额在一列；正数 = 支出、负数 = 收入（绝对值入库）
//	debit_credit 支出/收入分列；哪列有值方向就取哪列
type CSVMapping struct {
	SkipRows        int    `json:"skip_rows"` // 表头行之前跳过的行数
	TimeCol         int    `json:"time_col"`
	DateFormat      string `json:"date_format"` // Go layout，如 "2006-01-02 15:04:05"
	AmountMode      string `json:"amount_mode"` // single | debit_credit
	AmountCol       int    `json:"amount_col"`
	DebitCol        int    `json:"debit_col"`
	CreditCol       int    `json:"credit_col"`
	DescCols        []int  `json:"desc_cols"`
	CounterpartyCol int    `json:"counterparty_col"`
}

// Validate 基本合法性检查
func (m CSVMapping) Validate() error {
	if m.SkipRows < 0 || m.SkipRows > 100 {
		return fmt.Errorf("skip_rows 需在 0–100")
	}
	if m.TimeCol < 0 {
		return fmt.Errorf("必须指定时间列")
	}
	if m.DateFormat == "" {
		return fmt.Errorf("必须指定日期格式")
	}
	switch m.AmountMode {
	case "single":
		if m.AmountCol < 0 {
			return fmt.Errorf("single 模式必须指定金额列")
		}
	case "debit_credit":
		if m.DebitCol < 0 && m.CreditCol < 0 {
			return fmt.Errorf("debit_credit 模式至少指定支出列或收入列")
		}
	default:
		return fmt.Errorf("amount_mode 必须是 single 或 debit_credit")
	}
	return nil
}

// genericCSVParser 实现 Parser：构造时注入 mapping 与模板名
type genericCSVParser struct {
	mapping CSVMapping
	source  domain.Source // "csv:<模板名>"
}

// NewGenericCSVParser source 形如 csv:<模板名>，作为去重键与 Beancount 资金账户依据
func NewGenericCSVParser(mapping CSVMapping, templateName string) Parser {
	name := strings.TrimSpace(templateName)
	if name == "" {
		name = "generic"
	}
	return &genericCSVParser{mapping: mapping, source: domain.Source("csv:" + name)}
}

func (p *genericCSVParser) Source() domain.Source { return p.source }

// Parse 编码探测（UTF-8 优先，非法则转 GB18030）→ 跳过 skip_rows + 表头 → 逐行映射
func (p *genericCSVParser) Parse(r io.Reader) ([]domain.RawBillRow, error) {
	if err := p.mapping.Validate(); err != nil {
		return nil, fmt.Errorf("csv 映射不合法: %w", err)
	}
	records, err := ReadCSVRecords(r)
	if err != nil {
		return nil, err
	}
	dataStart := p.mapping.SkipRows + 1 // 跳过行 + 表头行
	if len(records) <= dataStart {
		return nil, fmt.Errorf("csv: 数据行为空（共 %d 行，表头前跳过 %d 行）", len(records), p.mapping.SkipRows)
	}

	var rows []domain.RawBillRow
	for _, record := range records[dataStart:] {
		row, err := p.parseRecord(record)
		if err == ErrSkipRow {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("csv: 按当前映射没有解析出任何有效行，请检查列映射与日期格式")
	}
	return rows, nil
}

func (p *genericCSVParser) parseRecord(record []string) (domain.RawBillRow, error) {
	get := func(col int) string {
		if col < 0 || col >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[col])
	}

	timeStr := get(p.mapping.TimeCol)
	if timeStr == "" {
		return domain.RawBillRow{}, ErrSkipRow // 空行/汇总行
	}
	occurredAt, err := time.ParseInLocation(p.mapping.DateFormat, timeStr, time.Local)
	if err != nil {
		return domain.RawBillRow{}, ErrSkipRow // 尾部汇总等无法解析的行静默跳过
	}

	amountFen, direction, err := p.parseAmount(get)
	if err != nil {
		return domain.RawBillRow{}, err
	}
	if amountFen == 0 {
		return domain.RawBillRow{}, ErrSkipRow
	}

	var descParts []string
	for _, col := range p.mapping.DescCols {
		if v := get(col); v != "" {
			descParts = append(descParts, v)
		}
	}
	desc := strings.Join(descParts, " ")
	counterparty := get(p.mapping.CounterpartyCol)

	// 去重键：时间 + 金额 + 描述 哈希（CSV 没有平台交易号）
	h := sha1.Sum([]byte(timeStr + "|" + strconv.FormatInt(amountFen, 10) + "|" + string(direction) + "|" + desc + "|" + counterparty))

	return domain.RawBillRow{
		Source:        p.source,
		OccurredAt:    occurredAt,
		Counterparty:  counterparty,
		Description:   desc,
		Amount:        amountFen,
		Direction:     direction,
		TransactionNo: hex.EncodeToString(h[:]),
		RawRow:        strings.Join(record, ","),
	}, nil
}

func (p *genericCSVParser) parseAmount(get func(int) string) (int64, domain.Direction, error) {
	if p.mapping.AmountMode == "single" {
		v, err := parseYuanString(get(p.mapping.AmountCol))
		if err != nil {
			return 0, "", ErrSkipRow
		}
		if v < 0 {
			return -v, domain.DirectionIncome, nil
		}
		return v, domain.DirectionExpense, nil
	}
	// debit_credit
	if s := get(p.mapping.DebitCol); s != "" {
		v, err := parseYuanString(s)
		if err == nil && v != 0 {
			return abs64(v), domain.DirectionExpense, nil
		}
	}
	if s := get(p.mapping.CreditCol); s != "" {
		v, err := parseYuanString(s)
		if err == nil && v != 0 {
			return abs64(v), domain.DirectionIncome, nil
		}
	}
	return 0, "", ErrSkipRow
}

// parseYuanString 元字符串 → 分（保留符号；容忍 ¥、千位逗号、空格）
func parseYuanString(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "¥")
	s = strings.TrimPrefix(s, "￥")
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if f >= 0 {
		return int64(f*100 + 0.5), nil
	}
	return -int64(-f*100 + 0.5), nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ReadCSVRecords 编码探测 + 全量读取：UTF-8（含 BOM）优先，非法 UTF-8 转 GB18030。
// preview 端点与解析器共用。
func ReadCSVRecords(r io.Reader) ([][]string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("csv: read: %w", err)
	}
	// 去 BOM
	raw = bytesTrimBOM(raw)
	if !utf8.Valid(raw) {
		decoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw)
		if err != nil {
			return nil, fmt.Errorf("csv: 编码既不是 UTF-8 也不是 GB18030: %w", err)
		}
		raw = decoded
	}
	reader := csv.NewReader(strings.NewReader(string(raw)))
	reader.FieldsPerRecord = -1 // 变长行容忍
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv: parse: %w", err)
	}
	return records, nil
}

func bytesTrimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
