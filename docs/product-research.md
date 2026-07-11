# 市场调研与产品方案：家庭理财数据 + AI 理财建议

> 2026-07 调研。产品定位：**自用为主、顺便开源的自托管家庭财务系统**，AI 深度做到**个性化资产配置方案**层（自用无投顾合规问题；开源分发时对外仅呈现为"教育/参考"性质并附免责声明）。

## 一、项目现状盘点

已完成：微信/支付宝账单导入（去重、GB18030/xlsx 解析）、规则 + LLM 兜底的自动分类、流水就地编辑（分类/备注/状态）、季度/年度报表聚合、KPI（可自由支配比例 35% 告警）、统计页、认证、Docker 部署。

Schema 已预留但未实现的落点：`asset_snapshots`（资产快照）、`financial_goals`（财务目标）、`insurance_policies`（保单）、`reports`（AI 财报）、`import_batches` 展示。**这四张表恰好对应本方案 P0–P2 的主体功能，不需要大改数据模型。**

## 二、市场格局：三类竞品

### 1. 开源自托管（直接竞品）

| 产品 | 定位 | 与本项目的关系 |
|---|---|---|
| **Firefly III** | 最成熟的自托管记账，复式记账、预算、规则引擎、报表、API | 功能天花板参考；无中国账单源，无原生 AI（靠第三方 AI Categorizer、社区 MCP server） |
| **Actual Budget** | local-first 信封预算，隐私优先 | 预算方法论参考；同样无中国场景 |
| **Maybe Finance / Sure（社区 fork）** | "AI + 个人财务"开源标杆：净值、投资、AI Chat（自带 OpenAI key 问自己的财务数据） | 2025-06 公司关停、仓库归档，留下**"开源 AI 个人财务"空缺**；其 AI Chat 交互是最值得抄的作业 |
| **Ghostfolio** | 开源财富管理/投资组合追踪（股票/ETF/加密），8k+ stars | 投资侧天花板：多资产、业绩分析、**再平衡工具**、基准对比 |
| **ezBookkeeping** | 国人写的轻量自托管记账 | 证明"中文 + 自托管"有真实需求，但无 AI、无资产配置 |
| **Beancount 中文生态**（double-entry-generator、Beancount-Trans） | 微信/支付宝账单 → 复式记账的脚本/平台 | 与本项目账单导入同源需求；用户全是技术人，体验门槛高——正是本项目 Web 化的机会 |

### 2. 海外商业 AI 记账（功能方向参考）

- **Monarch Money**（$99.99/年）：夫妻协作最佳；2026 年上线 AI Assistant（自然语言问财务）、AI Insights、**Weekly Recap 周报**。
- **Copilot Money**：AI 深度最佳（分类、洞察、可视化），仅 iOS。
- **PortfolioPilot**：AI 驱动的整体财务规划——退休规划、**1000 次蒙特卡洛压力测试**、历史危机情景模拟（2008/2020）、税务感知的月度建议。是"AI 个性化配置方案"做到极致的样板。
- **Origin / Richify / Era**：wealth planning + AI agent 教育向。

### 3. 国内产品（用户习惯与合规边界参考）

- **随手记**：老牌全家桶，广告多、理财推销、金融服务曾暴雷 → 用户对"记账 App 卖理财"信任度低。
- **钱迹**：简洁纯粹无广告，但个人向、无家庭/资产配置概念。
- **有知有行**：投资者陪伴 + 家庭财务总览 + 长钱/稳钱账户，2024 年底拿到基金销售牌照才敢做深。
- **且慢（盈米基金）**："**四笔钱**"框架首创者——活钱管理 / 稳健理财 / 长期投资 / 保险保障，是国内家庭资产配置最普及的心智模型。
- **支小宝 2.0（蚂蚁）**：国内首个大模型理财助理，行情分析、持仓诊断、资产配置、投教陪伴；从 18 亿组合中匹配个性化配置。证明"LLM 做资产配置建议"技术路径成立，但它绑定蚂蚁生态、数据在平台手里。

### 结论：竞争力与空白点

四个圈的交集没有人做：**① 中国账单源（微信/支付宝）② 自托管数据主权 ③ 家庭（而非个人）视角 ④ LLM 深度分析与配置建议**。

- 海外开源有 ③④ 没有 ①，且 Maybe 死后 ④ 也出现空缺；
- Beancount 生态有 ①② 没有 ③④，且门槛劝退非技术配偶；
- 国内商业产品有 ①③ 没有 ②，④ 受投顾合规限制只能泛泛而谈（有牌照的除外）；
- 本项目已有 ①② 的地基，补上 ③④ 即占住这个交集。

一句话定位：**"自托管的、懂微信支付宝账单的 Maybe Finance，内置四笔钱框架的 AI 家庭财务顾问。"**

## 三、功能缺口分析（按"数据依据 → AI 建议"链路排序）

AI 建议质量取决于喂给它的数据完整度。当前只有**收支流水**一条腿；资产配置建议需要**存量资产**这条腿，缺口按此排序：

| # | 缺口 | 竞品对标 | 现有落点 |
|---|---|---|---|
| 1 | **资产负债快照**：季度手填各账户余额（现金/存款/理财/股票基金/房产/公积金/负债），净值曲线 | Maybe 净值、Ghostfolio、有知有行家庭总览 | `asset_snapshots` 表已建 |
| 2 | **AI 季/年财报生成**（TO-AGENT 原始需求点 5，未完成） | Monarch Weekly Recap | `reports` 表已建 |
| 3 | **AI 资产配置分析与建议**：四笔钱视角诊断 + 个性化目标配置 + 再平衡差距 | 支小宝持仓诊断、PortfolioPilot、Ghostfolio 再平衡 | 依赖 #1 |
| 4 | **家庭风险画像**：一次性问卷（年龄/收入稳定性/负债/目标/风险承受）作为 AI 建议的输入 | 所有投顾类产品的 KYC | 新表或 config |
| 5 | **财务目标追踪**：教育金/购房/退休目标，进度 = 目标 vs 快照 | Monarch Goals、且慢 | `financial_goals` 表已建 |
| 6 | **保单管理**：保障缺口是四笔钱之一，AI 检查保额/保费比 | 且慢保险保障 | `insurance_policies` 表已建 |
| 7 | **预算**：分类月/季预算 vs 实际，超支进 AI 财报 | Actual/YNAB 信封预算 | 新表 |
| 8 | **周期交易识别**：订阅/房贷/保费自动识别，现金流预测 | Rocket Money 订阅追踪 | 可从流水推断 |
| 9 | **AI 对话查询**："上季度餐饮为什么涨了" 自然语言问数 | Maybe AI Chat、Firefly MCP | 已有 LLM client |
| 10 | 更多导入源：银行流水 CSV、通用 CSV 映射导入 | Beancount 生态 | Parser 接口已留好 |
| 11 | 多成员：夫妻各自账单导入合并、成员维度统计 | Monarch 夫妻协作 | source 字段可扩展 |
| 12 | 数据出口：全量 CSV/JSON 导出、（可选）Beancount 格式导出 | 开源用户刚需 | — |

明确**不做**：银行 API 自动同步（国内无 Plaid，爬虫违规且脆弱）、直接交易/买卖（合规红线）、具体基金代码推荐（开源分发有投顾风险，建议停在大类资产 + 指数型工具描述层）。

## 四、产品方案：四个阶段

### P0 — 补齐"数据依据"（资产端地基）

1. **资产快照页** `/assets`：按季度手填各科目余额（科目沿用点分命名空间新增 `asset.*` / `liability.*`），支持从上季复制再改。净值 = 资产 − 负债，画季度净值曲线。
2. **AI 季/年财报**：`/reports` 选择期间 → 组装"财务上下文包"（收支聚合 + KPI + 资产快照 + 同比环比）→ LLM 生成结构化财报（摘要/亮点/风险/建议）→ 存 `reports` 表可回看。复用现有 LLM client 与"JSON 输出 + 白名单校验"模式。

### P1 — AI 理财建议核心（本产品的差异化）

3. **家庭风险画像问卷**：一页表单（家庭结构、年龄、收入稳定性、房贷、应急金月数、风险偏好、投资年限），存档并随时可改。
4. **四笔钱诊断**：把资产快照科目映射到 活钱/稳健/长期/保障 四桶，展示当前占比 vs 依据画像计算的建议区间；缺口高亮（如"活钱仅覆盖 1.2 个月支出，建议 ≥ 6 个月"）。
5. **AI 个性化配置方案**：LLM 输入 = 画像 + 四笔钱现状 + 收支结构，输出 = 目标大类配置比例（现金/固收/权益/海外/黄金等）+ 每桶调整动作 + 理由 + 风险提示。规则层校验（比例合计 100%、单类不超上限、应急金优先），不给具体产品代码。
6. **再平衡提醒**：每次录入新快照后自动对比目标配置，偏离超阈值（如 ±5pp）在 dashboard 出卡片。

### P2 — 完整家庭财务闭环

7. 财务目标（目标金额/日期 → 每月应存 → 用快照自动更新进度，AI 财报引用）。
8. 保单登记 + AI 保障缺口检查（保额/年收入倍数、保费/收入占比）。
9. 分类预算 + 超支进财报；周期交易识别与未来 90 天现金流提示。

### P3 — 体验与开源生态

10. AI 对话问数（基于已有上下文包，只读）。
11. 周报/季报推送（邮件或 webhook，Monarch Weekly Recap 式）。
12. 通用 CSV 导入映射器、银行流水解析器；全量导出（CSV/JSON/Beancount）。
13. 多成员合并视图；（开源向）MCP server 暴露只读查询。

### 技术与合规要点

- **财务上下文包（context pack）**：把"期间聚合 + 快照 + 画像 + 目标"组装成一个稳定的 JSON 结构，作为财报/配置建议/对话三个 AI 功能的统一输入，避免每个功能各拼一套 prompt。
- LLM 输出一律 `json_object` + 服务端校验/裁剪后落库，页面渲染存库结果而非直连流式输出，保证财报可回看、可对比。
- 开源分发：README 与 AI 输出页脚固定免责声明（教育参考、非投资建议）；不内置任何产品代码推荐；AI 功能默认关闭、用户自带 key（Maybe 同款做法）。
- 隐私：向 LLM 只发聚合数与快照科目余额，不发交易对手方明细（现有"不给金额时间"的最小暴露原则延续到新功能）。

## 五、调研来源

- 开源竞品：[Firefly III](https://github.com/firefly-iii/firefly-iii)、[Actual Budget 对比](https://ezbookkeeping.mayswind.net/comparison/)、[Maybe 关停复盘](https://newsletter.failory.com/p/3-reasons-maybe-failed)、[Maybe 仓库](https://github.com/maybe-finance/maybe)、[Ghostfolio](https://github.com/ghostfolio/ghostfolio)、[Firefly III MCP](https://github.com/horsfallnathan/firefly-iii-mcp-server)、[Firefly LLM 分类讨论](https://github.com/firefly-iii/firefly-iii/issues/9753)
- 海外 AI 记账：[Era 对比 Monarch/Copilot/YNAB](https://era.app/articles/era-vs-monarch-vs-copilot-vs-ynab/)、[2026 AI 记账横评](https://www.techno-pulse.com/2026/04/best-ai-personal-finance-tools-in-2026.html)、[PortfolioPilot 退休规划](https://portfoliopilot.com/retirement-planning)
- 国内：[少数派 16 款记账横评](https://sspai.com/post/98549)、[有知有行产品分析](https://www.woshipm.com/pd/5662286.html)、[且慢四笔钱](https://sspai.com/post/54236)、[有知有行获基金销售牌照](https://www.21jingji.com/article/20241227/herald/13eb8b24170115cac719953b2ea62bb3.html)、[支小宝 2.0](https://www.cls.cn/detail/1458818)
- Beancount 生态：[double-entry-generator 实践](https://gaocegege.com/Blog/%E9%9A%8F%E7%AC%94/double-entry)、[Beancount-Trans](https://github.com/dhr2333/Beancount-Trans)
