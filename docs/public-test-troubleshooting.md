# 公开测试排障

本文只覆盖单节点小规模公开测试。先保护用户牌局和数据，再定位问题；不要通过公开
1780/6379/2021、关闭 Origin 校验、使用 `latest`、跳过 SHA-256 或添加第二个游戏
实例来“临时修复”。这些操作会破坏既定安全边界。

## 先做什么

仍能管理且存在活动牌局时，先进入 draining 并等待：

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action drain
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action status
```

确认状态已为 `draining` 或 `maintenance`、`active_games=0` 且
`safe_to_restart=true` 后再备份、停止或替换服务。`safe_to_restart` 是最终权威闸门：
它还会等待已承认牌局的终局投递、同步 Redis 结算调用返回、启动 lease 释放，
以及状态切换/取消边界完成：

```bash
./scripts/backup-redis.sh --env-file .env
```

安全事件、数据损坏或服务完全不可用时，发送维护通知，关闭该站点的公网代理，再保留
容器日志、当前固定 digest、`.sha256` 和 volume 状态。不要把 secret 或会话凭证粘贴
到工单。计划维护和回滚步骤见 [公开测试维护](public-test-maintenance.md)。

## Preflight 失败

`preflight-public-test.sh` 只打印固定诊断，不打印 Compose 的原始错误，因为 Compose
渲染内容可能包含 secret。任何 `ERROR` 都会返回非零；先修复再部署。

| code | 含义 | 修复 |
| --- | --- | --- |
| `env_file` | `.env` 缺失 | 从 `.env.example` 创建，权限设为 `0600` |
| `server_env` | 不是 production | 设置 `SERVER_ENV=production` |
| `redis_password_empty` | 有效 Redis 密码为空 | 从 secret store 导出 `REDIS_PASSWORD` |
| `redis_password_example` | 使用示例/占位密码 | 轮换为随机强 secret |
| `redis_password_hardcoded` | Compose 中出现字面密码 | 改用环境引用或 Compose secret |
| `origin_wildcard` | Origin 含通配符 | 写出每个完整 HTTPS Origin |
| `origin_http_public` | 公网 Origin 使用 HTTP | 部署 TLS，改为 `https://...` |
| `allowed_origins` | Origin 为空或格式错误 | 删除路径/凭据/query，使用完整 Origin |
| `trusted_proxies` | 未配置或配置全局网段 | 只列实际最后一跳 `/32` 或最小 CIDR |
| `connection_rate_second` | 每秒连接限流关闭/无效 | 设置正整数 |
| `connection_rate_minute` | 每分钟连接限流关闭/无效 | 设置正整数 |
| `message_rate` | 消息限流关闭/无效 | 设置正整数 |
| `max_connections` | 连接上限无效或超过 1000 | 公开测试推荐 `100` |
| `backend_bind` | 明文后端公开绑定 | 设置 `SERVER_BIND_ADDRESS=127.0.0.1` |
| `redis_ports` | 主 Redis 服务发布宿主机端口 | 删除 Redis 服务的 `ports` |
| `douzero_ports` | DouZero 发布宿主机端口 | 删除 DouZero `ports` |
| `host_network` | 服务使用 host network | 恢复私有 bridge network |
| `privileged` | 服务使用 privileged | 删除 privileged 权限 |
| `game_image_latest` | 游戏镜像不是固定版本 | 设置验证过的 digest 或明确 RC tag |
| `douzero_profile` | 已启用 DouZero 但 profile 未激活 | 命令增加 `--profile douzero` |
| `douzero_image_latest` | DouZero 镜像未固定 | 设置 `DOUZERO_IMAGE_REF` digest |
| `metrics_public` | metrics 随公开绑定暴露 | 恢复 loopback 绑定或关闭 metrics |
| `metrics_path` | metrics 开启但路径为空 | 设置 `/metrics` 或关闭 metrics |

`WARNING [metrics_proxy_acl]` 表示 Compose 本身没有公开监听，但脚本不能读取主机上的
Caddy/Nginx 和防火墙规则。必须从未授权外部地址确认 `/metrics` 返回 403，并从授权
来源确认 200；完成这一步才能接受告警。

`redis-debug` 是显式的本机排障 profile，只绑定 loopback；`redis_ports` 检查针对主
Redis 服务，不会把启用该 profile 报成公网暴露。生产公开测试仍不得启用
`redis-debug`，完成本地排障后应立即移除该容器。

若只看到 `Docker Compose could not render this configuration`，在受控终端本地运行：

```bash
docker compose --env-file .env config --quiet
```

不要把完整 `docker compose config` 输出发到聊天或工单，它可能展开 Redis 密码和
管理密钥。修复语法、缺失变量或镜像引用后重新运行 preflight。

## 容器没有启动

先检查状态和近期日志：

```bash
docker compose --env-file .env ps --all
docker compose --env-file .env logs --since=10m redis
docker compose --env-file .env logs --since=10m poker-server
```

只在受控位置保存日志，并先检查其中没有 Cookie、重连 token、管理密钥、Redis
密码、原始客户端 IP、完整手牌或完整聊天。应用的普通运行日志不记录原始
客户端 IP，但仍可能包含 player ID；对外分享前必须脱敏，只保留时间、
固定事件/原因和必要计数。常见原因：

- `poker-server` 等待 Redis：先修复 Redis healthy，不要取消 `depends_on`。
- production 配置拒绝启动：检查 `ADMIN_KEY` 至少 32 字节、Origin 非通配符、
  Redis 密码非空、限流为正数。
- digest 不存在或平台不匹配：重新核对 registry digest 和主机架构，不要换成
  `latest`。
- 端口 1780 被占用：用 `ss -lntp` 找到已有本机服务；不要改成公网地址绕过冲突。
- volume 或归档目录空间不足：用 `df -h` 检查，先保留最近可验证备份再清理。

`/livez` 200 但 `/readyz` 503 通常表示 Redis 检查失败或进程正在关闭：

```bash
curl --fail http://127.0.0.1:1780/livez
curl --verbose http://127.0.0.1:1780/readyz
```

不要把 `/livez` 当作接流量条件；Compose healthcheck 和公开恢复都以 `/readyz` 为准。

## Redis 不健康

Redis 不发布端口。通过容器内 secret 认证，不把密码放进参数或输出：

```bash
docker compose --env-file .env exec -T redis sh -ec \
  'REDISCLI_AUTH="$(cat /run/secrets/redis_password)" redis-cli ping'

docker compose --env-file .env exec -T redis sh -ec \
  'REDISCLI_AUTH="$(cat /run/secrets/redis_password)" redis-cli CONFIG GET appendonly'
```

预期分别包含 `PONG` 和 `yes`。认证失败时确认运行 Compose 的 secret 环境与现有
容器一致；轮换密码需要计划更新，不要把新旧密码反复写进命令。AOF 错误或磁盘满时
先进入 maintenance、关闭公网代理并保留 `redis-data` volume，再处理磁盘；不要删除
`appendonlydir` 或重建 volume。

## HTTPS 或代理返回 502

按顺序检查：

```bash
curl --fail http://127.0.0.1:1780/livez
curl --fail http://127.0.0.1:1780/readyz
curl --fail http://127.0.0.1:1780/version
sudo ss -lntp
```

本机 1780 失败说明问题在游戏容器、端口映射或 Redis，不在 TLS。只有公网失败时，
验证 Caddy/Nginx 配置、证书和 `proxy_pass`/`reverse_proxy` 是否仍指向
`127.0.0.1:1780`。不要把后端改绑 `0.0.0.0`。

版本不符时同时比较：

```bash
docker compose --env-file .env images
curl --fail http://127.0.0.1:1780/version
curl --fail https://game.example.com/version
```

`server_version` 与 `web_client_version` 应来自同一游戏镜像。代理不得缓存
`/version`；响应应有 `Cache-Control: no-store`。若本机和公网响应不同，检查 DNS
是否仍指向旧主机，或者代理是否指向了错误后端；不要增加第二实例。

## WebSocket 无法升级或反复断线

下面只验证 HTTPS 代理能返回 101，不完成应用 protobuf 握手，也不会玩一局。命令
会在两秒后超时；只看打印的 HTTP code，不要高频重复以免触发连接限流：

```bash
ws_status="$(
  curl --http1.1 --silent --show-error --output /dev/null \
    --write-out '%{http_code}' --max-time 2 \
    --header 'Connection: Upgrade' \
    --header 'Upgrade: websocket' \
    --header 'Sec-WebSocket-Version: 13' \
    --header 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
    --header 'Origin: https://game.example.com' \
    https://game.example.com/ws || true
)"
printf 'WebSocket HTTP status: %s\n' "$ws_status"
test "$ws_status" = 101
```

常见响应：

| 状态 | 原因 | 处理 |
| --- | --- | --- |
| 403 `Origin not allowed` | Origin 与白名单不一致 | 设置精确 `https://game.example.com`，代理保留 Origin |
| 429 | 连接尝试过快 | 停止重试，等待限流窗口；不要关闭限流 |
| 503 `Server Full` | 达到 `SERVER_MAX_CONNECTIONS` | 检查泄漏/异常客户端；不要盲目超过测试容量 |
| 400/426 | Upgrade/Connection 头丢失 | 修复代理的 HTTP/1.1 和 upgrade map |
| 502 | 代理无法连接 loopback 后端 | 检查容器、1780 映射和 ready 状态 |
| 101 后立即关闭 | 只升级但未完成应用握手或版本不兼容 | 使用真实 Web 客户端或完整 smoke 验证 |

浏览器页面必须从与白名单一致的 HTTPS Origin 打开。不要从 `file://`、另一个端口、
IP 地址或旧域名连接生产 WebSocket。Nginx/Caddy 必须转发 `Origin`、`Cookie` 和
`Set-Cookie`，并把 HTTPS 最后一跳声明为精确的 `X-Forwarded-Proto: https`。

## Cookie、身份或重连异常

浏览器会话 Cookie 是 host-only、`HttpOnly`、`SameSite=Strict`、`Path=/`，保存上限
7 天；HTTPS 下还必须带 `Secure`。它不会出现在 JavaScript localStorage。排障时只
检查属性和是否存在，绝不复制值。

Cookie 没有 `Secure` 时，应用没有把最后一跳识别为受信 HTTPS 代理。检查：

1. `SECURITY_TRUSTED_PROXY_CIDRS` 是否包含 Go `RemoteAddr` 的实际 loopback 或
Docker bridge gateway，并且范围没有过宽。
2. 代理是否只发送一个、值严格为小写 `https` 的 `X-Forwarded-Proto`。
3. 更新环境后是否替换了 `poker-server` 容器，而不是只 reload 代理。

反复产生新身份时检查域名、Cookie path、浏览器隐私设置和 `/session/commit`、
`/session/refresh` 是否被代理缓存或丢掉 Cookie。清理 Cookie、换 host、过期后没有
有效凭证，都会产生新身份；当前没有正式账号找回，也不能从 Redis 重建活动会话。

物理断线后的重连窗口固定为 2 分钟。`GAME_OFFLINE_WAIT_TIMEOUT` 只是轮到离线玩家
行动时的等待秒数，不会延长会话窗口。进程重启会丢失会话和活动牌局，即使 Redis
仍有战绩也不能重连原 `GameSession`。

## 管理命令失败

先使用容器内 CLI，不要从宿主机或公网直接调用 HTTP：

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action status
```

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| `ADMIN_KEY is required` | 容器环境没有管理密钥 | 从 secret store 注入并替换游戏容器 |
| 401 | 密钥与服务启动时的值不一致 | 核对 secret 版本，禁止在日志打印密钥 |
| 403 | 非服务进程 loopback，或带转发 header | 使用容器内 CLI；确认公网 `/admin/*` 仍被代理阻断 |
| 405 | 方法错误 | 使用 CLI 或维护手册列出的实际方法 |
| 413 | JSON 超过 4096 字节 | 只发送规定字段 |
| 429 | 每分钟超过 60 个管理请求 | 停止轮询，降低频率 |

`mute`/`ban` 要求 `-admin-player`，还要求 1 秒至 7 天的整秒
`-admin-duration`。`unmute`/`unban` 成功但没有输出是正常的 204。所有控制和 moderation
状态在内存中，重启后消失。

## Draining 一直不能重启

先降低状态轮询频率并同时查看 `active_games` 和 `safe_to_restart`。draining 会
拒绝新建房间、快速匹配、人机练习和 ready/再来一局，但允许现有等待房间加入、
活动牌局继续、取消 ready、离开和重连。状态切换会先等待已进入的创房/入队/人机
短操作离开临界区，阻止新短操作，并取消队列、bot-fill 定时器和未发布 matcher 尝试。

离线玩家可能等待回合 timeout，长局也可能自然持续。如果 `active_games=0` 但
`safe_to_restart=false`，不要强制停机：状态切换/取消边界可能尚未完成，已承认的
牌局可能仍在发布，或终局消息/同步 Redis 战绩结算调用尚未返回，对应 start lease
尚未释放。持续时先查 Redis 健康、
Redis error metric 和不含敏感数据的固定事件日志。该信号只表示同步写入调用已返回，
不保证写入成功。

不要仅因为在线人数不为零而强制停止；重启条件是明确的
`safe_to_restart=true`。超过维护窗口时再次通知用户，再进入 maintenance 和关闭公网
代理。只有业务或安全紧急程度确实高于丢局影响时才强制停止，并在事后明确记录被中止
牌局；Redis 无法恢复它们。

## 备份失败

`backup-redis.sh` 要求 Redis 容器为 healthy、认证 `PING` 成功、`BGSAVE` 在 timeout
内完成，并且本机有 `tar` 与 SHA-256 工具。先检查：

```bash
docker compose --env-file .env ps --all
df -h
./scripts/backup-redis.sh --help
```

使用了非默认 project 或 Compose 文件时，备份和恢复必须传入同一
`--project-name`、`--compose-file` 和 `--env-file`。非默认数据库应在有效环境中设置
同一个 `REDIS_DB`；只有备份命令可用 `--redis-db` 覆盖 key inventory，恢复会从环境
推导目标 DB 并拒绝与归档不一致的值。超时可以在确认 Redis 仍有进展后调整
`--timeout`，不能跳过健康或校验步骤。

备份成功应同时存在 `.tar.gz` 和 `.tar.gz.sha256`。归档固定只含 `dump.rdb` 与
`metadata.txt`；不要手工添加文件或改 metadata。归档可能含战绩和临时会话数据，
权限保持 `0600`，不要作为普通 CI artifact 公开上传。

## 恢复失败或被拒绝

未提供 `--confirm-restore` 是安全拒绝，不是故障。默认模式发现 Redis 或
`poker-server` 正在运行也会拒绝；要么先显式停止两者，要么使用
`--stop-running` 让脚本先在线备份再停止。

SHA-256、文件清单、metadata、`redis-check-rdb` 任一校验失败时，不得绕过。重新从
可信备份位置复制归档和同名 `.sha256`；仍失败则换用上一个已验证备份。恢复后的 key
下限检查覆盖：

- `player:stats:*` 玩家统计数量；
- `leaderboard:score` 总榜是否存在；
- `leaderboard:settlement:*` 结算幂等 key 数量。

恢复脚本不会启动游戏服务。成功后先看 Redis healthy、`/readyz` 和 `/version`，再
启动代理。看到 `automatic rollback was incomplete` 时保持代理和游戏服务停止，
保留 `pre-restore-redis-volume-*.tar.gz`，不要继续写入 volume。

## 排行榜或局数不一致

排行榜支持 `total`、`daily`、`weekly`；每页最多 50，offset 必须实际生效。日榜和
周榜是周期积分，不等于玩家累计总分。同一 `gameID` 的同一玩家只应结算一次，
Redis 中的 `leaderboard:settlement:<gameID>` 用于阻止重复写分。

先保存 smoke 报告和目标版本，不要直接编辑 Redis sorted set。检查是否发生 Redis
错误、跨日/跨周边界、恢复到旧备份或多个测试进程使用同一 Redis。重复结算数量不为
0 时立即进入 draining，保留 Redis volume 和报告，停止继续跑测试；手工删 key 会
破坏取证和幂等性。

## DouZero 异常

DouZero 不健康时先确认它只在私有 network、使用固定 digest，并且 profile 与
`DOUZERO_ENABLED=true` 一致：

```bash
docker compose --env-file .env --profile douzero ps --all
docker compose --env-file .env --profile douzero logs --since=10m douzero
```

服务会对超时、HTTP/JSON 错误和非法动作使用规则引擎生成一次合法回退，不应让牌局
无限等待或跨回合提交旧结果。观察固定 metric：

```text
fight_landlord_bot_invalid_action_total{reason="timeout"}
```

`reason` 只允许 `timeout`、`http_error`、`decode_error`、`not_owned`、
`invalid_hand`、`cannot_beat`、`must_play_pass`、`stale_turn`、
`submit_rejected`。不要把外部响应、完整手牌或可变错误文本变成 label/log。

指标增长但牌局继续说明 fallback 正在保护流程；持续快速增长说明 DouZero 制品或协议
不兼容，应进入 draining，设置 `DOUZERO_ENABLED=false`，通过 preflight 后替换游戏
容器，继续使用内置规则机器人。不要为了排障发布 2021 端口。

## 完整牌局 smoke 失败

只在独立 Compose project、专用 Redis volume 和测试端口运行脚本，绝不能指向已有
玩家数据的部署；测试身份会写入战绩、排行榜和结算幂等 key。脚本需要仓库源码和 Go
工具链。短预设覆盖创建/加入房间、ready、叫地主、合法出牌、结算、再来一局、随机
断线和重连，并生成 JSON 报告与 Markdown 摘要。先建立隔离目标，再运行 workload。

一种最小隔离方式是在维护主机准备 `.env.smoke`，保持待验证 digest，但设置
`COMPOSE_PROJECT_NAME=fight-landlord-smoke` 和未占用的
`SERVER_PORT=1781`，并为下面的规则机器人样例设置 `DOUZERO_ENABLED=false`。
`SERVER_MAX_CONNECTIONS` 至少等于 `players`。下面的 18 人、`0.02` 断线率样例已对
建连和动作做节流，连接和消息限速应保持公开测试的正常安全值。更大的
单 IP 隔离测试可能耗尽每分钟建连额度；应先减少 players/断线率或增加建连节流，
确有需要时只在该隔离 project 调整限额，并另行保留正常限额验证，绝不能改公开测试
`.env`。加载专用 secret 后先 preflight，再启动该 project：

```bash
./scripts/preflight-public-test.sh --env-file .env.smoke
docker compose --env-file .env.smoke \
  --project-name fight-landlord-smoke up -d --wait --wait-timeout 120
curl --fail http://127.0.0.1:1781/readyz
export PUBLIC_TEST_URL=ws://127.0.0.1:1781/ws
export PUBLIC_TEST_METRICS_URL=http://127.0.0.1:1781/metrics
./scripts/run-public-test-smoke.sh \
  --duration 10m \
  --players 18 \
  --disconnect-rate 0.02 \
  --douzero false
```

对启用了 DouZero 的隔离目标运行时，改为固定 `DOUZERO_IMAGE_REF`、
`DOUZERO_ENABLED=true`，给 preflight 和 Compose 命令增加 `--profile douzero`，
并把 workload 参数改成 `--douzero true`。该参数只记录实际目标配置，不能替代
profile 启动。完整牌局 harness 用三名模拟玩家建房，不会证明 inference 确实被调用；
DouZero 还必须单独通过服务 health、非法响应 fault 和规则 fallback 检查。

Compose project 名会给 `redis-data` volume 加独立前缀。运行前仍要用
`docker compose ... config` 和 `ps` 核对目标，确认不是公开测试 project。这只是
制品验证环境，不是同时承载用户的多实例架构。

`smoke` 是默认的 10 分钟预设，`public-test` 是 1 小时预设；只有这两个预设名可用，
`--duration` 会明确覆盖预设时长。`--douzero` 只记录目标是否启用了 DouZero，不会
替你启动、停止或切换推理服务：

```bash
PUBLIC_TEST_URL=ws://127.0.0.1:1781/ws \
PUBLIC_TEST_METRICS_URL=http://127.0.0.1:1781/metrics \
./scripts/run-public-test-smoke.sh smoke

PUBLIC_TEST_URL=ws://127.0.0.1:1781/ws \
PUBLIC_TEST_METRICS_URL=http://127.0.0.1:1781/metrics \
./scripts/run-public-test-smoke.sh public-test
```

脚本默认目标虽然是 `ws://127.0.0.1:1780/ws`，但当 1780 属于已有玩家数据的公开
测试 project 时禁止使用该默认值。从授权的远程测试机验证公网 TLS 时，域名也必须
指向专用隔离 project/Redis，且该主机必须被 metrics ACL 允许：

```bash
PUBLIC_TEST_URL=wss://smoke.game.example.com/ws \
PUBLIC_TEST_METRICS_URL=https://smoke.game.example.com/metrics \
./scripts/run-public-test-smoke.sh smoke
```

若设置了 `SERVER_MIN_CLIENT_VERSION`，还要把
`PUBLIC_TEST_CLIENT_VERSION` 设为满足门槛的实际 release/RC 版本。不要关闭服务端
版本检查。默认报告写到 `artifacts/public-test/smoke-report.json` 和
`smoke-report.md`；一小时预设使用 `public-test-report.*`。可用
`PUBLIC_TEST_OUTPUT_DIR` 改目录，目录应是受控测试 artifact，而不是公开 Web 路径。

JSON 的 `status` 只能是 `passed` 或 `failed`。重点字段为：

- `completed_games`、`failed_games`、`complete_game_success_rate`；
- `reconnect_success_rate` 和 `latency.p50_ms/p95_ms/p99_ms`；
- `initial_rss_bytes`、`peak_rss_bytes`、`final_rss_bytes`；
- `initial_goroutines`、`peak_goroutines`、`final_goroutines`；
- `redis_errors`、`remaining_active_connections`、`remaining_active_rooms`；
- `duplicate_settlements`、`total_games_reconciled`、`leaderboard_verified`；
- `memory_trend_assessed`、`linear_memory_growth`、`threshold_failures`、`errors`。

门槛不通过时 workload 仍写报告并退出 1。workload 拒绝参数、URL、
duration，或它写报告失败时退出 2；wrapper 的输出目录初始化或前置
`go build` 失败只保证非零退出，不承诺固定为 2。自动化仍应归档并阅读 JSON、
Markdown 和 stderr，不能只根据终端的一行摘要判定原因。

通过门槛：

- 至少开始一组完整牌局；
- 完整牌局成功率至少 99%；
- disconnect rate 非零时至少尝试一次重连，重连成功率至少 99%；
- 重复结算为 0；
- Redis error telemetry 可用且增量为 0；
- 结束活动连接、metrics 活动房间和协议房间列表均为 0；
- goroutine telemetry 可用且结束值比起始值增长不超过 10；
- memory telemetry 可用、RSS 没有持续线性增长，运行至少 10 分钟时必须完成趋势评估；
- 玩家总局数与排行榜核对通过；
- started/finished game metrics 与 harness 计数一致；
- Prometheus 采集错误和 harness error 均为 0。

失败时保留 JSON、Markdown、开始/峰值/结束 RSS、goroutine、p50/p95/p99、Redis
错误、剩余连接/房间和重复结算计数。先按首个失败阶段定位，不要仅提高 duration、
连接上限或 timeout 掩盖错误。push/PR workflow 运行缩短到 30 秒、6 名玩家的 CI
smoke；`workflow_dispatch` 可运行完整 10 分钟 `smoke`，1 小时 `public-test` 预设
也只允许手工触发。

所有需要的预设结束并保存报告后，只清理隔离 project：

```bash
docker compose --env-file .env.smoke \
  --project-name fight-landlord-smoke down --volumes --remove-orphans
```

测试结束必须清理测试容器、network、专用 volume、临时文件和测试进程。绝不能把
restore、flush 或清理命令指向部署者的真实 `redis-data`。

## 仍然不支持

以下现象不是可在值班中打开的隐藏功能：多实例、跨实例重连、活动牌局跨进程恢复、
Redis Sentinel、Redis Cluster、正式账号恢复、观战、回放和赛事。需要其中任何能力时，
停止公开扩大规模并单独设计后续项目，不要修改本部署绕过限制。
