# 容量与 Soak 测试

仓库内置的 Go 压测客户端复用正式 protobuf 协议和 WebSocket 协商流程，不依赖
浏览器或第三方压测二进制。它用于建立可重复的单实例容量基线，不代表生产容量承诺。

## 已实现的场景

每次运行按非零配置执行以下场景；普通 `run-load-test.sh` 的 matcher 操作数默认是
`0`，PR smoke 与 soak workflow 会显式设置各自需要的值：

1. 以可配置并发度建立指定数量的 WebSocket 连接，完成 `hello`、协议协商和
   TUI 会话创建。
2. 选取指定数量的连接并发模拟物理断线和重连，验证 player ID 保持不变、旧 token
   被单次消费且新 token 已轮换。
3. 重复执行建房、第二名玩家加入、双方离房；只有完整清理成功才计为成功场景。
4. 以最多两个客户端为一批并发进入匹配队列并并发取消，避免三个客户端被组为真实牌局；
   soak 还会保留一个队列项并观察服务端 30 秒超时事件。
5. 保持所有连接空闲指定时间，随后对每条连接发送一次协议 Ping，证明连接仍可响应。
6. 关闭所有客户端，等待 cooldown，并从 Prometheus 比较基线和最终连接数。

运行期间每秒抓取 `/metrics`，记录服务端 RSS、goroutine 峰值以及 Redis 错误、
慢客户端断开和牌局计数的增量。指标不可用时，服务端字段写为 JSON `null`，不会用
压测进程自身的数据冒充服务端数据。压测进程的 heap/goroutine 另有明确命名的字段。

## 短测试

目标服务必须是隔离的测试实例。默认连接限流为每 IP 每秒 10、每分钟 60；运行
100/500/1,000 连接基线前，应只在该实例提高限流和最大连接数：

```bash
export SERVER_MAX_CONNECTIONS=2000
export SECURITY_RATE_LIMIT_PER_SECOND=2000
export SECURITY_RATE_LIMIT_PER_MINUTE=100000
export SECURITY_MESSAGE_LIMIT_PER_SECOND=100
export REDIS_PASSWORD="$(openssl rand -hex 32)"
docker compose up -d --wait redis poker-server
```

这些变量已由 `docker-compose.yml` 显式传入服务容器；`docker compose config`
可在启动前核对最终值。测试结束后必须执行
`docker compose down --timeout 90 --volumes --remove-orphans`。也可以直接用同一组环境
变量启动 `go run ./cmd/server`，但不能只在一个终端 `export` 后测试另一个未继承环境的
既有服务。

服务就绪后运行：

```bash
./scripts/run-load-test.sh --connections 100 --duration 60s
./scripts/run-load-test.sh --connections 500 --connect-concurrency 20 --duration 5m
./scripts/run-load-test.sh --connections 1000 --connect-concurrency 40 --duration 10m
```

脚本参数会覆盖 `LOAD_TEST_*` 默认值。例如：

```bash
LOAD_TEST_URL=wss://game.example.com/ws \
LOAD_TEST_METRICS_URL=https://game.example.com/metrics \
LOAD_TEST_MAX_P99_MS=750 \
LOAD_TEST_MAX_SERVER_RSS_BYTES=1073741824 \
./scripts/run-load-test.sh --connections 500
```

不要直接对公共服务运行连接风暴。先确认来源白名单、测试账号隔离、出口 IP 和生产
告警抑制策略。

## 门槛

协议正确性门槛默认要求连接、重连、房间场景和空闲后 Ping 全部成功。性能门槛默认
关闭，必须根据同一硬件和配置上的已记录基线设置：

| CLI 参数 | 环境变量 | 禁用值 |
| --- | --- | --- |
| `--max-p99-ms` | `LOAD_TEST_MAX_P99_MS` | `0` |
| `--max-server-rss-bytes` | `LOAD_TEST_MAX_SERVER_RSS_BYTES` | `0` |
| `--max-server-goroutines` | `LOAD_TEST_MAX_SERVER_GOROUTINES` | `0` |
| `--max-final-goroutines-delta` | `LOAD_TEST_MAX_FINAL_GOROUTINES_DELTA` | `-1` |
| `--max-redis-errors` | `LOAD_TEST_MAX_REDIS_ERRORS` | `-1` |
| `--max-final-connections-delta` | `LOAD_TEST_MAX_FINAL_CONNECTIONS_DELTA` | `-1` |

任何启用的门槛在缺少所需遥测时会失败，不会静默跳过。门槛失败返回非零状态。
报告始终记录服务端 goroutine 的 baseline、peak 和 final。普通 load 的最终 goroutine
增量门槛默认禁用，直到同一 runner/配置积累稳定基线；启用时使用例如
`LOAD_TEST_MAX_FINAL_GOROUTINES_DELTA=10`，且遥测缺失会 fail-closed。soak 根据短 smoke
中 baseline/final 均为 13 的一次本地 10 连接 smoke 结果，暂时保守允许 `+10`；
这只是工具正确性证据，不是可跨硬件比较的生产基线。后续应保留同一 runner、配置、
commit 和原始报告并随 nightly 基线收紧；连接回落门槛在 CI 固定为 `0`。

## 输出

默认写入：

```text
artifacts/load/load-test.json
artifacts/load/load-test.md
```

报告包含连接、重连、房间、空闲检查成功率，连接/重连/房间/Ping 的 p50、p95、
p99，服务端资源峰值和清理后的连接数。以下字段有意允许为 `null`：

- `peak_rss_bytes`、`peak_goroutines`：需要 Prometheus process/Go collector；
- `redis_error_count`、`slow_client_disconnect_count`：需要 `/metrics`；
- `server_crash_count`：远程客户端无法可靠区分进程重启和网络中断。

工作流会额外检查服务进程和 `/livez`，因此 CI 中的服务崩溃仍会失败。

## Soak workflow

`.github/workflows/soak.yml` 在定时任务和手动触发时启动隔离 Redis 与服务端，严格
限制总时长，并无条件上传报告、日志和最终 metrics。手动任务提供 100/500/1,000
连接与 5/15/30 分钟选项。也可本地执行：

```bash
SOAK_CONNECTIONS=500 \
SOAK_DURATION=15m \
SOAK_RECONNECTS=50 \
SOAK_ROOM_OPERATIONS=25 \
SOAK_MATCH_OPERATIONS=10 \
SOAK_MATCH_TIMEOUTS=1 \
./scripts/run-soak-test.sh
```

普通 PR 只运行 10 连接、5 秒空闲的短 smoke，以验证工具、协议、重连和房间流程；
它不是性能回归基线。

## 可控故障矩阵

nightly/手动 soak 在容量阶段后执行 `scripts/run-chaos-test.sh`。Redis 服务容器带有
本次 workflow run 的 `fight-landlord.chaos-scope` label，脚本要求 `CHAOS_SCOPE`
精确匹配该 label，并校验 PID 的 `/proc/.../exe` 确实是隔离构建的服务端二进制；
任一校验失败都会在注入故障前退出。执行场景包括：

1. matcher 的并发进入/取消、服务端超时和事务回滚 race 测试；
2. 用显式 channel barrier 模拟延迟读，并验证完全不读会耗尽 256 槽发送缓冲、只计数
   一次且断开客户端；
3. `docker pause/unpause` Redis，验证 `/readyz` 失败、`/livez` 保持成功并恢复；
4. stop/start Redis 模拟短时不可达和单节点重启，验证同一服务进程恢复；
5. 建立一个活动等待房间后向服务端发送 SIGTERM，确认进程正常退出并重启；旧进程的
   token 和等待房间必须被拒绝，客户端取得新会话后 Ping 必须成功；
6. 通过受控 HTTP mock 覆盖 DouZero 正常响应、超时、连接拒绝、畸形 JSON、非法牌和
   服务声明错误，并验证全部故障路径回退到合法的启发式出牌。

故障报告写入 `artifacts/soak/chaos/chaos-report.json` 和 `chaos-report.md`。依赖的
Docker、Go、jq 和 curl 可用时，EXIT trap 会在 fault-target preflight 被拒绝或场景
中途失败后恢复被 pause/stop 的 Redis、清理私密 checkpoint，并写出已执行场景状态。

## 边界与语义

当前 Room、Matcher 和重连 Session 都由单进程内存持有。故障测试中的“连接恢复”
明确表示：重启后拒绝旧进程 token 和旧等待房间，然后证明新会话可用；**不表示**旧
会话或房间跨进程恢复。该预期会同时写入 JSON/Markdown 报告。

尚未覆盖 Redis Sentinel/Cluster/托管 HA、多实例 Room/Matcher 所有权、进行中完整
牌局的重启恢复、特定 WAN 带宽模型以及真实 Python DouZero 模型容量。普通 load
也不会完整打完牌局；`games_started`/`games_finished` 仍来自可选服务端指标。
当前最长自动 soak 选项只有 30 分钟，尚未执行数小时或数天的生产环境 soak；长期
内存碎片、资源缓慢增长和跨日基础设施故障仍是发布后的显式观察项。
