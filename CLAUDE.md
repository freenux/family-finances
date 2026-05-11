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

# 测试（项目当前没有 *_test.go，建议新代码带 table-driven 测试）
go test ./...
go test ./internal/usecase -run TestXxx -v

网页测试使用playwright-cli **不要使用**curl测试
```

`.env`（参见 `.env.example`）控制 `OPENAI_API_KEY/BASE_URL/MODEL`、`SERVER_ADDR`（默认 `:8787`）、`DATABASE_PATH`（默认 `./family.db`）。未设 `.env` 也能跑，但大模型相关功能会失效。

README 里的 `uv run python -m wepay_classifier ...` 是独立的 Python 微信账单预分类脚本（尚未进仓，仅 `.venv/` 和 `.wepay_llm_cache.json` 存在），与 Go 后端解耦。

## 架构

### 分层（Clean Architecture，禁止跨层反向依赖）

```
cmd/server/main.go         组装入口：config → sqlite.Open → Migrate → repos → llm.Client → usecases → renderer → handler → chi router；
                           同时起一个 goroutine 跑 ClassifyPending.Run（LLM 未配置则 no-op 返回）
internal/
  domain/                  纯领域模型（Transaction / Category / Period / ReportData / KPI / RawBillRow / ImportBatch）
  port/                    Repository 接口 + ImportRow / TransactionUpdate / ImportResult 数据对象
  usecase/                 QueryReport、ImportBill、ClassifyByRules、ClassifyPending（LLM）
  infrastructure/
    config/                env/godotenv 加载 Config
    sqlite/                sql.DB + goose 迁移 + 各 Repo 实现
    sqlite/migrations/     goose SQL，通过 //go:embed 嵌入二进制
  adapter/
    bill/                  账单解析器（alipay.csv / wepay.xlsx → []RawBillRow）
    llm/                   OpenAI 兼容 chat completions 客户端
    web/
      render.go            html/template 渲染器，template/ 与 static/ 均 //go:embed
      handler/             HTTP handler（chi），Request → usecase → ViewModel → 模板
      template/{layout,pages,partials}/
      static/{css,js}/     app.css + tx_table.js（Alpine 组件）
```

关键规则：`domain` 不引用任何其他包；`usecase` 只依赖 `domain` + `port` + 自己调用的 `adapter/llm` / `adapter/bill`（向下 OK，向上不行）；`infrastructure/sqlite` 实现 `port`；`adapter/web` 调 usecase 与 port，不反过来。

### 领域约定

- **金额单位是 `int64` 分（fen）**，整条链路不用 float。账单解析器把元乘 100 并 +0.5 转分；模板里用 `{{yuan .Amount}}` 格式化回元，前端 JS 用 `tx_table.js` 的 `fmtYuan`。
- **Period 标签格式**：季度 `"2025Q3"`、年度 `"2025"`。`domain.ParsePeriod` 是唯一解析入口；`Period.End` 是独占（`< End`），SQL 里对应 `occurred_at < ?`。
- **Category ID 用点分命名空间**：一级（`level=1`）是分组（如 `income.salary`、`expense.discretion`），二级（`level=2`）是真正的科目（如 `expense.discretion.shopping`）。聚合与模板靠 `parent_id` 与 `strings.HasPrefix(GroupID, "income.")/"expense."` 分流。
- **DiscretionRatio 告警阈值 35%**（`computeKPI` in `usecase/query_report.go`）。若要改阈值或新增 KPI，改那里一处即可。
- **Transaction.Status**：`pending_review | confirmed | excluded`。聚合 SQL 只算 `confirmed`。
  - 导入时：命中本地规则 → `confirmed`；未命中 → `pending_review`（等 LLM 或人工）。
  - 人工在列表页下拉改分类 / LLM 补上分类 → 自动转 `confirmed`。
  - 用户想彻底忽略某笔（如误记）→ 改为 `excluded`，不参与季/年报。
- **Transaction.Note**：用户在流水列表就地填的备注，与 `Description`（来自账单的商品说明）分开存，不要覆盖。
- **唯一键防重**：`imported_transaction_keys(source, transaction_no)` 由 `InsertBatch` 在事务内检查。重复导入同一文件全部跳过。

### 账单导入流程

1. `GET /imports` 上传表单（source ∈ {alipay, wechat}，文件 multipart）。
2. `POST /imports` → `ImportBill.Execute`：
   - `adapter/bill/ParserFor(source)` 得到解析器；alipay 走 GB18030 → UTF-8 → `encoding/csv`；wechat 走 `excelize.OpenFile`（先拷贝到临时文件）。
   - 解析器跳过元信息头，找 `交易时间` 表头行后逐行解析，过滤非交易成功 / 不计收支。输出 `[]RawBillRow`。
   - `ClassifyByRules(row)` 本地规则分类：alipay 直接映射"交易分类"（`alipayCategoryMap`）；wechat 按关键词（`wechatKeywordRules`，顺序敏感）。规则命中空字符串 → 视为"应跳过"（转账/中性交易），不入库；未命中 → `category_id=NULL`, `status=pending_review`。
   - `TransactionRepo.InsertBatch` 一个事务里：对每行 `SELECT FROM imported_transaction_keys` 去重 → `INSERT transactions` + `INSERT imported_transaction_keys` → 最后 `INSERT import_batches`。
   - 成功后调 `uc.trigger()`（在 main.go 里绑定到 `ClassifyPending.Trigger`）唤醒 LLM 后台兜底。
3. Flash cookie 回显结果，302 到 `/transactions`。

### LLM 异步分类

- `adapter/llm/Client` 是 OpenAI 兼容 Chat Completions 客户端，带 `response_format: json_object`。`OPENAI_API_KEY` 为空时 `Enabled()=false`。
- `ClassifyPending.Run(ctx, 30s, batch=20)` 在 main.go 里 goroutine 启动：每 30s 或被 `Trigger()` 唤醒跑一轮；取 `status=pending_review AND category_id IS NULL` 前 20 条，一次 prompt 让 LLM 输出 `{assignments:[{id,category_id}]}`，校验 category_id 必须在我们的二级科目白名单里，否则丢弃。
- 只向 LLM 暴露 `id / counterparty / description / direction`——不给金额和时间，避免误导。
- 分类成功后自动把 `status` 转为 `confirmed`。

### 流水列表就地编辑

- `pages/transactions.html` 里嵌入两个 `<script type="application/json">`：`data-transactions` 和 `data-categories`。
- **重要**：嵌入 JSON 必须用 `{{rawJSON .X}}`（在 `render.go` 的 `funcMap` 里定义为 `template.JS`）。否则 Go `html/template` 在 `<script>` 上下文里会把整个 JSON 字符串再字符串化，前端 `JSON.parse` 会失败。
- Alpine 组件 `txTable()`（`static/js/tx_table.js`）读取这两个 JSON，处理排序（点击列头三态）、筛选（方向/来源/状态/分类/关键词）、底部汇总（收入/支出/净额实时重算）。
- 行内 `<select>`/`<input>` change/blur 触发 `fetch PATCH /transactions/{id}`，body `{category_id?, note?, status?}`。PATCH 一旦提交 `category_id` 非空，后端自动把 `status` 置为 `confirmed`。失败时前端回滚本地状态并 `alert`。

### HTTP / 渲染

- chi router，中间件 `Recoverer` + `Logger`。路由见 `cmd/server/main.go`（`GET /`, `GET /transactions`, `GET /transactions/new` → 301 `/imports`, `PATCH /api/transactions/{id}`, `GET/POST /imports`, `GET /partials/report`）。
- **注意**：chi 的 trie 里 `{id}` 占位段会"吞掉"同级字面量段（即使不同 method），所以页面路由 `/transactions/{something}` 故意避免注册，API 用 `/api/transactions/{id}` 分开。
- 模板组织：`base.html`（layout，定义 `{{define "base"}}`）+ 每个 `pages/*.html`（定义 `{{define "content"}}` 和 `{{define "page"}}{{template "base" .}}{{end}}`）+ `partials/*.html`（独立 `define`，HTMX 局部刷新用）。
- `Renderer.RenderPage(w, "dashboard", vm)` 渲染整页；`RenderPartial(w, "report_view", vm)` 给 HTMX 返回片段。新加页面时按 `pages/` 文件名即为 key，模板自动被 `NewRenderer` 注册。
- 模板函数：`yuan`、`pct`、`formatDate`、`categoryName`、`rawJSON`（见上方"流水列表就地编辑"一节）。新增函数加到 `render.go` 的 `funcMap`。
- `alpinejs` + `htmx` 通过 CDN 在 `base.html` 引入；自有 JS 放 `static/js/` 并在 `base.html` `<head>` 里 `defer` 引入。

### 数据库

- 驱动 `modernc.org/sqlite`（纯 Go，无 CGO）。DSN 带 `journal_mode=WAL` + `foreign_keys=1` + `busy_timeout=5000`。
- 迁移用 `pressly/goose`，SQL 文件放 `internal/infrastructure/sqlite/migrations/NNN_*.sql`，`//go:embed migrations/*.sql`；新增迁移直接加编号更高的文件，服务器启动自动 `goose up`。
- Schema 已预建了仓库里尚未使用的表：`import_batches`、`imported_transaction_keys`、`category_rules`、`asset_snapshots`、`financial_goals`、`insurance_policies`、`reports`——这些是 `TO-AGENT.md` 里功能点（账单导入、AI 财报、资产快照、保单、财务目标）的落点。新增用例时优先复用这些表而不是再建。

## 协作约定

- UI 文本、错误信息、模板注释用简体中文；代码注释按需简短。
- 新增 Repository 方法：先在 `internal/port/` 加接口，再在 `internal/infrastructure/sqlite/` 加实现，usecase 通过接口依赖。
- 新增分类规则：优先改 `internal/usecase/classify_rules.go`（快、白盒、可测）。LLM 只是兜底，不要把"期望永远命中"的规则丢给它。
- 新增账单来源：在 `internal/adapter/bill/` 下加 `xxx.go` 实现 `Parser` 接口，然后在 `bill.go` 的 `ParserFor` 里注册，再在 handler 上传表单里加 option。
