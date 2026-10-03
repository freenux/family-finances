package apiv1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
)

type fakeCats struct {
	list []domain.Category
	err  error
}

func (f fakeCats) ListAll(context.Context) ([]domain.Category, error) { return f.list, f.err }

type fakeSpecials struct {
	list []domain.SpecialProject
	err  error
}

func (f fakeSpecials) ListAll(context.Context) ([]domain.SpecialProject, error) {
	return f.list, f.err
}

func newAPI(cats CategoryLister, sp SpecialLister) http.Handler {
	return New(Deps{Categories: cats, Specials: sp, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var sampleCats = []domain.Category{
	{ID: "expense.discretion", Level: 1, Name: "可选消费", Type: domain.CategoryTypeExpense},
	{ID: "income.salary", Level: 1, Name: "工资", Type: domain.CategoryTypeIncome},
	{ID: "expense.empty", Level: 1, Name: "空分组", Type: domain.CategoryTypeExpense},
	{ID: "expense.discretion.shopping", ParentID: "expense.discretion", Level: 2, Name: "购物", Type: domain.CategoryTypeExpense},
	{ID: "income.salary.base", ParentID: "income.salary", Level: 2, Name: "基本工资", Type: domain.CategoryTypeIncome},
	{ID: "expense.discretion.dining", ParentID: "expense.discretion", Level: 2, Name: "餐饮", Type: domain.CategoryTypeExpense},
	{ID: "orphan.x", ParentID: "missing", Level: 2, Name: "孤儿"},
}

func TestMetaCategories(t *testing.T) {
	rec := get(newAPI(fakeCats{list: sampleCats}, nil), "/meta")
	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var resp struct {
		Data struct {
			Categories []struct {
				GroupID string            `json:"group_id"`
				Type    string            `json:"type"`
				Items   []json.RawMessage `json:"items"`
			} `json:"categories"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	var order []string
	for _, g := range resp.Data.Categories {
		got[g.GroupID] = len(g.Items)
		order = append(order, g.GroupID)
	}
	tests := []struct {
		group string
		want  int
		why   string
	}{
		{"expense.discretion", 2, "两个二级科目都要挂到各自 parent_id 对应的分组下"},
		{"income.salary", 1, "收入科目不能串到支出分组"},
		{"expense.empty", 0, "没有二级科目的分组仍要出现"},
	}
	for _, tt := range tests {
		if got[tt.group] != tt.want {
			t.Errorf("%s items = %d; want %d（%s）", tt.group, got[tt.group], tt.want, tt.why)
		}
	}
	if len(order) != 3 || order[0] != "expense.discretion" {
		t.Errorf("groups = %v; want 3 个一级分组且保持输入顺序（孤儿二级科目被丢弃、不成组）", order)
	}
	// items 必须是 [] 而不是 null，客户端才能直接遍历
	if !strings.Contains(rec.Body.String(), `"group_id":"expense.empty","group_name":"空分组","type":"expense","items":[]`) {
		t.Errorf("body = %s; want 空分组 items 序列化为 []", rec.Body)
	}
}

func TestMetaSpecials(t *testing.T) {
	started := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		sp   SpecialLister
		want string
		why  string
	}{
		{"依赖为 nil", nil, `"specials":[]`, "专项功能未启用要降级成空数组，而不是 null 或 500"},
		{"无专项", fakeSpecials{}, `"specials":[]`, "空列表同样是 []"},
		{"进行中与已结束", fakeSpecials{list: []domain.SpecialProject{
			{ID: "a", Name: "装修", StartedOn: started},
			{ID: "b", Name: "购车", StartedOn: started, EndedOn: started.AddDate(0, 1, 0)},
		}}, `"specials":[{"id":"a","name":"装修","active":true},{"id":"b","name":"购车","active":false}]`, "active 取自 IsActive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(newAPI(fakeCats{}, tt.sp), "/meta")
			if rec.Code != 200 {
				t.Fatalf("status = %d; want 200", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body = %s; want 含 %s（%s）", rec.Body, tt.want, tt.why)
			}
		})
	}
}

func TestMetaEnums(t *testing.T) {
	rec := get(newAPI(fakeCats{}, nil), "/meta")
	for _, want := range []string{
		`{"value":"husband","label":"男主"}`, `{"value":"alipay","label":"支付宝"}`,
		`{"value":"pending_review","label":"待处理"}`, `{"value":"income","label":"收入"}`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body 缺少 %s（枚举中文说法须与现有 UI 一致）", want)
		}
	}
}

func TestErrorEnvelope(t *testing.T) {
	dbErr := errors.New("SQLITE_BUSY: database is locked at /var/data/family.db")
	tests := []struct {
		name     string
		h        http.Handler
		path     string
		method   string
		wantCode int
		wantErr  string
	}{
		{"分类查询失败", newAPI(fakeCats{err: dbErr}, nil), "/meta", "GET", 500, "internal"},
		{"专项查询失败", newAPI(fakeCats{}, fakeSpecials{err: dbErr}), "/meta", "GET", 500, "internal"},
		{"路径不存在", newAPI(fakeCats{}, nil), "/nope", "GET", 404, "not_found"},
		{"方法不允许", newAPI(fakeCats{}, nil), "/meta", "POST", 405, "method_not_allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d; want %d", rec.Code, tt.wantCode)
			}
			var resp struct {
				Error map[string]string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Error["code"] != tt.wantErr || resp.Error["message"] == "" {
				t.Fatalf("error = %v; want code=%s 且 message 非空", resp.Error, tt.wantErr)
			}
			body := rec.Body.String()
			for _, leak := range []string{"SQLITE", "family.db", "locked"} {
				if strings.Contains(body, leak) {
					t.Fatalf("body = %s; 不得泄露数据库错误原文（含 %q）", body, leak)
				}
			}
		})
	}
}

func TestMetaAccountsSplit(t *testing.T) {
	rec := get(newAPI(fakeCats{}, nil), "/meta")
	var resp struct {
		Data struct {
			Accounts     []option `json:"accounts"`
			AccountViews []option `json:"account_views"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	has := func(l []option, v string) bool {
		for _, o := range l {
			if o.Value == v {
				return true
			}
		}
		return false
	}
	if has(resp.Data.Accounts, "family") || !has(resp.Data.Accounts, "husband") || !has(resp.Data.Accounts, "wife") {
		t.Errorf("accounts = %v; want 仅 husband/wife（family 不是存储值，PATCH 必失败）", resp.Data.Accounts)
	}
	if !has(resp.Data.AccountViews, "family") || len(resp.Data.AccountViews) != 3 {
		t.Errorf("account_views = %v; want 含 family 共 3 项", resp.Data.AccountViews)
	}
}
