package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
)

func newCreateUC(t *testing.T) (*CreateTransaction, *fakeTransactionRepo) {
	t.Helper()
	repo := &fakeTransactionRepo{}
	uc := NewCreateTransaction(repo, &fakeCategoryRepo{cats: testCategories()})
	uc.newID = func() string { return "tx-fixed" }
	uc.now = func() time.Time { return time.Date(2025, 8, 15, 12, 0, 0, 0, time.Local) }
	return uc, repo
}

func validInput() NewTransactionInput {
	return NewTransactionInput{
		OccurredAt: "2025-07-03T14:22", Account: "husband", Direction: "expense",
		AmountFen: 1234, CategoryID: "expense.discretion.shopping",
	}
}

func TestCreateTransactionValidation(t *testing.T) {
	tests := []struct {
		name    string
		mod     func(*NewTransactionInput)
		wantMsg string // 空 = 期望成功
	}{
		{"合法输入落库", func(*NewTransactionInput) {}, ""},
		{"日期格式错", func(in *NewTransactionInput) { in.OccurredAt = "昨天" }, "日期时间格式不正确"},
		{"日期为空", func(in *NewTransactionInput) { in.OccurredAt = "" }, "日期时间格式不正确"},
		{"账户为空", func(in *NewTransactionInput) { in.Account = "" }, "请选择账户归属（男主/女主）"},
		{"family 是查询视图，不能写入", func(in *NewTransactionInput) { in.Account = "family" }, "请选择账户归属（男主/女主）"},
		{"方向非法", func(in *NewTransactionInput) { in.Direction = "transfer" }, "请选择收支方向"},
		{"金额 0", func(in *NewTransactionInput) { in.AmountFen = 0 }, "金额必须为正数"},
		{"金额为负", func(in *NewTransactionInput) { in.AmountFen = -1 }, "金额必须为正数"},
		{"金额超上限", func(in *NewTransactionInput) { in.AmountFen = MaxAmountFen + 1 }, "金额超出可接受范围"},
		{"金额恰为上限", func(in *NewTransactionInput) { in.AmountFen = MaxAmountFen }, ""},
		{"科目不存在", func(in *NewTransactionInput) { in.CategoryID = "nope" }, "请选择有效的二级分类"},
		{"一级分组不是合法科目", func(in *NewTransactionInput) { in.CategoryID = "expense.discretion" }, "请选择有效的二级分类"},
		{"不给分类也允许", func(in *NewTransactionInput) { in.CategoryID = "" }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, repo := newCreateUC(t)
			in := validInput()
			tt.mod(&in)
			_, err := uc.Execute(context.Background(), in)
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if len(repo.inserted) != 1 {
					t.Fatalf("inserted = %d; want 1", len(repo.inserted))
				}
				return
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v; want ErrInvalidInput（校验失败必须用既有哨兵，适配器才能映射成 400）", err)
			}
			if err.Error() != tt.wantMsg {
				t.Errorf("message = %q; want %q", err.Error(), tt.wantMsg)
			}
			if len(repo.inserted) != 0 {
				t.Errorf("校验失败却写了 %d 条流水；want 0", len(repo.inserted))
			}
		})
	}
}

func TestCreateTransactionAssembly(t *testing.T) {
	uc, repo := newCreateUC(t)
	in := validInput()
	in.Member, in.Counterparty, in.Description, in.Note = " 孩子 ", " 星巴克 ", " 咖啡 ", " 备注 "
	got, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.inserted) != 1 || repo.inserted[0] != got {
		t.Fatalf("落库的流水与返回值不一致：inserted=%+v got=%+v", repo.inserted, got)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"ID", got.ID, "tx-fixed"},
		{"Source（手填恒为 manual）", got.Source, domain.SourceManual},
		{"Amount（分原样落库，不经任何换算）", got.Amount, int64(1234)},
		{"Status（给了分类 → confirmed）", got.Status, domain.TxStatusConfirmed},
		{"CategoryID", got.CategoryID, "expense.discretion.shopping"},
		{"Account", got.Account, domain.AccountHusband},
		{"Direction", got.Direction, domain.DirectionExpense},
		{"Member 去首尾空白", got.Member, "孩子"},
		{"Counterparty 去首尾空白", got.Counterparty, "星巴克"},
		{"Description 去首尾空白", got.Description, "咖啡"},
		{"Note 去首尾空白", got.Note, "备注"},
		{"OccurredAt", got.OccurredAt, time.Date(2025, 7, 3, 14, 22, 0, 0, time.Local)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v; want %v", c.name, c.got, c.want)
		}
	}
}

func TestCreateTransactionInsertFailureIsNotInputError(t *testing.T) {
	uc, repo := newCreateUC(t)
	repo.insertErr = errors.New("SQLITE_IOERR SECRET")
	_, err := uc.Execute(context.Background(), validInput())
	if err == nil || errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v; want 非 ErrInvalidInput 的内部故障（否则 DB 故障会被映射成 400 并外泄文本）", err)
	}
}

func TestParseOccurredAt(t *testing.T) {
	loc := time.Local
	tests := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"2025-07-03T14:22", time.Date(2025, 7, 3, 14, 22, 0, 0, loc), false},
		{"2025-07-03 14:22", time.Date(2025, 7, 3, 14, 22, 0, 0, loc), false},
		{"2025-07-03T14:22:00+08:00", time.Date(2025, 7, 3, 6, 22, 0, 0, time.UTC), false},
		{" 2025-07-03 14:22 ", time.Date(2025, 7, 3, 14, 22, 0, 0, loc), false},
		{"2025-07-03", time.Time{}, true},
		{"", time.Time{}, true},
	}
	for _, tt := range tests {
		got, err := ParseOccurredAt(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseOccurredAt(%q) err = %v; wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && !got.Equal(tt.want) {
			t.Errorf("ParseOccurredAt(%q) = %v; want %v（同一时刻）", tt.in, got, tt.want)
		}
	}
}

func TestParseYuanToFen(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantMsg string
	}{
		{"12.34", 1234, ""},
		{"100", 10000, ""},
		{" 5.5 ", 550, ""},
		{"0.01", 1, ""},
		{".5", 50, ""},
		{"5.", 500, ""},
		{"+5", 500, ""},
		{"19.999", 2000, "第三位小数四舍五入，与账单解析器 +0.5 同向"},
		{"0.004", 0, "金额必须为正数"},
		{"0.005", 1, ""},
		{"19.994", 1999, ""},
		// 浮点会算错的值：0.29*100 = 28.999999999999996，必须仍是 29
		{"0.29", 29, ""},
		{"1.15", 115, ""},
		{"0", 0, "金额必须为正数"},
		{"-3", 0, "金额必须为正数"},
		{"-0", 0, "金额必须为正数"},
		{"NaN", 0, "金额格式不正确"},
		{"Inf", 0, "金额格式不正确"},
		{"-Inf", 0, "金额格式不正确"},
		{"1e3", 0, "金额格式不正确"},
		{"1,234.5", 0, "金额格式不正确"},
		{"1.2.3", 0, "金额格式不正确"},
		{"abc", 0, "金额格式不正确"},
		{"", 0, "金额格式不正确"},
		{".", 0, "金额格式不正确"},
		{"10000000000", 1_000_000_000_000, ""},
		{"10000000000.01", 0, "金额超出可接受范围"},
		{"99999999999999999999", 0, "金额超出可接受范围"},
	}
	for _, tt := range tests {
		got, err := ParseYuanToFen(tt.in)
		if tt.wantMsg == "" || tt.want != 0 {
			if err != nil {
				t.Errorf("ParseYuanToFen(%q) err = %v; want %d", tt.in, err, tt.want)
			} else if got != tt.want {
				t.Errorf("ParseYuanToFen(%q) = %d; want %d", tt.in, got, tt.want)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidInput) || err.Error() != tt.wantMsg {
			t.Errorf("ParseYuanToFen(%q) err = %v; want ErrInvalidInput %q", tt.in, err, tt.wantMsg)
		}
	}
}

func TestResolveAmountFen(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	tests := []struct {
		name    string
		fen     *int64
		yuan    string
		want    int64
		wantMsg string
	}{
		{"只给 yuan", nil, "12.34", 1234, ""},
		{"只给 fen", i64(1234), "", 1234, ""},
		{"yuan 与 fen 换算结果相同", nil, "12.34", 1234, ""},
		{"两个都给：不猜优先级，报错", i64(1234), "12.34", 0, "金额只能给 amount_fen 或 amount_yuan 其中一个"},
		{"两个都给且数值冲突也是同一个错", i64(1), "99", 0, "金额只能给 amount_fen 或 amount_yuan 其中一个"},
		{"都不给", nil, "", 0, "请提供金额（amount_fen 或 amount_yuan）"},
		{"yuan 仅空白等同没给", nil, "  ", 0, "请提供金额（amount_fen 或 amount_yuan）"},
		{"amount_fen=0 是「给了」，原样交给 Execute 报必须为正", i64(0), "", 0, ""},
		{"yuan 格式错走 ParseYuanToFen 的报错", nil, "1e3", 0, "金额格式不正确"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAmountFen(tt.fen, tt.yuan)
			if tt.wantMsg != "" {
				if !errors.Is(err, ErrInvalidInput) || err.Error() != tt.wantMsg {
					t.Fatalf("err = %v; want ErrInvalidInput %q", err, tt.wantMsg)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestCreateTransactionOccurredAtDefault(t *testing.T) {
	now := time.Date(2025, 8, 15, 12, 0, 0, 0, time.Local)
	tests := []struct {
		name    string
		allow   bool
		at      string
		want    time.Time
		wantMsg string
	}{
		{"允许缺省且为空 → 服务器当前时间", true, "", now, ""},
		{"允许缺省但只有空白也算空", true, "  ", now, ""},
		{"允许缺省但给了值 → 按值", true, "2025-07-03 14:22", time.Date(2025, 7, 3, 14, 22, 0, 0, time.Local), ""},
		{"允许缺省但值非法 → 仍报错，不静默改成现在", true, "昨天", time.Time{}, "日期时间格式不正确"},
		{"不允许缺省（SSR 表单）空串仍报错", false, "", time.Time{}, "日期时间格式不正确"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, _ := newCreateUC(t)
			uc.WithClock(func() time.Time { return now })
			in := validInput()
			in.OccurredAt, in.AllowEmptyOccurredAt = tt.at, tt.allow
			got, err := uc.Execute(context.Background(), in)
			if tt.wantMsg != "" {
				if !errors.Is(err, ErrInvalidInput) || err.Error() != tt.wantMsg {
					t.Fatalf("err = %v; want %q", err, tt.wantMsg)
				}
				return
			}
			if err != nil || !got.OccurredAt.Equal(tt.want) {
				t.Errorf("OccurredAt = %v, %v; want %v", got.OccurredAt, err, tt.want)
			}
		})
	}
}

func TestMemberLimitSharedByCreateAndUpdate(t *testing.T) {
	long := strings.Repeat("字", maxMemberRunes+1)
	ok := strings.Repeat("字", maxMemberRunes)
	uc, _ := newCreateUC(t)
	for _, tt := range []struct {
		name, member string
		wantErr      bool
	}{
		{"恰好 20 字通过", ok, false},
		{"21 字被拒（与 PATCH 同一上限）", long, true},
		{"首尾空白不计入长度", " " + ok + " ", false},
	} {
		in := validInput()
		in.Member = tt.member
		_, err := uc.Execute(context.Background(), in)
		if (err != nil) != tt.wantErr || (err != nil && !errors.Is(err, ErrInvalidInput)) {
			t.Errorf("%s: err = %v; wantErr %v", tt.name, err, tt.wantErr)
		}
	}
	// PATCH 侧同一常量同一判定
	up := NewUpdateTransaction(&fakeTransactionRepo{}, &fakeCategoryRepo{}, nil)
	if err := up.Update(context.Background(), "x", TxPatch{Member: &long}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("PATCH 21 字 err = %v; want ErrInvalidInput", err)
	}
}
