#!/usr/bin/env bash
# 安装 Codex CLI 并写入本仓库约定的配置。
#
# 用法：
#   scripts/setup-codex.sh                # 缺失时安装 codex，再生成配置
#   scripts/setup-codex.sh --skip-install # 只生成配置，不碰安装
#
# 生成目标：$CODEX_HOME/config.toml（默认 ~/.codex/config.toml）。
# 已存在的配置会先备份成 config.toml.bak.<时间戳>，不会直接覆盖丢失。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATE="$REPO_ROOT/scripts/codex/config.toml"
CODEX_HOME="${CODEX_HOME:-$HOME/.codex}"
CONFIG="$CODEX_HOME/config.toml"

skip_install=0
for arg in "$@"; do
  case "$arg" in
    --skip-install) skip_install=1 ;;
    -h|--help) awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; exit 0 ;;
    *) echo "未知参数：$arg（可用：--skip-install、--help）" >&2; exit 2 ;;
  esac
done

if [ ! -f "$TEMPLATE" ]; then
  echo "找不到配置模板：$TEMPLATE" >&2
  exit 1
fi

if [ "$skip_install" -eq 0 ] && ! command -v codex >/dev/null 2>&1; then
  if command -v npm >/dev/null 2>&1; then
    echo "==> 安装 Codex CLI：npm install -g @openai/codex"
    npm install -g @openai/codex
  elif command -v brew >/dev/null 2>&1; then
    echo "==> 安装 Codex CLI：brew install --cask codex"
    brew install --cask codex
  else
    echo "没有找到 npm 或 brew，请先手动安装 Codex CLI：" >&2
    echo "  curl -fsSL https://chatgpt.com/codex/install.sh | sh" >&2
    exit 1
  fi
fi

if command -v codex >/dev/null 2>&1; then
  echo "==> 已安装 $(codex --version)"
else
  echo "==> 跳过安装，未检测到 codex 命令（配置仍会写入 $CONFIG）"
fi

mkdir -p "$CODEX_HOME"
if [ -f "$CONFIG" ]; then
  backup="$CONFIG.bak.$(date +%Y%m%d%H%M%S)"
  cp "$CONFIG" "$backup"
  echo "==> 旧配置已备份到 $backup"
fi

# 模板里的 __PROJECT_ROOT__ 换成本机仓库绝对路径，用于 [projects.*] 免信任确认。
sed "s|__PROJECT_ROOT__|$REPO_ROOT|g" "$TEMPLATE" > "$CONFIG"
echo "==> 配置已写入 $CONFIG"

if command -v codex >/dev/null 2>&1; then
  echo "==> 校验配置解析"
  codex doctor 2>&1 | sed -n '/^Configuration/,/^Updates/p' || true
fi

cat <<'EOF'

下一步：
  1. 登录：codex login（ChatGPT 订阅）或 codex login --api-key "$OPENAI_API_KEY"
  2. 自检：codex doctor
  3. 进仓库根目录直接跑 codex；项目约定由根目录 AGENTS.md（软链到 CLAUDE.md）提供

注意：Codex 需要访问 api.openai.com / chatgpt.com，受限网络环境（含部分 CI 容器）会连不上。
详见 docs/codex.md。
EOF
