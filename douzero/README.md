# Marvel 推理服务

使用 MARVEL v1.3 的 CPU 策略，通过现有 DouZero HTTP 协议为 Go 游戏服务提供出牌决策。地主使用 `landlord.pt`，两个农民席位共享 `farmers.pt`。

## 本地运行

使用 Python 3.12 和 uv 0.13.5。在仓库根目录运行：

```bash
bash scripts/setup-marvel.sh
douzero/.venv/bin/python douzero/server.py
```

安装脚本从 `pyproject.toml` 和 `uv.lock` 安装 CPU 依赖，并编译固定提交的 nanopokerlib；需要 Git、C++ 编译器和 make。模型由部署者提供，放入 `douzero/models/landlord.pt` 与 `douzero/models/farmers.pt`。来源为 [GentleKingson/doudizhu](https://github.com/GentleKingson/doudizhu)，固定提交为 `acdcf199cf422e2b683cb22142b8e7cb33b14460`。

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `DOUZERO_MODELS` | 权重目录 | 服务目录下的 `models` |
| `MARVEL_ROOT` | 固定上游源码目录 | `douzero/vendor/doudizhu` |
| `PORT` | HTTP 端口 | `2021` |
| `TORCH_NUM_THREADS` | CPU 推理线程数 | `2` |
| `AI_BACKEND` | 兼容旧部署配置，仅接受 `marvel` | `marvel` |

其他 `AI_BACKEND` 值会在启动时明确报错。

## Docker

在仓库根目录构建，使用现有镜像名称及构建入口：

```bash
docker build -f douzero/Dockerfile.marvel -t palemoky/fight-the-landlord-douzero:latest .
docker compose build ai
```

权重挂载到 `/models`，完整部署说明见[网页部署指南](../docs/web-play.md)。Go 侧继续使用 `DOUZERO_ENABLED`、`DOUZERO_URL` 或配置文件中的 `bot.douzero_enabled`、`bot.douzero_url`。

## HTTP 协议

`POST /decide_play` 接收现有请求字段：`position`、`hand`、`bottom_cards`、`card_play_action_seq`、`num_cards_left`、`last_move`、`last_move_position`、`must_play` 和各席位的 `played_cards_*`。历史记录包含 PASS；手牌、底牌及历史必须相互一致。

席位为 `landlord`、`landlord_down`、`landlord_up`。牌值 3–14 对应 3–A，17 为 2，20 为小王，30 为大王。

成功响应为 `{"action":[3]}`，PASS 为 `{"action":[]}`。请求大小错误返回 HTTP 400 和 `error`；解析、模型或非法动作错误保持 HTTP 500、空 `action` 和 `error`。

`GET /health` 保留 `status`、`backend`、`agents`、`models`，以及模型已有的文件、摘要、架构和迭代元数据。

## 检查与回归

```bash
uv sync --project douzero --locked --inexact
uv run --project douzero --locked ruff check douzero
uv run --project douzero --locked python -m unittest discover -s douzero/tests -v
# 固定源码和两份真实权重就绪后运行：
MARVEL_INTEGRATION=1 douzero/.venv/bin/python -m unittest discover -s douzero/tests -v
```

规则与 HTTP 测试无需权重；真实完整牌局测试缺省跳过，保留为模型部署验收入口。
