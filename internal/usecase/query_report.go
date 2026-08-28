package usecase

import (
	"context"
	"sort"
	"strings"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

type QueryReport struct {
	txRepo      port.TransactionRepo
	catRepo     port.CategoryRepo
	specialRepo port.SpecialProjectRepo // 可选：未注入时 SpecialByProject 为空
}

func NewQueryReport(tx port.TransactionRepo, cat port.CategoryRepo) *QueryReport {
	return &QueryReport{txRepo: tx, catRepo: cat}
}

// WithSpecialRepo 注入专项仓库，用于报表里"其中：专项"的按项目拆行
func (uc *QueryReport) WithSpecialRepo(r port.SpecialProjectRepo) *QueryReport {
	uc.specialRepo = r
	return uc
}

func (uc *QueryReport) Execute(ctx context.Context, p domain.Period, account domain.Account) (domain.ReportData, error) {
	cats, err := uc.catRepo.ListAll(ctx)
	if err != nil {
		return domain.ReportData{}, err
	}
	// 日常与专项各聚合一次：报表既要拆出专项行，又要能和全口径合计对上账。
	dailyAggs, err := uc.txRepo.AggregateByCategory(ctx, p, account, domain.ScopeDaily)
	if err != nil {
		return domain.ReportData{}, err
	}
	specialAggs, err := uc.txRepo.AggregateByCategory(ctx, p, account, domain.ScopeSpecial)
	if err != nil {
		return domain.ReportData{}, err
	}

	dailyMap := make(map[string]int64, len(dailyAggs))
	for _, a := range dailyAggs {
		dailyMap[a.CategoryID] = a.Amount
	}
	specialMap := make(map[string]int64, len(specialAggs))
	for _, a := range specialAggs {
		specialMap[a.CategoryID] = a.Amount
	}
	// 全口径 = 日常 + 专项，保证 IncomeGroups/ExpenseGroups 语义不变
	allMap := make(map[string]int64, len(dailyMap)+len(specialMap))
	for id, amt := range dailyMap {
		allMap[id] = amt
	}
	for id, amt := range specialMap {
		allMap[id] += amt
	}

	income, expense := buildCategoryGroups(cats, allMap)
	dailyIncome, dailyExpense := buildCategoryGroups(cats, dailyMap)
	_, specialExpense := buildCategoryGroups(cats, specialMap)

	data := domain.ReportData{
		Period:             p,
		IncomeGroups:       income,
		ExpenseGroups:      expense,
		DailyIncomeGroups:  dailyIncome,
		DailyExpenseGroups: dailyExpense,
		SpecialGroups:      nonZeroGroups(specialExpense),
		KPI:                computeKPI(income, expense, dailyIncome, dailyExpense),
	}
	if uc.specialRepo != nil {
		byProject, err := uc.specialByProject(ctx, p, account)
		if err != nil {
			return domain.ReportData{}, err
		}
		data.SpecialByProject = byProject
	}
	return data, nil
}

// specialByProject 本期各专项的净花费，按金额降序（同额时按名称、再按 id 定序，
// 保证渲染与 prompt 稳定）。以专项 id 为身份：同名专项不合并。
func (uc *QueryReport) specialByProject(ctx context.Context, p domain.Period, account domain.Account) ([]domain.SpecialAggregation, error) {
	sums, err := uc.specialRepo.SumByProjectInPeriod(ctx, p, account)
	if err != nil {
		return nil, err
	}
	if len(sums) == 0 {
		return nil, nil
	}
	projects, err := uc.specialRepo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	nameByID := make(map[string]string, len(projects))
	for _, sp := range projects {
		nameByID[sp.ID] = sp.Name
	}
	out := make([]domain.SpecialAggregation, 0, len(sums))
	for id, amt := range sums {
		name := nameByID[id]
		if name == "" {
			name = id // 专项已被删除：退回 id，至少还能对上账
		}
		out = append(out, domain.SpecialAggregation{SpecialID: id, Name: name, Amount: amt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Amount != out[j].Amount {
			return out[i].Amount > out[j].Amount
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].SpecialID < out[j].SpecialID
	})
	return out, nil
}

// buildCategoryGroups 把二级科目金额按一级组装成收入/支出两组（组与科目顺序沿用 categories.sort_order）
func buildCategoryGroups(cats []domain.Category, amounts map[string]int64) (income, expense []domain.CategoryGroupAggregation) {
	groupsByID := make(map[string]*domain.CategoryGroupAggregation)
	ordered := make([]*domain.CategoryGroupAggregation, 0)
	for _, c := range cats {
		if c.Level != 1 {
			continue
		}
		g := &domain.CategoryGroupAggregation{
			GroupID: c.ID, GroupName: c.Name, GroupEmoji: c.GroupEmoji,
		}
		groupsByID[c.ID] = g
		ordered = append(ordered, g)
	}
	for _, c := range cats {
		if c.Level != 2 {
			continue
		}
		g, ok := groupsByID[c.ParentID]
		if !ok {
			continue
		}
		amt := amounts[c.ID]
		g.Items = append(g.Items, domain.CategoryAggregation{
			CategoryID: c.ID, Name: c.Name, ParentID: c.ParentID, Amount: amt,
		})
		g.Subtotal += amt
	}

	for _, g := range ordered {
		if strings.HasPrefix(g.GroupID, "income.") {
			income = append(income, *g)
		} else if strings.HasPrefix(g.GroupID, "expense.") {
			expense = append(expense, *g)
		}
	}
	return income, expense
}

// nonZeroGroups 只保留有金额的组与科目（专项拆行时不必列出满屏 0）
func nonZeroGroups(groups []domain.CategoryGroupAggregation) []domain.CategoryGroupAggregation {
	var out []domain.CategoryGroupAggregation
	for _, g := range groups {
		if g.Subtotal == 0 {
			continue
		}
		items := make([]domain.CategoryAggregation, 0, len(g.Items))
		for _, it := range g.Items {
			if it.Amount != 0 {
				items = append(items, it)
			}
		}
		g.Items = items
		out = append(out, g)
	}
	return out
}

// computeKPI 汇总 KPI。income/expense 是全口径分组，dailyIncome/dailyExpense 是剔除专项后的分组。
// 自由裁量占比的分子分母都取日常口径，否则装修落在"购物消费"里会让比值失真甚至超过 1。
// 日常结余同样两侧都取日常口径：换车专项里的旧车折价收入若算进分子，
// 花掉 25 万的那个季度反而会显得更会存钱。
func computeKPI(income, expense, dailyIncome, dailyExpense []domain.CategoryGroupAggregation) domain.ReportKPI {
	var k domain.ReportKPI
	for _, g := range income {
		k.TotalIncome += g.Subtotal
	}
	for _, g := range expense {
		k.TotalExpense += g.Subtotal
	}
	for _, g := range dailyIncome {
		k.DailyIncome += g.Subtotal
	}
	for _, g := range dailyExpense {
		k.DailyExpense += g.Subtotal
	}
	k.SpecialIncome = k.TotalIncome - k.DailyIncome
	k.SpecialExpense = k.TotalExpense - k.DailyExpense

	k.Surplus = k.TotalIncome - k.TotalExpense
	k.DailySurplus = k.DailyIncome - k.DailyExpense
	if k.TotalIncome > 0 {
		k.SurplusRate = float64(k.Surplus) / float64(k.TotalIncome)
	}
	if k.DailyIncome > 0 {
		k.DailySurplusRate = float64(k.DailySurplus) / float64(k.DailyIncome)
	}
	for _, g := range dailyExpense {
		// 分母是日常支出：一次装修把全口径分母从 4 万抬到 18.5 万，
		// 占比被稀释到 5.6%，35% 告警正好在最该响的时候静默关掉。
		if g.GroupID == "expense.discretion" && k.DailyExpense > 0 {
			k.DiscretionRatio = float64(g.Subtotal) / float64(k.DailyExpense)
		}
	}
	k.DiscretionWarning = k.DiscretionRatio > 0.35
	return k
}
