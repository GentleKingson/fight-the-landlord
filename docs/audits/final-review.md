# Web 客户端系统整改最终审计

审计日期：2026-07-15（Asia/Hong_Kong）  
起点：`agent/import-upstream-pr-65-web-client` / `9b98c237c40a55a60808cb96f89cbdc70144cb90`  
修复分支：`codex/web-client-system-remediation`

## 结论

所有 P0、P1 和 P2 验收项均已实现并通过对应自动化检查。canonical protobuf
生成检查、Go/TypeScript 双向 codec、完整规则黄金用例、连续令牌轮换重连、真实
三客户端完整牌局、Docker 镜像和 Compose 生产冒烟均通过。没有删除、跳过关键
测试，没有关闭 React StrictMode，也没有用 `any`、静默异常或纯延时掩盖失败。

## 问题状态

| ID | 状态 | 修复证据 |
| --- | --- | --- |
| P0-01 重连凭证与身份绑定 | 完成 | provisional identity、原子身份迁移、令牌轮换/回滚、连续重连与房间/叫分/出牌恢复测试 |
| P0-02 出牌合法性和提示 | 完成 | Web 全牌型纯函数规则、Go 权威校验、共享黄金用例、合法响应生成与按钮门禁 |
| P0-03 命令静默失败与状态分叉 | 完成 | `SendResult`、逐命令 pending/ack、统一业务错误、服务端事件提交关键状态 |
| P0-04 生产部署 | 完成 | Vite 构建 + Go embed、SPA/cache/version/health、公开基础镜像、Compose 与容器冒烟 |
| P1-01 协议单一来源 | 完成 | `internal/protocol/proto` 唯一来源；确定性 TS 生成器和双向 fixture |
| P1-02 完整快照 | 完成 | 完整 `GameStateDTO`、ended 阶段、绝对 deadline、幂等 snapshot 恢复 |
| P1-03 记牌器 | 完成 | 完整牌堆减手牌、公开底牌和已出牌；重复/重连事件不重复扣减 |
| P1-04 Store 状态语义 | 完成 | connection/lobby/room/game/chat/UI slices；game/turn/version 水位线 |
| P1-05 WebSocket 容错 | 完成 | 编解码错误、退避+jitter、Pong watchdog、online/visibility、单资源约束 |
| P1-06 聊天隔离 | 完成 | lobby/room/game 分桶、权威上下文、稳定 ID 去重、服务端防伪造和限流错误 |
| P1-07 辅助功能 | 完成 | 牌 button/`aria-pressed`、roving focus、键盘/触摸/拖拽、drawer focus/inert、Axe |
| P1-08 测试覆盖 | 完成 | 211 单测、15 mock/截图/a11y E2E、1 个真实三客户端完整牌局、Go race 全仓库 |
| P2 工程和样式问题 | 完成 | 真实 ESLint、CSS 分域、Demo 门禁/乱码、gofmt/actionlint、部署文档与 CI |

## 分阶段审计

| 阶段 | 根因与改动文件 | 新增/强化测试与结果 | 剩余风险 |
| --- | --- | --- | --- |
| 0 基线 | 原分支只有 18 个 Web 单测和 9 个 demo E2E，Go 不在宿主 `PATH`；新增 `review-baseline.md` | `npm ci`、18/18 单测、build、9/9 demo E2E 通过；原始 `go test ./...` 因宿主无 Go 记录为环境阻断 | 基线仅描述问题，不修改行为 |
| 1 协议 | Web 复制 proto、运行时拼接 schema、unknown fallback；修改 canonical proto、`web/scripts/generate-protocol.mjs`、generated codec/types、Go codec/convert、Makefile，删除 `web/src/protocol/proto/*` | `make proto-check` 通过；Go/TS 双向 fixtures 覆盖所有消息、int64/零值/空数组/Unicode/Chat/Reconnected | 两端生成器升级时仍需同步审查数值边界 |
| 2 重连 | 首个 Connected 覆盖旧凭证，服务端映射替换后再找旧 client，cleanup 删除身份；修改 server client/session/handler/room 与 `App.tsx`、`wsClient.ts`、store | session/handler/room race 测试和 `wsClient`/store 测试通过；覆盖无效/过期 token、连续两次轮换、StrictMode、多标签及各游戏阶段 | 未做跨地域高延迟与进程崩溃恢复压力测试 |
| 3 规则 | 前端仅按张数/点数猜牌型和提示；修改 Go rule 响应生成、共享 JSON golden、`web/src/game/rules.ts`、牌面模型与操作门禁 | Go rule race 测试、57 个 Web golden、53 个 Web 规则测试及桌面操作测试通过 | 两套实现将来新增规则时必须同时更新 golden |
| 4 命令/传输 | send 静默失败、乐观提交、重复点击、半开连接无检测；修改协议错误关联、matcher/room/handler、command dispatcher、socket、错误/版本 UI | 命令、socket、畸形帧、断网、重连风暴、重复点击、matcher/room/handler 测试通过 | 浏览器后台极端节流行为只做事件级自动化，未长时间 soak |
| 5 快照/Store | 快照字段不足、事件无水位、客户端重置倒计时、记牌重复扣减；修改 room/session DTO/events/timer、全部 store slices、clock/counter | session/handler/room race 测试及 authoritative/store/codec reducer 测试通过 | 旧式无版本帧只在建立 versioned stream 前兼容 |
| 6 聊天/交互 | 聊天跨上下文污染且元数据可伪造；牌组和 drawer 缺少一致键盘/指针语义；CSS 单文件叠加；修改 chat handler/access、Lobby/Table/Hand/Card/Drawer、store、分域样式、ESLint | Go chat race 测试通过；Web 211/211；桌面、移动竖/横屏截图与 Axe 共 15/15；Hand 8、Chat 5、Drawer 3 项专项测试通过 | 自动化无障碍不能替代真实读屏用户测试 |
| 7 部署/CI | 原镜像无 Web、Compose 强依赖 DouZero、布尔 env 不能关闭默认值、无版本/SPA/生产联机 CI；修改 Docker/Compose/config/server embed/health/version、部署文档、workflow 和 real E2E | real E2E 1/1、镜像构建、Compose health/version/SPA/cache、生产 Chromium `/ws` 均通过；actionlint 通过 | 见“尚存风险” |

每阶段的精确文件清单可由下列对应提交的 `git show --name-only <hash>` 重现；
表中列出了行为边界和主要所有者文件，测试文件与实现保存在同一逻辑提交中。

## 提交

| 提交 | 目的 |
| --- | --- |
| `d0cefa6` | `docs: record web integration baseline` |
| `2f2808e` | `build(protocol): generate web codec from canonical protobuf schema` |
| `9e6a2a8` | `fix(reconnect): make session restoration atomic and repeatable` |
| `d267946` | `fix(web-rules): validate selected hands and generate legal hints` |
| `18bd1c2` | `fix(web-transport): make commands observable and resilient` |
| `11544df` | `refactor(web-state): restore complete authoritative game snapshots` |
| `2e19ea4` | `refactor(web-ui): improve card input accessibility and isolate styles` |
| `3977374` | `feat(web-deploy): ship and verify the production web client` |

## 最终测试证据

| 实际命令 | 结果 |
| --- | --- |
| `make proto-check`（指定 protoc、protoc-gen-go、Go 1.26.2） | 通过；Go pb、msgtype mapping、Web generated codec 均无 diff |
| `actionlint .github/workflows/test.yml` | 通过 |
| `git ls-files '*.go' \| xargs gofmt -l` | 通过；输出为空 |
| `go test -tags=ci -race -p 4 -count=1 ./...` | 通过；所有 Go 包完成，无失败或跳过的游戏关键测试 |
| `npm run proto:check && npm run typecheck && npm run lint && npm test -- --run && npm run build` | 通过；17 files、211/211 tests；ESLint 0 warning；Vite 126 modules |
| `npm run test:e2e` | 通过；Chromium 桌面、移动竖屏、移动横屏共 15/15，含截图非空/边界检查和 Axe WCAG AA |
| `npm run test:e2e:real` | 通过；1/1，测试 21.5s、总计 24.5s |
| `docker compose --env-file .env.example config --quiet` | 通过；默认仅 Redis + poker-server，DouZero 为显式 profile |
| `docker build --build-arg VERSION=v0.5.3 --tag fight-the-landlord:codex .` | 通过；Node 22/Vite + Go 1.26 static embed + distroless runtime |
| Compose `up -d --wait redis poker-server`（1782/6380） | 通过；两个容器均 `healthy`，DouZero 未启动 |
| `curl` 检查 `/health`、`/version`、`/`、`/room/smoke` 和哈希资源 HEAD | 通过；版本 `v0.5.3`，SPA fallback 一致，JS/CSS 为一年 immutable cache |
| 无头 Chromium 打开 `http://127.0.0.1:1782/` 并等待“创建房间” | 通过；生产版本门禁、页面资源、Origin 白名单和同源 `/ws` Upgrade 均生效 |

Playwright 真实联机使用三个相互隔离的 browser context，连接同一 Go 服务和
Redis，依次完成建房、两人加入、三人准备、叫/抢地主、提示后出牌、不出、牌局
聊天、三轮以上继续出牌、关闭并重建第三个页面完成身份重连、持续合法出牌至
GameOver，最后返回大厅。该测试不使用 demo state 或 mock transport。

## 镜像冒烟结果

- 镜像：`fight-the-landlord:codex`，发布版本 `v0.5.3`。
- `/health` 返回 `OK`；`/version` 同时报出 server/web `v0.5.3`。
- 首页和 `/room/smoke` 返回同一带版本 meta 的嵌入 index。
- `index.html` 为 `no-cache` + ETag；哈希 JS/CSS 为
  `public, max-age=31536000, immutable`。
- 容器内建健康检查通过，浏览器可以从生产页面进入真实 WebSocket 大厅。

## 尚存风险

1. 浏览器自动化覆盖 Chromium；尚未对 Firefox、WebKit、真实 iOS/Android 和
   屏幕阅读器做人工兼容性验收。
2. 已实现按页面协议选择 `wss://` 并给出 Caddy/Nginx 配置，但本次本地验收未
   搭建真实证书、CDN 或生产负载均衡器做端到端 TLS/Upgrade 测试。
3. `/health` 是进程 liveness；启动时会验证 Redis，但运行中 Redis 失联不会立刻
   把该端点切为失败。Redis AOF 恢复、故障转移和高并发容量仍需部署环境演练。
4. 重连和乱序已有确定性测试与真实中途重连，但未做长时间随机丢包、浏览器崩溃
   和多实例服务端切换的压力测试。
5. DouZero profile 的 Compose 图已验证，但最终运行冒烟使用内置/禁用机器人，
   未拉取并运行外部 DouZero 推理镜像。
6. 本机 Playwright 输出了宿主同时设置 `NO_COLOR`/`FORCE_COLOR` 的 Node 环境
   提示；它不来自 TypeScript/Go 编译，也不影响 CI。Docker `npm ci` 另报告
   jsdom 间接依赖 `whatwg-encoding` deprecated，后续依赖大版本升级应单独评估。
