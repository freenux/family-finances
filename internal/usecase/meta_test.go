package usecase

import (
	"context"
	"reflect"
	"testing"

	"family-finances/internal/domain"
)

func TestGroupCategories(t *testing.T) {
	cats := []domain.Category{
		{ID: "expense.discretion", Level: 1, Name: "可选消费", Type: domain.CategoryTypeExpense},
		{ID: "income.salary", Level: 1, Name: "工资", Type: domain.CategoryTypeIncome},
		{ID: "expense.empty", Level: 1, Name: "空分组", Type: domain.CategoryTypeExpense},
		{ID: "expense.discretion.shopping", ParentID: "expense.discretion", Level: 2, Name: "购物"},
		{ID: "income.salary.base", ParentID: "income.salary", Level: 2, Name: "基本工资"},
		{ID: "expense.discretion.dining", ParentID: "expense.discretion", Level: 2, Name: "餐饮"},
		{ID: "orphan.x", ParentID: "missing", Level: 2, Name: "孤儿"},
	}
	got := GroupCategories(cats)
	type shape struct {
		group string
		typ   string
		items []string
	}
	var gotShape []shape
	for _, g := range got {
		if g.Items == nil {
			t.Errorf("分组 %s 的 Items 为 nil; want 空数组（前端 x-for 不能拿到 null）", g.GroupID)
		}
		var ids []string
		for _, it := range g.Items {
			ids = append(ids, it.ID)
		}
		gotShape = append(gotShape, shape{g.GroupID, g.Type, ids})
	}
	want := []shape{
		{"expense.discretion", "expense", []string{"expense.discretion.shopping", "expense.discretion.dining"}},
		{"income.salary", "income", []string{"income.salary.base"}},
		{"expense.empty", "expense", nil},
	}
	if !reflect.DeepEqual(gotShape, want) {
		t.Errorf("分组形状 = %+v; want %+v（保持输入顺序，孤儿二级科目丢弃，空分组保留）", gotShape, want)
	}
}

func TestMetaExecute(t *testing.T) {
	t.Run("专项未启用：specials 是空数组而非 null", func(t *testing.T) {
		m := NewMeta(&fakeCategoryRepo{cats: testCategories()}, nil, navAt(2025, 8, 15))
		v, err := m.Execute(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if v.Specials == nil || len(v.Specials) != 0 {
			t.Errorf("Specials = %#v; want 非 nil 空切片", v.Specials)
		}
		if got := v.DefaultPeriod["monthly"].Key; got != "2025-07" {
			t.Errorf("default_period[monthly] = %q; want 2025-07", got)
		}
	})
	t.Run("account 与 account_views：family 只出现在视图里", func(t *testing.T) {
		m := NewMeta(&fakeCategoryRepo{cats: testCategories()}, nil, navAt(2025, 8, 15))
		v, _ := m.Execute(context.Background())
		if len(v.Accounts) != 2 || len(v.AccountViews) != 3 {
			t.Errorf("accounts/views = %d/%d; want 2/3（family 不能作为 PATCH 目标值）", len(v.Accounts), len(v.AccountViews))
		}
	})
}
