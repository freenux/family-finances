# 开发设计文档:P0 资产端地基 + P1 AI 建议核心

> 依据:`docs/product-research.md` **v2**(commit 03c3f8e,含第六节评审合议)+ `prototypes/*.html` 原型(已评审通过)+ `CLAUDE.md` 架构约定。
>
> 原型与 v2 不一致处,**以本文档(即 v2 口径)为准**:四笔钱按"用途/流动性分层"诊断而非三桶占比区间;配置方案由确定性引擎输出**区间**、LLM 只解释;再平衡以"偏出区间"触发而非 ±pp 点值阈值;全量导出提前到 P0。

## 0. 铁律(适用于所有新功能)

1. **确定性计算 + AI 解释**:金额、比例、覆盖月数、同环比、目标缺口、配置区间一律由 Go 代码计算;LLM 只解释、叙述、生成行动选项,不发明数字。
2. LLM 输出一律 `response_format: json_object`,服务端结构校验 + 白名单裁剪后**落库**,页面渲染存库结果,不做流式。
3. 每条 AI 结论必须带 `refs` 字段,标注引用了上下文包里的哪些数据键;refs 不在白名单内的条目整条丢弃。
4. 隐私:发给 LLM 的上下文只含聚合数与快照科目余额,不含交易对手方、单笔明细。
5. AI 输出页脚固定免责声明:"由大模型基于家庭聚合数据生成,仅作教育与参考用途,不构成投资建议"。

## 1. 里程碑

| 里程碑 | 内容 | 状态 |
|---|---|---|
| **M1(本次实现)** | P0:① 资产快照 `/assets` ② AI 季/年财报 `/reports` ③ 全量导出 `/export` | 待开发 |
| **M2(下一次)** | P1:④ 风险画像 ⑤ 四笔钱分层诊断 ⑥ 配置区间引擎 + LLM 解释 ⑦ 再平衡卡片 | 本文档给出设计,暂不实现 |
| P2/P3 | 不在本文档范围 | — |

## 2. 通用约定(摘自 CLAUDE.md,必须遵守)

- 分层:`domain`(纯模型,零依赖)→ `port`(接口)→ `usecase` → `infrastructure/sqlite`(实现 port)/ `adapter/web`(调 usecase)。禁止反向依赖。
- **金额一律 `int64` 分**;模板用 `{{yuan .X}}`,前端 JS 用 `fmtYuan`。
- Period 标签 `2026Q2` / `2026`,唯一解析入口 `domain.ParsePeriod`;`Period.End` 独占。
- 迁移:新增 `internal/infrastructure/sqlite/migrations/NNN_*.sql`(goose,编号接在现有 006 之后),启动自动 up。
- 路由:页面路由避免注册 `/xxx/{id}`(chi trie 会吞同级字面量),API 走 `/api/` 前缀;新路由加入 `cmd/server/main.go` 认证 group。
- 模板:`pages/xxx.html` 定义 `content` + `page`,文件名即 renderer key;嵌 JSON 必须 `{{rawJSON .X}}`;自有 JS 放 `static/js/` 并在 `base.html` defer 引入。
- UI 文本简体中文;新增 Repository 方法先在 `internal/port/` 加接口。
- 网页验证用 playwright(禁 curl);新代码带 table-driven 测试;`go test ./...`、`go build ./cmd/server` 必须通过。
- 视觉:完全沿用 `app.css` 既有 class(kpi-grid/section/ledger/btn/badge/form-grid 等),原型 `prototypes/p0.html` 是版式基准,可按需在 app.css 追加少量样式。

## 3. M1-1 资产快照页 `/assets`

### 领域模型(internal/domain/asset.go)

```go
type AssetAccount struct { Code, Name, Group string; Sort int } // Group: "asset" | "liability"

// 科目目录为代码内常量(不进 categories 表——categories 与交易/规则耦合,资产科目无交易语义)。
// 权益细分为境内/海外/黄金,为 M2 长期桶内配置诊断预留,避免日后迁移数据。
var AssetCatalog = []AssetAccount{
  {"asset.cash",          "现金及活期存款", "asset", 1},
  {"asset.mmf",           "货币基金(余额宝/零钱通)", "asset", 2},
  {"asset.deposit",       "定期存款", "asset", 3},
  {"asset.wealth",        "银行理财/债券基金", "asset", 4},
  {"asset.equity_cn",     "境内股票及偏股基金", "asset", 5},
  {"asset.equity_global", "海外权益(QDII等)", "asset", 6},
  {"asset.gold",          "黄金及另类", "asset", 7},
  {"asset.pension",       "公积金/养老金账户", "asset", 8},
  {"asset.house",         "自住房产(估值)", "asset", 9},
  {"liability.mortgage",  "房贷余额", "liability", 10},
  {"liability.consumer",  "信用卡/消费贷", "liability", 11},
}

type AssetSnapshot struct {
  ID string; Period string; SnapshotDate time.Time
  Data map[string]int64 // code -> 分;只允许 AssetCatalog 中的 code,未知 code 保存时报错
  NetWorth int64        // 资产合计 − 负债合计,usecase 计算后冗余存列
}
```

### 存储

复用已有表 `asset_snapshots(id, period UNIQUE, snapshot_date, data TEXT, net_worth, created_at)`;`data` 存 JSON 对象 `{"asset.cash":5800000,...}`(单位分)。

- port:`AssetSnapshotRepo { Upsert(ctx, *AssetSnapshot) error; GetByPeriod(ctx, period string) (*AssetSnapshot, error); ListByPeriodAsc(ctx, limit int) ([]*AssetSnapshot, error) }`
- 实现:`internal/infrastructure/sqlite/asset_snapshot_repo.go`,Upsert 用 `INSERT ... ON CONFLICT(period) DO UPDATE`。

### 用例(internal/usecase/asset_snapshot.go)

- `SaveSnapshot(ctx, period string, data map[string]int64)`:校验 period(仅季度)与 code 白名单 → 计算 NetWorth → Upsert。
- `SnapshotView(ctx, period)`:返回当季快照(可为空)、上一季快照(供"从上季复制"与环比)、近 12 季净值序列 `[]{Period, NetWorth}`。

### HTTP 与页面

| 路由 | 说明 |
|---|---|
| `GET /assets?period=2026Q2` | 整页;period 缺省 = 当前季度 |
| `PUT /api/assets/{period}` | body `{"data":{"asset.cash":5800000,...}}`,保存并返回 `{net_worth, totals}` |
| `GET /api/assets/{period}/prev` | 返回上季 data,前端"从上季复制"用 |

页面 `pages/assets.html`(版式对照 `prototypes/p0.html` ①):KPI 四卡(总资产/总负债/净资产/环比)+ 左侧可编辑科目表(按 Group 分组、小字显示 code)+ 右侧净值 SVG 曲线。数据经 `{{rawJSON}}` 嵌入,交互放 `static/js/assets.js`(Alpine 组件,参考 `tx_table.js` 风格):就地编辑重算合计、保存按钮调 PUT、曲线悬停 tooltip。

### 验收

录入两个季度快照 → 曲线两点、环比正确;同季重复保存为覆盖;非法 code / 非季度 period 返回 400;`go test`:NetWorth 计算、code 校验、Upsert 幂等(table-driven)。

## 4. M1-2 AI 季/年财报 `/reports`

### 上下文包(internal/usecase/context_pack.go)

新类型 `ContextPack`,序列化为稳定 JSON,M2 的配置解释与 P3 对话复用同一构造函数:

```json
{
  "period": "2026Q2", "period_type": "quarter",
  "income":   {"total_fen":..., "groups":[{"category_id":"income.salary","name":"工资","amount_fen":...}]},
  "expense":  {"total_fen":..., "groups":[...]},
  "kpi":      {"savings_rate":0.352, "discretion_ratio":0.31, "discretion_alert":false},
  "compare":  {"prev_period":"2026Q1", "income_delta":..., "expense_delta":..., "net_worth_delta":...},
  "snapshot": {"period":"2026Q2","net_worth_fen":..., "items":{"asset.cash":...}},   // 无快照则为 null
  "findings": [ {"key":"savings_rate_3q","text":"结余率连续3季≥30%"}, {"key":"overspend_shopping","text":"购物类环比+24%"} ]
}
```

`findings` 是**确定性诊断**(铁律 1):M1 至少实现——结余率及连续性、支出类目环比 Top 变动、可自由支配占比告警、净资产环比、活钱覆盖月数(有快照时;活钱 = asset.cash + asset.mmf,月均支出 = 近 4 个季度支出均值/3)。每条有稳定 `key`,作为 LLM refs 白名单。收入/支出只给**类目聚合**,不含单笔与对手方(铁律 4)。

### 生成与校验(internal/usecase/generate_report.go)

1. 组包 → prompt(系统提示:只解释、每条结论给 refs、不得编造数字)→ `llm.Client`(json_object)。
2. 期望输出:`{"summary":"...","highlights":[{"text":"...","refs":["savings_rate_3q"]}],"risks":[...],"advice":[...]}`。
3. 校验:结构完整、每条 refs ⊆ findings/kpi/compare 键集合,违规条目丢弃;summary 里出现的数字不校验但 prompt 要求"数字必须来自上下文包"。
4. 落库 `reports`:income_data/expense_data/kpi_data/comparison 存包内对应 JSON,ai_prompt/ai_analysis/ai_model 照存,status='final'。同 (period, period_type) 重新生成 = 覆盖(表有 UNIQUE)。
5. `llm.Enabled()==false`:页面正常浏览历史,生成按钮禁用并提示"未配置 OPENAI_API_KEY"。

### HTTP 与页面

| 路由 | 说明 |
|---|---|
| `GET /reports?period=2026Q2` | 列表(历史财报,读库)+ 当前选中财报渲染 |
| `POST /reports/generate` | form: period;同步生成(LLM 单次调用,30s 超时)→ 302 回 GET |

页面 `pages/reports.html` 对照原型 ②:期间选择 + 生成按钮 + 财报卡(数据速览 chips / 摘要 / 亮点 / 风险 / 建议,每条列表项后小字展示 refs 对应的 finding 文本)+ 右侧历史列表 + 页脚免责声明(铁律 5)。

### 验收

无 key:历史可回看、生成禁用;有 key(或用可注入的 fake client 测试):生成 → 落库 → 刷新读库不再调 LLM;refs 校验单测覆盖"违规条目被丢弃"。usecase 测试用 fake LLM(接口已在 `ClassifyPending` 有先例,沿用其注入方式)。

## 5. M1-3 全量数据导出 `/export`

| 路由 | 内容 |
|---|---|
| `GET /export` | 页面:三张卡说明 + 下载按钮 |
| `GET /export/transactions.csv` | 全量流水 CSV,UTF-8 **带 BOM**(Excel 兼容),列:`id,occurred_at,source,account,direction,amount_fen,amount_yuan,category_id,category_name,description,counterparty,note,status` |
| `GET /export/full.json` | `{exported_at, transactions, categories, category_rules, asset_snapshots, reports, import_batches}`,`json.Encoder` 流式写出 |

Beancount 导出按 v2 留在 P3,不做。实现放 `handler` + 一个 `usecase/export.go`(遍历 repo,避免 handler 直接摸 SQL);需要在 port 补 `ListAll` 类方法时按约定先加接口。验收:两个端点可下载,CSV 行数 = transactions 行数,中文不乱码(带 BOM),JSON 可被 `jq` 解析。

## 6. M2(P1)设计概要 — 本次不实现,评审后另行开工

- **迁移 007**:`family_profile` 单行表(id 固定 'default'):family_structure, main_age, income_stability, annual_income_fen, mortgage_monthly_fen, monthly_expense_fen(可空=用流水自动值), emergency_months, risk_appetite, horizon, updated_at。
- **画像**:`/profile` 一页表单;`CalcProfileGrade(profile) (grade C1..C5, equityCapPct)` 纯函数 + table-driven 测试(评分:偏好 0/3/6 + 年限 0/1/2 + 稳定性 0/1/2 − 年龄惩罚,映射 C1–C5)。
- **四笔钱分层诊断** `usecase/bucket_engine.go`(全部确定性,v2 口径):
  - 活钱 = cash+mmf → 覆盖月数 vs 画像目标月数;
  - 稳健 = deposit+wealth vs 未来 3 年已知大额支出(financial_goals 中 3 年内目标 + 画像字段);
  - 长期 = equity_cn+equity_global+gold,**桶内**再算权益/海外/黄金占比;
  - 保障:insurance_policies 双十检查(保费/收入、寿险保额倍数),**不算占比桶**,无保单则提示"未登记"。
- **配置区间引擎** `usecase/allocation_engine.go`:输入画像 + 长期桶现状 → 输出长期桶内目标**区间**(如权益 40–55%、海外 ≤长期桶 25%、黄金 ≤10%)+ 显式假设列表(每条假设一个 key)。table-driven 测试为主交付物。
- **LLM 解释**:ContextPack + 引擎输出 → LLM 生成解释与行动选项(每项含代价说明、refs)→ 校验落库(复用 reports 表,period_type='advice')。输出范式:系统计算 → AI 解释 → 可选动作及代价。
- **再平衡**:SaveSnapshot 后同步计算引擎区间 vs 当前,**偏出区间**即生成 dashboard 卡片(存内存/查询时现算均可,倾向查询时现算,无新表)。

## 7. 交付与验证清单(M1)

1. `go build ./cmd/server` && `go test ./...` 全绿;新 usecase 均有 table-driven 测试。
2. 启动服务(临时 DATABASE_PATH,如 `/tmp` 下),用 playwright 驱动:登录 → `/assets` 录入保存 → 刷新数据仍在 → `/reports` 页可开 → `/export` 两个文件可下载。**不要用 curl 测页面**。
3. 迁移无新增(M1 复用现有表);若确需调整表结构,新增 goose 迁移,禁止改旧文件。
4. 提交:分支 `claude/product-prototype-html-8akn67`,git user = `Claude <noreply@anthropic.com>`,按功能分 2–3 个提交;push 若遇 403 权限错误,保留本地提交并在总结中说明,不要反复重试。
