# 账单导入 + 流水编辑 实施计划

## 决策摘要（已对齐）

- 导入**替换**现有手填入口：`/transactions/new` 改为上传页；手填入口不再保留。
- 导入**直接入库**（`status=pending_review`），列表页就地改分类 + 备注，无中间预览页。
- 分类：**本地规则** 优先；未命中行先 `category_id=NULL` 入库，后台 goroutine 异步调 LLM 兜底（无 API key 时保持 NULL）。
- 列表页排序/筛选/汇总：**前端 Alpine.js** 全量渲染，不走 query。
- xlsx 解析：**excelize/v2**。
- 时间范围：**全部入库**，不按 Period 过滤。列表页继续用现有季度/年度选择器筛。
- 去重：`imported_transaction_keys(source, transaction_no)` 唯一键，重复直接跳过。

## 阶段 1 — 解析 + 导入骨架（同步可用）

- [ ] `go.mod` 加 `github.com/xuri/excelize/v2`；`go mod tidy`。
- [ ] 新增 `internal/domain/bill.go`：`RawBillRow`（统一中间结构：Source、OccurredAt、Counterparty、Description、Amount 分、Direction、TransactionNo、RawCols map）。
- [ ] 新增 `internal/adapter/bill/` 解析器：
  - `alipay.go`：GB18030 → UTF-8（用 `golang.org/x/text/encoding/simplifiedchinese` — 已是 `modernc.org/sqlite` 的间接依赖，否则用 `go mod tidy` 补齐）。跳过前 24 行直到找到表头 `交易时间,`；只取 `交易状态=交易成功` 且 `收/支 ∈ {收入, 支出}` 的行（过滤"不计收支"）。把支付宝"交易分类"存进 `RawCols["alipay_category"]` 以便规则用。
  - `wepay.go`：excelize 打开，找到以"交易时间"开头的表头行，向下读取；过滤 `当前状态 ∈ {支付成功, 已转账, 已收钱}` 且 `收/支 ∈ {收入, 支出}`。
- [ ] 扩展 `internal/port/repository.go`：
  - `TransactionRepo.InsertBatch(ctx, txs []domain.Transaction, keys []domain.ImportedKey) (inserted, skipped int, err error)`：一个 tx 里先查 `imported_transaction_keys` 去重，再 insert transactions + insert keys。
  - 新增 `TransactionRepo.UpdateCategory(ctx, id string, categoryID *string, note string)`（note 写入 `description`；或在 transactions 加 `note` 字段——见阶段 4）。
- [ ] `internal/infrastructure/sqlite/transaction_repo.go` 实现 `InsertBatch`（`BEGIN`/defer rollback/`COMMIT`）。
- [ ] 新增 `internal/usecase/import_bill.go`：入参 `Source` + `io.Reader` + filename，调对应解析器 → 规则分类 → 生成 `domain.Transaction`（`ImportBatchID` 用新 UUID 写入 `import_batches`）→ `InsertBatch`。返回 `ImportResult{TotalRows, InsertedRows, SkippedRows, PendingCategoryRows}`。
- [ ] 新增 `internal/usecase/classify_rules.go`：`ClassifyByRules(row RawBillRow, cats []Category) (categoryID string, ok bool)`。规则表（硬编码在 Go map 里，不读 DB 的 category_rules 表，后续再迁）：
  - alipay：`RawCols["alipay_category"]` → 二级科目映射（餐饮美食→necessary.food，交通出行→necessary.transport，日用百货→necessary.home，运动户外→discretion.leisure，购物→discretion.shopping，医疗健康→necessary.medical，文化休闲→discretion.leisure，亲友代付/转账→跳过或 NULL 等）。
  - wepay：按 `交易对方 + 商品` 关键词匹配（"深圳通/地铁/哈啰/滴滴"→transport，"美宜多/盒马/超市"→home，"餐饮/咖啡/luckin/茶饮"→food，"加油"→transport，"理发"→discretion.shopping 等）。规则列表放 `internal/usecase/classify_rules.go` 顶部，注释标明怎么扩。
- [ ] Handler 层：
  - 删掉 `GET /transactions/new` 和 `POST /transactions` 单笔手填（连同 `AddTransaction` usecase、对应模板）。
  - 新增 `GET /imports`：上传页（`source` radio: wechat/alipay；`file` input；提交到 `/imports`）。
  - 新增 `POST /imports`：`r.ParseMultipartForm(10<<20)` → 根据 source 调 import_bill usecase → flash 结果 → 302 到 `/transactions`。
  - 路由 main.go 相应调整；顶部导航「录入流水」改为「导入账单」。

## 阶段 2 — 流水列表就地编辑（Alpine.js）

- [ ] `GET /transactions` 的 VM 新增分类元数据（分组列表、二级列表），用 `application/json` 序列化后放 `<script type="application/json" id="data-transactions">` 给 Alpine 读取。
- [ ] 模板 `pages/transactions.html` 重写为 Alpine 组件：
  - 表头列：日期 / 来源 / 对方 / 说明 / 金额 / 方向 / 分类 / 备注 / 状态。每列 `@click` 切换排序方向（与原序/升/降三态）。
  - 顶部筛选条：分类多选（按分组折叠）、来源 chip、方向 toggle、关键词搜索。
  - 底部汇总行：按当前筛选结果 sum(income)、sum(expense)、net。
  - 每行"分类"列是 `<select>`，按分组 optgroup；"备注"列是 `<input>`，blur/Enter 触发 PATCH。
- [ ] 新增 `PATCH /transactions/{id}`（或 `POST /transactions/{id}/update`）：接收 `category_id`、`note`、`status`（用户可把误导入的设为 `excluded`）。Alpine 里用 `fetch` 发。响应返回 204 或新行 JSON。
- [ ] 新增 `TransactionRepo.UpdateCategory` 实现；新增 `TransactionRepo.UpdateStatus`。
- [ ] `transactions` 表需要 `note` 列（当前用 `description` 存了商品说明，不能覆盖）→ 新增 migration `003_add_transaction_note.sql`（`ALTER TABLE transactions ADD COLUMN note TEXT`）。

## 阶段 3 — LLM 后台异步分类

- [ ] 新增 `internal/adapter/llm/openai.go`：OpenAI 兼容 chat completions 客户端，入参系统 prompt + batch of rows，输出 `[{id, category_id}]`。
- [ ] 新增 `internal/usecase/classify_pending.go`：`ClassifyPending(ctx, batchSize)`，查 `status=pending_review AND category_id IS NULL` 的行，分批调 LLM，写回 `category_id`。
- [ ] `main.go` 启动一个 goroutine：`ticker := time.NewTicker(30s)`，每次触发 `ClassifyPending`；导入 handler 成功后也 `go uc.Trigger()`（非阻塞唤醒 channel）。若 `OpenAIAPIKey == ""` 直接不启动 goroutine。
- [ ] 失败行记一次 `classify_attempts++`（在 transactions 表加一列，或直接存在内存；重试 3 次后放弃）。

## 阶段 4 — 清理

- [ ] 删除 `internal/usecase/add_transaction.go` 和相关模板 `transaction_new.html`。
- [ ] 删除 `internal/domain/transaction.go` 里没用到的字段？— 保留，导入批次要用。
- [ ] `CLAUDE.md` 更新：新增「账单导入流程」与「LLM 分类 goroutine」两节。

## 验收路径

1. `go run ./cmd/server` 起服务，点导航「导入账单」。
2. 传 `./data/alipay.csv`（source=alipay），提示"导入 N 条 / 跳过 M 条"，跳转流水列表。
3. 重复上传同一文件 → 全部跳过。
4. 传 `./data/wepay.xlsx`（source=wechat），同上。
5. 列表页切到 `2025Q4` / `2026Q1`，点列头排序可用；选"餐饮食材"筛选后汇总行正确。
6. 某行 `<select>` 改分类 / 备注输入 → 刷新页面仍在。
7. 若配置了 `OPENAI_API_KEY`，等 30s 后 NULL 分类行被异步填上。

## 实施回顾（2026-05-09）

### 已完成
- 阶段 1：`domain/bill.go` + `adapter/bill/{alipay,wechat}.go` + `usecase/classify_rules.go` + `usecase/import_bill.go`；`transaction_repo.go` 重写以支持 `InsertBatch`/`Update`/`ListPendingCategory` 等接口；迁移 003 加 `note` 列；旧的 `add_transaction` usecase 和 `transaction_new.html` 已删。
- 阶段 2：`pages/transactions.html` 重写为 Alpine 组件，`static/js/tx_table.js` 实现排序/筛选/汇总/就地改分类+备注；`PATCH /transactions/{id}` 接口；新增 CSS for toolbar & inline editors。
- 阶段 3：`adapter/llm/openai.go` OpenAI 兼容客户端；`usecase/classify_pending.go` 后台 goroutine；`main.go` 用 `signal.NotifyContext` 优雅关停，导入成功 trigger 唤醒 LLM。
- 阶段 4：CLAUDE.md 更新三大节：账单导入流程 / LLM 异步分类 / 流水列表就地编辑。

### 验收过的路径（本地 curl）
- `POST /imports` alipay + wechat 上传都成功，Flash 提示"新增 X 条/跳过重复 Y 条/忽略转账 Z 条/未分类 W 条"。
- 重复上传同文件 → 428 条全跳过。
- `/transactions?period=2025Q1` 渲染 134 条，未分类仅 2 条（规则覆盖率好）。
- `PATCH /transactions/{id}` body `{"note":"...","category_id":"..."}` 返回 204；列表里 note 持久化，category 也变更且 status 自动 confirmed。

### 踩过的坑（已捕获到 CLAUDE.md）
- Go html/template 在 `<script type="application/json">` 里会把字符串值再 JS-escape，必须用 `{{rawJSON .X}}` (template.JS) 绕过。
- 本机代理 `HTTP_PROXY=127.0.0.1:15236` 没配 `NO_PROXY=localhost`，curl 打 localhost 也要 `--noproxy '*'`。
- excelize 新版 v2.10+ 要求 Go 1.25，本机只有 1.24.1，锁定到 v2.8.1 + go.mod `go 1.23`。

## 风险 / 开放问题

- 支付宝"转账"类（交易分类=转账、交易对方=某个人）应跳过还是归到某科目？默认**跳过**（不入库，skipped+1），避免污染季报。导入结果里单独回显"忽略转账 N 笔"。
- 微信"中性交易"（零钱通存取、信用卡还款等）：默认**跳过**。
- 支付宝 CSV 尾部有多行空白行 + "------" 分隔线，解析要以"非 '2025-/2026-' 开头"作为数据结束标志。
- alipay 订单号字段末尾有 `\t` 字符（为防 Excel 科学计数法），入库前 trim。
