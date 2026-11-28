# 开发与回归约定

## 组织与风格

- Go 游戏会话位于 `internal/game/session`，共享持久化位于 `internal/storage`；HTTP/WebSocket 接入保留在 `internal/server`。使用 goimports、gofumpt 和现有 golangci-lint 配置。
- 前端协议 DTO 位于 `web/src/protocol`，应用状态类型位于 `web/src/stores/types.ts`；牌桌工具抽屉作为完整交互单元独立维护，继续使用单一 appStore。保持 TypeScript strict，使用 Prettier。
- Python HTTP 入口保留为 `douzero/server.py`，经典规则合法动作计算位于 `douzero/src/inference.py`；Marvel 使用 Python 3.12、CPU PyTorch、uv 锁文件和固定上游提交。使用 Ruff 基础检查和 unittest。
- 输入验证放在系统边界，内部逻辑使用已成立的类型和状态约束。保留既有错误码、消息、回退及协议兼容路径；行为变更单独评审。
- 仅格式化有实质改动的文件；生成代码、第三方源码、模型不参与格式化。提交钩子检查发生改动的前端与 Python 文件。

规范依据：[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)、[Google Go Style](https://google.github.io/styleguide/go/)、[React Effect 指南](https://react.dev/learn/you-might-not-need-an-effect)、[TypeScript Narrowing](https://www.typescriptlang.org/docs/handbook/2/narrowing.html)、[Google TypeScript Style](https://google.github.io/styleguide/tsguide.html)、[PEP 8](https://peps.python.org/pep-0008/)、[unittest](https://docs.python.org/3.12/library/unittest.html)。仓库业务约束优先，规范不作为批量机械改写的依据。

## 验证命令

```bash
make lint
go build ./...
DDZ_REDIS_INTEGRATION=1 go test -race -count=1 -p 4 ./...
python3 -m unittest discover -s scripts/tests -v
npm test --prefix web
npm run build --prefix web
npm run format:check --prefix web -- src/stores/types.ts src/features/table/UtilityDrawer.tsx
uv sync --project douzero --locked --inexact
uv run --project douzero --locked ruff check douzero
uv run --project douzero --locked python -m unittest discover -s douzero/tests -v
```

Redis 集成测试需要 `redis-server` 与 `redis-cli` 位于 PATH。启动 Redis 和构建后的 Go 服务后，设置 `DDZ_LIVE_URL` 执行 `npm run test:e2e --prefix web`，包含真实三人房间和关闭末个标签页后的重连测试。默认浏览器测试启动 Vite 开发服务器。

Python 的 `uv sync --inexact` 保留安装脚本编译的 nanopokerlib。真实 Marvel 权重测试需要先执行 `scripts/setup-marvel.sh`、提供两份权重，然后设置 `MARVEL_INTEGRATION=1`。无权重回归必须明确报告该测试跳过。

## 重构测试覆盖对应关系

| 调整 | 保留保障 |
| --- | --- |
| session、storage 目录迁移 | 原 Go 测试随包迁移，断言保留；共享 Redis 夹具复用，自动关闭客户端 |
| UI 类型归位、抽屉提取、在线状态分支合并 | 保留全部前端单测和浏览器回归，包括焦点、Escape、出牌、结算与重连 |
| 经典规则计算从 HTTP 入口迁出 | 共享 `testdata/classic-rules.json` 分类、比较、出牌生成测试保持；合并同义过滤函数后，250 组固定种子随机输入与原实现逐项一致 |
| ONNX 历史特征测试迁移 | Marvel 编码测试覆盖炸弹动作及两次 PASS；保留初始手牌、历史一致性、农民共享策略测试 |
| ONNX 模型下载测试删除 | 对应下载功能已随 ONNX 明确移除；Marvel 权重加载与真实完整牌局测试保留 |
| Marvel 唯一后端 | 新增 HTTP 健康、路由、合法出牌/PASS、空手牌原有错误、非法动作、后端异常、请求大小/JSON 及启动配置回归 |

保留不同语言的规则实现及协议编解码边界，各自承担服务端裁决、前端提示和模型合法性校验。保留独有回归用例；不按测试数量或代码行数设精简指标。
