## Grill Me Workflow

When the user requests "grill-me" or "使用 grill-me":

1. Locate the root of the selected Git repository.
2. Read `.agents/skills/grilling/SKILL.md` directly.
3. Follow its complete instructions as the active workflow.
4. Use repository evidence to resolve factual questions.
5. Wait for the user's decisions between rounds.
6. Do not modify project files until the user explicitly
   approves implementation.

This workflow must work without native skill registration,
skill discovery, slash commands, or the Skill tool.

## 日常开发与验证

- 默认零新增测试：修改业务、UI、音效、样式或交互时，不新建或扩写 `*_test.go`、`web/tests/**/*.test.*`、E2E 或 `douzero/tests/*.py`。仅在用户明确要求「补测试」时增加持久化测试；核心逻辑例外仅授权运行已有测试。
- 修改完成后执行 `make check-fast`，依靠 Go build/vet/lint、严格 TypeScript 检查、Python Ruff 和协议生成一致性约束。缺工具或检查失败要明确报告，不能跳过后声称通过。`make lint` 是全量 Go 静态检查；`CHECK_BASE` 可覆盖默认的 `origin/main` 增量基线。
- 修改外部输入、WebSocket 解码或房间/对局状态转换时，在所改入口先验证参数、身份和合法状态，失败立即返回明确错误。不以静态检查代替边界校验。
- 运行时逻辑用一次性命令验证一条主流程和一条典型异常；UI 外观检查真实渲染，交互检查实际操作。临时验证脚本用完清理，不提交；执行环境要求保留的沙盒备份目录除外。
- 日常仅在修改以下核心底线时运行对应的已有定向测试，不自动执行全量单测、race、覆盖率或 E2E：
  - 跨语言牌型：`testdata/classic-rules.json`、`internal/game/rule/`、`web/src/shared/cards/rules.ts`、`douzero/src/move_*.py`，使用共享样例核对 Go/Web/Python。
  - 重连、重启恢复、房间/对局状态机：`internal/game/session/`、`internal/server/recovery.go`。
  - 身份隔离与安全：`internal/server/security.go`、`internal/server/identity.go`。
- 发版或大改前，用户可显式调用 `make test-core` 运行已有牌型测试和界面冒烟；AI 不将它挂入日常校验。CI 继续全量测试，不受本地运行限制。
- `.proto` 是协议源；修改后执行 `make proto`，同步 Go pb、消息映射和 TS 生成文件，禁止手改生成产物。`make check-proto` 可强制核对漂移。
- 每次提交前基于 `git diff` 用 3–5 行说明：静态检查结果、主流程/异常实测证据、并发/状态机/重连风险和未验证部分。不提交时在交付说明中给出同等记录。
