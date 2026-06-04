#!/usr/bin/env bash
set -euo pipefail

# 在全新的 Ubuntu 22.04/24.04 腾讯云 CVM/轻量服务器上执行。
# 用途：安装 Docker、Compose 插件、git、sqlite3，并创建应用目录。

APP_DIR="${APP_DIR:-/opt/family-finances}"
APP_USER="${APP_USER:-$USER}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "请用 sudo 执行：sudo bash deploy/tencent-cloud/bootstrap-ubuntu.sh" >&2
  exit 1
fi

apt-get update
apt-get install -y ca-certificates curl gnupg git sqlite3 ufw
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
. /etc/os-release
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

mkdir -p "$APP_DIR" "$APP_DIR/data" "$APP_DIR/backups"
chown -R "$APP_USER:$APP_USER" "$APP_DIR"
usermod -aG docker "$APP_USER" || true

ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

echo "Bootstrap 完成。请重新登录 SSH 让 docker 用户组生效，然后部署应用到 $APP_DIR。"
