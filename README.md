<div align="center">
    <img src="https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/docs/logo.png" alt="Logo" height="100px" />

# 🎮 斗地主

**一个真正公平的斗地主游戏 - 无控牌、无算法操控、纯粹的运气与技巧**

基于 Go 语言实现的斗地主游戏，支持联网对战、断线重连、智能机器人等功能。

[![Docker Image Size](https://img.shields.io/docker/image-size/gentlekingson/fight-the-landlord/latest)](https://hub.docker.com/r/gentlekingson/fight-the-landlord)
[![Test](https://github.com/GentleKingson/fight-the-landlord/actions/workflows/test.yml/badge.svg)](https://github.com/GentleKingson/fight-the-landlord/actions/workflows/test.yml)
[![Release](https://github.com/GentleKingson/fight-the-landlord/actions/workflows/release.yml/badge.svg)](https://github.com/GentleKingson/fight-the-landlord/actions/workflows/release.yml)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

</div>

> 本仓库是由 [GentleKingson](https://github.com/GentleKingson/fight-the-landlord)
> 维护的独立 fork，默认安装脚本、发行版本和容器镜像均来自该 fork。
> 原始上游项目为 [palemoky/fight-the-landlord](https://github.com/palemoky/fight-the-landlord)，
> Go 模块路径为保持协议和导入兼容而继续使用上游路径。

## 项目初衷

在某些知名斗地主游戏中，新手或回归玩家刚开始会获得好牌，匹配豆子少的对手，营造"连胜"的错觉。但随着游戏时间增长，牌质量明显下降，且频繁匹配高段位玩家，导致快速输光豆子。这种算法操控严重破坏了游戏的公平性和纯粹性，在本项目中：

- **真随机发牌**：每局洗牌完全随机，无任何控牌算法
- **公平匹配**：不考虑胜率、段位、游戏时长，纯随机或房间匹配
- **开源透明**：所有代码公开，欢迎审计和贡献
- **无内购无广告**：纯粹的游戏体验，技巧决定胜负

> **核心理念**：斗地主应该是运气与技巧的博弈，而不是算法与钱包的较量。

## 游戏截图

<div align="center">
  <img src="https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/docs/lobby.png" alt="Lobby" width="45%" />
  <img src="https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/docs/in-game.png" alt="In Game" width="45%" />
</div>

## DouZero 机器人出牌演示

[DouZero](https://github.com/kwai/DouZero) 是快手开源的基于深度强化学习的斗地主 AI。相比于经常出非法牌型的 LLM 而言，DouZero 能展现出更丰富的高级策略：自由出牌时主动组合复杂牌型、农民间默契配合、精准顶牌与拆牌等，对局体验更接近真人对手。

<div align="center">
  <img src="https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/docs/douzero-game.png" alt="Game" width="45%" />
  <img src="https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/docs/douzero-log.png" alt="Log" width="45%" />
</div>

## 快速开始

> **发布状态**：首次 fork tag workflow 成功前，GitHub Releases 和 Docker Hub
> 可能还没有本 fork 的二进制或镜像，下面的发行版安装/Compose 命令会因此失败。
> 先检查 [Releases](https://github.com/GentleKingson/fight-the-landlord/releases)
> 和镜像 digest；尚未发布时请使用本页“本地开发”中的源码构建流程。生产环境不要
> 把可变 `main` 安装脚本或 `latest` 当作已验证 artifact。

### 客户端安装

**macOS / Linux**：

```bash
curl -fsSL https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/install.sh | bash
```

**Windows (PowerShell)**：

```powershell
irm https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/install.ps1 | iex
```

**运行客户端**：

```bash
ddz
```

支持的牌型和按键见 [游戏规则](#游戏规则) / [常用按键](#常用按键)

> **Windows 10 用户提示**：游戏界面使用了 emoji 图标，Windows 10 自带的传统 cmd / PowerShell 窗口（conhost）不支持彩色 emoji 渲染，会显示异常。建议改用 [Windows Terminal](https://aka.ms/terminal)（Microsoft Store 免费安装，Windows 11 已默认内置）运行客户端。

### 服务端部署

**使用 Docker Compose（推荐）**：

```bash
# 1. 创建项目目录
mkdir fight-the-landlord && cd fight-the-landlord

# 2. 下载配置文件
curl -fsSL https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/docker-compose.yml -o docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/GentleKingson/fight-the-landlord/main/.env.example -o .env

# 3. 修改公开来源等配置，并从 secret manager 注入 Redis 密码（必填）
vim .env
read -rsp "Redis password: " REDIS_PASSWORD && export REDIS_PASSWORD

# 4. 启动服务
docker compose up -d

# 5. 停止服务
docker compose down
```

启动后可在浏览器打开 `http://localhost:1780/`。Compose 默认只把未加密的后端
绑定到 `127.0.0.1`。互联网部署必须由同机或私有网络中的 TLS 代理访问该端口，
把 `.env` 中的 `SECURITY_ALLOWED_ORIGINS` 改为实际 HTTPS 来源，并保留 `/ws`
的 WebSocket Upgrade 头；不要公开后端端口绕过 TLS 和 `/metrics` 访问控制。

浏览器重连凭证保存在 `HttpOnly`、`SameSite=Strict` Cookie 中，页面 JavaScript
不会读取或写入 token；CLI/TUI 仍使用兼容的显式 token 协议。连接在线期间，
服务端凭证不会因墙钟时间过期，活动页面每 24 小时通过同源接口续期 7 天 Cookie；
物理连接断开后有完整 2 分钟恢复窗口，成功恢复会单次消费并轮换凭证。浏览器若被
暂停超过 7 天而无法运行续期请求，Cookie 仍可能过期。
`GAME_OFFLINE_WAIT_TIMEOUT` 只控制牌局中离线玩家的行动等待时间，
不是会话重连期限。显式退出会立即撤销凭证
并清除 Cookie。

升级后的页面会删除旧 `ddz_next_reconnect` localStorage token，但不会把它转换成
Cookie；没有新 Cookie 的浏览器可能获得新身份并需要重新加入房间。服务端与 Web
资源在同一镜像中，应原子升级；回滚到旧版也无法把 HttpOnly Cookie 转回 JavaScript
token。CLI/TUI 的显式 token 协议字段保持兼容，但单进程重启不保留内存会话。

```bash
curl --fail http://localhost:1780/health
curl --fail http://localhost:1780/livez
curl --fail http://localhost:1780/readyz
curl --fail http://localhost:1780/version
curl --fail http://localhost:1780/metrics
```

完整生产资料：

- [Web 生产部署](docs/deployment.md)
- [安全边界](docs/security.md)
- [可观测性](docs/observability.md)
- [容量与 Soak 测试](docs/performance-testing.md)
- [Redis 备份与恢复](docs/redis-backup.md)
- [镜像签名、SBOM 与 provenance 验证](docs/release-verification.md)

生产部署应固定镜像 digest，并在拉取前验证本仓库 tag workflow 的 keyless
cosign 身份。首次 fork tag 发布成功前，不应假设 Docker Hub 上已存在可验证镜像。
当前 Room/Matcher 所有权仍为单实例，Redis Sentinel/Cluster、跨实例恢复和跨区域
高可用尚未实现。

💡 推荐使用 [lazydocker](https://github.com/jesseduffield/lazydocker) 管理服务

### 本地开发

```bash
# 1. 启动 Redis
redis-server

# 2. 启动 douzero 机器人
cd douzero && uv sync && uv run python server.py

# 3. 启动服务端
go run ./cmd/server

# 4. 启动终端客户端
go run ./cmd/client

# 5. 启动 Web 开发服务器（另一个终端）
cd web
npm ci
npm run dev
```

## 游戏规则

与常见的斗地主相同，开局叫地主后，两位农民需配合击败地主，地主则需要阻击两个农民，率先出完手牌的一方获胜。

### 牌型示例

```
单张: 3, K, 2
对子: 33, KK
三张: 333
三带一: 3334
三带二: 33344
顺子: 34567 (5张+)
连对: 334455 (3对+)
飞机: 333444 (两个连三+)
飞机带单: 33344456
飞机带对: 3334445566
四带二: 333345
四带两对: 33334455
炸弹: 3333
王炸: 小王大王
```

### 常用按键

以下按键不区分大小写：

| 按键 | 功能                   |
| ---- | ---------------------- |
| M    | 开关音乐（默认静音）   |
| C    | 开关记牌器（默认关闭） |
| P    | Pass                   |
| H    | 帮助                   |
| B    | 小王（Black Joker）    |
| R    | 大王（Red Joker）      |
| Esc  | 返回上一页             |


---

<div align="center">

**让斗地主回归纯粹 - 无控牌，真公平**

Upstream created by [palemoky](https://github.com/palemoky). This fork is maintained by
[GentleKingson](https://github.com/GentleKingson/fight-the-landlord).

</div>
