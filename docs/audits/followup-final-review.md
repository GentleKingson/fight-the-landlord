# Follow-up Remediation Final Review

审计日期：2026-07-15（Asia/Hong_Kong）

起点：`codex/web-client-system-remediation` / `ffd7740de7b40fdc48d2afd8d0074c117fa74cff`

修复分支：`codex/web-client-remediation-followup`

最终实现验证提交：`e2bc4c5`

本报告不沿用旧 `final-review.md` 的通过结论。Phase 0 的原始基线记录在
`followup-baseline.md`；以下状态来自本分支实现、定向测试和 2026-07-15
重新执行的最终门禁。

## 结论

代码层面的合并门槛全部通过：存量连接配额、无上限配置、发送/关闭并发、Room
所有权、Matcher 事务回滚、权威匹配超时、结算重连、协议协商、命令幂等、默认
Redis 隔离、可信代理/Origin/CSP、配置失败即退出，以及最终镜像真实三客户端牌局
均有自动化证据。最终 Go race、生成检查、TypeScript、ESLint、单元测试、构建、
规则 benchmark、mock E2E 和最终镜像生产 E2E 均通过，关键测试无 skip。

远端 `Test` workflow 已在修复分支登记并通过
`workflow_dispatch`。最终运行 `29413404382` 对 `11267bc` 执行了 Go lint/
gofmt/proto/race/coverage、Web proto/typecheck/ESLint/unit/benchmark/build/mock E2E，
以及最终镜像 Compose 与 11 项生产 E2E。三个 job 及严格 cleanup 均通过，
并上传 race/coverage、SBOM、trace/screenshot/video/Compose 日志证据。
因此本报告建议合并；剩余项均为不阻塞本次合并的外部部署与长时压测风险。

独立审查随后发现两个此前遗漏的运行时阻断项：重连快照入队失败后 token rotation
未回滚，以及 `activeCommand` 可能按消息类型误捕获并发广播。`6731bb3` 将重连恢复
补成 Room、client registry/identity、session token 三层回滚事务，并把直接命令结果
与领域事件改为显式发送路径。原 token 注入失败后重试、Chat 和 Ready 并发关联/
幂等重放测试均已通过；临时分支 CI trigger、Actions 版本回退和根目录审计报告也已
同步清理。

合并前复核又发现重连凭证采用连接建立后绝对 10 分钟 TTL，导致正常长局在第 10
分钟后失去重连能力。`e2bc4c5` 改为在线期间不按墙钟淘汰凭证，只有物理连接实际
断开时才开启完整两分钟恢复窗口；成功恢复仍原子消费并轮换 token，显式退出仍立即
撤销。Web 不再用本地绝对时间抢先删除服务端凭证，而把有效性判定交给服务端。

## 问题状态

| ID | 状态 | 实现与测试证据 |
| --- | --- | --- |
| P0-01 活跃连接限制 | 完成 | active lease 保持到真实断线；`MaxConnections <= 0` 无限制；覆盖 max=3、第 4 条拒绝、释放后重进、握手失败和重连替换不泄漏。 |
| P0-02 `SendMessage`/`Close` panic | 完成 | producer channel 不再并发关闭，write pump 单写所有权；满缓冲主动断开并计数；数万次 send/close、broadcast/close、shutdown/send、replacement/send 在 race 下通过。 |
| P0-03 Room 并发所有权 | 完成 | 成员、顺序和状态改为 Room 安全 API；锁内复制、锁外发送；GameSession 不再直接读写成员 map；离线私信和 cleanup nil 路径有测试。 |
| P0-04 Matcher 事务 | 完成 | `QueueEntry` + generation/deadline/state/cancel；queued/inflight/commit/rollback 受控；临时房间原子发布，逐步失败完整回滚。 |
| P0-05 Redis 暴露 | 完成 | 默认 Compose 无 Redis host binding；debug profile 仅回环地址；生产密码注入；脚本和最终容器 inspect 双重验证。 |
| P0-06 重连快照失败原子性 | 完成 | `Reconnected` 入队失败会回滚 Room 绑定、client registry/identity 和 token rotation；原 token 可再次恢复同一 player、room 与权威快照。 |
| P0-07 命令响应因果隔离 | 完成 | 只有显式 `SendCommandResult` 携带 request ID 并进入缓存；大厅/房间广播按发起者和其他接收者分别投递；Chat、Ready 交叠事件不再污染缓存。 |
| P1-01 结算重连 | 完成 | canonical proto 增加 settlement；ended 快照保存完整胜者、倍数、分数和剩余手牌；刷新、三方重连、第二局及缺失 settlement 错误 UI 均覆盖。 |
| P1-02 协议兼容 | 完成 | Hello 强制协商 protocol/client version、capabilities、client kind；不兼容/过低版本握手拒绝；旧 Chat fixture 有受控兼容与计数。 |
| P1-03 request ID 和幂等 | 完成 | 所有命令 dispatcher 统一带 `request_id`；叫地主、出牌和不出额外带 `expected_game_id`/`expected_turn_id`；ack/error 关联；服务端 TTL/容量有界缓存复用首次结果并拒绝冲突重放。 |
| P1-04 匹配权威超时 | 完成 | 服务端 deadline/context 独立于浏览器计时；冻结客户端、主动取消、断线、inflight deadline 和 shutdown 均回滚。 |
| P1-05 trusted proxy / Origin | 完成 | 只有配置 CIDR 内的代理来源可提供转发 IP；生产拒绝 `*`；最终镜像同源 Upgrade=101、异源=403。 |
| P1-06 Web 会话和安全头 | 完成（受控 localStorage 方案） | Web 重连凭证仍保存在 `localStorage`；在线凭证由服务端持有，物理断线时才开启两分钟恢复窗口，成功重连单次消费并轮换，退出调用 `/session/revoke`；CSP、nosniff、referrer、permissions、DENY/frame-ancestors 在最终镜像验证。 |
| P1-07 GameSession 注销 | 完成 | Room removal exact-once 通知 Handler；games、timer、Redis、Bot 和 Matcher 关联统一退休；并发删除和长期循环不持续增长。 |
| P1-08 配置验证 | 完成 | `Config.Validate()` 覆盖端口、连接、timeout、Redis、URL、semver、Origin、proxy CIDR、rate limit；非法 env 返回错误，生产启动不回退。 |
| P1-09 生产镜像 E2E | 完成 | 最终 Docker 镜像直接由 Compose 启动，无 Vite/`go run`；Chromium 完整部署/故障/两局流程，Firefox/WebKit 真实连接和选牌；本地和远端均 11/11。 |
| P1-10 CI 合并清理 | 完成 | 仅保留 `push: main`、`pull_request: main`、`workflow_dispatch`；checkout/cache/codecov 恢复主线 v7/v6/v7；actionlint 通过。 |

## P2 状态

| 项目 | 状态 | 证据 |
| --- | --- | --- |
| 长生命周期资源边界 | 完成 | RoomManager、SessionManager、RateLimiter、Matcher 接收 context 并可 Close/Stop；ticker/timer 和 worker 可等待退出。 |
| Web 协议状态边界 | 完成 | `seenGameStreams` 有界；未知 phase 触发协议错误/resync；连续畸形帧达到阈值后关闭并重新协商。 |
| 生成元数据和 int64 策略 | 完成 | required-field 元数据由 canonical proto 生成；Web codec 的 int64 转换策略和边界测试固定。 |
| Chat 输出加固 | 完成 | 服务端清理控制字符/双向覆盖符并命名空间化 message ID；TUI 终端输出转义有测试。 |
| 规则响应性能 | 完成 | 最终复测中，20 张手牌完整响应枚举与 Hint benchmark 的 p99 分别为 16.2844ms、17.4243ms，低于 50ms 门槛。 |
| 原 P2 工程和样式门禁 | 完成 | StrictMode 保留；无 `any` 绕过；gofmt、actionlint、ESLint `--max-warnings=0`、生成 diff、桌面/移动 mock E2E 均通过。 |
| 发布供应链 | 完成（发布流程） | 基础镜像 digest pin；release workflow 生成 provenance/SBOM 并用 cosign 签名；Phase 8 对实际测试镜像另生成 SPDX JSON SBOM。 |

## 分阶段交付

| 阶段 / 根因 | 主要修改与先失败测试 | 最终结果 / 剩余风险 | 提交 |
| --- | --- | --- | --- |
| 0：旧报告无法证明当前分支 | 新建 `followup-baseline.md`，记录工具链、实际命令、warning 和未覆盖路径。 | 基线命令通过，但明确列出所有后续门槛缺口。 | `63d5fde` |
| 1：握手 semaphore 过早释放；closed-check/send 竞态 | `internal/server/connection_limit.go`、`connection.go`、`client.go`；新增真实 WebSocket 配额和高并发发送生命周期测试。 | 全仓 race 通过；慢客户端策略为断开。未做互联网级慢链路 soak。 | `c56ca26` |
| 2：Room 字段由多个包直接读写，网络发送可能发生在锁内 | Room membership/state/broadcast API、session delivery；新增 deal/bid/redeal/GameOver/reconnect/cleanup 并发测试。 | race 通过；锁顺序和锁外 I/O 固定。多实例 Room 所有权仍需外部协调层。 | `e561092` |
| 3：Matcher 用裸 client slice，房间组装可部分提交 | QueueEntry 状态机、room match transaction；对 Create/Join 每一步、断线、取消、Bot 竞争、删除和 deadline 注入失败。 | 所有 rollback/commit race 通过。跨进程匹配不是本阶段目标。 | `72784f6` |
| 4：ended 快照缺 settlement，Room 删除未统一注销 GameSession | canonical settlement、Room removal lifecycle、Redis/Bot/game retirement、Web restore/result UI。 | 刷新/重连/再来一局及长期删除测试通过。进程崩溃中途恢复仍依赖部署级持久化演练。 | `3336f02` |
| 5：连接无强制协议协商；命令无法关联和去重 | Hello/command meta、bounded idempotency cache、legacy Chat、Web dispatcher；新增迟到 ack、重放、冲突、旧 game/turn 测试。 | codec/transport/server/Web 测试与 proto check 通过。未来协议升级仍需维护 capability 矩阵。 | `216b68e` |
| 6：Redis 默认暴露；代理/IP/Origin/session/header 生产边界不足 | Compose/security/web/session、digest pin、release provenance/SBOM/cosign；新增安全单测和部署检查脚本。 | 最终镜像安全头、Origin、Redis 隔离通过。真实 TLS/CDN/LB 未在本机搭建。 | `f761fc4` |
| 7：非法 env 静默；manager/ticker/Web 状态集合可能无界 | Config.Validate、runtime Close、Web LRU/畸形帧、Chat 清理、规则 benchmark。 | race、244 Web 单测、benchmark 均通过。长时间生产 soak 仍未执行。 | `66efe3d` |
| 8：旧 CI 运行 Vite/`go run`，curl 冒烟不能证明发布镜像浏览器和故障路径 | production Playwright 配置、deployment/full-game/fault/cross-browser specs、`production-e2e` job、race/coverage/trace/video/log/SBOM artifacts。初始脚本不存在和旧服务断言先失败。 | 本地最终镜像 11/11；15 screenshots、9 traces、17 videos；远端完整 job 和严格 Compose cleanup 通过。 | `c6157f0` |
| 8 远端门禁：workflow 未登记，存量 Go lint 阻塞 race，旧 setup-protoc 不兼容 Node 24 | 修复分支 push 触发与手动运行；消除 93 个 lint 问题而不隐藏错误；恢复 `"cancelled"` wire 兼容；改为 SHA-256 校验的 protoc 3.13.0 官方资产。 | `golangci-lint` 0 issues；全量 race 与生产 E2E 复测通过；手动运行 `29413404382` 全绿。 | `5e55079`, `6ad793d`, `11267bc` |
| 9 独立审查：重连失败消费 token；广播按类型误入命令缓存 | Room/client/session 三层 rollback；显式 command-result/event sender；发起者专属 Chat/Ready/game event 关联；并发缓存回放和原 token 重试测试；CI/审计文件清理。 | 定向 Go 包、golangci-lint、Web 全门禁、mock E2E、最终镜像 11/11 和 actionlint 通过。 | `6731bb3` |
| 10 合并复核：在线会话仍受绝对 10 分钟重连 TTL 限制 | 在线凭证不按墙钟失效；实际断线时设置完整两分钟 deadline；重复离线信号不能延长窗口；Web 不再按本地时间删除凭证。 | 注入时钟覆盖在线 24 小时、旧 TTL 前十秒断线、完整窗口、失败保留旧 token 和并发单次消费；全量 Go race、Web 244/244、mock E2E 通过。 | `e2bc4c5` |

## 提交清单

| Commit | 目的 |
| --- | --- |
| `63d5fde` | `docs: record follow-up remediation baseline` |
| `c56ca26` | `fix(server-connection): enforce active limits and make client writes race-free` |
| `e561092` | `refactor(room): centralize membership and state synchronization` |
| `72784f6` | `fix(matcher): make queue expiry cancellation and room assembly transactional` |
| `3336f02` | `fix(game-lifecycle): restore settlements and retire removed sessions` |
| `216b68e` | `feat(protocol): negotiate capabilities and correlate idempotent commands` |
| `f761fc4` | `security(deploy): close infrastructure exposure and harden web sessions` |
| `66efe3d` | `refactor(runtime): validate configuration and bound long-lived state` |
| `c6157f0` | `test(production): verify the shipped image with fault-aware browser flows` |
| `5e55079` | `ci: exercise the complete follow-up branch workflow` |
| `6ad793d` | `fix(ci): satisfy the full Go quality gate` |
| `11267bc` | `fix(ci): install a pinned protoc toolchain` |
| `6731bb3` | `fix(server): close post-audit merge blockers` |
| `e2bc4c5` | `fix(session): preserve reconnect window for long sessions` |

### 独立审查后的本地复测

| 实际执行命令 | 结果 |
| --- | --- |
| `go test ./internal/server ./internal/server/handler ./internal/server/session ./internal/game/room ./internal/game/match -count=1`（Go 1.26.1 容器） | 全部通过；包含原 token 故障重试、registry rollback、Chat/Ready 因果隔离与缓存回放。 |
| `golangci-lint run --build-tags=ci`（v2.11.1 容器） | `0 issues.` |
| Web `proto:check`、typecheck、ESLint、Vitest、Vite build | 全部通过；18 files、244/244 tests，94 modules build。 |
| `vitest bench --run tests/rules.bench.ts` | 通过；完整响应枚举 p99 10.9866ms，Hint p99 11.2488ms。 |
| `playwright test` | 沙箱内 Chromium 因 macOS Mach port 权限拒绝启动；同一代码在沙箱外复测 15/15 通过。 |
| `actionlint .github/workflows/test.yml` | 通过，无输出。 |
| `docker build --build-arg VERSION=ci --tag fight-the-landlord:ci .` | 通过；manifest list `sha256:bdab6b6728471e05e13313ff6d1c81c0cf51aa729f44a3428f703743bce385f8`。 |
| 最终镜像 `playwright test --config playwright.production.config.ts` | 11/11 通过（2.4m）；Chromium 部署/故障/双牌局，Firefox/WebKit 真实连接与选牌均通过。 |
| `docker compose ... down --timeout 90 --volumes --remove-orphans` | 通过；测试 server、Redis、network 和 volume 全部删除。 |

### 长连接重连修复复测

| 实际执行命令 | 结果 |
| --- | --- |
| `go test -race ./internal/server/session ./internal/server/handler -count=1`（Go 1.26 容器） | 全部通过；覆盖在线 24 小时、旧 TTL 前十秒断线仍获得完整两分钟窗口、重复离线不续期、轮换失败保留旧 token 及并发单次消费。 |
| `go test -tags=ci -race -p 4 -count=1 ./...`（Go 1.26 容器） | 全部通过，无 race 或失败。 |
| `golangci-lint run --build-tags=ci`（v2.11.1 容器） | `0 issues.` |
| Web `proto:check`、typecheck、ESLint、Vitest、benchmark、Vite build | 全部通过；18 files、244/244 tests；规则枚举和 Hint p99 分别为 10.9315ms、11.9142ms。 |
| `playwright test` | 15/15 通过；desktop、mobile portrait、mobile landscape 均通过。 |

## 最终实际命令与关键原始输出

Go 命令的实际工具链与缓存路径为：

```text
PATH=/tmp/go/bin:/tmp/codex-go-bin:/Users/kingson/.cache/uv/archive-v0/oVf7ZUJW6wX4eqK6VL7gU/torch/bin:/opt/homebrew/bin:/usr/bin:/bin
GOPATH=/tmp/codex-gopath
GOCACHE=/tmp/codex-go-cache
GOMODCACHE=/tmp/codex-go-mod
```

`golangci-lint` 另外使用 `GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache`。

Web 命令的 cwd 为 `web`，其余命令的 cwd 为仓库根目录。最终本地
Compose/Playwright 命令使用以下完整环境：

```text
COMPOSE_PROJECT_NAME=fight-landlord-phase8-final
GAME_IMAGE=fight-the-landlord
IMAGE_TAG=phase8-final
SERVER_ENV=production
SERVER_PORT=1783
REDIS_ADDR=redis:6379
REDIS_PASSWORD=phase8-final-local-4f31e90a
BOT_ENABLED=false
DOUZERO_ENABLED=false
GAME_BID_TIMEOUT=5
GAME_TURN_TIMEOUT=5
GAME_ROOM_CLEANUP_DELAY=0
GAME_OFFLINE_WAIT_TIMEOUT=5
GAME_SHUTDOWN_TIMEOUT=1
GAME_SHUTDOWN_CHECK_INTERVAL=1
SERVER_MIN_CLIENT_VERSION=
SECURITY_ALLOWED_ORIGINS=http://127.0.0.1:1783
E2E_BASE_URL=http://127.0.0.1:1783
E2E_EXPECTED_VERSION=phase8-final
E2E_COMPOSE_PROJECT_NAME=fight-landlord-phase8-final
E2E_COMPOSE_FILE=/Users/kingson/GitHub/fight-the-landlord/docker-compose.yml
E2E_REDIS_SERVICE=redis
E2E_SERVER_SERVICE=poker-server
```

| 实际执行命令 | 关键原始输出 / 结果 |
| --- | --- |
| `make proto-check` | exit 0；重新生成 Go pb、msgtype mapping、Web runtime/types 后 `git diff --exit-code` 无差异。 |
| `golangci-lint run --build-tags=ci` | exit 0；原始输出 `0 issues.`。 |
| `go test -tags=ci -race -p 4 -count=1 ./...` | exit 0；所有含测试 Go 包 `ok`，`internal/server` 4.997s，`internal/transport` 7.486s；无 `WARNING: DATA RACE`、失败或关键 skip。 |
| `npm run proto:check` | exit 0，无生成差异。 |
| `npm run typecheck` | exit 0，无 TypeScript diagnostic。 |
| `npm run lint` | exit 0，ESLint `--max-warnings=0`。 |
| `npm test -- --run` | 18 files passed；244/244 tests passed；duration 7.10s。 |
| `npm run build` | exit 0；Vite 6.4.3，94 modules transformed；主 JS 308.35kB（gzip 92.95kB）。 |
| `npm run bench:rules` | exit 0；enumeration p99 16.2844ms，Hint p99 17.4243ms。 |
| `npm run test:e2e` | 15/15 passed in 10.6s；desktop、mobile portrait、mobile landscape，含截图和 Axe。 |
| `docker compose --env-file .env.example config --quiet` | exit 0。 |
| `./scripts/check-compose-security.sh` | exit 0；原始输出 `Compose deployment security checks passed`。 |
| `actionlint .github/workflows/test.yml` | exit 0，无输出。 |
| `docker build --build-arg VERSION=phase8-final --tag fight-the-landlord:phase8-final .` | exit 0；最终 distroless 镜像构建成功；manifest list `sha256:4f238624aaca641606bc7bae6d0b659b3fa41d606505c4f7f44a80f40300e31b`。 |
| `docker compose --project-name fight-landlord-phase8-final --file docker-compose.yml up -d --wait redis poker-server` | exit 0；Redis 与 `poker-server` 原始状态均为 `Healthy`。 |
| `npm run test:e2e:production -- --project=webkit-smoke` | 1/1 passed in 5.9s。 |
| `npm run test:e2e:production -- --project=firefox-smoke` | 1/1 passed in 6.9s。 |
| `npm run test:e2e:production` | 11/11 passed in 1.6m；JSON duration 94,588ms，expected 11、skipped 0、unexpected 0、flaky 0；Chromium 9 项，Firefox/WebKit 各1项。 |
| `jq '.stats' web/test-results/production/results.json` | 输出字段值：`duration=94588.268`、`expected=11`、`skipped=0`、`unexpected=0`、`flaky=0`。 |
| `find web/test-results/production -type f -name '*.png' \| wc -l`<br>`find web/test-results/production -type f -name 'trace.zip' \| wc -l`<br>`find web/test-results/production -type f -name '*.webm' \| wc -l`<br>`test -s web/test-results/production/results.json` | 原始计数依次为 `15`、`9`、`17`；`results.json` 非空。 |
| `docker compose --project-name fight-landlord-phase8-final --file docker-compose.yml down --timeout 90 --volumes --remove-orphans` | exit 0；server/Redis 停止并删除，network/volume 删除。 |
| `docker ps -a --filter name=fight-landlord-phase8-final --format '{{.Names}} {{.Status}}'`<br>`docker volume ls --filter name=fight-landlord-phase8-final --format '{{.Name}}'`<br>`docker network ls --filter name=fight-landlord-phase8-final --format '{{.Name}}'` | exit 0；三条命令原始输出均为空。 |
| `git diff --check` | exit 0，无输出。 |
| `curl --fail --location --retry 3 --proto '=https' --tlsv1.2 https://github.com/protocolbuffers/protobuf/releases/download/v3.13.0/protoc-3.13.0-linux-x86_64.zip --output /tmp/protoc-3.13.0-linux-x86_64.zip`<br>`shasum -a 256 /tmp/protoc-3.13.0-linux-x86_64.zip` | exit 0；原始 SHA-256 为 `4a3b26d1ebb9c1d23e933694a6669295f6a39ddc64c3db2adf671f0a6026f82e`。 |
| `unzip -q -o /tmp/protoc-3.13.0-linux-x86_64.zip -d /tmp/protoc-ci-test`<br>`docker run --rm --platform linux/amd64 --volume /tmp/protoc-ci-test:/protoc:ro golang:1.26 /protoc/bin/protoc --version` | exit 0；Linux 原始输出 `libprotoc 3.13.0`。 |
| `gh workflow run test.yml --ref codex/web-client-remediation-followup`（首次） | workflow 未登记时 HTTP 404；`5e55079` 增加修复分支 push 触发后登记成功。 |
| `gh run watch 29411312010 --exit-status` | Web job 通过；Go job 在 golangci-lint 阶段失败，未绕过门禁，后续修复 93 个实际 lint 问题。 |
| `gh run watch 29413116989 --exit-status` | Go lint/gofmt 已通过；第三方 `arduino/setup-protoc@v3` 在 Node 24 下报 `unable to get latest version`，于是改用官方资产+校验和本地 Linux 执行验证。 |
| `gh run watch 29413404382 --exit-status` | exit 0；Go checks 1m58s、Web checks 1m16s、Shipped-image production E2E 4m40s；run 对应完整 SHA `11267bc8f0b733c66304affa160cad4257db5085`。 |
| `gh api repos/GentleKingson/fight-the-landlord/actions/runs/29413404382/artifacts --jq '.artifacts[] \| {name,size_in_bytes,expired,archive_download_url}'` | 3 个未过期 artifact：`go-race-and-coverage` 50,280 bytes，`production-image-sbom` 107,780 bytes，`production-e2e-evidence` 150,228,409 bytes。 |

Phase 0 的完整原始工具版本、`npm ci`、旧 `test:e2e:real`、Compose 和
`VERSION=followup` 镜像结果保存在 `followup-baseline.md`，没有用旧报告数字替代。

## 分阶段失败优先与定向复测

Phase 1-7 的修复前定向失败 stdout 只存在本次 Codex 任务执行记录中，没有伪装成
独立持久 artifact；下表如实保留当时的命令和失败结论。可持久复核的证据是各阶段
提交中的测试源码、最终全仓 race 日志与远端 artifacts；Phase 8 和远端 CI 的失败则另有
运行 ID 可查。

| 阶段 | 实际定向命令 / 失败优先结果 | 修复后结果 |
| --- | --- | --- |
| 1 | `go test -race ./internal/server -run 'TestHandleWebSocket_|TestClient_SendMessageAndClose|TestServer_(BroadcastAndClose|ShutdownAndSend|ReconnectReplacementAndSend)'`；新测试在旧实现上复现握手后配额提前释放、`max=0` 全拒绝与 send/close 竞态。 | 真实 WebSocket 配额、释放和四类并发发送用例全部通过 race。 |
| 2 | `go test -race ./internal/game/room ./internal/server/session`；deal/bid/redeal/GameOver/reconnect 与 Room broadcast/remove 用例先暴露外部直接访问成员状态和锁内发送路径。 | Room 和 GameSession 定向包与全仓 race 通过。 |
| 3 | `go test -race ./internal/game/match ./internal/game/room -run 'TestMatcher|TestMatchRoom'`；故障注入先复现部分 Join 可见、inflight 无法取消和冻结客户端不超时。 | Create/Join 各边界、deadline、Bot 竞争、room removal 和 shutdown rollback 全部通过。 |
| 4 | `go test -race ./internal/server/session ./internal/server/handler ./internal/game/room`；ended DTO/reconnect/removal 新断言先证明 settlement 缺失且删房不会统一退役 GameSession。 | 三方结算快照、结果页恢复、exact-once removal 和长期循环测试通过。 |
| 5 | `make proto-check`、`go test -race ./internal/protocol/... ./internal/server/... ./internal/transport`、`npm test -- --run`；握手拒绝、迟到 ack、重放/冲突 request ID 和旧 game/turn 用例在旧协议上失败。 | Go/Web codec、命令 dispatcher、有界幂等缓存和 legacy Chat fixture 通过。 |
| 6 | `./scripts/check-compose-security.sh`、`docker compose --env-file .env.example config --quiet`与 security/session 定向 Go 测试；旧 Compose 默认发布 Redis，代理/Origin/header/token 失败分支不满足新断言。 | 部署检查脚本、安全单测和最终镜像 header/Origin/Redis inspect 通过。 |
| 7 | `go test -race ./internal/config ./internal/game/... ./internal/server/...`、`npm test -- --run`、`npm run bench:rules`；非法 env、worker 停止、无界 Web 状态、畸形帧和 Chat 控制字符用例先失败。 | 全量 race、244 个 Web 单测与两个 p99 < 50ms 的 benchmark 通过。 |
| 8 | 首次 `npm run test:e2e:production` 因脚本不存在失败；针对旧 1782 服务的 deployment/fault 断言失败。最终本地复测中，一次未将 Compose env 传给故障子进程，导致它重建成默认远端镜像；日志明确显示错误镜像和旧 `-healthcheck` 参数。 | 传入与 CI 一致的完整 env 后，当前 `phase8-final` 镜像 11/11；远端 `29413404382` 再次执行并通过。 |

## Race 与故障注入证据

- 连接：`TestHandleWebSocket_LimitTracksActiveConnections`、
  `ZeroMeansUnlimited`、`UpgradeFailureReleasesCapacity`、
  `ReconnectReplacementDoesNotLeakCapacity` 全部包含在最终 race。
- 发送生命周期：`TestClient_SendMessageAndCloseAreConcurrentSafe`、
  `BroadcastAndClose`、`ShutdownAndSend`、`ReconnectReplacementAndSend` 通过。
- Room/GameSession：disconnect during deal、caller/landlord disconnect、redeal offline、
  GameOver/reconnect、cleanup/reconnect、broadcast/leave、info/delete 均通过 race。
- Matcher：Create/Join 各步骤失败、queued/inflight cancel、冻结客户端超时、Bot/human
  竞争、room removal 和 shutdown rollback 均通过 race。
- 资源注销：Room removal exact-once、Redis delete、Bot close、game timer retirement、
  matcher association cleanup 的并发测试通过。
- 浏览器故障：匹配主动取消与断线、发牌前断线、地主确定时断线、SIGTERM 优雅关闭/
  重启、Redis pause 导致 `/readyz` 503 而 `/livez` 和既有 WebSocket 保持正常，全部通过。

## 最终镜像三客户端结果

Chromium 在三个隔离 browser context 中直接访问 Compose 暴露的最终镜像。流程实际
完成：建房、两人加入、三人准备、叫/抢、提示出牌、不出、牌局聊天；同一客户端在
牌局中连续两次关闭页面并重连；三人完成 GameOver 并比对同一 settlement；结果页
reload 后 settlement 不变；三名客户端逐一重连仍得到完整结算；三人点击再来一局，
完成第二局并全部返回大厅。测试不启动 Vite、不调用 `go run`、不使用 demo/mock
transport。

部署断言同时证明 HTML、哈希 JS/CSS、immutable cache、CSP 和其他安全头、
`/health`、`/livez`、`/readyz`、`/version=phase8-final`、同源/异源 WebSocket Upgrade，
以及 Redis container 无 host port binding。

## 尚存风险

1. 本机和 GitHub 验证都是单实例 Compose。真实 TLS 证书、CDN、反向代理、负载均衡、多实例
   房间/匹配故障转移和跨区域网络没有做端到端演练。
2. 已有 race 和确定性故障注入，但没有持续数小时的随机断网、进程崩溃、Redis
   failover 和高连接数 soak；容量上限仍需按生产硬件压测。
3. 外部 DouZero profile、模型拉取和推理服务没有进入最终 E2E；本次明确禁用它以
   验证核心服务不依赖可选组件。
4. Web 重连凭证仍在 `localStorage`，因此同源 JavaScript 在会话期间可读取；
   当前用严格 CSP、断线后两分钟有效期、单次消费/轮换和显式撤销降低风险，
   但未达到 HttpOnly cookie 对凭证脚本可见性的隔离程度。
5. Playwright 在本机输出宿主 `NO_COLOR` 与 `FORCE_COLOR` 同时存在的 Node warning；
   这不是 TypeScript、Go、ESLint 或生成文件 warning。基线 `npm ci` 的间接依赖
   deprecation（`whatwg-encoding`、`inflight`、`glob@8`）也仍需随依赖升级处理。
6. 匹配断线浏览器用例以“重连后能建房”观察回滚；更细的队列/事务无残留证明来自
   Go Matcher 定向 race/fault 测试，而非浏览器直接读取内部状态。
7. protoc 3.13.0 为现有生成契约所需的固定工具链；CI 已校验官方资产 SHA-256，
   但未来升级 protobuf 时需同步更新该版本、摘要与生成快照。

## 合并建议

所有列入“十二、合并门槛”的实现、本地自动化和远端完整 workflow 条件均满足，
没有删除/skip 关键测试，也没有关闭 race detector 或 React StrictMode。
建议合并 `codex/web-client-remediation-followup`；发布时应保留运行
`29413404382` 的 race/coverage、SBOM 和 production E2E artifacts 作为审计证据。
