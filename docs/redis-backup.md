# Redis 备份与恢复

Compose 的 Redis 使用命名 volume `redis-data`，开启 AOF，且不发布宿主机端口。
下面的脚本通过容器内的认证连接操作 Redis，不需要临时开放 6379，也不会打印密码。
备份包含战绩、排行榜以及当时仍在 Redis 中的临时 key，但不能恢复 Go 进程内的
活动牌局、匹配队列所有权或 WebSocket 会话。恢复必须安排维护窗口。

## 准备

脚本需要 Docker Compose v2、`tar`，以及 `sha256sum` 或 `shasum`。使用与部署
相同的 Compose project、`.env` 和 secret：

```bash
cd /srv/fight-the-landlord
read -rsp "Redis password: " REDIS_PASSWORD && export REDIS_PASSWORD
```

不要把真实 `REDIS_PASSWORD` 写入仓库、命令行参数或 cron 文件。默认备份目录为
`./backups/redis`，目录权限为 `0700`，归档和校验文件权限为 `0600`。可在 `.env`
中设置 `REDIS_BACKUP_DIR` 和 `REDIS_BACKUP_KEEP`；非默认数据库使用 `REDIS_DB` 或
备份命令的 `--redis-db`。

同一个 Compose project 的备份和恢复通过 `/tmp` 中的互斥锁串行执行。如果脚本被
`kill -9` 或主机掉电打断，确认没有同项目操作仍在运行后，才可按报错路径删除遗留
锁目录；不要为了绕过正在运行的恢复而删除锁。

## 创建备份

```bash
./scripts/backup-redis.sh --env-file .env

# 保留最近 14 个标准归档
./scripts/backup-redis.sh --env-file .env --keep 14
```

脚本要求 Redis 容器为 `healthy` 且认证 `PING` 成功，然后请求并等待一次新的
`BGSAVE`。输出包括：

- `redis-backup-<UTC>-<pid>.tar.gz`：`dump.rdb` 和 `metadata.txt`；
- 同名 `.sha256`：整个压缩归档的 SHA-256；
- metadata 中的 Redis 版本、实际运行的项目镜像引用和 image ID、源码版本、
  RDB SHA-256 以及关键 key 下限。

保留数量只删除同目录下最旧的 `redis-backup-*.tar.gz` 及校验文件，不删除恢复前
自动创建的 volume 快照。定期检查剩余空间，并在另一台受控主机验证恢复。

可用 cron 定时调用备份脚本，再使用 rclone 或其他工具把归档和 `.sha256` 一起同步
到远端对象存储。本项目不负责远端凭据、上传或跨区域灾难恢复。

## 恢复

先进入 draining/维护状态，等待或结束当前牌局并停止游戏服务。标准流程默认拒绝
覆盖仍在运行的 Redis 或 `poker-server`：

```bash
archive=./backups/redis/redis-backup-20260717T120000Z-1234.tar.gz

docker compose stop poker-server redis
./scripts/restore-redis.sh \
  --env-file .env \
  --confirm-restore \
  "$archive"
```

如果启用过本地 `redis-debug` profile，也必须先停止它。脚本会拒绝任何仍在运行的
`redis-debug`，防止恢复和验证期间有本地客户端写入新数据。

也可以显式传入 `--stop-running`。此模式先创建一份在线标准备份，再依次停止
`poker-server` 和 Redis。无论使用哪种模式，脚本都不会自动重新启动游戏服务。

恢复过程会按顺序执行：

1. 校验归档 `.sha256`、内部 RDB SHA-256、固定文件清单和 metadata，并确认归档
   `REDIS_DB` 与目标部署一致；
2. 使用部署所固定的 Redis 镜像运行 `redis-check-rdb`；
3. 在修改 volume 前创建 `pre-restore-redis-volume-*.tar.gz` 和校验文件；
4. 清除旧 RDB/AOF 文件，安装目标 RDB，并通过仅使用 Unix socket 的临时 Redis
   转换成新的 multipart AOF，然后启动正式 Redis；
5. 检查容器健康、认证 `PING`、AOF 已启用，以及 `player:stats:*`、
   `leaderboard:settlement:*` 和 `leaderboard:score` 的预期数量。

成功后先检查 Redis 和服务版本，再启动游戏服务：

```bash
docker compose ps --all
docker compose up -d poker-server
curl --fail http://127.0.0.1:1780/readyz
curl --fail http://127.0.0.1:1780/version
```

恢复失败时，脚本会停止新 Redis，并尽力从恢复前的原始 volume 快照回滚。如果
Redis 在脚本开始时正在运行，回滚会重新启动 Redis；`poker-server` 始终保持停止，
以免在数据状态未人工确认前接受流量。看到 `automatic rollback was incomplete` 时
不要继续启动服务，保留该 volume 快照和日志进行手工恢复。

## 升级与回滚

升级前先执行备份，再保存当前 `GAME_IMAGE_REF`、可选 `DOUZERO_IMAGE_REF`、
`SERVER_MIN_CLIENT_VERSION` 和代理配置。生产部署使用已经验证的
`repository@sha256:...`；小规模测试可以使用明确的 RC tag（例如
`v0.6.0-rc.1`），但不要把 `latest` 当成可回滚版本。

回滚应用镜像通常不需要恢复 Redis。只有迁移或新版本确实写入了不兼容数据，并且
已经接受备份时间点之后的战绩会丢失时，才执行 Redis 恢复。恢复 Redis 仍不会恢复
任何活动 `GameSession`。
