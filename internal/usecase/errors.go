package usecase

import "errors"

// 哨兵错误：usecase 用它们告诉适配器「这是调用方的错」，由适配器映射状态码。
// 除这几类与 port.ErrNotFound 外的任何错误都视为内部故障（500），其文本绝不回给客户端。
var (
	// ErrInvalidPeriod 周期 label 非法
	ErrInvalidPeriod = errors.New("invalid period")
	// ErrRuleNotApplicable 规则无法批量应用：死规则（指向收入科目）、没有目标科目、或科目不存在
	ErrRuleNotApplicable = errors.New("rule not applicable")
	// ErrInvalidInput 其它入参校验失败（科目不是二级、状态/账户不合法、成员过长、专项不存在、
	// ids 为空或超限……）
	ErrInvalidInput = errors.New("invalid input")
)

// userError 面向用户的业务拒绝：Error() 是给用户看的中文原话，Is 命中对应哨兵。
// 不包装底层错误——这类错误的文本本来就是要展示给用户的。
type userError struct {
	kind error
	msg  string
}

func (e *userError) Error() string        { return e.msg }
func (e *userError) Is(target error) bool { return target == e.kind }

func newUserError(kind error, msg string) error { return &userError{kind: kind, msg: msg} }
