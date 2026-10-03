package usecase

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

func newTestTxQuery(t *testing.T) (*TxQuery, *fakeTransactionRepo, *fakeCategoryRuleRepo) {
	t.Helper()
	tx := &fakeTransactionRepo{}
	rules := &fakeCategoryRuleRepo{rules: []domain.CategoryRule{
		{ID: "r-sbux", Pattern: "星巴克", PatternType: "contains", Field: "counterparty", CategoryID: "expense.discretion.shopping"},
		{ID: "r-skip", Pattern: "提现", Field: "any"},
		{ID: "r-salary", Pattern: "工资", PatternType: "exact", Field: "description", CategoryID: "income.salary.husband"},
		{ID: "r-ghost", Pattern: "x", CategoryID: "no.such.category"},
	}}
	q := NewTxQuery(tx, &fakeCategoryRepo{cats: testCategories()}, rules).
		WithSpecialRepo(&fakeSpecialProjectRepo{projects: []domain.SpecialProject{{ID: "sp1", Name: "装修"}}}).
		WithNav(navAt(2025, 8, 15))
	return q, tx, rules
}

func TestTxQueryNormalizesParams(t *testing.T) {
	tests := []struct {
		name  string
		req   TxQueryRequest
		check func(t *testing.T, q port.TransactionQuery, res TxQueryResult)
	}{
		{"status 缺省不含 excluded", TxQueryRequest{}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if want := []string{"pending_review", "confirmed"}; !reflect.DeepEqual(q.Statuses, want) {
				t.Errorf("Statuses = %v; want %v（excluded 必须显式要求）", q.Statuses, want)
			}
		}},
		{"显式 excluded 才出现", TxQueryRequest{Status: "excluded"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if want := []string{"excluded"}; !reflect.DeepEqual(q.Statuses, want) {
				t.Errorf("Statuses = %v; want %v", q.Statuses, want)
			}
		}},
		{"status 含非法值：丢弃非法项", TxQueryRequest{Status: "confirmed, bogus ,excluded"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if want := []string{"confirmed", "excluded"}; !reflect.DeepEqual(q.Statuses, want) {
				t.Errorf("Statuses = %v; want %v", q.Statuses, want)
			}
		}},
		{"status 全是非法值：回到缺省", TxQueryRequest{Status: "bogus"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if len(q.Statuses) != 2 {
				t.Errorf("Statuses = %v; want 缺省两项", q.Statuses)
			}
		}},
		{"source 缺省全选 = 不过滤", TxQueryRequest{}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if len(q.Sources) != 0 {
				t.Errorf("Sources = %v; want 空（不过滤）", q.Sources)
			}
		}},
		{"source 逗号分隔并 trim", TxQueryRequest{Source: "alipay, csv,,"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if want := []string{"alipay", "csv"}; !reflect.DeepEqual(q.Sources, want) {
				t.Errorf("Sources = %v; want %v", q.Sources, want)
			}
		}},
		{"page_size=9999 钳到 200", TxQueryRequest{PageSize: "9999"}, func(t *testing.T, q port.TransactionQuery, res TxQueryResult) {
			if q.Limit != 200 || res.Page.PageSize != 200 {
				t.Errorf("Limit/PageSize = %d/%d; want 200/200（超出钳到上限而不是报错）", q.Limit, res.Page.PageSize)
			}
		}},
		{"page_size 缺省 50", TxQueryRequest{}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Limit != 50 {
				t.Errorf("Limit = %d; want 50", q.Limit)
			}
		}},
		{"page_size 非数字/非正数 回到 50", TxQueryRequest{PageSize: "-3"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Limit != 50 {
				t.Errorf("Limit = %d; want 50", q.Limit)
			}
		}},
		{"page 最小 1", TxQueryRequest{Page: "0"}, func(t *testing.T, q port.TransactionQuery, res TxQueryResult) {
			if res.Page.Page != 1 || q.Offset != 0 {
				t.Errorf("page/offset = %d/%d; want 1/0", res.Page.Page, q.Offset)
			}
		}},
		{"page=3,size=20 → offset 40", TxQueryRequest{Page: "3", PageSize: "20"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Offset != 40 || q.Limit != 20 {
				t.Errorf("offset/limit = %d/%d; want 40/20", q.Offset, q.Limit)
			}
		}},
		{"sort 非法回落 occurred_at desc", TxQueryRequest{Sort: "id; DROP TABLE x"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.SortBy != "occurred_at" || !q.SortDesc {
				t.Errorf("SortBy/Desc = %s/%v; want occurred_at/true", q.SortBy, q.SortDesc)
			}
		}},
		{"sort=category 映射到列 category_id", TxQueryRequest{Sort: "category", Order: "asc"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.SortBy != "category_id" || q.SortDesc {
				t.Errorf("SortBy/Desc = %s/%v; want category_id/false", q.SortBy, q.SortDesc)
			}
		}},
		{"sort 白名单全部能映射（含 source / direction）", TxQueryRequest{}, func(t *testing.T, _ port.TransactionQuery, _ TxQueryResult) {
			for _, k := range []string{"occurred_at", "amount", "counterparty", "category", "special", "status", "member", "account", "source", "direction"} {
				if _, ok := txSortKeys[k]; !ok {
					t.Errorf("白名单缺少 %s", k)
				}
			}
		}},
		{"sort=source/direction 透传到 repo", TxQueryRequest{Sort: "direction"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.SortBy != "direction" {
				t.Errorf("SortBy = %s; want direction", q.SortBy)
			}
		}},
		{"order 缺省 desc，asc 才升序", TxQueryRequest{Sort: "amount", Order: "ASC"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.SortDesc {
				t.Errorf("SortDesc = true; want false")
			}
		}},
		{"direction 非法 = 不过滤", TxQueryRequest{Direction: "all"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Direction != "" {
				t.Errorf("Direction = %q; want 空", q.Direction)
			}
		}},
		{"direction=income 透传", TxQueryRequest{Direction: "income"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Direction != "income" {
				t.Errorf("Direction = %q; want income", q.Direction)
			}
		}},
		{"account 非法回落 family", TxQueryRequest{Account: "nobody"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Account != domain.AccountFamily {
				t.Errorf("Account = %q; want family", q.Account)
			}
		}},
		{"special 三态原样透传", TxQueryRequest{Special: "__any__"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Special != port.FilterAny {
				t.Errorf("Special = %q; want __any__", q.Special)
			}
		}},
		{"周期缺省 = 上一个完整季度", TxQueryRequest{}, func(t *testing.T, q port.TransactionQuery, res TxQueryResult) {
			if q.Period.Label != "2025Q2" || res.Period.Key != "2025Q2" {
				t.Errorf("period = %s/%s; want 2025Q2", q.Period.Label, res.Period.Key)
			}
		}},
		{"type=monthly 无 period", TxQueryRequest{Type: "monthly"}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Period.Label != "2025-07" {
				t.Errorf("period = %s; want 2025-07", q.Period.Label)
			}
		}},
		{"keyword trim", TxQueryRequest{Keyword: "  咖啡 "}, func(t *testing.T, q port.TransactionQuery, _ TxQueryResult) {
			if q.Keyword != "咖啡" {
				t.Errorf("Keyword = %q", q.Keyword)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, repo, _ := newTestTxQuery(t)
			res, err := q.Execute(context.Background(), tt.req)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, repo.lastQuery, res)
		})
	}
}

func TestTxQueryBadPeriodIsError(t *testing.T) {
	q, _, _ := newTestTxQuery(t)
	if _, err := q.Execute(context.Background(), TxQueryRequest{Period: "2025Q9"}); err == nil {
		t.Error("非法 period 应返回错误")
	}
}

func TestTxQueryRuleView(t *testing.T) {
	t.Run("rule_id 且无 type/period：当前季度，并带规则预筛", func(t *testing.T) {
		q, repo, _ := newTestTxQuery(t)
		res, err := q.Execute(context.Background(), TxQueryRequest{RuleID: "r-sbux"})
		if err != nil {
			t.Fatal(err)
		}
		if repo.lastQuery.Period.Label != "2025Q3" {
			t.Errorf("period = %s; want 2025Q3（规则预览用当前季度）", repo.lastQuery.Period.Label)
		}
		if repo.lastQuery.Rule == nil || repo.lastQuery.Rule.ID != "r-sbux" {
			t.Errorf("Rule 未下传: %+v", repo.lastQuery.Rule)
		}
		want := &RuleView{ID: "r-sbux", Label: "交易对方 包含「星巴克」", CategoryID: "expense.discretion.shopping", CategoryName: "购物消费"}
		if !reflect.DeepEqual(res.Rule, want) {
			t.Errorf("Rule = %+v; want %+v", res.Rule, want)
		}
	})
	t.Run("显式给了 type+period 以显式为准（只给 period 而与默认 type 对不上会退回默认周期）", func(t *testing.T) {
		q, repo, _ := newTestTxQuery(t)
		if _, err := q.Execute(context.Background(), TxQueryRequest{RuleID: "r-sbux", Type: "annual", Period: "2024"}); err != nil {
			t.Fatal(err)
		}
		if repo.lastQuery.Period.Label != "2024" {
			t.Errorf("period = %s; want 2024", repo.lastQuery.Period.Label)
		}
	})
	t.Run("规则不存在报错", func(t *testing.T) {
		q, _, _ := newTestTxQuery(t)
		if _, err := q.Execute(context.Background(), TxQueryRequest{RuleID: "nope"}); err == nil {
			t.Error("want error")
		}
	})
}

func TestRuleLabel(t *testing.T) {
	tests := []struct {
		rule domain.CategoryRule
		want string
	}{
		{domain.CategoryRule{Field: "counterparty", PatternType: "contains", Pattern: "星巴克"}, "交易对方 包含「星巴克」"},
		{domain.CategoryRule{Field: "description", PatternType: "exact", Pattern: "工资"}, "商品说明 等于「工资」"},
		{domain.CategoryRule{Field: "platform_category", Pattern: "餐饮"}, "平台分类 包含「餐饮」"},
		{domain.CategoryRule{Field: "any", Pattern: "x"}, "任意字段 包含「x」"},
		{domain.CategoryRule{Field: "weird", Pattern: "x"}, "任意字段 包含「x」"},
	}
	for _, tt := range tests {
		if got := RuleLabel(tt.rule); got != tt.want {
			t.Errorf("RuleLabel(%+v) = %q; want %q", tt.rule, got, tt.want)
		}
	}
}

func TestTxQueryTotalsAndPageInfoComeFromRepo(t *testing.T) {
	q, repo, _ := newTestTxQuery(t)
	repo.queryPage = port.TransactionPage{
		Rows:   []domain.Transaction{{ID: "x"}},
		Total:  312,
		Totals: port.TransactionTotals{IncomeFen: 500000, ExpenseFen: 123450},
	}
	res, err := q.Execute(context.Background(), TxQueryRequest{PageSize: "50"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Page.Total != 312 || res.Page.TotalPages != 7 {
		t.Errorf("page = %+v; want total=312 total_pages=7", res.Page)
	}
	want := TotalsView{IncomeFen: 500000, IncomeText: "5,000.00", ExpenseFen: 123450, ExpenseText: "1,234.50", NetFen: 376550, NetText: "3,765.50"}
	if res.Totals != want {
		t.Errorf("Totals = %+v; want %+v（直接取 repo 的全集合计，不对当页求和）", res.Totals, want)
	}
}

func TestTxQueryFacetsMembers(t *testing.T) {
	q, repo, _ := newTestTxQuery(t)
	repo.members = []string{"李四", "张三"}
	var first []FacetOption
	for _, page := range []string{"1", "2", "5"} {
		res, err := q.Execute(context.Background(), TxQueryRequest{Page: page, PageSize: "10", Keyword: "x", Status: "excluded"})
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = res.Facets.Members
		}
		if !reflect.DeepEqual(res.Facets.Members, first) {
			t.Errorf("page %s members = %v; want 与第 1 页相同（不随分页/筛选变化）", page, res.Facets.Members)
		}
	}
	want := []FacetOption{{"__none__", "未标注"}, {"李四", "李四"}, {"张三", "张三"}}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("members = %v; want %v（最前面固定一项「未标注」）", first, want)
	}
}

func TestTxQueryRowDTO(t *testing.T) {
	q, repo, _ := newTestTxQuery(t)
	at := time.Date(2025, 7, 3, 14, 22, 5, 0, time.Local)
	repo.queryPage = port.TransactionPage{Rows: []domain.Transaction{
		{ID: "1", OccurredAt: at, Source: "csv:招商", Account: domain.AccountWife, Amount: 123450,
			Direction: domain.DirectionExpense, Status: domain.TxStatusPendingReview,
			CategoryID: "expense.discretion.shopping", SpecialID: "sp1", Counterparty: "店", Note: "n", Member: "张三"},
		{ID: "2", OccurredAt: at, Source: domain.SourceAlipay, Account: domain.AccountHusband, Amount: -5,
			Direction: domain.DirectionIncome, Status: domain.TxStatusConfirmed},
	}}
	res, err := q.Execute(context.Background(), TxQueryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	r1, r2 := res.Rows[0], res.Rows[1]
	checks := []struct{ name, got, want string }{
		{"occurred_text", r1.OccurredText, "2025-07-03 14:22"},
		{"occurred_at", r1.OccurredAt, at.Format(time.RFC3339)},
		{"amount_text", r1.AmountText, "1,234.50"},
		{"source_text csv", r1.SourceText, "CSV·招商"},
		{"account_text", r1.AccountText, "女主"},
		{"direction_text", r1.DirectionText, "支出"},
		{"status_text", r1.StatusText, "待处理"},
		{"category_text", r1.CategoryText, "购物消费"},
		{"special_text", r1.SpecialText, "装修"},
		{"source_text alipay", r2.SourceText, "支付宝"},
		{"direction_text income", r2.DirectionText, "收入"},
		{"status_text confirmed", r2.StatusText, "已确认"},
		{"无分类时 category_text 为空", r2.CategoryText, ""},
		{"无专项时 special_text 为空", r2.SpecialText, ""},
		{"负金额", r2.AmountText, "-0.05"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q; want %q", c.name, c.got, c.want)
		}
	}
	if r1.AmountFen != 123450 || r1.Member != "张三" || r1.Note != "n" {
		t.Errorf("原始字段丢失: %+v", r1)
	}
}

func TestFormatYuan(t *testing.T) {
	tests := []struct {
		fen  int64
		want string
	}{
		{0, "0.00"}, {5, "0.05"}, {100, "1.00"}, {99999, "999.99"}, {100000, "1,000.00"},
		{123450, "1,234.50"}, {123456789, "1,234,567.89"}, {-5, "-0.05"}, {-123450, "-1,234.50"},
		{100000000000, "1,000,000,000.00"},
	}
	for _, tt := range tests {
		if got := FormatYuan(tt.fen); got != tt.want {
			t.Errorf("FormatYuan(%d) = %q; want %q（千分位+两位小数，负号在最前）", tt.fen, got, tt.want)
		}
	}
}

func TestTxQueryApplyRule(t *testing.T) {
	p, _ := domain.ParsePeriod("2025Q3")
	tests := []struct {
		name      string
		ruleID    string
		wantN     int
		wantErr   string
		wantCalls int
		wantDir   string
	}{
		{"支出科目规则只改支出方向", "r-sbux", 7, "", 1, "expense"},
		{"收入科目规则是死规则：报错且不碰库", "r-salary", 0, "死规则", 0, ""},
		{"跳过导入类规则：返回 0 和说明性错误，不碰库", "r-skip", 0, "没有目标科目", 0, ""},
		{"规则不存在", "nope", 0, "not found", 0, ""},
		{"目标科目不存在", "r-ghost", 0, "不存在", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, repo, _ := newTestTxQuery(t)
			repo.applyResult = 7
			n, err := q.ApplyRule(context.Background(), tt.ruleID, p, domain.AccountFamily)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v; want 含 %q", err, tt.wantErr)
			}
			if tt.wantErr != "" {
				n = 0
			}
			if n != tt.wantN || len(repo.applyCalls) != tt.wantCalls {
				t.Errorf("n/calls = %d/%d; want %d/%d", n, len(repo.applyCalls), tt.wantN, tt.wantCalls)
			}
			if tt.wantCalls == 1 && repo.applyCalls[0].direction != tt.wantDir {
				t.Errorf("direction = %q; want %q", repo.applyCalls[0].direction, tt.wantDir)
			}
		})
	}
}

func TestTxQueryRulePrefilterForcesExpense(t *testing.T) {
	for _, dir := range []string{"", "income", "expense"} {
		q, repo, _ := newTestTxQuery(t)
		if _, err := q.Execute(context.Background(), TxQueryRequest{RuleID: "r-sbux", Direction: dir}); err != nil {
			t.Fatal(err)
		}
		if repo.lastQuery.Direction != "expense" {
			t.Errorf("请求 direction=%q 时预筛 Direction = %q; want expense（分类器对收入直接 return，覆盖请求值）", dir, repo.lastQuery.Direction)
		}
	}
	q, repo, _ := newTestTxQuery(t)
	if _, err := q.Execute(context.Background(), TxQueryRequest{Direction: "income"}); err != nil {
		t.Fatal(err)
	}
	if repo.lastQuery.Direction != "income" {
		t.Errorf("无规则时 Direction = %q; want 原样 income", repo.lastQuery.Direction)
	}
}
