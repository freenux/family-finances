package usecase

import (
	"strings"

	"family-finances/internal/domain"
)

// 本地规则分类：Alipay 走 platform_category 精确映射；Wechat 走交易对方/商品关键词。
// 规则未命中返回 "", false，让调用方入库为 NULL，后续 LLM 兜底或人工。

// alipayCategoryMap 支付宝的"交易分类"字段 → 我们的二级科目 ID
var alipayCategoryMap = map[string]string{
	"餐饮美食": "expense.necessary.food",
	"食品酒水": "expense.necessary.food",
	"超市便利": "expense.necessary.home",
	"日用百货": "expense.necessary.home",
	"数码电器": "expense.discretion.shopping",
	"服饰装扮": "expense.discretion.shopping",
	"美容美发": "expense.discretion.shopping",
	"运动户外": "expense.discretion.leisure",
	"文化休闲": "expense.discretion.leisure",
	"娱乐休闲": "expense.discretion.leisure",
	"交通出行": "expense.necessary.transport",
	"爱车养车": "expense.necessary.transport", // ETC、加油、保养
	"酒店住宿": "expense.discretion.travel",
	"旅游出行": "expense.discretion.travel",
	"医疗健康": "expense.necessary.medical",
	"教育培训": "expense.fixed.education",
	"母婴亲子": "expense.family.child_growth",
	"生活服务": "expense.necessary.home",
	"家居家装": "expense.family.home_maintenance",
	"通讯物流": "expense.necessary.home",
	"公共服务": "expense.fixed.housing",
	"保险":   "expense.fixed.insurance",
	"金融服务": "expense.fixed.insurance",
	"亲友代付": "", // 跳过：通常是转账
	"转账红包": "",
	"转账":   "",
	"信用借还": "",
	"投资理财": "",
}

// wechatKeywordRule 微信按关键词匹配；按优先级（越靠前越优先）。
// 每条命中 Counterparty 或 Description 任意子串即可。
type wechatKeywordRule struct {
	keywords   []string
	categoryID string
}

var wechatKeywordRules = []wechatKeywordRule{
	// 交通
	{[]string{"深圳通", "地铁", "公交", "出行", "哈啰", "摩拜", "滴滴", "高速", "通行费", "加油", "停车", "ETC"}, "expense.necessary.transport"},
	{[]string{"12306", "铁路", "航空", "机票", "高铁"}, "expense.discretion.travel"},
	// 餐饮（长辈买菜/做饭代付）
	{[]string{"伊一奶奶", "六六爷爷"}, "expense.necessary.food"},
	// 餐饮
	{[]string{
		"餐饮", "美食", "食品", "咖啡", "coffee", "luckin", "cotti", "星巴克",
		"茶饮", "茶", "奶茶", "奈雪", "喜茶",
		"火锅", "烤肉", "快餐", "胡辣汤", "拌饭", "汤包", "米线", "面馆", "饺子", "烧烤",
		"牛肉面", "粑粑", "锅盔", "私房", "味道", "小女当家", "阿甘", "张家", "唐穆",
		"百果园", "果园", "水果", "蛋糕", "烘焙", "面包",
		"美团", // 默认归餐饮；不是外卖的可由用户改
		"送水", "售卖机",
	}, "expense.necessary.food"},
	// 超市/日用
	{[]string{"超市", "便利", "盒马", "美宜多", "永辉", "沃尔玛", "百货", "天虹", "壹方汇"}, "expense.necessary.home"},
	// 医疗
	{[]string{"医院", "药房", "药店", "诊所", "体检"}, "expense.necessary.medical"},
	// 教育/子女
	{[]string{"幼儿园", "培训", "教育", "学校", "早教", "兴趣班"}, "expense.fixed.education"},
	// 居住
	{[]string{"物业", "水费", "电费", "燃气", "宽带", "房租"}, "expense.fixed.housing"},
	// 自由裁量 - 个人消费
	{[]string{"理发", "美容", "美发", "spa"}, "expense.discretion.shopping"},
	// 自由裁量 - 娱乐
	{[]string{
		"电影", "影院", "KTV", "演出", "游乐", "游戏", "Steam",
		"运动", "球馆", "健身", "乐刻", "体育",
	}, "expense.discretion.leisure"},
	// 自由裁量 - 网购
	{[]string{"淘宝", "天猫", "京东", "拼多多", "唯品会", "apple.com", "app store", "itunes"}, "expense.discretion.shopping"},
	// 通讯/订阅
	{[]string{"腾讯云", "阿里云", "话费", "流量"}, "expense.necessary.home"},
}

// wechatPlatformMap 微信"交易类型"字段兜底。
// 亲属卡交易：你为家人花钱、从自己账户扣；需要人工区分子女/父母/配偶，
// 所以这里不做跳过、也不预填分类，保持未分类待处理，由用户在列表页下拉选。
var wechatPlatformMap = map[string]string{
	"零钱提现":  "",
	"信用卡还款": "",
	"理财通转入": "",
	"理财通转出": "",
	"零钱通存入": "",
	"零钱通取出": "",
	"充值":    "",
	"提现":    "",
}

// ClassifyByRules 根据本地规则返回二级科目 ID。
// 返回 (categoryID, skip, matched)：
//   - matched=true, categoryID!=""：命中具体科目
//   - matched=true, categoryID=="": 命中"应跳过"规则（例如转账/中性交易）
//   - matched=false：未命中，交由调用方决定（入库为 NULL 等 LLM/人工）
func ClassifyByRules(row domain.RawBillRow) (categoryID string, skip, matched bool) {
	// 收入类暂不自动分类，让用户自己挑（工资/租金往往需要区分人）
	if row.Direction == domain.DirectionIncome {
		return "", false, false
	}

	switch row.Source {
	case domain.SourceAlipay:
		if cat, ok := alipayCategoryMap[row.PlatformCategory]; ok {
			return cat, cat == "", true
		}
	case domain.SourceWechat:
		if cat, ok := wechatPlatformMap[row.PlatformCategory]; ok {
			return cat, cat == "", true
		}
		hay := strings.ToLower(row.Counterparty + " " + row.Description)
		for _, rule := range wechatKeywordRules {
			for _, kw := range rule.keywords {
				if strings.Contains(hay, strings.ToLower(kw)) {
					return rule.categoryID, rule.categoryID == "", true
				}
			}
		}
	}
	return "", false, false
}

func ClassifyByCustomRules(row domain.RawBillRow, rules []domain.CategoryRule) (categoryID string, matched bool) {
	for _, rule := range rules {
		if !rule.IsActive || rule.CategoryID == "" || rule.Pattern == "" {
			continue
		}
		if ruleMatches(row, rule) {
			return rule.CategoryID, true
		}
	}
	return "", false
}

func ruleMatches(row domain.RawBillRow, rule domain.CategoryRule) bool {
	pattern := strings.ToLower(strings.TrimSpace(rule.Pattern))
	if pattern == "" {
		return false
	}
	for _, value := range ruleFieldValues(row, rule.Field) {
		value = strings.ToLower(value)
		switch rule.PatternType {
		case "exact":
			if value == pattern {
				return true
			}
		default:
			if strings.Contains(value, pattern) {
				return true
			}
		}
	}
	return false
}

func ruleFieldValues(row domain.RawBillRow, field string) []string {
	switch field {
	case "counterparty":
		return []string{row.Counterparty}
	case "description":
		return []string{row.Description}
	case "platform_category":
		return []string{row.PlatformCategory}
	default:
		return []string{row.Counterparty, row.Description, row.PlatformCategory}
	}
}
