package bill

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"family-finances/internal/domain"
)

type wechatParser struct{}

func (wechatParser) Source() domain.Source { return domain.SourceWechat }

var wechatHeaderKeys = []string{
	"交易时间", "交易类型", "交易对方", "商品", "收/支",
	"金额(元)", "支付方式", "当前状态", "交易单号", "商户单号", "备注",
}

// Parse 解析微信支付账单 xlsx。excelize 只接受 io.Reader 用于 open from reader，
// 因此把流拷贝到临时文件。
func (p wechatParser) Parse(r io.Reader) ([]domain.RawBillRow, error) {
	tmp, err := os.CreateTemp("", "wepay-*.xlsx")
	if err != nil {
		return nil, fmt.Errorf("wechat: create tmp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("wechat: copy to tmp: %w", err)
	}
	tmp.Close()

	f, err := excelize.OpenFile(tmp.Name())
	if err != nil {
		return nil, fmt.Errorf("wechat: open xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("wechat: xlsx 不包含任何 sheet")
	}
	rawRows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("wechat: read rows: %w", err)
	}

	var (
		rows      []domain.RawBillRow
		headerIdx = make(map[string]int)
		inData    bool
	)
	for _, row := range rawRows {
		if len(row) == 0 {
			continue
		}
		first := strings.TrimSpace(row[0])
		if !inData {
			if first == "交易时间" {
				for i, c := range row {
					headerIdx[strings.TrimSpace(c)] = i
				}
				if _, ok := headerIdx["交易时间"]; !ok {
					return nil, fmt.Errorf("wechat: 无法识别表头")
				}
				inData = true
			}
			continue
		}
		// 数据行：第一格是时间戳
		if _, err := time.ParseInLocation("2006-01-02 15:04:05", first, time.Local); err != nil {
			continue
		}
		raw, err := parseWechatRow(row, headerIdx)
		if err != nil {
			if err == ErrSkipRow {
				continue
			}
			return nil, err
		}
		rows = append(rows, raw)
	}
	if !inData {
		return nil, fmt.Errorf("wechat: 未找到表头行")
	}
	return rows, nil
}

func parseWechatRow(cols []string, idx map[string]int) (domain.RawBillRow, error) {
	get := func(name string) string {
		i, ok := idx[name]
		if !ok || i >= len(cols) {
			return ""
		}
		// 微信在金额前加了 ¥ 前缀，备注字段用 / 占位
		v := strings.TrimSpace(cols[i])
		return v
	}

	direction, ok := parseDirection(get("收/支"))
	if !ok {
		// "/"、"中性交易" 之类跳过
		return domain.RawBillRow{}, ErrSkipRow
	}
	if !isWechatSuccess(get("当前状态")) {
		return domain.RawBillRow{}, ErrSkipRow
	}

	t, err := time.ParseInLocation("2006-01-02 15:04:05", get("交易时间"), time.Local)
	if err != nil {
		return domain.RawBillRow{}, fmt.Errorf("wechat: 时间格式错误 %q: %w", get("交易时间"), err)
	}

	amountStr := strings.TrimPrefix(get("金额(元)"), "¥")
	amountStr = strings.ReplaceAll(amountStr, ",", "")
	amountStr = strings.TrimSpace(amountStr)
	amountYuan, err := strconv.ParseFloat(amountStr, 64)
	if err != nil {
		return domain.RawBillRow{}, fmt.Errorf("wechat: 金额格式错误 %q: %w", amountStr, err)
	}
	amountFen := int64(amountYuan*100 + 0.5)

	txNo := get("交易单号")
	if txNo == "" {
		return domain.RawBillRow{}, ErrSkipRow
	}

	return domain.RawBillRow{
		Source:           domain.SourceWechat,
		OccurredAt:       t,
		Counterparty:     get("交易对方"),
		Description:      get("商品"),
		Amount:           amountFen,
		Direction:        direction,
		TransactionNo:    txNo,
		PlatformCategory: get("交易类型"),
		RawRow:           strings.Join(cols, " | "),
	}, nil
}

func isWechatSuccess(status string) bool {
	switch status {
	case "支付成功", "已转账", "已收钱", "对方已收钱", "已存入零钱", "充值完成":
		return true
	}
	return false
}
