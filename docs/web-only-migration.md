# Web-Only 升级与数据保留

项目仅支持网页游玩。CLI、安装器、自动更新、聊天和 `/version` 接口已退役，旧 CLI 无法连接；历史源码可从 Git 标签获取。CLI 用户首次访问网页会创建游客身份，本次不提供 CLI 身份导入。现有网页用户在原域名下继续使用原 Cookie。

默认入口为根 `Dockerfile` 和 `docker-compose.yml`。游戏镜像包含网页与 Go 服务，AI 镜像默认运行 MARVEL；两个模型由部署者提供并挂载到 `/models`。推理服务仅保留 MARVEL，统一使用 Python 3.12 和 uv 锁文件。默认开启匿名牌局采集，可通过 `COLLECT_TRAINING_DATA=false` 关闭。

## 现有 Web 部署

统一入口保留 `server`、`ai`、`redis` 服务名及 `redis-web`、`training-data` 卷键。实际卷名由 Compose 项目名决定，因此升级时必须沿用原项目名、原目录和原公网域名。不得同时启动两个游戏服务实例连接同一个 Redis。

1. 升级源码前，在旧部署目录执行 `docker compose -f compose.web.yml ps`，记录项目名。用 `docker inspect` 查看旧 Redis 和游戏容器的 Mounts，记录真实卷名。
2. 停止旧游戏服务，再停止 Redis；保留容器和卷。对停止后的 Redis 卷、训练数据卷分别做完整备份。下面的备份示例适用于旧 Web 和旧正式部署。
3. 更新源码，保留 `.env` 和 `douzero/models/`，提供 `landlord.pt`、`farmers.pt`，确认容器用户可读。使用 `docker compose -p "$COMPOSE_PROJECT_NAME" config --quiet` 校验配置。
4. 本地构建运行 `docker compose -p "$COMPOSE_PROJECT_NAME" up -d --build`。使用已发布版本时先设置两个镜像的版本标签，再执行 `pull` 和 `up -d`。
5. 检查新容器 Mounts 与升级前一致，再用原浏览器检查身份、战绩和保留期内的牌局恢复；检查训练记录继续进入原卷。

`COMPOSE_PROJECT_NAME` 应设置为步骤 1 中记录的真实项目名。若目录或项目名必须改变，使用下面的 external 卷映射指定原卷，训练卷也应显式映射。升级和回退均使用 `stop` 或普通 `down`，保留所有持久卷。

## 旧正式部署的 redis-data 卷

旧正式部署的服务名为 `poker-server`、`douzero`、`redis`，Redis 卷键为 `redis-data`。切换到新入口前，先停止旧游戏服务和 AI，再停止 Redis。保留原 `.env` 中的 Redis 密码与数据库编号；新 Compose 支持 `REDIS_PASSWORD`、`REDIS_DB`。

记录实际 Redis 卷名后，在一个受控备份目录执行以下命令。`REDIS_VOLUME` 填入已检查的真实卷名，`BACKUP_DIR` 使用已存在的绝对路径；备份文件名必须尚不存在。

```sh
docker volume inspect "$REDIS_VOLUME" >/dev/null && \
docker run --rm --user 0 \
  -v "$REDIS_VOLUME:/source:ro" -v "$BACKUP_DIR:/backup" \
  alpine:3.23 sh -c 'umask 077; test ! -e /backup/redis-volume.tar && tar -cpf /backup/redis-volume.tar -C /source .'
```

现有 Web 的训练卷按同样方式备份为 `training-volume.tar`。备份时相关写入服务须已停止。保留整个 Redis 目录，包括 AOF 文件、清单和 RDB，不拼接不同时点的数据。

在部署目录新建 `compose.volumes.yml`：

```yaml
volumes:
  redis-web:
    external: true
    name: ${REDIS_VOLUME:?Set the existing Redis volume name}
```

若要映射已有训练卷，在上述 `volumes` 下增加：

```yaml
  training-data:
    external: true
    name: ${TRAINING_VOLUME:?Set the existing training volume name}
```

旧正式部署尚无训练卷时，保留默认创建行为。使用两个配置文件运行：

```sh
docker compose -p "$COMPOSE_PROJECT_NAME" -f docker-compose.yml -f compose.volumes.yml config --quiet
docker compose -p "$COMPOSE_PROJECT_NAME" -f docker-compose.yml -f compose.volumes.yml up -d --build
```

external 映射要求指定卷已存在，卷名填错会直接失败。原来的 `poker-server` 与 `douzero` 容器保持停止，验收后再单独移除旧容器。新旧服务不能同时占用端口或写入同一个 Redis。

## 恢复备份与回退

停止新游戏服务和 Redis。创建一个新空卷，将完整备份恢复进去，保留原卷供检查：

```sh
docker volume create "$RESTORED_REDIS_VOLUME"
docker run --rm --user 0 \
  -v "$RESTORED_REDIS_VOLUME:/restore" -v "$BACKUP_DIR:/backup:ro" \
  alpine:3.23 sh -c 'test -z "$(ls -A /restore)" && tar -xpf /backup/redis-volume.tar -C /restore'
```

将 `REDIS_VOLUME` 指向恢复卷，按相同配置启动 Redis，再启动一个游戏服务实例。训练卷按相同过程恢复并显式映射。备份之后产生的数据不会包含在该恢复点中。

回退应用时使用升级前的 Git 标签、镜像版本和 Compose 配置，仍保持单实例写入。先验证健康检查、原浏览器身份、战绩和结算次数，再开放访问。权威快照与完整 Redis 恢复要求见[重启恢复说明](restart-recovery.md)。
