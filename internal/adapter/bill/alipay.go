package bill

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"family-finances/internal/domain"
)

type alipayParser struct{}

func (alipayParser) Source() domain.Source { return domain.SourceAlipay }

// 表头字段（按顺序）
var alipayHeader = []string{
	"交易时间", "交易分类", "交易对方", "对方账号", "商品说明",
	"收/支", "金额", "收/付款方式", "交易状态", "交易订单号", "商家订单号", "备注",
}

// Parse 解析支付宝个人账单 CSV（GB18030 编码，头部 20+ 行元信息）。
func (p alipayParser) Parse(r io.Reader) ([]domain.RawBillRow, error) {
	// GB18030 → UTF-8
	decoder := simplifiedchinese.GB18030.NewDecoder()
	utf8Reader := transform.NewReader(r, decoder)

	scanner := bufio.NewScanner(utf8Reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		rows       []domain.RawBillRow
		inData     bool
		headerIdx  = make(map[string]int)
	)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 数据段起点：第一列严格以"交易时间"开头 & 包含逗号
		if !inData {
			if strings.HasPrefix(trimmed, "交易时间") && strings.Contains(trimmed, ",") {
				cols := splitCSV(trimmed)
				for i, c := range cols {
					headerIdx[strings.TrimSpace(c)] = i
				}
				// 最低校验
				if _, ok := headerIdx["交易时间"]; !ok {
					return nil, fmt.Errorf("alipay: 无法识别表头行")
				}
				inData = true
			}
			continue
		}
		// 数据行：以 4 位年份开头（2023- / 2024- / 2025- / 2026-）
		if len(trimmed) < 4 || !isDigit(trimmed[0]) || !isDigit(trimmed[1]) ||
			!isDigit(trimmed[2]) || !isDigit(trimmed[3]) {
			// 遇到非数据行（例如尾部的说明、分隔线），提前结束
			break
		}
		cols := splitCSV(trimmed)
		row, err := parseAlipayRow(cols, headerIdx)
		if err != nil {
			if err == ErrSkipRow {
				continue
			}
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("alipay: read: %w", err)
	}
	if !inData {
		return nil, fmt.Errorf("alipay: 未找到表头，可能文件格式不匹配")
	}
	return rows, nil
}

func parseAlipayRow(cols []string, idx map[string]int) (domain.RawBillRow, error) {
	get := func(name string) string {
		i, ok := idx[name]
		if !ok || i >= len(cols) {
			return ""
		}
		return strings.TrimSpace(strings.TrimRight(cols[i], "\t"))
	}

	direction, ok := parseDirection(get("收/支"))
	if !ok {
		// "不计收支" / 空等都跳过
		return domain.RawBillRow{}, ErrSkipRow
	}
	status := get("交易状态")
	if !isAlipaySuccess(status) {
		return domain.RawBillRow{}, ErrSkipRow
	}

	t, err := time.ParseInLocation("2006-01-02 15:04:05", get("交易时间"), time.Local)
	if err != nil {
		return domain.RawBillRow{}, fmt.Errorf("alipay: 时间格式错误 %q: %w", get("交易时间"), err)
	}

	amountYuan, err := strconv.ParseFloat(get("金额"), 64)
	if err != nil {
		return domain.RawBillRow{}, fmt.Errorf("alipay: 金额格式错误 %q: %w", get("金额"), err)
	}
	amountFen := int64(amountYuan*100 + 0.5)

	txNo := get("交易订单号")
	if txNo == "" {
		// 交易订单号为空的通常是"不计收支"；直接跳过
		return domain.RawBillRow{}, ErrSkipRow
	}

	return domain.RawBillRow{
		Source:           domain.SourceAlipay,
		OccurredAt:       t,
		Counterparty:     get("交易对方"),
		Description:      get("商品说明"),
		Amount:           amountFen,
		Direction:        direction,
		TransactionNo:    txNo,
		PlatformCategory: get("交易分类"),
		RawRow:           strings.Join(cols, ","),
	}, nil
}

func isAlipaySuccess(status string) bool {
	switch status {
	case "交易成功", "等待确认收货", "退款成功":
		return true
	}
	return false
}

// splitCSV 使用 encoding/csv 解析单行，避免引号处理错误
func splitCSV(line string) []string {
	r := csv.NewReader(strings.NewReader(line))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	rec, err := r.Read()
	if err != nil {
		// 降级到朴素逗号切分
		return strings.Split(line, ",")
	}
	return rec
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func parseDirection(s string) (domain.Direction, bool) {
	switch s {
	case "收入":
		return domain.DirectionIncome, true
	case "支出":
		return domain.DirectionExpense, true
	}
	return "", false
}
