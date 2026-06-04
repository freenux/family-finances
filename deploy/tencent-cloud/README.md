# 腾讯云部署方案（Family Finances）

> 当前项目是 Go + SQLite 的动态 Web 应用，不是纯静态站点。仅有域名 + CDN 不能承载后端和 SQLite 数据库；至少需要一个能长期运行进程并持久化磁盘的计算资源。

## 推荐架构

```text
用户浏览器
  │ HTTPS
  ▼
域名 finance.example.com
  │
  ├─ 方案 A（推荐先上线）：DNS A 记录 → 腾讯云 CVM/轻量服务器公网 IP → Caddy(80/443) → Go 应用(:8787) → SQLite(/opt/family-finances/data/family.db)
  │
  └─ 方案 B（上线稳定后再接 CDN）：DNS CNAME → 腾讯云 CDN → 源站 CVM/轻量服务器 → Caddy/Go 应用/SQLite
```

先用 **方案 A** 跑通最简单、可控，确认域名/HTTPS/导入/编辑/报表都正常后，再把 CDN 加在前面。这个应用有账单上传、PATCH 修改、HTMX 动态片段、Cookie flash 等动态行为；如果直接全站 CDN 且缓存配置不当，会出现数据不刷新或接口被缓存的问题。

## 腾讯云前置工作清单

### 1. 购买/确认计算资源

必须二选一：

- **轻量应用服务器 Lighthouse**：最省事，1C1G 可跑，建议 2C2G / 40GB SSD 起步。
- **CVM 云服务器**：更通用，Ubuntu 22.04/24.04 LTS，1C1G 最低，建议 2C2G。

地域选择：

- **中国大陆地域**：域名通常需要 ICP 备案；使用大陆 CDN 也通常要求已备案。
- **中国香港/海外地域**：一般不需要 ICP，部署阻力小，但大陆访问链路可能稍慢。

### 2. 安全组/防火墙

入站规则最少开放：

- TCP 22：SSH 管理，建议限制为你的固定 IP。
- TCP 80：Caddy/Let's Encrypt HTTP 校验与 HTTP 跳转。
- TCP 443：HTTPS 访问。

不要对公网开放 8787；Go 应用只给 Caddy 内网反代。

### 3. 域名解析

先上线推荐：

```text
finance.your-domain.com  A  <服务器公网 IP>
```

等站点稳定后再接 CDN：

```text
finance.your-domain.com  CNAME  <腾讯云 CDN 分配的 CNAME>
```

### 4. CDN 配置（可选，建议第二阶段）

如果要用腾讯云 CDN：

- 源站：CVM/轻量服务器公网 IP 或源站域名。
- 回源协议：优先 HTTPS；若证书链/ACME 有问题，可先 HTTP 回源。
- 缓存规则：
  - `/static/*`：可缓存 7-30 天。
  - `/*`、`/api/*`、`/imports*`、`/transactions*`、`/rules*`、`/partials/*`：不缓存。
- 请求方法：必须允许 GET / POST / PATCH。
- 查询字符串、Cookie：动态页面建议全部回源/透传。
- HTTPS 证书：可用腾讯云免费证书绑定 CDN；源站 Caddy 也可自动签证书。

### 5. 数据与备份

SQLite 数据库放在：

```text
/opt/family-finances/data/family.db
```

建议每天备份一次到：

```text
/opt/family-finances/backups/
```

本仓库已提供：

```bash
scripts/backup_sqlite.sh
```

可用 crontab：

```cron
15 3 * * * APP_DIR=/opt/family-finances /opt/family-finances/scripts/backup_sqlite.sh >> /var/log/family-finances-backup.log 2>&1
```

更稳妥的长期方案：把 backups 目录同步到 COS（对象存储）。

## 仓库内已准备的部署文件

- `Dockerfile`：多阶段构建 Go 服务，运行时带健康检查。
- `docker-compose.prod.yml`：Go 应用 + Caddy HTTPS 反向代理。
- `.env.prod.example`：生产环境变量模板。
- `deploy/tencent-cloud/Caddyfile`：Caddy 域名、HTTPS、反代配置。
- `deploy/tencent-cloud/bootstrap-ubuntu.sh`：新 Ubuntu 服务器初始化脚本。
- `scripts/backup_sqlite.sh`：SQLite 在线备份脚本。

## 首次部署步骤（服务器上执行）

假设服务器是 Ubuntu，应用目录用 `/opt/family-finances`。

### 1. 初始化服务器

```bash
# 第一次 SSH 到服务器后执行
sudo apt-get update
sudo apt-get install -y git

# 获取代码（二选一：如果仓库私有，需要先配置 GitHub SSH key 或 token）
sudo mkdir -p /opt/family-finances
sudo chown -R "$USER:$USER" /opt/family-finances
git clone https://github.com/freenux/family-finances.git /opt/family-finances
cd /opt/family-finances

# 安装 Docker/Compose/sqlite3 并开放 80/443
sudo bash deploy/tencent-cloud/bootstrap-ubuntu.sh

# 重要：执行完后退出 SSH 再重新登录，让 docker 用户组生效
```

### 2. 配置生产环境变量

```bash
cd /opt/family-finances
cp .env.prod.example .env.prod
nano .env.prod
```

至少修改：

```dotenv
APP_DOMAIN=finance.your-domain.com
ACME_EMAIL=your-email@example.com
```

如果要用 AI 分类，再填：

```dotenv
OPENAI_API_KEY=...
OPENAI_BASE_URL=...
OPENAI_MODEL=...
```

### 3. 启动

确保域名 A 记录已经指向服务器公网 IP，然后：

```bash
cd /opt/family-finances
docker compose -f docker-compose.prod.yml up -d --build
```

查看状态：

```bash
docker compose -f docker-compose.prod.yml ps
docker compose -f docker-compose.prod.yml logs -f --tail=100
```

访问：

```text
https://finance.your-domain.com/healthz
https://finance.your-domain.com/
```

### 4. 后续更新

```bash
cd /opt/family-finances
git pull
docker compose -f docker-compose.prod.yml up -d --build
```

### 5. 备份验证

```bash
cd /opt/family-finances
APP_DIR=/opt/family-finances bash scripts/backup_sqlite.sh
```

## 我无法替你完成的手动事项

除非你提供腾讯云 API 密钥/服务器 SSH 权限，否则以下必须你在腾讯云控制台完成：

1. 购买 CVM/轻量服务器，选择地域、系统镜像、磁盘规格。
2. 配置安全组：开放 22/80/443。
3. 配置域名解析：A 记录到服务器公网 IP。
4. 如果选择中国大陆服务器或大陆 CDN：完成 ICP 备案。
5. 如启用 CDN：创建 CDN 加速域名、配置源站、缓存规则、HTTPS 证书。
6. 如果仓库是私有：给服务器配置 GitHub 拉取权限，或手动上传代码包。

## 上线检查表

- [ ] `https://域名/healthz` 返回 `ok`。
- [ ] 首页可打开。
- [ ] `/imports` 可上传微信/支付宝账单。
- [ ] `/transactions` 可筛选、编辑分类/备注。
- [ ] `/rules` 可增删改分类规则。
- [ ] `docker compose -f docker-compose.prod.yml ps` 显示 app healthy、caddy running。
- [ ] SQLite 备份脚本可生成 `.db.gz` 文件。
- [ ] CDN 如已启用，确认 `/api/*`、`/imports*`、`/transactions*` 没有被缓存。
