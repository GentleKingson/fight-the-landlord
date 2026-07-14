# Web 生产部署

生产镜像采用单容器方案：Vite 在镜像构建阶段生成 `web/dist`，Go 使用
`webui` build tag 将资源嵌入服务端二进制。浏览器页面、`/ws`、`/health`
和 `/version` 因此共享同一来源，不需要单独维护静态 Web 容器。

## 构建镜像

Dockerfile 默认使用公开的官方 Go/Node 构建镜像和 distroless 运行镜像：

```bash
docker build \
  --build-arg VERSION=v1.2.0 \
  --tag fight-the-landlord:v1.2.0 \
  .
```

需要使用企业镜像仓库时，可以通过 `GO_REGISTRY`、`GO_VARIANT` 和
`RUNTIME_IMAGE` build args 覆盖这些基础镜像。

`VERSION` 同时写入服务端、嵌入的 HTML 和 `/version` 响应。生产发布应使用
不可变的语义化版本标签，不要把 `dev` 镜像用于兼容性门禁。

## Docker Compose

```bash
cp .env.example .env
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps --all
```

默认使用内置启发式机器人，不启动 DouZero。需要神经网络推理服务时设置
`DOUZERO_ENABLED=true`，并用 `docker compose --profile douzero up -d` 启动。
不指定该 profile 时，`docker compose up -d` 只启动 Go 服务和 Redis。

默认访问地址为 `http://localhost:1780/`。修改 `.env` 中的 `SERVER_PORT`
只改变宿主机公开端口，容器内部始终监听 1780。

生产环境必须把 `SECURITY_ALLOWED_ORIGINS` 改成浏览器实际访问页面的完整
来源，例如：

```dotenv
SERVER_PORT=1780
SECURITY_ALLOWED_ORIGINS=https://game.example.com
SERVER_MIN_CLIENT_VERSION=v1.2.0
```

多个来源使用逗号分隔。WebSocket 的 `Origin` 值仍是 `https://...`，不是
`wss://...`。不要在互联网部署中使用 `*`。

## TLS 和反向代理

Go 服务在容器内提供 HTTP。TLS 应在 Caddy、Nginx、负载均衡器或 Kubernetes
Ingress 终止。前端根据页面协议自动选择 `ws://` 或 `wss://`。

Caddy 示例：

```caddyfile
game.example.com {
    reverse_proxy 127.0.0.1:1780
}
```

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
部署时，来源白名单应填写代理对外的 HTTPS 来源。

## HTTP 行为

- `/health` 返回进程健康状态，镜像和 Compose 都使用它进行健康检查。
- `/version` 返回 `server_version`、`min_client_version` 和
  `web_client_version`，并使用 `Cache-Control: no-store`。
- Vite 哈希资源位于 `/assets/`，使用一年不可变缓存。
- `index.html` 使用 `no-cache` 和内容 ETag。
- 不带文件扩展名的未知路径回退到 `index.html`，支持 SPA 深链接。
- 缺失的资源文件返回 404，不回退到 HTML。

## Smoke Test

```bash
curl --fail http://localhost:1780/health
curl --fail http://localhost:1780/version
curl --fail http://localhost:1780/
curl --fail http://localhost:1780/room/example
docker inspect --format '{{.State.Health.Status}}' "$(docker compose ps -q poker-server)"
```

预期健康状态为 `healthy`。`/version` 中的 `server_version` 和
`web_client_version` 应与部署的镜像标签一致。

## 升级和回滚

```bash
# 升级
sed -i.bak 's/^IMAGE_TAG=.*/IMAGE_TAG=v1.2.0/' .env
docker compose pull poker-server redis
docker compose up -d

# 启用了 DouZero 时同时更新可选服务：
docker compose --profile douzero pull douzero
docker compose --profile douzero up -d

# 回滚时把 IMAGE_TAG 改回上一个已验证版本，再重复 pull/up。
```

抬高 `SERVER_MIN_CLIENT_VERSION` 会阻止旧客户端继续进入牌局。先发布兼容的
新 Web 资源，再提高最低版本；回滚服务端时也要同步检查该值。
