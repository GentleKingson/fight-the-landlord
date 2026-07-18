# 公开测试维护手册

本文是单节点公开测试的值班步骤。所有命令都从仓库部署目录执行，并使用同一
Compose project、`.env` 和已加载的 `REDIS_PASSWORD`、`ADMIN_KEY`。不要把管理
密钥、Redis 密码、Cookie、重连 token、玩家完整手牌或完整聊天内容写入工单和日志。

部署边界和代理配置见 [小规模公开测试部署](small-public-test.md)，Redis 脚本的完整
行为见 [Redis 备份与恢复](redis-backup.md)。

## 运行状态

| 状态 | 新建房间 | 快速匹配 | 人机练习 | 已有牌局 | 断线重连 | 用途 |
| --- | --- | --- | --- | --- | --- | --- |
| `normal` | 允许 | 允许 | 允许 | 继续 | 允许 | 正常开放 |
| `draining` | 拒绝 | 拒绝 | 拒绝 | 继续至结束 | 允许 | 计划更新前排空 |
| `maintenance` | 拒绝 | 拒绝 | 拒绝 | 继续至结束 | 允许 | 故障或维护窗口 |

`draining` 不会踢出现有玩家，也不会中断活动牌局。它仍允许玩家加入一个已经存在的
等待房间，但新的 ready/再来一局不能启动牌局，因此不要把它描述为完全断流。
等待房间和所有连接仍在 Go 进程内，进程重启时都会丢失；值班必须以
`safe_to_restart` 而不是在线人数或 `active_games` 单一字段作为最终闸门。

状态是进程内数据，不写入 Redis。新进程始终从 `normal` 启动。因此更新时必须在
公网代理处维持外部维护闸门，直到新进程验证完成；不能指望旧进程的
`maintenance` 状态跨重启保留。

状态切换会向所有客户端广播：

- `draining`：`服务器正在排空，已停止新房间、新匹配和人机练习`
- `maintenance`：`服务器正在维护，已停止新游戏入口`
- `normal`：发送 `MaintenancePush=false` 解除维护标记，不附加错误提示

进入 draining 前还应通过测试邀请渠道发布：

> 服务器即将维护，现已停止创建新房间、快速匹配和人机练习。当前牌局可以完成，
> 请不要开始下一局；排空后会短暂断线，未完成牌局不能跨服务重启恢复。

## 本机管理命令

管理 HTTP 接口只接受进程 loopback 请求。Docker 端口转发后的宿主机 `curl` 可能
表现为 bridge peer 并被 403 拒绝，这是预期边界。使用镜像内置 CLI 从容器网络
命名空间请求 `127.0.0.1`；CLI 从容器环境读取 `ADMIN_KEY`，不会把密钥放入 argv：

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action status
```

状态响应字段为：

```json
{
  "state": "draining",
  "active_games": 0,
  "safe_to_restart": true,
  "online_players": 4,
  "muted_players": 1,
  "banned_players": 0,
  "server_version": "v0.6.0-rc.1"
}
```

`active_games` 只计算正在叫地主或出牌的房间。`safe_to_restart` 是更严格的最终
信号：只有状态不是 `normal`、`active_games=0`、所有已承认的牌局启动 lease
均已释放，而且状态切换/取消边界已完成时才为 `true`。lease 在 ready 或 matcher
真正启动牌局前获取，一直保留到
终局事件投递且同步 Redis 战绩/排行榜结算调用返回。状态切换本身还会等待
已进入的创建房间、入队和人机练习短操作离开临界区，拒绝之后的操作，并取消
队列和未发布的 matcher 尝试。因此 `active_games=0` 只是必要条件；终局投递或 Redis
调用仍在返回途中时，它可以已为零而 `safe_to_restart` 仍为 `false`。该信号表示结算
调用已返回，不代表 Redis 写入必然成功；同时还要检查 Redis error 指标和日志。

切换状态：

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action drain

docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action maintenance

docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action resume
```

断开指定玩家：

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action disconnect -admin-player PLAYER_ID
```

临时禁言、解除禁言：

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action mute -admin-player PLAYER_ID -admin-duration 30m

docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action unmute -admin-player PLAYER_ID
```

临时封禁、解除封禁：

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action ban -admin-player PLAYER_ID -admin-duration 2h

docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action unban -admin-player PLAYER_ID
```

时长必须是正的整秒倍数，最大 7 天，例如 `30m`、`2h` 或 `168h`。禁言只阻止聊天；
封禁会关闭当前连接并阻止该玩家重新连接。`disconnect` 只关闭当前连接，不等于封禁。
`unmute`、`unban` 和重复状态请求是幂等操作。禁言和封禁保存在内存中，服务重启后
丢失；确需跨维护继续时，验证新版本后重新执行并记录新的到期时间。
这些控制只按 player ID 生效，不是账号或 IP 封禁；用户清除 Cookie 获得新身份后
可能绕过。它们只适合 10 至 50 人测试，不应描述成完整内容审核能力。

成功的 `status` 以及所有已认证的状态、断开、禁言和封禁/解除请求都会写入不含
密钥的结构化审计日志。变更请求记录 action、`applied` 或 `no_change`、可选玩家 ID
和到期时间；`status` 记录 `success`。拒绝日志只记录固定原因
`non_loopback`、`wrong_method`、`rate_limit`、`authentication` 或
`invalid_body`。管理员应在独立值班记录中写明操作者、
理由和工单号，但不要复制 secret 或聊天正文。

### HTTP 接口契约

CLI 使用的实际接口如下，供本机测试和故障定位。不要在 Caddy/Nginx 发布这些路径。
所有响应都带 `Cache-Control: no-store`；认证 header 是 `X-Admin-Key`，比较采用
恒定时间方式；JSON body 最大 4096 字节；总管理请求限制为每分钟 60 次。
即使连接地址是 loopback，只要请求带 `Forwarded`、`X-Forwarded-For` 或
`X-Real-IP` 也会被拒绝，避免公网代理把外部请求伪装成本机调用。

| 方法和路径 | JSON body | 成功响应 |
| --- | --- | --- |
| `GET /admin/status` | 无 | 状态 JSON |
| `POST /admin/drain` | 空或 `{}` | 状态 JSON |
| `POST /admin/maintenance` | 空或 `{}` | 状态 JSON |
| `POST /admin/resume` | 空或 `{}` | 状态 JSON |
| `POST /admin/disconnect` | `{"player_id":"PLAYER_ID"}` | `204` |
| `POST /admin/mute` | `{"player_id":"PLAYER_ID","duration_seconds":1800}` | 玩家和到期时间 JSON |
| `POST /admin/unmute` | `{"player_id":"PLAYER_ID"}` | `204` |
| `POST /admin/ban` | `{"player_id":"PLAYER_ID","duration_seconds":7200}` | 玩家和到期时间 JSON |
| `POST /admin/unban` | `{"player_id":"PLAYER_ID"}` | `204` |

非 loopback 请求为 403，缺失或错误密钥为 401，错误方法为 405，超限为 429。
不要用带 `-H 'X-Admin-Key: ...'` 的公开 `curl` 命令替代 CLI，因为 header 参数可能
出现在进程列表或 shell history 中。

## 备份策略

至少在每次部署、回滚或数据操作前创建备份：

```bash
./scripts/backup-redis.sh --env-file .env
```

默认在 `REDIS_BACKUP_DIR` 创建 `redis-backup-<UTC>-<pid>.tar.gz` 和同名
`.sha256`，保留 `REDIS_BACKUP_KEEP` 个标准归档。需要临时保留 14 个：

```bash
./scripts/backup-redis.sh --env-file .env --keep 14
```

脚本先验证容器 healthy 和认证 `PING`，等待新的 `BGSAVE`，然后归档
`dump.rdb` 与 `metadata.txt`。metadata 记录项目/Redis 版本、实际镜像、数据库、
key 数量下限和内部 RDB 校验；不会输出 Redis 密码。备份目录为 `0700`，归档为
`0600`。

把归档和 `.sha256` 一起同步到受控的异机存储，并定期在隔离 Compose project
恢复验证。可使用 systemd timer、cron 和 rclone，但 secret 应来自权限受限的
EnvironmentFile 或 secret manager，不能写入 crontab 命令。该项目不提供远端上传
或跨区域灾难恢复。

## 计划更新流程

以下流程保留固定镜像引用（首选 digest）、阻止新牌局、等待安全重启信号、
备份 Redis，并在公开
恢复前验证新进程。示例假定反向代理只服务本应用；共享代理必须使用等价的站点级
503 维护路由，不能停止其他站点。

1. 在约定维护时间前通知测试用户，然后进入 draining。

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action drain
```

2. 轮询状态。只有同时看到 `"state":"draining"`、`"active_games":0` 和
`"safe_to_restart":true` 才继续。最后一个字段是权威闸门；不得因为活动计数已归零就
跳过终局投递、Redis 结算返回和牌局启动 lease 的等待。

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action status
```

3. 备份 Redis，并记录命令输出中的归档和 `.sha256` 路径。

```bash
./scripts/backup-redis.sh --env-file .env
```

4. 保存当前的完整镜像选择和最低客户端版本。digest 部署使用 `*_IMAGE_REF`；
明确 RC tag 部署还必须保存两个仓库名和 `IMAGE_TAG`，否则无法精确回滚。记录不应包含
secret；本流程不修改已经验证的代理配置。

```bash
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
install -d -m 700 "backups/releases/$stamp"
grep -E '^(GAME_IMAGE|DOUZERO_IMAGE|IMAGE_TAG|GAME_IMAGE_REF|DOUZERO_IMAGE_REF|REDIS_IMAGE|SERVER_MIN_CLIENT_VERSION)=' \
  .env >"backups/releases/$stamp/image-refs.env"
chmod 600 "backups/releases/$stamp/image-refs.env"
```

5. 把旧进程切到 maintenance，再关闭专用公网代理。此时已完成牌局，用户已收到
应用广播；代理关闭是为了防止新进程默认 `normal` 时提前接收新游戏。

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action maintenance
sudo systemctl stop caddy
# 使用 Nginx 时改为：sudo systemctl stop nginx
```

6. 在 `.env` 中写入已经验证的新 `GAME_IMAGE_REF` 和可选
`DOUZERO_IMAGE_REF`；如明确选用 RC tag，则同时更新对应仓库变量和 `IMAGE_TAG`。保持
Redis digest 不变，重新运行 preflight，然后只拉取目标服务。任何 `ERROR`
都必须先修复。以下是 DouZero 关闭时的流程：

```bash
vim .env
./scripts/preflight-public-test.sh --env-file .env
docker compose --env-file .env pull poker-server
```

DouZero 启用时，preflight 和 pull 都增加 `--profile douzero`，并先替换推理服务：

```bash
./scripts/preflight-public-test.sh --env-file .env --profile douzero
docker compose --env-file .env --profile douzero pull douzero poker-server
docker compose --env-file .env --profile douzero \
  up -d --no-deps --wait --wait-timeout 120 douzero
```

7. 替换游戏服务，不重启 Redis：

```bash
docker compose --env-file .env \
  up -d --no-deps --wait --wait-timeout 120 poker-server
docker compose --env-file .env ps --all
```

8. 在代理仍关闭时验证本机 health、ready 和 version。`server_version` 与嵌入的
`web_client_version` 应对应目标制品。

```bash
curl --fail http://127.0.0.1:1780/livez
curl --fail http://127.0.0.1:1780/readyz
curl --fail http://127.0.0.1:1780/version
```

9. 新进程默认 `normal`；在重新开放代理前把它显式切回 maintenance。启动代理后
验证 HTTPS、version、WebSocket Upgrade、Cookie 和 metrics ACL。

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action maintenance
sudo systemctl start caddy
# 使用 Nginx 时改为：sudo systemctl start nginx
curl --fail https://game.example.com/livez
curl --fail https://game.example.com/readyz
curl --fail https://game.example.com/version
```

WebSocket 和 ACL 的无敏感信息检查命令见
[公开测试排障](public-test-troubleshooting.md#websocket-无法升级或反复断线)。

10. 在恢复 normal 之前，确认这个精确镜像引用已在独立 Compose project 和专用 Redis
volume 上通过短 smoke；代理可以保持开启，但新服务必须保持 maintenance。不要对已有
玩家数据的 Redis 运行完整牌局 workload，因为测试身份会写入战绩和排行榜。报告通过
后才执行 `resume`、确认状态并发布恢复通知。

```bash
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action resume
docker compose --env-file .env exec -T poker-server \
  /app/server -admin-action status
```

恢复通知使用：

> 维护已完成，服务已恢复。请刷新页面后再开始新牌局；若浏览器生成了新身份，请注意
> 当前测试版本不提供正式账号找回。

## 应用回滚

应用回滚通常只恢复上一组已保存镜像变量（首选 digest）和
`SERVER_MIN_CLIENT_VERSION`，不恢复 Redis。内嵌 Web 客户端和 Go 服务在同一
游戏镜像中，必须一起回滚。

如果新版本已开放，先按计划流程进入 draining，等待 `safe_to_restart=true`，再创建
一份当前数据备份。然后切换 maintenance、关闭公网代理，从
`backups/releases/<UTC>/image-refs.env` 把旧值写回 `.env`。以下是 DouZero 关闭时的
流程：

```bash
vim .env
./scripts/preflight-public-test.sh --env-file .env
docker compose --env-file .env pull poker-server
docker compose --env-file .env \
  up -d --no-deps --wait --wait-timeout 120 poker-server
curl --fail http://127.0.0.1:1780/readyz
curl --fail http://127.0.0.1:1780/version
```

启用 DouZero 时必须回滚同一组已保存镜像引用，先替换并等待 DouZero healthy，再替换
游戏服务：

```bash
./scripts/preflight-public-test.sh --env-file .env --profile douzero
docker compose --env-file .env --profile douzero pull douzero poker-server
docker compose --env-file .env --profile douzero \
  up -d --no-deps --wait --wait-timeout 120 douzero
docker compose --env-file .env --profile douzero \
  up -d --no-deps --wait --wait-timeout 120 poker-server
```

按更新流程把新进程切到 maintenance，恢复代理，验证 HTTPS/WebSocket/metrics ACL，
并确认回滚镜像引用的隔离 smoke 报告后，最后执行 `resume`。如果恢复代理后的任一
验证失败，立即再次停止该站点代理或恢复 503 gate，并保持关闭，不要让不确定版本
接收新牌局。

## Redis 恢复

只有新版本确实写入不兼容或损坏数据时才恢复 Redis。应用镜像回滚本身不需要恢复；
恢复会丢弃备份时间点之后的战绩，而且不能恢复活动 `GameSession`。

先确认非 `normal` 状态下 `safe_to_restart=true`，再关闭公网代理。默认模式拒绝覆盖
正在运行的游戏或 Redis：

```bash
archive=./backups/redis/redis-backup-20260717T120000Z-1234.tar.gz
docker compose --env-file .env stop poker-server redis
./scripts/restore-redis.sh \
  --env-file .env \
  --confirm-restore \
  "$archive"
```

也可以明确要求脚本先在线备份再停止两个服务：

```bash
./scripts/restore-redis.sh \
  --env-file .env \
  --stop-running \
  --confirm-restore \
  "$archive"
```

脚本校验外部和内部 SHA-256、固定归档清单及 RDB，修改前创建原 volume 快照，恢复
后检查 Redis healthy、认证 `PING`、AOF，以及 `player:stats:*` 和
`leaderboard:score` 的保存下限。`leaderboard:settlement:*` 有 30 天 TTL，备份会记录
盘点数量但恢复时不作持久 key 下限。脚本绝不自动启动
`poker-server`。成功后人工检查并启动：

```bash
docker compose --env-file .env ps --all
docker compose --env-file .env up -d --wait --wait-timeout 120 poker-server
curl --fail http://127.0.0.1:1780/readyz
curl --fail http://127.0.0.1:1780/version
```

看到 `automatic rollback was incomplete` 时，保持游戏服务和公网代理停止，保存
日志与 `pre-restore-redis-volume-*.tar.gz`，按 [Redis 备份与恢复](redis-backup.md)
人工处理。

## 紧急维护

安全事件或数据损坏无法等待自然排空时，仍要先进入 maintenance、发送明确通知并
关闭公网代理，再停止服务。记录哪些牌局会被中止；不要假称能够恢复活动牌局。
能安全读取 Redis 时先备份，不能时保留 volume，不要反复重启覆盖证据。

紧急恢复后重新检查固定镜像引用、preflight、health、version、WebSocket、metrics
ACL、排行榜和 smoke。封禁/禁言及运行状态因重启丢失，需要按值班记录重新应用。
