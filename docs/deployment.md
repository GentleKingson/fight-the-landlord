# Web 生产部署

生产镜像采用单容器方案：Vite 在镜像构建阶段生成 `web/dist`，Go 使用
`webui` build tag 将资源嵌入服务端二进制。浏览器页面、`/ws`、`/health`
和 `/version` 因此共享同一来源，不需要单独维护静态 Web 容器。

本文档对应维护 fork `GentleKingson/fight-the-landlord`。发布 workflow 和 Compose
配置的目标仓库是 `gentlekingson/fight-the-landlord` 与可选的
`gentlekingson/fight-the-landlord-douzero`。上游 `palemoky` 项目仅作为来源归属和
兼容模块路径，不是本 fork 的默认部署源。

> **当前制品状态**：在首次成功的 fork tag workflow 之前，Docker Hub 仓库、Release、
> 远端 digest、SBOM 和签名可能都不存在。先在 GitHub Release 与 Docker Hub 核实
> tag/digest；不存在时应从已审查的 commit 本地构建，不能把以下 `pull` 示例当成
> 已发布证明。

## 构建镜像

Dockerfile 默认使用按多架构 manifest digest 固定的官方 Go/Node 构建镜像和
distroless 运行镜像：

```bash
docker build \
  --build-arg VERSION=v1.2.0 \
  --tag fight-the-landlord:v1.2.0 \
  .
```

需要使用企业镜像仓库时，可以通过 `GO_REGISTRY`、`GO_VARIANT`、
`GO_DIGEST`、`NODE_DIGEST` 和 `RUNTIME_IMAGE` build args 覆盖这些基础镜像。
标签和 digest 必须作为一组更新，避免标签变化后悄悄改变构建输入。

`VERSION` 同时写入服务端、嵌入的 HTML 和 `/version` 响应。生产发布应使用
不可变的语义化版本标签，不要把 `dev` 镜像用于兼容性门禁。

## Docker Compose

```bash
cp .env.example .env
# 示例仅用于交互式部署；自动化部署应由 secret manager 注入同名环境变量。
read -rsp "Redis password: " REDIS_PASSWORD && export REDIS_PASSWORD
docker compose config --quiet
# 只有已核实 fork 仓库中存在该 digest/tag 后才执行 pull。
docker compose pull
docker compose up -d
docker compose ps --all
```

默认使用内置启发式机器人，不启动 DouZero。需要神经网络推理服务时设置
`DOUZERO_ENABLED=true`，并用 `docker compose --profile douzero up -d` 启动。
不指定该 profile 时，`docker compose up -d` 只启动 Go 服务和 Redis。

`REDIS_PASSWORD` 是必填部署 secret。Compose 将它作为 secret 文件挂载给
Redis，并仅通过进程环境交给 Go 服务；不要把真实值写入 `.env`、镜像、
Compose 文件或仓库。Redis 默认没有任何宿主机端口映射，只能从内部
`poker-network` 访问。

排查本机 Redis 时可临时启用明确的非生产 profile：

```bash
docker compose --profile redis-debug up -d redis-debug
REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli -h 127.0.0.1 -p 6379 ping
docker compose --profile redis-debug rm -sf redis-debug
```

该代理只绑定 `127.0.0.1`，但仍然不得在生产环境启用。需要更换本机端口时
设置 `REDIS_DEBUG_PORT`。

默认访问地址为 `http://localhost:1780/`。修改 `.env` 中的 `SERVER_PORT`
只改变宿主机端口，容器内部始终监听 1780。默认 `SERVER_BIND_ADDRESS=127.0.0.1`，
因此未加密后端只供同机 TLS 代理使用。远程代理应通过受控私有网络访问，不要把
该值改为 `0.0.0.0` 后依赖代理 ACL；否则客户端可以绕过 TLS 和 `/metrics`
限制直接访问 Go 服务。

生产环境必须把 `SECURITY_ALLOWED_ORIGINS` 改成浏览器实际访问页面的完整
来源，例如：

```dotenv
SERVER_PORT=1780
SECURITY_ALLOWED_ORIGINS=https://game.example.com
# 仅在该 fork 已发布并验证对应客户端后填写，例如 v1.2.0；否则保持为空。
SERVER_MIN_CLIENT_VERSION=
```

多个来源使用逗号分隔。WebSocket 的 `Origin` 值仍是 `https://...`，不是
`wss://...`。不要在互联网部署中使用 `*`。

只有反向代理所在网段才应加入 `SECURITY_TRUSTED_PROXY_CIDRS`。留空时服务
忽略 `X-Forwarded-For` 和 `X-Real-IP`，直接使用连接的 `RemoteAddr`。

Web 客户端的重连凭证保存在 `HttpOnly`、`SameSite=Strict`、`Path=/` Cookie，
页面 JavaScript 不读取 token。服务端成功发送 WebSocket 身份消息后，页面使用
30 秒 pre-commit ticket 调用受信 Origin 的 `/session/commit`；该 ticket 不包含重连
token。首次连接的 ticket 绑定 101 响应中由服务端新建的防 fixation owner nonce；
已有会话的 ticket 才绑定请求携带的精确 predecessor Cookie。两个 Cookie 都省略
`Domain`，因此是 host-only。commit 在响应交付不确定期内可幂等重试同一轮换，
随后页面立即调用 `/session/refresh`，由新 Cookie 的回传确认交付并淘汰旧凭证。
HTTP 本地开发时 Cookie 不带 `Secure`，直接 TLS 或可信最后一跳代理精确声明
`X-Forwarded-Proto: https` 时带 `Secure`。Cookie 的浏览器保存上限为 7 天。

连接保持活动时，Web 客户端每 24 小时调用 `/session/refresh` 续期同一 HttpOnly
Cookie。浏览器若休眠、冻结或停止页面脚本超过 7 天，无法保证定时请求执行，Cookie
仍可能过期；这是浏览器保存期限，不是服务端在线 token 的墙钟期限。

连接保持在线期间，重连凭证不因墙钟时间过期；物理连接首次断开时才启动
完整 2 分钟恢复窗口，重复离线通知不会延长截止时间。成功恢复会单次消费并轮换凭证。
如果 commit 响应丢失，连接关闭后旧、新 Cookie 两种可能结果都可恢复，但服务端会
原子地只接受其中一个恢复者；观察到新 Cookie 后旧 Cookie 立即失效。身份、房间或
权威快照恢复失败时，服务端会回滚轮换，旧 Cookie 对应的凭证仍可
在原截止时间内重试。大厅的退出操作会先停止重连，再调用 `/session/revoke` 立即
撤销凭证并清除 Cookie；该请求是受信 Origin 的 POST。升级后的页面只删除一次旧
`ddz_next_reconnect` localStorage 字段，不会读取或继续使用其中的 token。

CLI/TUI/Bot 继续在 protobuf 字段中显式发送 token。带非空 Origin 的连接必须
声明 Web client kind；原生客户端不带 Origin，从而保持协议兼容且不能借 Web
transport 获取 JavaScript 可读的长期凭证。

`GAME_OFFLINE_WAIT_TIMEOUT` 控制牌局中轮到离线玩家行动时的等待秒数，默认
30 秒；它不是会话重连窗口，也不会改变上述 2 分钟凭证恢复期限。

## TLS 和反向代理

Go 服务在容器内提供 HTTP。TLS 应在 Caddy、Nginx、负载均衡器或 Kubernetes
Ingress 终止。前端根据页面协议自动选择 `ws://` 或 `wss://`。

Caddy 示例：

```caddyfile
game.example.com {
    # 把 10.0.0.0/8 换成实际 Prometheus 来源；其他来源不能读取 metrics。
    @metricsDenied {
        path /metrics
        not remote_ip 10.0.0.0/8
    }
    respond @metricsDenied 403
    reverse_proxy 127.0.0.1:1780
}
```

Caddy 作为最后一跳时，把它实际使用的内网网段加入
`SECURITY_TRUSTED_PROXY_CIDRS`。不要把 `0.0.0.0/0` 用于生产。

Nginx 示例：

```nginx
location / {
    proxy_pass http://127.0.0.1:1780;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
}
```

Nginx 的 `http` 块还需要定义连接升级变量：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}
```

代理必须让 `/ws` 保持 Upgrade/Connection 头，且不得缓存 `/version`。TLS
部署时，来源白名单应填写代理对外的 HTTPS 来源。服务端只信任来自已配置直接
peer 的 `X-Forwarded-Proto: https`；公网客户端伪造该头不会获得 Secure 判断。
同机代理使用 Compose 的默认 loopback 绑定；代理位于其他主机或容器时，应改用
不对公网路由的私有网络，而不是公开宿主机 1780 端口。

Prometheus 端点默认启用，但不包含应用认证。应在 Nginx 中单独限制：

```nginx
location = /metrics {
    allow 10.0.0.0/8;
    deny all;
    proxy_pass http://127.0.0.1:1780;
}
```

也可以设置 `OBSERVABILITY_METRICS_ENABLED=false` 完全关闭。完整指标和日志字段见
[可观测性](observability.md)。

## HTTP 行为

- `/health` 返回进程存活状态；Compose healthcheck 每 10 秒请求 `/readyz`，
  因而 Redis 故障也会把容器标为 unhealthy 并刷新 readiness 指标。
- `/livez` 返回进程存活状态；`/readyz` 同时检查 Redis 和关闭状态。
- `/version` 返回 `server_version`、`min_client_version` 和
  `web_client_version`，并使用 `Cache-Control: no-store`。
- `/metrics` 默认返回 Prometheus/OpenMetrics 文本；公网代理应限制该路径。
- `/session/commit`、`/session/refresh` 与 `/session/revoke` 只接受 `Origin` 非空且
  命中 `SECURITY_ALLOWED_ORIGINS` 的 POST、严格 JSON 和有界请求体。白名单中的每个
  来源都拥有完整会话操作权限，因此只能加入实际受信的 Web 前端。
- Vite 哈希资源位于 `/assets/`，使用一年不可变缓存。
- `index.html` 使用 `no-cache` 和内容 ETag。
- 不带文件扩展名的未知路径回退到 `index.html`，支持 SPA 深链接。
- 缺失的资源文件返回 404，不回退到 HTML。

## Smoke Test

```bash
curl --fail http://localhost:1780/health
curl --fail http://localhost:1780/livez
curl --fail http://localhost:1780/readyz
curl --fail http://localhost:1780/version
curl --fail http://localhost:1780/metrics
curl --fail http://localhost:1780/
curl --fail http://localhost:1780/room/example
docker inspect --format '{{.State.Health.Status}}' "$(docker compose ps -q poker-server)"
```

预期健康状态为 `healthy`。`/version` 中的 `server_version` 和
`web_client_version` 应与部署的镜像标签一致。

## 升级和回滚

升级前先使用 [Redis 备份脚本](redis-backup.md) 创建并校验恢复点。Redis 恢复
不能恢复单进程内存中的活动牌局或 WebSocket 会话，因此恢复必须在 draining 后的
维护窗口执行。

生产环境设置了 `GAME_IMAGE_REF`/`DOUZERO_IMAGE_REF` 后，`IMAGE_TAG` 不再生效。
升级和回滚都应直接编辑 `.env` 中已经过 cosign、SBOM 和 provenance 验证的完整
`repository@sha256:...` 引用，并保存上一个 digest：

```bash
# 维护窗口内先接受或排空活跃牌局，再写入新 GAME_IMAGE_REF。
docker compose pull poker-server
docker compose up -d poker-server
curl --fail http://127.0.0.1:1780/readyz
curl --fail http://127.0.0.1:1780/version

# 启用 DouZero 时独立更新其已验证 digest；不要顺带升级 Redis。
docker compose --profile douzero pull douzero
docker compose --profile douzero up -d douzero
```

回滚时恢复上一组 digest、代理配置和 `SERVER_MIN_CLIENT_VERSION` 后重复同样检查。
只有对应 fork 客户端二进制确已发布并验证后才能提高最低版本；示例版本号不是发布事实。

抬高 `SERVER_MIN_CLIENT_VERSION` 会阻止旧客户端继续进入牌局。先发布兼容的
新 Web 资源，再提高最低版本；回滚服务端时也要同步检查该值。

HttpOnly 迁移没有把旧 `ddz_next_reconnect` localStorage token 转换成 Cookie：升级后
页面会删除它，缺少有效新 Cookie 的浏览器会建立新 player identity，可能需要重新登录
或重新加入房间；缺少新 Web capability 的已打开旧页面应刷新后再连接。服务端与 Web
资源来自同一镜像，必须原子升级。回滚到旧 Web/服务端也不会把 HttpOnly Cookie 转回
JavaScript token，浏览器同样可能需要新身份。CLI/TUI 的显式 protobuf token 字段保持
兼容，但任何单进程重启都会丢失内存中的 session、Room 和 Matcher 状态。

标签发布工作流使用 BuildKit 为两个镜像生成最大模式 provenance 和 SBOM，
随后使用 GitHub OIDC 身份通过 cosign 对具体 digest 做无密钥签名。部署系统
应校验签名主体和仓库工作流身份，而不是只信任可变标签。命令见
[发布验证](release-verification.md)。首次成功 fork tag 发布前没有可声明的远端
digest、SBOM 或签名状态。

## 运行模型与限制

Room、Matcher 和连接表由单个 Go 进程拥有，当前不支持多副本负载均衡、跨实例
重连或活跃房间迁移。Redis 客户端当前只支持一个明文 `addr/password/db` endpoint，
没有原生 TLS、Sentinel 或 Cluster；默认 Compose 还固定依赖内置单节点 Redis。
外部 HA 只能由另一个部署清单/编排器移除该依赖，并提供私网中的单一 Redis 兼容代理
endpoint，随后在隔离环境验证；“托管 HA”不是当前 Compose 的即开即用能力，也不会
消除内存房间的单实例限制。

DouZero 是可选服务。未启用、超时、连接拒绝或返回非法动作时，服务端使用规则
启发式机器人继续决策；相关延迟、timeout 和 fallback 可从 Prometheus 观察。
容量基线和故障场景见 [性能测试](performance-testing.md)。
