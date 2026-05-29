# DouZero Service

DouZero 强化学习模型的 ONNX 推理服务，供 Go 游戏服务端通过 HTTP 调用。

## 本地调试

```bash
cd douzero

# 安装依赖
uv sync

# 启动服务（首次运行自动从 HuggingFace 下载 ONNX 模型）
uv run python server.py
# 监听 :2021
```

环境变量：

| 变量             | 说明     | 默认值     |
| ---------------- | -------- | ---------- |
| `DOUZERO_MODELS` | 模型目录 | `./models` |
| `PORT`           | 监听端口 | `2021`     |

## Docker 构建

```bash
# 构建镜像（会自动从 HuggingFace 下载 ONNX 模型）
docker build -t palemoky/fight-the-landlord-douzero:latest ./douzero

# 也可以通过 docker compose 一起构建
docker compose build douzero
```

## Go 侧启用

`config.yaml`：

```yaml
ai:
  enabled: true
  douzero_enabled: true
  douzero_url: "http://localhost:2021" # 本地调试
  # douzero_url: "http://douzero:2021"  # Docker Compose
```

## API

### `POST /decide_play`

```json
// 请求
{
  "position": "landlord",
  "hand": [3, 5, 7, 14, 17],
  "bottom_cards": [17, 20, 30],
  "played_cards_landlord": [],
  "played_cards_landlord_down": [],
  "played_cards_landlord_up": [],
  "card_play_action_seq": [],
  "num_cards_left": {"landlord": 20, "landlord_down": 17, "landlord_up": 17},
  "last_move": [],
  "last_move_position": "",
  "must_play": true
}

// 响应
{"action": [3, 5]}   // 出牌
{"action": []}       // pass
```

### `GET /health`

```json
{ "status": "ok", "agents": ["landlord", "landlord_down", "landlord_up"] }
```

## 卡牌编码

| 牌面 | DouZero int |
| ---- | ----------- |
| 3–A  | 3–14        |
| 2    | 17          |
| 小王 | 20          |
| 大王 | 30          |

## 模型来源

ONNX 模型托管在 [HuggingFace](https://huggingface.co/palemoky/douzero-baselines)。

如需重新转换（从 PyTorch checkpoint → ONNX），参考 [DouZero](https://github.com/kwai/DouZero) 项目。
