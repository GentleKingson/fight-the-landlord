# 小规模公开测试部署

本文面向一台 Linux 服务器、一个游戏服务实例和一个 Redis 的公开测试。目标容量为
10 至 50 名测试用户，峰值 WebSocket 连接不超过 100。DouZero 仍是可选 profile，
默认使用内置规则机器人。

> **测试版本告知**：这不是正式账号或正式游戏平台。测试期间可能重置战绩；清除
> 本站 Cookie、改用其他域名或浏览器可能产生新身份，项目不提供正式账号恢复。
> 服务进程重启会终止活动牌局；Redis 保存的战绩不能恢复内存中的 `GameSession`。

## 安全边界和架构

```text
Internet users (:80/:443 application traffic)
          |
          v
  Caddy or Nginx
  HTTPS + WebSocket proxy
  blocks /admin/*
  ACL for /metrics
          |
          v
  127.0.0.1:1780 on the host
  single poker-server container
          |
          v
  private Docker bridge: poker-network
          |--------------------|
          v                    v
  Redis :6379             optional DouZero :2021
  named volume + AOF      profile: douzero

Restricted admin network ----> SSH on the configured management port
```

只有反向代理暴露 80/443。明文后端只发布到宿主机 loopback；Redis 和 DouZero
没有宿主机端口。`/metrics` 与游戏 HTTP 监听共用 1780，因此必须在反向代理处
限制，不能仅依赖应用认证。`/admin/*` 必须在公网代理处直接拒绝；日常管理使用
容器内 CLI，详见 [公开测试维护](public-test-maintenance.md)。

当前架构不支持多个游戏服务副本。不要在反向代理后增加第二个 `poker-server`，
也不要把 Redis、DouZero 或 1780 发布到公网来规避该限制。

## 部署前准备

准备已更新的 Linux、Docker Engine、Docker Compose v2、Python 3、一个指向该
服务器的域名，以及 Caddy 或 Nginx。备份脚本还需要 `tar` 和 `sha256sum` 或
`shasum`。确认系统时间同步，并在修改防火墙前保留可用的带外或 SSH 管理通道。

生产部署使用已验证的完整镜像引用：

```dotenv
GAME_IMAGE_REF=gentlekingson/fight-the-landlord@sha256:<64-hex-digest>
DOUZERO_IMAGE_REF=gentlekingson/fight-the-landlord-douzero@sha256:<64-hex-digest>
```

小规模测试可以临时使用明确的 RC tag，例如 `v0.6.0-rc.1`，但 digest 更适合
回滚。不得使用隐式 tag 或裸 `latest`。先按 [发布验证](release-verification.md)
核对来源、签名、SBOM 和 provenance；不存在已发布制品时，应从已审查 commit
本地构建并记录生成的 digest，不能猜测远端引用。

### 环境和 secret

先创建非 secret 配置文件，并限制权限：

```bash
umask 077
cp .env.example .env
chmod 600 .env
vim .env
```

至少设置以下值；示例域名、代理地址和 digest 必须替换：

```dotenv
COMPOSE_PROJECT_NAME=fight-the-landlord
GAME_IMAGE_REF=gentlekingson/fight-the-landlord@sha256:<64-hex-digest>
SERVER_ENV=production
SERVER_BIND_ADDRESS=127.0.0.1
SERVER_PORT=1780
SERVER_MAX_CONNECTIONS=100
SECURITY_ALLOWED_ORIGINS=https://game.example.com
SECURITY_TRUSTED_PROXY_CIDRS=127.0.0.1/32,172.18.0.1/32
SECURITY_RATE_LIMIT_PER_SECOND=10
SECURITY_RATE_LIMIT_PER_MINUTE=60
SECURITY_MESSAGE_LIMIT_PER_SECOND=20
OBSERVABILITY_METRICS_ENABLED=true
OBSERVABILITY_METRICS_PATH=/metrics
DOUZERO_ENABLED=false
```

`SECURITY_ALLOWED_ORIGINS` 是浏览器页面的 Origin，使用 `https://`，不是
`wss://`；不能包含路径、通配符或公网 `http://`。多个确实受信的前端用逗号分隔。

`SECURITY_TRUSTED_PROXY_CIDRS` 只列出 Go 服务实际看到的最后一跳。裸进程和同一
网络命名空间通常是 `127.0.0.1/32`；宿主机代理通过 Docker 端口转发时，Go 服务
还可能看到 `poker-network` 的网关。先按下一段加载 secret，再返回这里让 Compose
创建网络并查询网关：

```bash
docker compose --env-file .env create redis
docker network inspect fight-the-landlord_poker-network \
  --format '{{(index .IPAM.Config 0).Gateway}}'
```

把输出作为单个 `/32` 加入白名单，并保留实际使用的 loopback 地址。不要使用
`0.0.0.0/0`、`::/0` 或整个云/VPC 网段。只有受信最后一跳提供的
`X-Forwarded-For`、`X-Real-IP` 和精确的 `X-Forwarded-Proto: https` 才会生效；
这一配置也决定 Web 会话 Cookie 是否带 `Secure`。

`REDIS_PASSWORD` 和 `ADMIN_KEY` 不应写入仓库、`config.yaml`、shell history 或
命令行参数。由部署 secret store 导出到运行 Compose 的进程环境；
`ADMIN_KEY` 至少 32 字节。交互式小规模部署也应使用仓库外、权限 `0600` 的
环境文件，并在同一受控 shell 中加载：

```bash
set -a
. /etc/fight-the-landlord/secrets.env
set +a
```

该文件只包含 `REDIS_PASSWORD=...` 和 `ADMIN_KEY=...`，由部署用户读取。不要在
故障报告中粘贴它。Compose 把 Redis secret 作为文件挂载给 Redis，并把同名变量
交给游戏服务；管理 CLI 从容器环境读取密钥，不会把它放入参数。

启用 DouZero 时还要设置固定的 `DOUZERO_IMAGE_REF`、
`DOUZERO_ENABLED=true`，并在所有 preflight、pull 和 up 命令中加入
`--profile douzero`。不要发布 2021 端口。

### 防火墙

下面是独占主机的 UFW 基线。先把 `22` 替换为实际 SSH 端口并确认规则，再启用：

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw deny 1780/tcp
sudo ufw deny 6379/tcp
sudo ufw deny 2021/tcp
sudo ufw enable
sudo ufw status verbose
```

Docker 的转发规则可能绕过普通 UFW 链，因此防火墙只是纵深防御。真正的边界仍是：
1780 绑定 `127.0.0.1`，Redis/DouZero 完全没有 `ports`，Compose 不使用 host
network 或 privileged，也不挂载 Docker socket。部署后应从另一台主机确认只有
SSH、80 和 443 可达。

## HTTPS 反向代理

以下二选一。两个示例都保留浏览器的 `Origin` 和 `Cookie`，把 `Set-Cookie`
传回浏览器，转发 HTTPS 信息，支持 WebSocket Upgrade，拒绝公网管理路由，并限制
metrics。将 `10.20.30.40/32` 换成实际 Prometheus 来源；若 Prometheus 在同机，
更简单的做法是直接抓取 `http://127.0.0.1:1780/metrics` 并对公网始终返回 403。

### Caddy

Caddy 对有效公网域名自动申请证书并把 HTTP 重定向到 HTTPS。下面的 `route`
保持顺序，避免被最后的通用代理绕过 ACL：

```caddyfile
{
    email ops@example.com
}

game.example.com {
    header Strict-Transport-Security "max-age=31536000; includeSubDomains"

    route {
        @admin path /admin/*
        respond @admin 404

        @metricsAllowed {
            path /metrics
            remote_ip 127.0.0.1/32 10.20.30.40/32
        }
        reverse_proxy @metricsAllowed 127.0.0.1:1780

        @metricsDenied path /metrics
        respond @metricsDenied 403

        reverse_proxy 127.0.0.1:1780 {
            header_up Origin {http.request.header.Origin}
            header_up Cookie {http.request.header.Cookie}
            header_up X-Forwarded-For {http.request.remote.host}
            header_up X-Real-IP {http.request.remote.host}
            header_up X-Forwarded-Proto https
        }
    }
}
```

Caddy 的 `reverse_proxy` 自动处理 WebSocket Upgrade 并转发响应中的
`Set-Cookie`；示例还把客户端地址覆盖为直连 edge 实际看到的 remote host，不接受
客户端自带的转发链。不要添加删除 `Cookie`、
`Origin` 或 `Set-Cookie` 的 header 规则，也不要在 `/ws`、`/session/*` 前增加
缓存层。验证并加载：

```bash
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

### Nginx

把 `map` 放在 `http` 块内，两个 `server` 块放在同一站点配置中。证书路径应由
实际 ACME 客户端管理：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    listen [::]:80;
    server_name game.example.com;
    return 308 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name game.example.com;

    ssl_certificate     /etc/letsencrypt/live/game.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/game.example.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    location ^~ /admin/ {
        return 404;
    }

    location = /metrics {
        allow 127.0.0.1;
        allow 10.20.30.40/32;
        deny all;

        proxy_pass http://127.0.0.1:1780;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }

    location = /ws {
        proxy_pass http://127.0.0.1:1780;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Origin $http_origin;
        proxy_set_header Cookie $http_cookie;
        proxy_pass_header Set-Cookie;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    location / {
        proxy_pass http://127.0.0.1:1780;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Origin $http_origin;
        proxy_set_header Cookie $http_cookie;
        proxy_pass_header Set-Cookie;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }
}
```

验证并加载：

```bash
sudo nginx -t
sudo systemctl reload nginx
```

不要让 CDN 或另一个未记录的代理成为额外最后一跳。确需增加代理时，重新收窄
trusted proxy、重新检查真实客户端限流，并验证 Cookie 仍带 `Secure`。

## Preflight 和启动

secret 已加载且代理配置准备好后，运行公网预检。DouZero 关闭时使用下面的命令。
任何 `ERROR` 都会返回非零；
不要通过改脚本或关闭检查继续部署。metrics 与游戏监听共用端口时会出现
`WARNING [metrics_proxy_acl]`，它要求人工验证上面的路径 ACL，而不是可忽略告警。

```bash
./scripts/preflight-public-test.sh --env-file .env
```

启用 DouZero 时使用：

```bash
./scripts/preflight-public-test.sh --env-file .env --profile douzero
```

首次部署的完整顺序是：先按“环境和 secret”执行一次 `cp`、`chmod`、`vim` 并加载
secret；随后从 preflight 继续。已有 `.env` 时绝不能再次复制 `.env.example` 覆盖它。
下面是 DouZero 关闭时的完整命令：

```bash
./scripts/preflight-public-test.sh --env-file .env

docker compose --env-file .env pull
docker compose --env-file .env up -d --wait --wait-timeout 120

curl --fail http://127.0.0.1:1780/livez
curl --fail http://127.0.0.1:1780/readyz
curl --fail http://127.0.0.1:1780/version
```

上述精简流程假定 secret 已由受控环境注入、`.env` 权限为 `0600`、代理和防火墙
已经配置。启用 DouZero 时用以下三条命令替换对应的 preflight、pull 和 up：

```bash
./scripts/preflight-public-test.sh --env-file .env --profile douzero
docker compose --env-file .env --profile douzero pull
docker compose --env-file .env --profile douzero \
  up -d --wait --wait-timeout 120
```

继续检查容器、HTTPS、ACL 和固定镜像：

```bash
docker compose --env-file .env ps --all
docker compose --env-file .env images
curl --fail https://game.example.com/livez
curl --fail https://game.example.com/readyz
curl --fail https://game.example.com/version
curl --fail --output /dev/null https://game.example.com/
```

从未被 metrics ACL 允许的外部主机访问 `https://game.example.com/metrics`，预期
403；从允许来源访问则预期 200。外部扫描应确认 1780、6379 和 2021 不可达。
用浏览器开发者工具确认 `/ws` 返回 101，响应中的会话 Cookie 为 host-only、
`HttpOnly`、`SameSite=Strict`、`Path=/` 且在 HTTPS 下带 `Secure`。不要记录 Cookie
值或重连 token。

最后用同一精确目标镜像引用和独立 Compose project/Redis volume 运行
[公开测试 smoke](public-test-troubleshooting.md#完整牌局-smoke-失败)；不得指向已有玩家
数据的 `redis-data`，因为完整牌局会写入测试身份的战绩和排行榜。只有完整牌局、重连、
结算、排行榜核对和资源回收达到报告阈值，并清理隔离容器/network/volume 后才开放
测试。已含真实测试用户数据的部署只做 health、WebSocket Upgrade 和 ACL 验证。

## 测试用户告知

在入口页、测试邀请和维护频道使用一致的告知，至少保留以下完整内容：

> 当前为小规模公开测试版本，功能和数据可能变化，战绩可能被重置。身份依赖当前
> 浏览器的本站 Cookie；清除 Cookie、改用其他浏览器或域名可能产生新身份，暂不提供
> 正式账号找回。维护或服务重启会断开连接并终止未完成牌局，活动牌局无法从 Redis
> 恢复。请勿在聊天中发送敏感信息。

进入 draining 前另行通知：

> 服务器即将维护，现已停止创建新房间、快速匹配和人机练习。当前牌局可以完成，
> 请不要开始下一局；排空后会短暂断线，未完成牌局不能跨服务重启恢复。

应用状态切换还会向所有客户端广播状态。可见提示为：

- `draining`：`服务器正在排空，已停止新房间、新匹配和人机练习`
- `maintenance`：`服务器正在维护，已停止新游戏入口`
- `normal`：发送 `MaintenancePush=false` 解除维护标记，不附加错误提示

## 明确限制

- 当前是测试版本，不承诺战绩永久保留，维护或恢复备份可能重置战绩。
- 清理本站 Cookie、换浏览器或换域名可能产生新身份；没有正式账号恢复流程。
- 服务重启会终止所有活动牌局、等待房间、匹配和 WebSocket 连接。
- Redis 保存战绩、排行榜和结算幂等 key，但不恢复活动 `GameSession`。
- 只支持一个游戏服务实例，不支持多实例或负载均衡扩容。
- 不支持跨实例重连、房间迁移或跨进程会话恢复。
- 只支持单节点 Redis，不支持 Redis Sentinel，也不支持 Redis Cluster。
- 不提供观战、回放、赛事、举报申诉、支付或完整账号系统。
- 临时禁言和封禁仅按 player ID 保存在内存中，不是正式账号或 IP 封禁。
- DouZero 可选；关闭或故障时使用规则机器人，不应因此增加公网端口。

部署、更新、排空、备份和回滚步骤见 [公开测试维护](public-test-maintenance.md)，
故障定位见 [公开测试排障](public-test-troubleshooting.md)。
