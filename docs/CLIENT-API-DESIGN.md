# 多端薄客户端改造设计（唯一契约）

目标：把现在散在前端 JS 里的业务逻辑搬回服务端，对外只留一套平台无关的 JSON API；
客户端做成薄壳，依次支持 **手机浏览器** → **微信小程序**，PC 端代码尽量复用。

本文件是实现的唯一依据。与本文件冲突的既有代码按本文件改；本文件没写到的沿用 `CLAUDE.md`。

---

## 0. 为什么要搬

当前 `static/js/` 1372 行里，有五类**业务逻辑**而不是展示逻辑，每多一个端就要重写一遍：

| 逻辑 | 现在在哪 | 问题 |
|---|---|---|
| 筛选（方向/来源/状态/分类/专项/成员/关键词） | `tx_table.js` `get filtered` | 依赖「整期流水全在手」，移动端无法分页 |
| 排序（含分类名/专项名按中文 collate） | `tx_table.js` `get filtered` | 同上 |
| 底部汇总 收入/支出/净额 | `tx_table.js` `get totals` | 同上 |
| 默认周期 + 上下期进位 | `period_utils.js` | 与后端 `defaultPeriodFor` 两份实现，`CLAUDE.md` 已明文警告必须同步改 |
| 规则匹配预览 | `tx_table.js` `matchesRule` | 与 Go `ruleMatches` 两份实现，**且语义已经不一致**（见 §6） |

还有展示层重复：`fmtYuan`、`sourceLabel`、`ruleLabel`、`catNameById` 查表。每端重写一遍。

### 薄客户端第一原则

**服务端返回可直接渲染的字符串，客户端不做任何推导。**

客户端**不允许**再实现：金额格式化、枚举中文翻译、分类/专项名查表、周期加减、
排序、筛选、合计、规则匹配、分组装配。凡是需要这些的地方，服务端已经给好了。

---

## 1. 分层落点

```
internal/domain/period.go              + Next() / Shift(n)              ← 周期进位的唯一实现
internal/domain/rule_match.go   新增   RuleMatchFields / RuleMatches     ← 规则匹配的唯一实现
internal/port/transaction.go           + TransactionQuery / TransactionPage
internal/infrastructure/sqlite/        + QueryTransactions / DistinctMembers（筛选排序分页下推 SQL）
internal/usecase/tx_query.go    新增   TxQuery：参数归一 + 合计 + facets + DTO 装配
internal/usecase/period_nav.go  新增   PeriodNav：默认周期 + prev/next + label（服务端唯一来源）
internal/adapter/web/apiv1/     新增   平台无关 JSON API（薄壳：Request → usecase → DTO）
internal/adapter/web/handler/          SSR 改为调同一个 usecase，不再自己拼
```

分层纪律照 `CLAUDE.md`：`domain` 不依赖任何包；`apiv1` 调 usecase 与 port，不反向。
`apiv1` 与 `handler` 是**平级的两个适配器**，`apiv1` 不许 import `handler`，
共用的东西（鉴权中间件、`writeJSON`）下沉或复制，不要交叉依赖。

---

## 2. API 总则

- 前缀 `/api/v1`，挂在 `RequireAuth` 之内（`/api/v1/auth/token` 例外，见 §7）。
- **现有 `/api/*` 全部保持不动**，PC 页面切到 v1 之后再决定是否下线。不要在本次改动里删。
- 成功信封：`{"data": <payload>}`。失败：对应 HTTP 状态码 + `{"error":{"code":"...","message":"..."}}`。
  `code` 用稳定短串，客户端可据此分支：`bad_request` / `unauthorized` / `not_found` /
  `method_not_allowed` / `too_many_requests` / `internal`。
  `message` 是面向用户的简体中文。
  `too_many_requests` 必须是独立的码而不是复用 `bad_request`：客户端要据它决定「退避后重试」，
  而不是把限流当成自己参数写错。`method_not_allowed` 同理，它指示的是客户端代码写错了。
- **信封的实现只有一份**，导出在 `apiv1`（`apiv1.WriteData` / `apiv1.WriteError`）。
  `handler` 需要吐同样的信封时（例如 `/api/v1/auth/token`）**import `apiv1` 复用**，不要各写一份
  —— 两份信封实现迟早会在格式上漂开。方向是单向的：`handler` 可以 import `apiv1`，
  `apiv1` 永远不许 import `handler`（否则成环）。
- 所有金额字段成对出现：`xxx_fen`（`int64`，原始分）+ `xxx_text`（已格式化，如 `"1,234.50"`）。
  客户端只渲染 `_text`，`_fen` 留给需要做条形图高度之类的场景。
- 所有枚举字段成对出现：`xxx`（稳定机器值）+ `xxx_text`（中文）。
- 时间：`occurred_at` 用 RFC3339，`occurred_text` 用 `"2025-07-03 14:22"`。

---

## 3. `GET /api/v1/meta`

小程序/移动端启动时拉一次的 bootstrap，替代现在 SSR 嵌的四个 `<script type="application/json">`。

```json
{"data":{
  "categories":[{"group_id":"expense.discretion","group_name":"可选消费","type":"expense",
                 "items":[{"id":"expense.discretion.shopping","name":"购物"}]}],
  "specials":[{"id":"...","name":"装修","active":true}],
  "accounts":[{"value":"husband","label":"男主"},{"value":"wife","label":"女主"}],
  "account_views":[{"value":"family","label":"家庭总账"},{"value":"husband","label":"男主"}],
  "sources":[{"value":"alipay","label":"支付宝"},{"value":"csv","label":"CSV"}],
  "statuses":[...],"directions":[...],
  "default_period":{"monthly":{...},"quarterly":{...},"annual":{...}}
}}
```

**分组装配在服务端做**（现在是 `tx_table.js` 里那段 `groupIndex` 循环），客户端直接拿树。
`specials` 为空或专项功能未启用时返回空数组，不报错（沿用 `specialsEnabled()` 的降级姿势）。
- **`accounts` 与 `account_views` 必须分开两个字段**，判据就是现成的 `domain.Account.IsStorageAccount()`：
  `accounts` 只含能写入 DB 的真实成员（husband / wife），用于「把这笔流水改成谁的账户」这种**可写**场景；
  `account_views` 额外含 `family`，用于顶部的**查询视图**切换。
  合成一个列表会让薄客户端拿 `family` 去填单条流水的账户下拉，而 `family` 从来不是合法的存储值
  —— 客户端不该靠自己记住这条规则，这正是「服务端不让客户端推导」要覆盖的事。
- 枚举的中文说法以**现有页面**为准，不要另起叫法：状态是「待处理 / 已确认 / 已排除」
  （`transactions.html` 就是这么写的，不是 `CLAUDE.md` 里顺手写的「待核对」），
  账户是 `Account.Label()` 的「男主 / 女主 / 家庭总账」。

---

## 4. 周期导航：`GET /api/v1/periods/nav?type=&period=`

```json
{"data":{"type":"quarterly","key":"2025Q3","label":"2025年第三季度",
         "prev":"2025Q2","next":"2025Q4","has_next":true,"is_current":false}}
```

- `type` 缺省/非法 → `quarterly`；`period` 缺省 → `PeriodNav` 给的默认周期。
- **默认周期规则不变**：上一个完整周期（annual→去年，monthly→上月，quarterly→上季度）。
  现有 `handler.defaultPeriodFor` 的逻辑整体搬进 `usecase.PeriodNav`，handler 改为调它，
  不要留两份。`txListPeriod` 的 `?rule_id=` 例外（当前季度）也搬进去，作为 `PeriodNav` 的一个显式入口。
- **每个带周期的响应都内嵌同形状的 `period` 对象**。客户端翻页只用 `prev`/`next` 里给好的 key，
  自己不做任何日期运算 → `period_utils.js` 的 `defaultPeriodKey` / `shiftPeriodKey` 随 §9 删除。
- `has_next=false` 时客户端禁用「下一期」按钮：不让用户翻到未来。

---

## 5. 流水查询：`GET /api/v1/transactions`

### 入参

```
type, period, account                    周期与账户，同现有语义
direction  = all | income | expense      缺省 all
source     = alipay,wechat,manual,csv    逗号分隔，缺省全选；csv 匹配所有 csv:<模板名>
status     = pending_review,confirmed    缺省就是这两个，excluded 必须显式要求
category   = <id> | __none__             __none__ = 未分类
special    = <id> | __none__ | __any__   __none__ = 日常，__any__ = 任意专项
member     = <name> | __none__
keyword    =                             匹配 counterparty/description/note/raw_row
rule_id    =                             按该规则预筛（语义见 §6）
sort       = occurred_at|amount|counterparty|category|special|status|member|account|source|direction
order      = asc | desc                  缺省 occurred_at desc
page       = 1                           1 起
page_size  = 50                          缺省 50，上限 200，超出钳到上限而不报错
```

`sort` 必须走**白名单**映射到列名，绝不拼接用户输入。非法值回落到 `occurred_at`。
白名单必须覆盖 `transactions.html` 表头今天已经提供的全部排序列（日期/来源/账户/成员/方向/
对方/金额/分类/专项），否则客户端改薄之后用户会丢掉现有功能。注意前端今天用的键名是
`amount_fen`，服务端的键名是 `amount`，映射由客户端负责，服务端只认白名单里的名字。

### 出参

```json
{"data":{
  "rows":[{...见下...}],
  "period":{...§4 同形状...},
  "page":{"page":1,"page_size":50,"total":312,"total_pages":7},
  "totals":{"income_fen":0,"income_text":"0.00","expense_fen":0,"expense_text":"0.00",
            "net_fen":0,"net_text":"0.00"},
  "facets":{"members":[{"value":"__none__","label":"未标注"},{"value":"张三","label":"张三"}]},
  "rule":{"id":"...","label":"交易对方 包含「星巴克」","category_id":"...","category_name":"..."}
}}
```

**红线一：`totals` 是整个筛选结果集的合计，不是当页的。** 这是今天 `tx_table.js` 靠「整期全在手」
才算对的东西；一分页就必须由服务端用独立的 `SUM` 查出来。实现成两条 SQL（当页 `SELECT` +
合计 `SELECT COUNT/SUM`），不要在 Go 里对当页求和。必须有测试钉住：
构造 60 行、`page_size=10`，断言 `totals` 等于 60 行的合计而不是前 10 行的。

**红线二：`facets.members` 是整个周期（叠加其它筛选前）的 DISTINCT，不是当页的。**
否则翻页时成员下拉的选项会跳。

**红线三：筛选/排序/分页必须下推到 SQL。** 不许「取全量再在 Go 里 filter」——那样移动端分页
毫无意义。`keyword` 用 `LIKE '%kw%'`，走不了索引是可以接受的（范围已被周期窗口限定）；
记得转义 `%` `_` `\`。

**红线四：流水列表是「真实现金流」视图，不是基线视图，所以不传 `domain.Scope`。**
沿用现有 `List` 的行为（不按 `special_id` 过滤），专项的取舍完全交给 `special=` 参数。
不要顺手塞一个 `ScopeDaily` 进去——那会把专项流水从列表里藏掉，而用户正是来这里标注专项的。

**红线五：不得改动现有聚合 SQL 与迁移 013/014 建的覆盖索引。** 本次只加「取行」的查询。
除非 `EXPLAIN QUERY PLAN` 实测需要，不新增迁移。

**红线六：`rule_id` 预筛必须只看支出流水，而且预筛集合要与「批量应用」改动的集合完全相同。**
理由是真实分类器 `ClassifyByCustomRules` 对收入流水直接 `return`，一条规则永远不会分类收入。
预筛如果把收入行也列出来，「查看流水」说 37 条、点「应用」只改了 24 条，用户无从理解差额。
这与 §6.1 那个 bug 是同一个病根：**预览必须镜像分类器的真实行为**，不能自己放宽。
实现上把方向约束放在 usecase（`Rule != nil` 时强制 `direction=expense`），不要烧进 repo 的
`ruleWhere`，让 repo 保持通用。

### 行 DTO

```json
{"id":"...","occurred_at":"2025-07-03T14:22:00+08:00","occurred_text":"2025-07-03 14:22",
 "counterparty":"...","description":"...","note":"...","raw_row":"...",
 "member":"...","account":"husband","account_text":"...",
 "source":"alipay","source_text":"支付宝",
 "amount_fen":123450,"amount_text":"1,234.50",
 "direction":"expense","direction_text":"支出",
 "status":"confirmed","status_text":"已确认",
 "category_id":"expense.discretion.shopping","category_text":"购物",
 "special_id":"","special_text":""}
```

`source_text` 对 `csv:<模板名>` 返回 `"CSV·<模板名>"`（把 `tx_table.js` 的 `sourceLabel` 搬过来）。
`category_text` / `special_text` 由服务端查表填好，客户端不再需要 `catNameById`。

---

## 6. 规则：匹配逻辑去重 + 批量应用

### 6.1 先修一个真 bug

`usecase/classify_rules.go` 的 `ruleFieldValues`，`field="any"` 时只看
`{Counterparty, Description, PlatformCategory}`；而 `tx_table.js` 的 `ruleFieldValues`
还多看了 `note` 和 `raw_row`。两份实现语义不一致，后果是「分类规则」页点「查看流水」
预览出来的流水，有一部分是真正的分类器**永远不会**命中的。

**以 Go 的语义为准**（分类器是真实行为，预览必须跟它一致）。把匹配逻辑抽到
`domain/rule_match.go`：

```go
// RuleMatchFields 参与规则匹配的字段集合，与分类器完全一致
type RuleMatchFields struct{ Counterparty, Description, PlatformCategory string }
func RuleMatches(rule CategoryRule, f RuleMatchFields) bool
```

`usecase.ruleMatches` 改为调它（行为必须完全不变，现有 `classify_rules_test.go` 必须全绿），
流水查询的 `rule_id` 预筛也调它。JS 里那份连带 `ruleLabel` 一起删，`rule.label` 由服务端给。

### 6.2 批量应用规则

`tx_table.js` 的 `applyRule()` 现在是「前端 for 循环逐条 PATCH」。改成一次调用：

```
POST /api/v1/rules/{id}/apply        body {type, period, account}
→ {"data":{"updated":37}}
```

服务端在**单个事务**里一条 `UPDATE` 写完（参照 `SetSpecialForIDs` 的写法），
只改「不是(已经是该科目 且 已确认)」的行，并按既有约定把 `status` 置为 `confirmed`。
弱网下 37 次请求变 1 次，这是移动端的刚需。

另外三条语义：
- **不动 `excluded` 的行**。那是用户刻意排除的流水，批量应用不该把它复活。
- **改动集合 = §5 红线六的预筛集合**，即只有支出流水。不要再额外按「目标科目是收入还是支出」
  去决定方向：那恰好会让收入科目的规则去改收入流水，而分类器从来不会那么做，
  等于让批量应用比真实分类器更激进。
- 规则的 `category_id` 在科目表里查不到、或为空（跳过导入类规则）时，返回错误且不改任何行。
  指向收入科目的规则在分类器眼里是一条死规则，同样返回错误并说明原因，而不是悄悄改支出行。

---

## 7. 鉴权：小程序拿不到 Cookie

`wx.request` 不可靠地携带 Cookie，所以必须支持 Bearer。

- `authManager.authenticated` 增加分支：先看 Cookie，再看 `Authorization: Bearer <token>`，
  **token 直接复用现有 `newToken`/`validToken`**（HMAC-SHA256 + exp），不要另造一套机制。
- 新增 `POST /api/v1/auth/token`，body `{"key":"..."}` → `{"data":{"token":"...","expires_at":"..."}}`。
  挂在 `RequireAuth` **之外**，并且**必须接入现有 `loginLimiter`**，否则等于给暴力破解开了后门。
- 鉴权未启用（`key` 为空）时沿用现在「一律放行」的行为。
- Cookie 那条路径一个字不改，PC 端不受影响。

---

## 8. 移动端浏览器（复用 PC 代码）

**不 fork 模板。** 同一套 Go 模板 + 同一份 `app.css`，用媒体查询适配。`app.css` 已有 12 处
媒体查询、`base.html` 已有 viewport，继续在这个路子上走。

### 8.1 宽表的处理

流水表 / 现金流表在手机上横向滚不可用。在 `max-width: 700px` 下把 `<table>` 转成堆叠卡片：
给每个 `<td>` 加 `data-label` 属性，CSS 用 `td::before { content: attr(data-label) }` 显示表头。
这样**模板只加属性、不分叉结构**，PC 端完全不受影响。

### 8.2 兼容性硬要求（微信内置浏览器 X5 / iOS WKWebView）

- JS 目标 **ES2017**。不用 `?.` `??` `??=` `Array.prototype.at` `structuredClone` `:has()`。
- **flex/grid 的 `gap` 在 iOS < 14.5 的 flex 布局里不生效**，手机断点内用 margin 实现间距。
- 输入框 `font-size: 16px` 以上，否则 iOS 聚焦时整页缩放。
- 点击目标最小 44×44px。
- 不要 hover-only 的交互。仪表盘现在的**双击穿透**在触屏上不可发现也难触发，
  手机断点下要给一个显式的「查看流水」入口，PC 的双击行为保留。
- 视口高度用 `100dvh` 并以 `100vh` 兜底，避免 iOS 地址栏导致的抖动。
- 滚动容器加 `-webkit-overflow-scrolling: touch`。
- `position: sticky` 带 `-webkit-` 前缀。

### 8.3 `tx_table.js` 改薄

`filtered` / `totals` / `memberOptions` / `matchesRule` / `ruleFieldValues` / `ruleLabel` /
`sortIcon` 之外的排序逻辑 / `fmtYuan` / `sourceLabel` / `catNameById` / `specialNameById`
**全部删除**。组件退化为：持有筛选状态 → 拼 query → `fetch /api/v1/transactions` →
把 `rows` / `totals` / `page` / `facets` 直接渲染。就地编辑的 PATCH 与乐观回滚保留
（那是交互而不是业务逻辑），但改打 `/api/v1`。

`period_utils.js` 删掉 `defaultPeriodKey` 与 `shiftPeriodKey`，周期按钮改用响应里的
`period.prev` / `period.next`。`granularityFromPeriodType` 这类纯映射可以留。

**`CLAUDE.md` 的「默认周期（前后端两处实现，必须同步改）」一节随之改写**——改完就只有一处了，
这是本次改造最值钱的产物之一，别让文档继续指着一个已经不存在的陷阱。

---

## 9. 小程序（最后一步）

仓库根目录 `miniprogram/`，只做三件手机上真正值钱的事：**手填记账**、**流水列表与就地改分类**、
**季/年报 KPI**。导入、规则维护、AI 财报继续留在网页端。

- 全部数据来自 `/api/v1`，**不内置任何业务逻辑**（§0 第一原则）。
- 本地 `wx.setStorage` 只存 token 与上次选的周期。**不缓存流水**：10MB 上限装不下，
  也没有索引可用（见上一轮结论）。
- 请求统一走一个 `request.js`：注入 Bearer、解信封、401 时跳登录页。
- 页面分包，主包只放登录与 tabBar 首页。

---

## 10. 测试要求

照 `CLAUDE.md` 的约定：表驱动 + `got/want` 且说明「为什么该是这样」。

- `infrastructure/sqlite`：真库夹具（`Open(t.TempDir())` + `Migrate`），不 mock SQL。
- `usecase`：替身集中在 `fakes_test.go`，不要在新文件里重造。
- `apiv1`：`httptest` 打真 handler，断言 JSON 结构与状态码。

必须有的用例：

1. `totals` 等于整个筛选结果集的合计，而不是当页（60 行 / `page_size=10`）。
2. `facets.members` 覆盖整周期，不随分页变化。
3. `status` 缺省不含 `excluded`；显式传 `excluded` 才出现。
4. `keyword` 四个字段都能命中；`%` `_` 作为字面量不被当通配符。
5. `sort` 非法值回落 `occurred_at`，不产生 SQL 注入面。
6. `page_size=9999` 钳到 200。
7. `period.prev` / `next` 跨年进位正确（`2025-01` → `2024-12`，`2025Q1` → `2024Q4`）。
8. `has_next=false` 在当期上成立。
9. `special` 的 `__none__` / `__any__` / 具体 id 三态语义。
10. `RuleMatches` 与现有 `classify_rules_test.go` 行为完全一致（回归）。
11. Bearer token 能过 `RequireAuth`；过期 token 被拒；`/api/v1/auth/token` 受限流保护。


---

## 11. 已接受的取舍（不要反复重开）

实现中确认过、权衡后接受的行为差异，记在这里免得日后被当成 bug 反复讨论。

1. **中文排序是 Unicode 码点序，不是拼音序。** 旧前端用 `localeCompare(…, 'zh-CN')` 得到拼音序，
   但那依赖「整期全在手」。服务端分页要求排序发生在 SQL 里，而 SQLite 默认排序规则是字节序。
   要恢复拼音序得注册自定义 collation 或加一列预计算排序键，代价远超这个功能的价值。
   影响 `counterparty` / `member` 两个键，以及 `facets.members` 的顺序。
2. **`sort=category` / `sort=special` 按 id 排，不按显示名排。** 科目 id 是点分命名空间
   （`expense.discretion.shopping`），按 id 排等于按一级分组聚拢，比按名字排更有结构。
3. **规则预筛的子串匹配用 SQLite 的 `instr(lower(col), ?)`，而 `domain.RuleMatches` 用 Go 的
   `strings.ToLower`。** SQLite 的 `lower()` 只处理 ASCII，Go 处理全部 Unicode，所以含非 ASCII
   大写字母（如 `CAFÉ`）的字段在两边可能判定不一致。中文、英文、数字不受影响。
4. **`total_pages` 在 0 行时是 0 而不是 1。** 客户端此时展示空态，不需要「第 1 页 / 共 1 页」。
5. **`order` 缺省一律 `desc`，不按排序键区分。** 文本键降序略反直觉，但规则统一比分键特判好记。
6. **查询能力放在独立的窄接口 `port.TransactionQueryRepo`，没有并进 `TransactionRepo`。**
   这是刻意的接口隔离：`TransactionRepo` 已经很宽，handler 测试里的 `stubTxRepo` 实现了它的全部方法，
   每加一个方法就要去改所有替身。新接口由 `sqlite.TransactionRepo` 一并实现，有编译期断言钉住。
   `CLAUDE.md` 的「新增 Repository 方法」一节要补上这条：宽接口加方法的代价由所有替身承担，
   新增查询能力优先开窄接口。
