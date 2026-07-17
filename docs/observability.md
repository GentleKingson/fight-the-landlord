# 可观测性

服务端默认在 `/metrics` 暴露独立 Prometheus registry，并在生产 Compose 中输出
JSON 日志。配置项如下：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSERVABILITY_METRICS_ENABLED` | `true` | 是否启用指标端点 |
| `OBSERVABILITY_METRICS_PATH` | `/metrics` | 不得与业务路由冲突的绝对路径 |
| `OBSERVABILITY_LOG_FORMAT` | `json` | `json` 或 `text` |

端点默认与游戏 HTTP 端口共用监听地址。互联网部署必须在反向代理、网络策略或
防火墙中限制访问；关闭指标时配置路径明确返回 404，不会回退到 SPA。

## Prometheus

```yaml
scrape_configs:
  - job_name: fight-the-landlord
    metrics_path: /metrics
    static_configs:
      - targets: [poker-server:1780]
```

`poker-server` 只在加入 Compose `poker-network` 的容器内可解析；容器化 Prometheus
必须显式加入该网络并限制网络成员。宿主机或外部 Prometheus 应通过 loopback/私网
代理抓取并配置 `/metrics` ACL，不能为了让示例可达而公开 1780 后端端口。

指标清单：

```text
fight_landlord_websocket_connections_current
fight_landlord_websocket_connections_total
fight_landlord_websocket_rejected_total
fight_landlord_slow_client_disconnects_total
fight_landlord_reconnect_attempts_total
fight_landlord_reconnect_success_total
fight_landlord_reconnect_failure_total{reason}
fight_landlord_rooms_current
fight_landlord_games_current
fight_landlord_games_started_total
fight_landlord_games_finished_total
fight_landlord_game_duration_seconds
fight_landlord_room_cleanup_total
fight_landlord_match_queue_current
fight_landlord_match_wait_seconds
fight_landlord_match_cancelled_total{reason}
fight_landlord_match_transaction_rollback_total{stage}
fight_landlord_commands_total{type,result}
fight_landlord_command_latency_seconds{type}
fight_landlord_protocol_errors_total{reason}
fight_landlord_idempotency_cache_hits_total
fight_landlord_idempotency_conflicts_total
fight_landlord_redis_operation_seconds{operation}
fight_landlord_redis_errors_total{operation}
fight_landlord_readiness_status
fight_landlord_bot_decision_seconds{engine}
fight_landlord_bot_timeouts_total{engine}
fight_landlord_bot_fallback_total{from,to}
```

Go runtime 和 process collector 同时提供 goroutine、heap、CPU 和 RSS 等标准指标。
所有应用 label 都经过固定白名单归一化，未知值记为 `other`，不使用 `player_id`、
`room_id`、`game_id` 或昵称。

建议至少告警：readiness 变为 0、Redis error 增长、连接拒绝/慢客户端断开突增、
匹配回滚持续增长、重连失败率升高、goroutine 或 RSS 在负载结束后不回落。阈值应
先基于同一硬件上的容量报告建立，不要照搬未经测量的绝对数字。

## 结构化日志

`json` 是生产默认值，`text` 仅用于本地交互调试。当前具备固定 schema 的事件包括：

| `event` | 主要字段 | 语义 |
| --- | --- | --- |
| `server_starting`, `server_start_failed`, `shutdown_started` | `protocol_version`, `error_code` | 进程启动/关闭边界 |
| `websocket_connected`, `websocket_rejected` | `player_id`, `client_kind`, `protocol_version`, `error_code` | WebSocket 接受或握手拒绝 |
| `command_dispatch` | `request_id`, `player_id`, `client_kind`, `type`, `result`, `duration_ms`, `error_code` | 已注册 handler 返回或未知类型被拒绝；`completed` 不等于业务状态成功 |
| `room_created`, `room_joined`, `room_left`, `room_cleaned` | `room_id`, `player_id`, `reason`, `result` | 权威房间生命周期 |
| `match_enqueue`, `match_rollback`, `match_success` | `player_id`, `room_id`, `mode`, `reason`, `stage`, `result`, `duration_ms` | 匹配队列与事务结果 |

不同事件只携带适用字段，不伪造空的 `game_id` 或 `request_id`。目前 reconnect 的业务
成败主要由 `fight_landlord_reconnect_*` 指标和协议结果表达；没有独立的结构化
`reconnect_success` 事件，不能把 `command_dispatch completed` 解释成重连成功。

统一字段词汇为：

```text
event level request_id game_id room_id player_id client_kind
protocol_version error_code duration_ms
```

仍有遗留 `log.Printf` 生命周期尚未迁移；它们通过同一 JSON/text handler 输出，但
只有上表事件承诺固定字段。敏感属性名会输出
`[REDACTED]`；仍应在日志采集器上增加 Cookie/token/password 规则并限制日志
访问权限。Redis hook 只记录归一化操作名和耗时，不记录命令参数。

## 运行检查

```bash
curl --fail http://127.0.0.1:1780/livez
curl --fail http://127.0.0.1:1780/readyz
curl --fail http://127.0.0.1:1780/metrics
```

`/livez` 只说明进程 HTTP handler 可响应；`/readyz` 会检查 Redis 和关闭状态；
`fight_landlord_readiness_status` 与最近一次 readiness 检查结果一致。
