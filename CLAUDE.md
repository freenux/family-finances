# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目目标

家庭季/年度财务系统（参考 `TO-AGENT.md`）：导入微信/支付宝账单 + 手填 → 按季度/年度聚合展示 + AI 财报分析。后端 Go + SQLite 文件，整洁架构；前端 Server-Rendered HTML + HTMX + Alpine.js，要求对齐原 Excel 表结构。

## 常用命令

```bash
# 跑服务器（首次启动会自动跑 goose migrations 到 family.db）
go run ./cmd/server

# 构建
go build -o bin/server ./cmd/server

# 模块/依赖
go mod tidy

# 测试
go test ./...
go test ./internal/usecase -run TestXxx -v

网页测试使用playwright-cli **不要使用**curl测试
```

`.env`（参见 `.env.example`）控制 `OPENAI_API_KEY/BASE_URL/MODEL`、`SERVER_ADDR`（默认 `:8787`）、`DATABASE_PATH`（默认 `./family.db`）。未设 `.env` 也能跑，但大模型相关功能会失效。

README 里的 `uv run python -m wepay_classifier ...` 是独立的 Python 微信账单预分类脚本（尚未进仓，仅 `.venv/` 和 `.wepay_llm_cache.json` 存在），与 Go 后端解耦。

### 测试约定

当前约 70 个 `*_test.go`（usecase 30 / infrastructure/sqlite 14 / adapter/web/handler 12 / domain 6 / adapter/web/apiv1 5 / adapter/web 2 / adapter/bill 1；以 `find . -name "*_test.go"` 为准，数字会过时）。新代码按同样的路子写：

- **表驱动**：`tests := []struct{ name string; ... }{...}` + `for _, tt := range tests { t.Run(tt.name, ...) }`，约 20 个文件是这个形状。断言写成 `got = %v; want %v` 并在信息里说清"为什么该是这样"。
- **`internal/infrastructure/sqlite` 用真库夹具**，不 mock SQL：`Open(filepath.Join(t.TempDir(), "test.db"))` + `Migrate(db)`，每个 repo 一个 `newTestXxxRepo(t)`（见 `report_repo_test.go`）。查询计划/索引形状用 `EXPLAIN QUERY PLAN` + `pragma_index_info` 钉住（`query_plan_test.go`、`migration_014_test.go`）。
- **`internal/usecase` 用替身**：共享替身集中在 `fakes_test.go`（`fakeTransactionRepo` / `fakeCategoryRepo` / `fakeReportRepo` / `fakeLLM` …），别在各自的测试文件里重复造。
- **`adapter/web/handler`** 同理，`stubTxRepo` / `stubSpecialRepo` / `stubCatRepo` 定义在 `specials_test.go` 里全包共用；模板渲染直接用 `web.NewRenderer()` + `RenderPage/RenderPartial` 断言 HTML 片段（`report_view_test.go`、`reports_scope_test.go`）。

## 架构

### 分层（Clean Architecture，禁止跨层反向依赖）

```
cmd/server/main.go         组装入口：config → sqlite.Open → Migrate → Analyze → repos → llm.Client → usecases →
                           renderer → handler → chi router；再起两个 goroutine：ClassifyPending.Run（LLM 未配置
                           则 no-op 返回）与 DigestService.Run（周报/季报定时推送）
internal/
  domain/                  纯领域模型（Transaction / Category / Period / Scope / SpecialProject / ReportData /
                           ReportKPI / AssetSnapshot / FinancialGoal / InsurancePolicy / FamilyProfile / Budget /
                           DigestSettings / ImportTemplate / AIReport / RawBillRow / ImportBatch）
  port/                    Repository 接口（TransactionRepo / SpecialProjectRepo / CategoryRepo / AssetSnapshotRepo /
                           ReportRepo / BudgetRepo / …）+ ImportRow / TransactionUpdate / ImportResult /
                           PeriodBucket / TopTransaction / SpecialSpend 等数据对象
  usecase/                 一个文件一个用例，完整清单看目录：QueryReport、QueryStats、ImportBill、
                           ClassifyByCustomRules、ClassifyPending、ContextPackBuilder、GenerateReport、
                           GenerateAdvice、Ask、BucketEngine、AllocationEngine、BudgetView、RecurringEngine、
                           GoalView、InsuranceView、SpecialView、DigestService、Export、Scenario；
                           多端共用的「查询/整形」也在这里：TxQuery（流水筛选排序分页+合计+facets）、Meta
                           （下拉选项与科目分组 `GroupCategories`）、PeriodNav（周期规则）、UpdateTransaction /
                           CreateTransaction（流水写入）、ReportView
  infrastructure/
    config/                env/godotenv 加载 Config
    sqlite/                sql.DB + goose 迁移 + Analyze + 各 Repo 实现
    sqlite/migrations/     goose SQL（当前到 015），通过 //go:embed 嵌入二进制
  adapter/
    bill/                  账单解析器（alipay.csv / wepay.xlsx → []RawBillRow）+ 通用 CSV 模板解析
    llm/                   OpenAI 兼容 chat completions 客户端
    notify/                SMTP 发信（周报/季报推送）
    web/
      render.go            html/template 渲染器，template/ 与 static/ 均 //go:embed
      handler/             HTTP handler（chi），Request → usecase → ViewModel → 模板；按功能拆成十几个文件
      apiv1/               平台无关 JSON API（/api/v1，手机浏览器/小程序共用），与 handler 平级；
                           只剩 HTTP 壳（解析入参 → 调 usecase → 信封），不做数据整形；
                           依赖方向只能 handler → apiv1（复用信封），apiv1 不得 import handler
      template/            layout/base.html + pages/*.html（19 个）+ partials/*.html（4 个）
      static/css/          app.css
      static/js/           每页一个 Alpine 组件（tx_table / stats_page / dashboard_page / budget_page /
                           assets / ask_page / csv_import）+ 共享的 period_utils.js；
                           vendor/ 放本地固定版本的 htmx 与 alpinejs
```

关键规则：`domain` 不引用任何其他包；`usecase` 只依赖 `domain` + `port` + 自己调用的 `adapter/llm` / `adapter/bill`（向下 OK，向上不行）；`infrastructure/sqlite` 实现 `port`；`adapter/web` 调 usecase 与 port，不反过来。

### 领域约定

- **金额单位是 `int64` 分（fen）**，整条链路不用 float。账单解析器把元乘 100 并 +0.5 转分；模板里用 `{{yuan .Amount}}` 格式化回元。**金额格式化的方向是「服务端给 `xxx_text`，客户端只渲染」**（契约 §2）：流水页（`tx_table.js`）已不做任何金额格式化，直接显示 `/api/v1` 返回的 `amount_text` / `totals.*_text`。还没改薄的页面（统计页 `stats_page.js`、资产页 `assets.js`、预算页 `budget_page.js`）各自仍留着一份 `fmtYuan`，等它们有对应的 `/api/v1` 接口后再删，别在新页面里照抄。
- **Period 标签格式**：季度 `"2025Q3"`、年度 `"2025"`、月度 `"2025-07"`。`domain.ParsePeriod` 是唯一解析入口；`Period.End` 是独占（`< End`），SQL 里对应 `occurred_at < ?`。
- **Category ID 用点分命名空间**：一级（`level=1`）是分组（如 `income.salary`、`expense.discretion`），二级（`level=2`）是真正的科目（如 `expense.discretion.shopping`）。聚合与模板靠 `parent_id` 与 `strings.HasPrefix(GroupID, "income.")/"expense."` 分流。
- **DiscretionRatio 告警阈值 35%**（`computeKPI` in `usecase/query_report.go`，`k.DiscretionWarning = k.DiscretionRatio > 0.35`）。**分子分母都是日常口径**：分子是 `expense.discretion` 组在日常分组里的小计，分母是 `KPI.DailyExpense` 而**不是** `TotalExpense`——用全口径的话一次装修把分母从 4 万抬到 18.5 万，占比被稀释到阈值以下，告警正好在最该响的时候静默关掉。改阈值或新增 KPI 就改 `computeKPI` 这一处。
- **Transaction.Status**：`pending_review | confirmed | excluded`。聚合 SQL 只算 `confirmed`。
  - 导入时：命中本地规则 → `confirmed`；未命中 → `pending_review`（等 LLM 或人工）。
  - 手填（`/imports/manual` 与 `POST /api/v1/transactions`，同走 `CreateTransaction`）：给了科目 → `confirmed`；没给科目 → `pending_review`（同导入未命中，等 LLM 或人工）。**不能落 `confirmed`**：无科目的 `confirmed` 进不了任何聚合，`ClassifyPending` 又只捞 `pending_review`，这笔钱就永远没人看见。
  - 人工在列表页下拉改分类 / LLM 补上分类 → 自动转 `confirmed`。
  - 用户想彻底忽略某笔（如误记）→ 改为 `excluded`，不参与季/年报。
- **Transaction.SpecialID**：所属专项（`special_projects.id`），**空 = 日常开支**。跟 `excluded` 是两回事：专项**仍然计入支出合计**（`ReportKPI.TotalExpense`、现金流表的「支出合计 (B)」都含它），只是可以按统计口径被剔除；`excluded` 才是彻底不参与任何聚合。判据是"非经常性"而不是"金额大"，所以只能人工标注（流水页单条下拉，或勾选后批量 `PATCH /api/v1/transactions/batch`，或把整个筛选结果归入 `PATCH /api/v1/transactions/by-filter`），不做金额阈值自动判定。
- **Transaction.Note**：用户在流水列表就地填的备注，与 `Description`（来自账单的商品说明）分开存，不要覆盖。
- **唯一键防重**：`imported_transaction_keys(source, transaction_no)` 由 `InsertBatch` 在事务内检查。重复导入同一文件全部跳过。

### 统计口径（Scope）

`domain.Scope` 是与科目正交的第三个维度：科目回答"钱花在什么上"，口径回答"算不算日常基线"。纯查询侧概念，不作为存储值落库（落库的是 `transactions.special_id`）。

- `daily` —— 剔除专项（`special_id IS NULL`），用于趋势 / 环比同比 / 预算 / 攒钱能力这类**日常基线**判断
- `special` —— 只看专项
- `all` —— 日常 + 专项，真实现金流
- **不变式：`daily + special == all`**。`/cashflow` 的三行拆分、`ContextPack` 的"日常 + 专项 = 全口径"都直接吃这条，新增聚合必须保证它成立。
- `domain.ParseScope` 是唯一解析入口，**空串与任何非法值一律退回 `daily`**——默认视图必须是干净的（实测一次装修能把全局同比从 +2.9% 拉到 +368%）。
- SQL 片段由 `sqlite.scopeFilter(scope, col)` 生成。它属于持久化细节，故意不放在 `domain` 里：`domain.Scope` 不该知道流水存在关系库、更不该知道 repo 给表起了什么别名。

**谁用哪个口径**——这是新增用例最容易踩的坑：忘了传 `ScopeDaily`，一次装修就能把所有基线指标炸掉。改这些调用点前先想清楚它回答的是"基线"还是"真实现金流"。

- 固定 **daily**（基线类，不跟随请求）：
  - `usecase/bucket_engine.go` —— 四笔钱的月均支出
  - `usecase/budget_view.go` —— 季度预算对照、周期项识别（`ListForRecurring`）、近半年均值
  - `usecase/context_pack.go` —— AI 上下文包的收支/KPI/环比、攒钱连胜、活钱覆盖月数
  - `usecase/digest.go` —— 周报/季报摘要
  - `usecase/goal_view.go` —— 目标进度用的收支桶
- 跟随请求 `?scope=`：`usecase/query_stats.go` 的**饼图**（`AggregateByCategory`）、**月/季对比条**（`scopeAmountSeries`）、**Top 榜单**（`TopTransactions`）；入口是 `handler.StatsAPI` / `handler.StatsTopAPI` 里的 `domain.ParseScope(q.Get("scope"))`。
- 两遍都查：`usecase/query_report.go`（现金流表与季/年报）分别取 daily 与 special 聚合，再自己合成全口径与 `KPI.Special*` 字段。

`TransactionRepo.SumByBuckets` 是个例外——它**不收 scope 参数**：一次范围扫描同时返回 `(daily, special)` 两组按下标对齐的桶，基线调用方取第一个返回值即可，"全部"= 两者逐桶相加。别为了换口径把同一段范围扫两遍。

### 错误约定（usecase 哨兵 + 适配器映射）

- **`port.ErrNotFound`**：repo 的「按 id 取单条 / 更新无匹配行」找不到时**必须**返回它（`GetRule`、`TransactionRepo.Get` 也是），不要把 `sql.ErrNoRows` 漏出 sqlite 包——usecase 与适配器只认哨兵，认不出就会把 404 变成 500，而用替身的单测发现不了，所以 sqlite 层有真库测试钉住（`category_repo_test.go`）。
- **`usecase/errors.go` 三个哨兵**：`ErrInvalidPeriod`（周期 label 非法）、`ErrRuleNotApplicable`（规则是死规则 / 无目标科目 / 科目不存在）、`ErrInvalidInput`（其余入参校验：非二级科目、状态/账户非法、成员过长、专项不存在、ids 为空或超限……）。它们的 `Error()` 是面向用户的中文，适配器可原样回显；**除此之外的错误一律是内部故障**。
- **适配器只用 `errors.Is` 映射**：三个哨兵 → 400；`port.ErrNotFound` → 404；其余 → 500 且细节只进日志（`apiv1.writeUseCaseError`、`handler.writeTxUpdateError`）。**禁止**靠「有没有 `%w` 包装」或错误文本做判断——repo 直接返回的裸驱动错误会被误判成 400 并泄露给客户端。`apiv1/errors_test.go` 钉住这一点。
- `SpecialView.Ensure` 目标不存在时返回包装了 `port.ErrNotFound` 的错误，DB 故障原样返回，二者可区分。

### 流水编辑（单条 / 批量）只有一份实现

PATCH 的业务逻辑——叶子科目校验、status 白名单、`IsStorageAccount`、member 长度、`category_id` 非空自动 `confirmed`、专项校验、批量 ids 上限（`usecase.MaxBatchTxIDs`）与过滤空 id——全在 `usecase.UpdateTransaction`（`Update` / `AssignSpecial`）。`handler.UpdateTransaction` / `BatchUpdateTransactions`（旧 `/api/transactions/*`，204 / 纯文本错误；流水页已不使用，但按契约 §2 保持不动）与 `apiv1`（信封）只做 HTTP 解析与响应格式，**不要在任一侧再写校验**。账户参数解析统一用 `domain.ParseAccount`（空串与非法值退回 `family`）。

### 账单导入流程

1. `GET /imports` 上传表单（source ∈ {alipay, wechat}，文件 multipart）。
2. `POST /imports` → `ImportBill.Execute`：
   - `adapter/bill/ParserFor(source)` 得到解析器；alipay 走 GB18030 → UTF-8 → `encoding/csv`；wechat 走 `excelize.OpenFile`（先拷贝到临时文件）。
   - 解析器跳过元信息头，找 `交易时间` 表头行后逐行解析，过滤非交易成功 / 不计收支。输出 `[]RawBillRow`。
   - `ClassifyByCustomRules(row, rules)` 读取 `category_rules` 数据库规则分类；页面新增/保存的规则和迁移种下的内置规则都在同一张表。规则 `category_id=NULL` → 视为"应跳过"（转账/提现等），不入库；未命中 → `category_id=NULL`, `status=pending_review`。
   - `TransactionRepo.InsertBatch` 一个事务里：对每行 `SELECT FROM imported_transaction_keys` 去重 → `INSERT transactions` + `INSERT imported_transaction_keys` → 最后 `INSERT import_batches`。
   - 成功后调 `uc.trigger()`（在 main.go 里绑定到 `ClassifyPending.Trigger`）唤醒 LLM 后台兜底。
3. Flash cookie 回显结果，302 到 `/transactions`。

版式不固定的第三方 CSV 走另一条路：`/imports/csv` 用 `import_templates` 表存的列映射模板解析（`adapter/bill/generic_csv.go`），不需要为每种 CSV 新写 `Parser`。

### LLM 异步分类

- `adapter/llm/Client` 是 OpenAI 兼容 Chat Completions 客户端，带 `response_format: json_object`。`OPENAI_API_KEY` 为空时 `Enabled()=false`。
- `ClassifyPending.Run(ctx, 30s, batch=200)` 在 main.go 里 goroutine 启动：每 30s 或被 `Trigger()` 唤醒跑一轮；取 `status=pending_review AND category_id IS NULL` 前 `batch` 条（按 `occurred_at DESC`），一次 prompt 让 LLM 输出 `{assignments:[{id,category_id}]}`，校验 category_id 必须在我们的二级科目白名单里，否则丢弃。
- **无进展退避**：一轮里 pending > 0 但一条都没分出来时，静默 30 分钟再试，避免对同一批答不出的行反复烧 token；新导入触发 `Trigger()` 会立刻解除退避。
- 只向 LLM 暴露 `id / counterparty / description / direction`——不给金额和时间，避免误导。
- 写回时只认**本轮送进 prompt 的那批 id**，防止模型幻觉或账单文本里夹带的指令去改写其它流水。
- 分类成功后自动把 `status` 转为 `confirmed`。

### 流水列表（薄客户端）

流水页是第一个按「薄客户端」改造完的页面（契约 `docs/CLIENT-API-DESIGN.md` §0）：筛选 / 排序 / 分页 / 合计 / 中文文案 / 周期进位 / 规则匹配全在服务端，`tx_table.js` 只持有状态、拼 query、渲染响应，**不要把这些逻辑再长回前端**。

- **首屏数据**：`pages/transactions.html` 嵌入两个 `<script type="application/json">`——`#data-transactions` 是整个 `usecase.TxQueryResult`（`rows / period / page / totals / facets / rule?`，与 `GET /api/v1/transactions` 同形），`#data-meta` 是 `usecase.MetaView`（科目分组、专项、账户、来源、状态、方向的下拉选项，与 `GET /api/v1/meta` 同形）。打开页面不再额外发请求。旧的 `data-categories` / `data-rule` / `data-specials` 已不存在（规则预筛信息在 `TxQueryResult.rule` 里）。
- **重要**：嵌入 JSON 必须用 `{{rawJSON .X}}`（在 `render.go` 的 `funcMap` 里定义为 `template.JS`）。否则 Go `html/template` 在 `<script>` 上下文里会把整个 JSON 字符串再字符串化，前端 `JSON.parse` 会失败。
- **`txTable()`（`static/js/tx_table.js`）**：读取上面两份 JSON 做首屏 bootstrap；筛选 / 排序 / 翻页 / 换周期 / 换每页条数一变就 `GET /api/v1/transactions?...` 重新拉，把 `rows / totals / page / facets / period` 原样渲染。**不再**在前端排序、过滤或重算汇总；金额与枚举文案直接用响应里的 `*_text`；周期上一期 / 下一期用响应里的 `period.prev / next / has_next`。URL 上的筛选参数（含从统计页、规则页穿透来的）就是 API 参数，原样读回、原样写回地址栏。
- **就地编辑**：行内 `<select>`/`<input>` 触发 `PATCH /api/v1/transactions/{id}`，body 是 `{category_id?, note?, status?, account?, member?, special_id?}` 的任意子集（响应是 `{"data":...}` / `{"error":{"code","message"}}` 信封）。乐观更新，失败时回滚本地状态并 `alert`；成功后静默重查（编辑可能改变筛选 / 排序 / 合计）。PATCH 一旦提交 `category_id` 非空，后端自动把 `status` 置为 `confirmed`；`special_id` 传空字符串表示归回日常。
- **批量归入专项有两条路径**：① 勾选当页的行 → `PATCH /api/v1/transactions/batch`，body `{ids:[...], special_id}`；② 把「当前筛选的全部结果」（含其它页）→ `PATCH /api/v1/transactions/by-filter?<与列表相同的筛选参数>`，body `{special_id}`，命中哪些行完全由服务端按列表同一套 WHERE 决定。两者都在单个事务里写完。规则批量应用走 `POST /api/v1/rules/{id}/apply`。
- **旧接口仍在，但流水页已不使用**：`GET /api/transactions`、`PATCH /api/transactions/{id}`、`PATCH /api/transactions/batch` 等旧 `/api/*` 仍注册在 `main.go` 里（契约 §2 要求本轮不删，等所有 PC 页面切到 v1 后再决定是否下线）。改流水编辑的业务规则只改 `usecase.UpdateTransaction`，两边共用。

### HTTP / 渲染

- chi router，中间件 `Recoverer` + `Logger` + `Compress(5)`。**路由集中在 `cmd/server/main.go` 的 `r.Group(func(r chi.Router){...})`（`h.RequireAuth` 之内），改路由只看那一处**——这里不再逐条抄清单，抄一次过时一次。`/healthz`、`/auth/*`、`/static/*` 在鉴权组之外。
- **chi 的静态段 vs 占位段（v5.1.0 实测；早先文档里"`{id}` 会吞掉同级字面量段（即使不同 method）"的说法是错的，别照着它绕路）**：
  - **同 method 下静态段优先，且与注册顺序无关。** `PATCH /api/v1/transactions/batch`（以及 `/by-filter`）与 `PATCH /api/v1/transactions/{id}` 安心共存（旧的 `/api/transactions/batch` 与 `/api/transactions/{id}` 同理）：`/batch` 落静态处理器，`/batchx`、`/tx-1` 正确落到 `{id}`。同理 `POST /specials/{id}/delete` 和 `GET|POST /specials` 之间没有任何冲突。
  - **真正会踩的是"同一段路径上，静态段和占位段挂在不同 method 上"**：静态节点没有该 method 的 endpoint 时，chi 会**回退到占位节点**而不是返回 405。例如同时有 `GET /transactions/new` 和 `PATCH /transactions/{id}` 时，`PATCH /transactions/new` 会静默落到 `{id}` 且 `id="new"`，返回 200 而不是 405。要让某个字面量段当"保留字"防守住所有 method，就得给 API 加 `/api` 前缀跟页面路由分开——**只有这种情况需要绕**。
- **`/api/v1/*` 是给多端薄客户端的 JSON API**（实现在 `adapter/web/apiv1/`），挂在 `RequireAuth` 之内，仅 `/api/v1/auth/token` 在鉴权组之外；契约以 `docs/CLIENT-API-DESIGN.md` 为准。
- 模板组织：`base.html`（layout，定义 `{{define "base"}}`）+ 每个 `pages/*.html`（定义 `{{define "content"}}` 和 `{{define "page"}}{{template "base" .}}{{end}}`）+ `partials/*.html`（独立 `define`，HTMX 局部刷新用）。
- `Renderer.RenderPage(w, "dashboard", vm)` 渲染整页；`RenderPartial(w, "report_view", vm)` 给 HTMX 返回片段。新加页面时按 `pages/` 文件名即为 key，模板自动被 `NewRenderer` 注册。
- 模板函数（`render.go` 的 `funcMap`）：`rawJSON`、`yuan`、`pct`、`goalPct`、`formatDate`、`categoryName`、`groupCategories`。新增函数加到那里。注意科目分组有两份实现：给 JSON 客户端用的整形在 `usecase.GroupCategories`（`/api/v1/meta` 与流水页 `#data-meta` 共用，`apiv1` 里没有任何分组逻辑）；`funcMap.groupCategories` 只服务规则页 / 导入页这类仍由模板渲染 `<select>` 的 SSR 表单，尚未合并。
- **手机端适配（同一套模板、同一份 `app.css`，不 fork）**，细节与示例在 `app.css` 里「手机端适配（<= 700px）」一节的注释：
  - **新增宽表要在手机上可用**：给 `<table>` 加类 `stacked-on-mobile`，每个 `<td>` 加 `data-label="列名"`（= 对应 `<th>` 文字；操作列、勾选列写 `data-label=""`）。手机下表格自动转成堆叠卡片，PC 不受影响，模板只加属性、不分叉结构。范本是 `pages/transactions.html`。
  - **PC 靠双击 / hover 的交互，手机要补显式入口**：用 `.m-only` / `.pc-only` 控制显隐。范例：统计页科目行的「查看流水」链接与月/季对比条下的「切换到 / 查看流水」按钮（PC 的双击保留）；点击目标不小于 44×44px。
  - **flex 间距不要写 `gap`**（iOS < 14.5 / 旧 X5 的 flex 不认）：用 `.flex-row` + `.row-sp8/10/12/14` 修饰类（可换行再加 `.row-wrap`），其余容器照 `app.css`「兼容层 A」的 margin 写法。**不要把 `gap` 写进 `style=""`**——`app.css` 里原来那层靠匹配 `style` 字符串（`[style*="gap:"]`）生效的 gap 垫片已清除，只剩 `partials/report_tables.html` 专项明细行（`gap:12px`）一处在用，那两条规则带着注释标明了归属，迁完即删。另有两处同类的属性选择器兜底仍在：内联 `overflow-x:auto` 的惯性滚动、内联 `grid-template-columns` 在手机下改单列，新页面请直接写 class。
  - 输入框 `font-size >= 16px`；JS 目标 ES2017（不用 `?.` `??` `??=` `Array.prototype.at` `structuredClone`）；触屏图表要补 `touchstart/touchmove`（范例：`assets.js` 净值曲线）。
- **htmx 与 alpinejs 是本地固定版本**，放在 `static/js/vendor/`（`htmx-1.9.12.min.js`、`alpinejs-3.14.1.min.js`），由 `base.html` `defer` 引入，**不走 CDN**。自有 JS 同样放 `static/js/` 并在 `base.html` `<head>` 里 `defer` 引入。

### 默认周期（规则只在服务端 `usecase.PeriodNav`；前端还剩一份待删的旧实现）

- 仪表盘 `/`、收支流水 `/transactions`、现金流表 `/cashflow` 这三个页面（以及 `/reports`、`/assets`、`/api/stats`）的默认周期一律是**上一个完整周期**，不是当期——当期没走完，环比同比都会失真。
- **规则的唯一来源是 `usecase.PeriodNav`**（`Default` / `Resolve` / `ResolveForList` / `Nav`）：annual → 去年，monthly → 上月，quarterly（以及任何非法值）→ 上季度；`type` 与 `period` 对不上 → 退回该 `type` 的默认周期。`handler` 里的 `parsePeriodFromQuery` / `defaultPeriodFor` / `txListPeriod` 是纯委托，不得再写自己的判断；`StatsAPI` 先把 `month/quarter/year` 短别名翻成 `PeriodType` 再交给它。`/api/v1` 与 SSR 共用同一个 `PeriodNav`。
- **客户端只消费，不计算**：每个带周期的 `/api/v1` 响应都内嵌 `period{type,key,label,prev,next,has_next}`，翻页用 `prev` / `next`，`has_next=false` 时禁用「下一期」，换粒度则只传 `type` 不传 `period`、让服务端给默认周期。**目前只有流水页（`tx_table.js`）已按此切换**，它不再依赖 `period_utils.js`。
- **仍未统一的部分（如实记录）**：仪表盘（`dashboard_page.js`）与统计页（`stats_page.js`）还在用 `period_utils.js` 里的 `defaultPeriodKey` / `shiftPeriodKey`——那是前端的另一份实现。在这两页切到 `/api/v1` 之前，改默认周期规则或进位逻辑**仍须两处同步**（`PeriodNav` 与 `period_utils.js`），否则这两页首屏 SSR 的周期和 Alpine 接管后显示的周期会打架。这两页切过去之后，`period_utils.js` 里这两个函数就能删，本节也随之只剩服务端一处。
- **唯一例外**：流水页带 `?rule_id=`（从「分类规则」页点「查看流水」跳过来，且 URL 里既没有 `type` 也没有 `period`）时改用**当前季度**，规则在 `PeriodNav.ResolveForList`（`ForRuleView`）。这里不能复用 `Default`——它给的是上一个完整季度，同样盖不住当月刚导入待核对的那批流水，页面会误报"这条规则没匹配到任何流水"。URL 显式给了 `type` 或 `period` 时一律以显式为准。流水页 SSR 在 `type` 缺省时补 `monthly`（`TxQuery` 自己的缺省粒度是季度），唯独「带 `rule_id` 且 type/period 都没给」的情形不补，留给 `ResolveForList`。

### 数据库

- 驱动 `modernc.org/sqlite`（纯 Go，无 CGO）。DSN 带 `journal_mode=WAL` + `foreign_keys=1` + `busy_timeout=5000`。
- 迁移用 `pressly/goose`，SQL 文件放 `internal/infrastructure/sqlite/migrations/NNN_*.sql`（当前到 `015`），`//go:embed migrations/*.sql`；新增迁移直接加编号更高的文件，服务器启动自动 `goose up`。每个迁移都要写 `-- +goose Down`。
- 主要表：`transactions`、`categories`、`category_rules`、`import_batches`、`imported_transaction_keys`、`asset_snapshots`、`financial_goals`、`insurance_policies`、`family_profile`、`budgets`、`digest_settings`、`import_templates`、`special_projects`（迁移 014 新建，专项开支，已投入使用）、`reports`（AI 财报与 AI 建议共用一张表，靠 `period_type='advice'` 区分；迁移 015 加了 `data_scope` 标注每份存档的统计口径，默认 `'all'` 正是存量行的真实口径）。新增用例优先复用这些表而不是再建。
- **红线：任何新增的、带口径过滤的聚合，都必须保证 `special_id` 落在它走的覆盖索引里。** 迁移 013 为 `AggregateByCategory` 建了 `idx_tx_category_occurred (category_id, occurred_at, status, amount)`（100k 行实测季度聚合 165ms→2.9ms、年度 664ms→12ms）；014 给聚合 SQL 加上 `special_id` 过滤后必须把该索引重建成五列 `(category_id, occurred_at, status, special_id, amount)`，否则每行都要回表，013 的优化直接作废。`migration_014_test.go` 与 `query_plan_test.go` 用 `EXPLAIN QUERY PLAN` 钉死了"必须命中 COVERING INDEX"。
- **启动时跑一次 `sqlite.Analyze(db)`（即 `ANALYZE`）**，写在 `Migrate` 之后。原因：没有统计信息时 SQLite 只能按固定比例猜选择度（`special_id IS NOT NULL` 猜 1/4、`status='confirmed'` 猜 1/10），于是所有专项聚合都去走 `idx_tx_status` 扫十分之一张表，014 专门建的 `idx_tx_special` **永远不会被选中**。100k 行实测 `SumByProject` 49.5ms→6.7ms、月/季对比条的专项那一组 87.0ms→10.4ms。不写死 `INDEXED BY`（收益随专项占比变化，该由代价优化器按当下数据决定），也不用 `PRAGMA optimize`（它只考虑本连接查过的表，而 `database/sql` 是连接池，触发时机不可控）。失败不致命，只是查询计划变差。

## 协作约定

- UI 文本、错误信息、模板注释用简体中文；代码注释按需简短。
- 新增 Repository 方法：先在 `internal/port/` 加接口，再在 `internal/infrastructure/sqlite/` 加实现，usecase 通过接口依赖。宽接口加方法的代价由所有替身承担，新增查询能力优先开窄接口（现成的例子是 `port.TransactionQueryRepo`）。
- 新增聚合查询：先决定统计口径（见上方「统计口径（Scope）」），基线类的一律传 `domain.ScopeDaily`；SQL 里加了 `special_id` 过滤就检查覆盖索引。
- 新增分类规则：优先在"分类规则"页面维护，规则存入 `category_rules` 表；需要默认自带的规则时新增迁移种子。LLM 只是兜底，不要把"期望永远命中"的规则丢给它。
- 新增账单来源：固定版式的平台账单在 `internal/adapter/bill/` 下加 `xxx.go` 实现 `Parser` 接口，然后在 `bill.go` 的 `ParserFor` 里注册，再在 handler 上传表单里加 option；版式随手而变的第三方 CSV 优先用 `/imports/csv` 的列映射模板，不必新写解析器。
