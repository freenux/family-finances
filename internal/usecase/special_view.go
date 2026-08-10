package usecase

import (
	"context"
	"fmt"
	"strings"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// SpecialRow 单个专项 + 已花费（净额）/ 执行率 / 跨科目构成
type SpecialRow struct {
	Project domain.SpecialProject
	// SpentFen 净额：挂在专项上的收入（退款、退货返现、卖旧车）抵扣支出。
	// 可能为负（退回的比花掉的多），如实展示，不做 clamp。
	SpentFen  int64
	Ratio     float64                      // 执行率 = 净花费 / 预算；未设预算时为 0，净额为负时为负
	Status    string                       // none|ok|near|over，与预算页同一套口径
	Breakdown []domain.CategoryAggregation // 专项内部跨科目构成，金额降序（净额，可为负）
}

// NetRefunded 净额为负：退款/变卖已经超过投入，模板据此不画进度条、换个说法
func (r SpecialRow) NetRefunded() bool { return r.SpentFen < 0 }

// SpecialViewData /specials 页数据
type SpecialViewData struct {
	Rows           []SpecialRow
	TotalBudgetFen int64
	TotalSpentFen  int64 // 各专项净额之和，同样可能为负
	ActiveCount    int
}

type SpecialView struct {
	repo port.SpecialProjectRepo
}

func NewSpecialView(repo port.SpecialProjectRepo) *SpecialView {
	return &SpecialView{repo: repo}
}

func (uc *SpecialView) Load(ctx context.Context) (SpecialViewData, error) {
	projects, err := uc.repo.ListAll(ctx)
	if err != nil {
		return SpecialViewData{}, err
	}
	spent, err := uc.repo.SumByProject(ctx)
	if err != nil {
		return SpecialViewData{}, err
	}

	data := SpecialViewData{Rows: make([]SpecialRow, 0, len(projects))}
	for _, p := range projects {
		breakdown, err := uc.repo.SumByCategoryForProject(ctx, p.ID)
		if err != nil {
			return SpecialViewData{}, err
		}
		row := SpecialRow{
			Project:   p,
			SpentFen:  spent[p.ID],
			Breakdown: breakdown,
			Status:    budgetStatus(p.BudgetFen, spent[p.ID]),
		}
		// 净额为负时执行率也是负的，如实算出来；模板靠 NetRefunded 跳过进度条，不画反向条
		if p.BudgetFen > 0 {
			row.Ratio = float64(row.SpentFen) / float64(p.BudgetFen)
		}
		data.Rows = append(data.Rows, row)
		data.TotalBudgetFen += p.BudgetFen
		data.TotalSpentFen += row.SpentFen
		if p.IsActive() {
			data.ActiveCount++
		}
	}
	return data, nil
}

// Upsert 新建/保存专项（调用方负责在新建时生成 ID）
func (uc *SpecialView) Upsert(ctx context.Context, p *domain.SpecialProject) error {
	p.Name = strings.TrimSpace(p.Name)
	p.Note = strings.TrimSpace(p.Note)
	if p.Name == "" {
		return fmt.Errorf("请填写专项名称")
	}
	if len([]rune(p.Name)) > 40 {
		return fmt.Errorf("专项名称过长（限 40 字）")
	}
	if p.BudgetFen < 0 {
		return fmt.Errorf("预算不能为负数")
	}
	if !p.StartedOn.IsZero() && !p.EndedOn.IsZero() && p.EndedOn.Before(p.StartedOn) {
		return fmt.Errorf("结束日期不能早于开始日期")
	}
	if p.ID == "" {
		return fmt.Errorf("缺少专项 ID")
	}
	return uc.repo.Upsert(ctx, p)
}

// Delete 删除专项；原本挂在上面的流水会归回日常（不会被连带删除）
func (uc *SpecialView) Delete(ctx context.Context, id string) error {
	return uc.repo.Delete(ctx, id)
}
