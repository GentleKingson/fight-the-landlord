# 生产安全边界

本文描述维护 fork `GentleKingson/fight-the-landlord` 的安全边界。它不是对上游
镜像、第三方重新打包镜像或尚未发布的 fork artifact 的背书。

## Web 会话

浏览器不读取或持久化重连 token。服务端把 256-bit 随机 opaque token 放入
`ddz_web_session` Cookie，并设置 `HttpOnly`、`SameSite=Strict`、`Path=/` 和
7 天上限。直接 TLS 请求，或请求直接来自 `SECURITY_TRUSTED_PROXY_CIDRS` 且
`X-Forwarded-Proto` 精确为 `https` 时，Cookie 才带 `Secure`。
Cookie 省略 `Domain`，因此是 host-only。

WebSocket 成功发送 `Connected`/`Reconnected` 后，页面只收到 30 秒 pre-commit
ticket，再通过受信 Origin 的 `POST /session/commit` 换取 HttpOnly
Cookie。已有会话的 ticket 与精确 predecessor Cookie 绑定；首次连接则绑定 101 响应
中新建、登记且防 fixation 的 `ddz_web_session_owner` host-only Cookie。ticket 不包含
player ID 或 token。commit 可在交付不确定期幂等返回同一 successor；
页面立即调用 `/session/refresh` 回传新 Cookie 后，服务端才淘汰 predecessor。若响应
丢失，连接关闭后 predecessor/successor 任一实际保存结果都可恢复，但并发恢复仍只
有一个消费者。失败的快照投递会回滚轮换。

活动页面每 24 小时通过 `/session/refresh` 续期 7 天 Cookie。浏览器若冻结或停止
页面脚本超过 7 天，Cookie 仍可能过期，即使服务端在线 token 不按墙钟过期。
`POST /session/revoke` 使用 Cookie 撤销会话并清除 Cookie。

三个 session HTTP 端点都要求：

- 非空且在白名单中的 `Origin`；
- `Content-Type: application/json`；
- 有界请求体和严格 JSON 字段；
- `Cache-Control: no-store`。

这里的 Origin 边界是显式 allowlist，不是根据请求 Host 自动推导；白名单内每个来源
都具有完整 commit/refresh/revoke 权限，只能加入实际受信并共同运营的 Web 前端。
完整 ticket、owner nonce、轮换和撤销屏障见 [Web 会话安全](web-session-security.md)。

带浏览器 `Origin` 的连接必须声明 Web client kind；无 `Origin` 的 CLI/TUI/Bot
必须使用显式协议 token。该区分阻止同源脚本把自己降级为可读取 token 的终端
客户端。升级后页面会删除旧 `ddz_next_reconnect` localStorage 字段，但不会读取
或继续使用它。

## Origin、TLS 与代理

生产环境不得把 `SECURITY_ALLOWED_ORIGINS` 设为 `*`。值必须是浏览器页面的完整
HTTPS Origin，例如 `https://game.example.com`。只有实际反向代理网段可进入
`SECURITY_TRUSTED_PROXY_CIDRS`；留空时所有转发头都不可信。

TLS 在 Caddy、Nginx、Ingress 或负载均衡器终止时，代理必须覆盖而不是追加可信
scheme，并传递 WebSocket Upgrade：

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection $connection_upgrade;
```

不要信任来自公网的 `X-Forwarded-For` 或 `X-Forwarded-Proto`。多层代理场景应只
列出受控的最后一跳网段，并在上线前测试 direct peer spoof 被拒绝。

## Secrets 与数据

`REDIS_PASSWORD`、Docker Hub token 和通知 secret 必须由 secret manager 或
GitHub Secrets 注入，不得写入 `.env`、Compose、镜像或日志。镜像签名使用 GitHub
OIDC 临时身份，不配置长期 cosign 私钥；workflow 的 `id-token: write` 仍应只授予
实际签名 job。Redis 默认只在 Compose 内部网络监听；`redis-debug` profile 仅供
本机排障。

结构化日志的敏感键会被二次脱敏，但调用方仍不得记录 Cookie、token、Redis
密码、完整聊天正文或原始请求 payload。Prometheus labels 只允许固定白名单值，
不得加入玩家、房间、牌局或昵称。

## 供应链

所有第三方 Actions 固定到与版本 tag 核对过的 40 位 commit SHA；仓库检查脚本会
拒绝可变引用和格式错误。CODEOWNERS 会请求维护者审查，真正强制 code-owner
approval 仍依赖 GitHub branch protection/ruleset，不能只靠仓库文件。Dependabot
负责提出更新。标签发布使用 BuildKit 生成 SBOM 和最大模式 provenance，并用
GitHub OIDC 的 keyless cosign 身份签署镜像 digest。

安装脚本必须成功下载并验证 Release SHA-256 才会安装二进制，但从可变 `main`
直接执行安装脚本仍有 bootstrap 信任；高保证环境应固定并审查脚本 commit。
DouZero 模型固定到 Hugging Face commit，并对三个 ONNX 文件逐一校验 SHA-256；
BuildKit provenance 未必把 Dockerfile 内的 HTTP 下载列为独立 material。

具体校验命令见 [发布验证](release-verification.md)。Release 二进制当前只有
SHA-256 校验文件，不应误称为已经进行 keyless 签名。

## 已知限制

- Room、Matcher 和在线连接仍由单个进程拥有；没有跨实例恢复或一致性路由。
- Redis 客户端只支持单一明文 `addr/password/db` endpoint；没有原生 TLS、
  Sentinel/Cluster，默认 Compose 也不是托管高可用方案。
- 没有账号体系、设备管理、封禁治理或长期身份认证。
- 没有跨区域部署、回放、观战或比赛系统。
- Cookie 降低同源 JavaScript 直接窃取长期 token 的风险，但不能修复任意 XSS；
  CSP、依赖审计和输出编码仍是必要控制。
- 已轮换 predecessor 会作为仅撤销别名保留 2 分钟，以覆盖响应交付竞态；它不能恢复
  身份或执行命令，但窗口内仍能撤销 successor，会形成一个有界的拒绝服务能力。
