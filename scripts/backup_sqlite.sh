#!/usr/bin/env bash
set -euo pipefail

APP_DIR="${APP_DIR:-/opt/family-finances}"
DB_PATH="${DB_PATH:-$APP_DIR/data/family.db}"
BACKUP_DIR="${BACKUP_DIR:-$APP_DIR/backups}"
KEEP_DAYS="${KEEP_DAYS:-30}"

ts="$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP_DIR"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 未安装。Ubuntu 上执行：sudo apt-get update && sudo apt-get install -y sqlite3" >&2
  exit 1
fi

if [[ ! -f "$DB_PATH" ]]; then
  echo "数据库不存在：$DB_PATH" >&2
  exit 1
fi

backup_file="$BACKUP_DIR/family-$ts.db"
sqlite3 "$DB_PATH" ".backup '$backup_file'"
gzip -9 "$backup_file"

find "$BACKUP_DIR" -type f -name 'family-*.db.gz' -mtime +"$KEEP_DAYS" -delete

echo "备份完成：$backup_file.gz"
