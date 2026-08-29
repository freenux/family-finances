package handler

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ---- parseOptionalDate：专项起止日期与保单续期日共用的一份解析 ----
//
// 这段解析原本一份在 specials.go、一份内联在 insurance.go 里（连报错文案的写法都不同）。
// 收敛成一个 helper 后用表驱动把两边的行为一起钉住。

func TestParseOptionalDate(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		label   string
		want    time.Time
		wantErr string
	}{
		{name: "空串 → 零值（未填/进行中）", in: "", label: "开始日期"},
		{name: "只有空白也当作未填", in: "   ", label: "开始日期"},
		{
			name: "正常日期", in: "2026-04-01", label: "开始日期",
			want: time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local),
		},
		{
			name: "首尾空白被 trim", in: "  2026-08-31  ", label: "结束日期",
			want: time.Date(2026, 8, 31, 0, 0, 0, 0, time.Local),
		},
		{name: "斜杠分隔不接受", in: "2026/04/01", label: "开始日期", wantErr: "开始日期格式应为 YYYY-MM-DD"},
		{name: "缺前导零不接受", in: "2026-4-1", label: "结束日期", wantErr: "结束日期格式应为 YYYY-MM-DD"},
		{name: "不存在的日期", in: "2026-02-30", label: "续期日", wantErr: "续期日格式应为 YYYY-MM-DD"},
		{name: "纯文字", in: "明天", label: "续期日", wantErr: "续期日格式应为 YYYY-MM-DD"},
		// 报错文案里的 label 由调用方给，三处各不相同，不能写死
		{name: "label 原样拼进报错", in: "bad", label: "开始日期", wantErr: "开始日期格式应为 YYYY-MM-DD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOptionalDate(tt.in, tt.label)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("error = nil; want %q", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("error = %q; want %q", err.Error(), tt.wantErr)
				}
				if !got.IsZero() {
					t.Fatalf("出错时仍返回了 %v; want 零值", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v; want nil", err)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("= %v; want %v", got, tt.want)
			}
		})
	}
}

// TestInsuranceRenewalDateUsesSharedParser 保单续期日改用共用解析后，
// 行为要与专项日期一致：空串合法（留零值）、格式错给中文报错。
func TestInsuranceRenewalDateUsesSharedParser(t *testing.T) {
	base := url.Values{
		"insured_person":  {"男主"},
		"insurance_type":  {insuranceTypes[0]},
		"company_product": {"某某人寿·重疾"},
	}
	tests := []struct {
		name    string
		date    string
		want    time.Time
		wantErr string
	}{
		{name: "留空 = 没填续期日", date: ""},
		{name: "正常日期", date: "2027-03-15", want: time.Date(2027, 3, 15, 0, 0, 0, 0, time.Local)},
		{name: "格式错 → 中文报错", date: "2027/03/15", wantErr: "续期日格式应为 YYYY-MM-DD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			for k, v := range base {
				form[k] = v
			}
			form.Set("renewal_date", tt.date)
			r := httptest.NewRequest("POST", "/api/insurance", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			p, err := policyFromForm(r)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("error = %v; want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v; want nil", err)
			}
			if !p.RenewalDate.Equal(tt.want) {
				t.Fatalf("RenewalDate = %v; want %v", p.RenewalDate, tt.want)
			}
		})
	}
}
