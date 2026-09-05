# Family Finances

家庭财务管理 Web 应用：导入微信/支付宝账单，按月度、季度、年度聚合收支，支持分类规则、人工修正和可选的 OpenAI 兼容 LLM 辅助分类。

## 功能

- 导入支付宝 CSV、微信支付 XLSX 账单。
- 手动新增流水，维护账户、分类、状态和备注。
- 按账户、周期、分类查看收支统计和现金流报表。
- 通过本地分类规则优先自动归类，未命中时可由 LLM 异步补充分配。
- 使用 SQLite 单文件存储，适合个人或家庭自托管。

## 技术栈

- 后端：Go、chi、modernc.org/sqlite、goose migrations。
- 前端：服务端渲染 HTML、HTMX、Alpine.js。
- 数据库：SQLite，金额全链路使用分为单位的整数。

## 快速开始

```bash
cp .env.example .env
go run ./cmd/server
```

打开 <http://127.0.0.1:8787>。

默认配置：

- `SERVER_ADDR=:8787`
- `DATABASE_PATH=./family.db`
- `AUTH_KEY=`，为空时不启用页面访问认证
- `OPENAI_API_KEY=`，为空时 LLM 功能不可用但应用可正常运行

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `SERVER_ADDR` | HTTP 监听地址，默认 `:8787` |
| `DATABASE_PATH` | SQLite 数据库路径，默认 `./family.db` |
| `AUTH_KEY` | 非空时启用登录页，登录时输入同一个 key |
| `OPENAI_API_KEY` | OpenAI 兼容接口 API key，留空关闭 LLM |
| `OPENAI_BASE_URL` | OpenAI 兼容接口地址 |
| `OPENAI_MODEL` | 用于异步分类的模型名 |

## 常用命令

```bash
go test ./...
go vet ./...
go build ./cmd/server
```

Docker 运行：

```bash
docker compose -f docker-compose.prod.yml up -d --build
```

腾讯云/Caddy 部署说明见 [deploy/tencent-cloud/README.md](deploy/tencent-cloud/README.md)。

## 数据与隐私

请不要提交真实账单、SQLite 数据库、`.env`、导出的报表或包含个人交易信息的截图。仓库已忽略常见数据文件和本地缓存，但开源前仍建议执行一次历史扫描。

启用 LLM 分类时，应用只发送待分类流水的 `id`、交易对方、说明和收支方向，不发送金额和时间。要完全关闭外部模型调用，保持 `OPENAI_API_KEY` 为空。

公开网络部署时必须设置非空 `AUTH_KEY`，并确保只通过反向代理暴露 HTTP 服务，不要公开数据库和备份目录。

## 账单导入

在 `/imports` 页面选择来源和账户后上传账单文件：

- 支付宝：CSV。
- 微信支付：XLSX。

导入时会自动跳过重复交易号。命中分类规则的流水会直接确认为已分类；未命中的流水会进入待确认状态，可在 `/transactions` 手工调整，也可等待 LLM 异步分类。

## 开发约定

- 新增数据库结构使用 `internal/infrastructure/sqlite/migrations/` 下的 goose SQL 迁移。
- 新增 Repository 方法先改 `internal/port/` 接口，再改 SQLite 实现。
- UI 文本和面向用户的错误信息使用简体中文。
- 页面交互测试优先使用 Playwright。
- AI 编码代理的项目约定统一放在根目录 `AGENTS.md`（软链到 `CLAUDE.md`）；Codex CLI 的安装与配置见 [docs/codex.md](docs/codex.md)。

更多贡献说明见 [CONTRIBUTING.md](CONTRIBUTING.md)，安全说明见 [SECURITY.md](SECURITY.md)。
