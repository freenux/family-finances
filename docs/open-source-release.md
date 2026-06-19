# 开源发布检查

这个仓库曾经提交过真实账单和本地缓存。即使当前文件已删除或被 `.gitignore` 忽略，公开仓库前也必须清理 Git 历史。

## 必做检查

```bash
git status --short --ignored
git log --all --name-only --pretty=format: -- '*.db' '*.sqlite' '*.csv' '*.xlsx' 'data/*' '.env' '.env.*' '.wepay_llm_cache.json' | sort -u
git grep -n -I 'OPENAI_API_KEY=s[k]-\|AUTH_KEY=\S\|@' HEAD
```

确认没有真实账单、数据库、密钥、个人邮箱、个人域名或交易对手信息留在公开分支。

## 历史清理

推荐在备份仓库后使用 `git filter-repo` 清理曾提交过的数据文件：

```bash
git filter-repo \
  --path data \
  --path .wepay_llm_cache.json \
  --path-glob '*.xlsx' \
  --path-glob '*.xls' \
  --path-glob '*.csv' \
  --path-glob '*.db' \
  --invert-paths
```

清理后重新执行上面的检查命令。历史重写会改变 commit hash；如果远端已经存在，需要所有协作者重新 clone 或按 Git 历史重写流程同步。

## 发布前确认

- `LICENSE`、`CONTRIBUTING.md`、`SECURITY.md` 已存在。
- `README.md` 能让新用户从零启动应用。
- `go test ./...`、`go vet ./...`、`go build ./cmd/server` 通过。
- `.env.example` 不包含真实密钥。
- `AUTH_KEY` 的生产部署要求已写入文档。
- Docker context 不包含数据库、账单、缓存和虚拟环境。
