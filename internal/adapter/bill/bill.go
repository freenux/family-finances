// Package bill 负责把微信/支付宝导出的账单文件解析成统一的 domain.RawBillRow 切片。
// 各解析器只负责解析与基础清洗，不承担分类与去重职责。
package bill

import (
	"errors"
	"io"

	"family-finances/internal/domain"
)

// ErrSkipRow 解析器可以用它表示该行应被静默跳过（非业务错误）
var ErrSkipRow = errors.New("skip row")

// Parser 账单解析器接口
type Parser interface {
	Parse(r io.Reader) ([]domain.RawBillRow, error)
	Source() domain.Source
}

// ParserFor 根据 source 返回对应的解析器
func ParserFor(src domain.Source) (Parser, bool) {
	switch src {
	case domain.SourceAlipay:
		return &alipayParser{}, true
	case domain.SourceWechat:
		return &wechatParser{}, true
	}
	return nil, false
}
