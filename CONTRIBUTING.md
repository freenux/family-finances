# Contributing

感谢你愿意改进这个项目。这个仓库处理的是家庭账单和财务数据，提交代码时请优先保护数据隐私和可复现性。

## 本地开发

```bash
cp .env.example .env
go run ./cmd/server
```

默认监听 `:8787`，SQLite 数据库默认写入 `./family.db`。如果不配置 `OPENAI_API_KEY`，应用仍可运行，但 LLM 分类功能不可用。

## 提交要求

- 不要提交真实账单、数据库、`.env`、LLM 缓存、导出的报表或截图中的敏感信息。
- 新增数据库结构时添加 `internal/infrastructure/sqlite/migrations/` 下的新 goose 迁移。
- 新增 Repository 方法时先改 `internal/port/` 接口，再改 `internal/infrastructure/sqlite/` 实现。
- UI 文本和面向用户的错误信息使用简体中文。
- 对导入、分类、金额计算、权限等高风险逻辑补充 table-driven 测试。

## 验证

提交前至少运行：

```bash
go test ./...
go vet ./...
go build ./cmd/server
```

涉及网页交互时用 Playwright 验证关键流程；不要用 `curl` 代替页面测试。
