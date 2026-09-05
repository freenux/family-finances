# Codex CLI 接入说明

[Codex CLI](https://developers.openai.com/codex) 是 OpenAI 的本地编码代理。本仓库把它的安装和配置固定成脚本，保证每个人（以及每台新机器）拿到的沙箱策略、审批策略一致。

## 安装与配置

```bash
scripts/setup-codex.sh
```

脚本做三件事：

1. 缺少 `codex` 命令时用 `npm install -g @openai/codex` 安装（没有 npm 就退回 `brew install --cask codex`，两者都没有会提示官方安装脚本）。
2. 把 `scripts/codex/config.toml` 写入 `$CODEX_HOME/config.toml`（默认 `~/.codex/config.toml`），模板里的 `__PROJECT_ROOT__` 替换成本机仓库路径；已有配置先备份成 `config.toml.bak.<时间戳>`。
3. 跑一次 `codex doctor` 校验配置能被解析。

只想重新生成配置、不动安装时加 `--skip-install`。

## 登录

二选一：

- ChatGPT 订阅：`codex login`，浏览器里完成授权。
- API key：`codex login --api-key "$OPENAI_API_KEY"`，或直接让环境变量 `OPENAI_API_KEY` 生效（Codex 会自动识别）。

注意 `.env` 里的 `OPENAI_API_KEY` 是给**应用自身**的 LLM 分类功能用的，和 Codex CLI 的登录态是两回事，两边可以用不同的 key。

## 配置项为什么这么定

| 配置 | 值 | 原因 |
| --- | --- | --- |
| `approval_policy` | `on-request` | 常规读写不打断，删文件、出网这类越界操作再来问 |
| `sandbox_mode` | `workspace-write` | 允许改仓库内文件（含 `family.db` 这类本地产物），仓库外只读 |
| `sandbox_workspace_write.network_access` | `true` | `go mod download` / `go test ./...` 首次跑要拉依赖，关掉会直接失败 |
| `shell_environment_policy.inherit` | `core` | 保留 PATH、GOPATH 等基本环境，不额外注入密钥 |
| `model_reasoning_effort` | `medium` | 这个仓库多是分层改动和 SQL 口径问题，`medium` 够用且省额度 |
| `history.persistence` | `save-all` | 保留会话历史，方便 `codex resume` 接着上一次的排查 |
| `projects.<repo>.trust_level` | `trusted` | 免掉每次进目录的信任确认；换路径后重跑脚本即可 |

模板末尾还留了一段注释掉的 Playwright MCP 配置。仓库约定网页交互用 Playwright 验证、不要用 `curl` 顶替，需要让 Codex 自己开浏览器时把那两行放开。

## 项目上下文

仓库根目录的 `AGENTS.md` 是指向 `CLAUDE.md` 的软链，Codex 启动时会读它，所以架构分层、金额单位（分）、统计口径（`daily`/`special`/`all`）、默认周期这些约定对 Codex 和 Claude Code 是同一份，不需要维护两套。

改动完照常自查：

```bash
go test ./...
go vet ./...
go build ./cmd/server
```

## 常用命令

```bash
codex                      # 交互式会话（在仓库根目录起）
codex exec "跑一遍单测并修掉失败"   # 非交互执行
codex review               # 对当前改动做一次代码评审
codex resume --last        # 接着上一次会话
codex doctor               # 安装、配置、认证、网络自检
```

## 已知限制

- Codex 需要访问 `api.openai.com`（API key 登录）或 `chatgpt.com`（订阅登录）。受限出网的环境（部分 CI 容器、公司代理）里 `codex doctor` 的 reachability 会报红，安装和配置本身没问题，但跑不了推理。
- 别把真实账单文件、`family.db`、导出的报表交给 Codex 读取或粘进对话——和 `SECURITY.md`、`CONTRIBUTING.md` 里的数据要求一致。
