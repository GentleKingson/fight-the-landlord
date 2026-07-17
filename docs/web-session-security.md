# Web 会话安全

浏览器重连凭证由服务端保存为随机 opaque token，并只通过名为
`ddz_web_session` 的 Cookie 发送。Cookie 使用 `HttpOnly`、
`SameSite=Strict`、`Path=/` 和 7 天 `Max-Age`；页面 JavaScript 不读取、
记录或持久化该 token。旧版 `ddz_next_reconnect` localStorage 项只会在启动时
删除，不再作为兼容输入。

首次、无有效会话的 WebSocket `101` 响应会设置一个独立的短期
`ddz_web_session_owner` Cookie（`HttpOnly`、`SameSite=Strict`）。该 nonce 每次升级
都由服务端重新生成并登记，绝不复用请求中可被 JavaScript 预置的值；它只用于把
首次 ticket 绑定到发起该次升级的浏览器，不是重连凭证。服务端随后在 `Connected`
或 `Reconnected` 中返回一个 30 秒 pre-commit 随机 ticket。已有会话的 ticket 与升级
请求携带的精确前任 Cookie 绑定；首次 ticket 则与该 101 nonce 绑定。浏览器把 ticket
POST 到 `/session/commit`，接口确认 successor 和连接所有者仍有效后设置会话 Cookie，
并立即清除、消费 owner nonce。错误绑定不会消费合法浏览器的 ticket。

服务端无法从写入 HTTP 响应推断浏览器是否真正保存 `Set-Cookie`。因此 commit 进入
短暂的交付不确定状态：同一 ticket 和前任 Cookie 可幂等取回同一 successor，不会
产生第二次轮换。页面收到响应后立即 POST `/session/refresh`，新 HttpOnly Cookie
的回传会原子地淘汰 predecessor。如果响应丢失且 WebSocket 关闭，下一次连接可用
predecessor 或 successor 中浏览器实际保存的那个恢复；并发解析仍只有一个消费者。
快照发送、身份恢复或 pre-commit ticket 超时会回滚轮换，原有 Cookie 保持可恢复。
commit 后等待 successor 回传的状态同样只有 30 秒上限；超时会关闭未确认连接并把
轮换标记为可由 predecessor/successor 中实际落盘的一方恢复。ticket 的自动超时与
commit、refresh、revoke、身份重绑和重连发布共用同一授权边界。

浏览器连接在 successor 经 `/session/refresh` 回传确认前只允许 `Ping` 和
`Reconnect`。其他命令在命令缓存、限流和业务处理之前被拒绝；确认后，每条命令从
精确连接、玩家映射和当前 token 校验开始，并持有授权读锁直到处理完成。浏览器重连会先
把旧连接标记为转换中，释放全局会话写锁后排空该连接已经获权的命令，再重新取得全局锁并
复核 token 与精确连接仍未变化；只有复核成功后才轮换凭证、重绑身份和发布快照。这样旧代
的建房、加房或匹配命令不会在 replacement 发布后留下幽灵成员。被替换连接还会短暂保留
在 retired-generation registry 中作为撤销屏障的防御层。因此 `/session/revoke` 返回 204
前会等待当前连接和仍在登记的旧代连接，关闭该谱系的活动连接，且之后不会再有已授权命令
执行。携带正在轮换但暂不可用 predecessor 的第二个标签页会收到受控拒绝，不会创建并
提交新的匿名凭证；浏览器重连失败也会关闭并重试，而不会提交 provisional ticket。

活动 Web 客户端每 24 小时调用 `/session/refresh`，把 Cookie 的 7 天保存期限向后
续期。浏览器冻结、休眠或停止页面脚本超过 7 天时，定时续期无法执行，Cookie 仍
可能过期；服务端在线 token 本身不受该墙钟期限影响。

本地直接 HTTP 开发时 Cookie 不带 `Secure`。直接 TLS 连接始终带 `Secure`。
反向代理部署只有在请求的直接来源地址命中 `SECURITY_TRUSTED_PROXY_CIDRS`，
且 `X-Forwarded-Proto` 的值精确为 `https` 时，服务端才信任代理并添加
`Secure`；非可信客户端伪造该头不会生效。生产代理应覆盖写入而不是追加该头，
并继续传递浏览器的原始 `Origin`。

浏览器 WebSocket 必须带非空且命中白名单的 `Origin`，协议握手中的
`client_kind` 必须为 `web`。无 `Origin` 的连接只允许 `tui` 或 `bot`，并继续
在 protobuf `ReconnectPayload` 和响应中使用显式 token，因此现有 CLI/TUI
协议保持兼容。

显式退出会等待已发出的 commit/refresh 写 Cookie 请求结束，再向 `/session/revoke`
发送同源 POST。接口会从会话 Cookie 或首次 owner Cookie 解析待撤销谱系，原子撤销
current、pending 和短期 rotation alias，关闭精确活动连接，并同时返回两个过期
Cookie。延迟的 predecessor revoke 也会撤销已确认 successor；与 refresh 并发时，
无论哪一方先获得授权写锁，revoke 返回后该谱系都不可用。请求体有严格 JSON 和大小
限制，响应使用 `Cache-Control: no-store`。

为覆盖 Set-Cookie 响应和后续请求在网络中的交付不确定性，已淘汰 predecessor 只保留
2 分钟的“仅撤销”别名：它不能 refresh、恢复身份或授权 WebSocket 命令，但在窗口内仍
可撤销同一 successor 谱系。这是有意保留的可用性权衡；持有刚被轮换旧 Cookie 的主体
在窗口内仍具有终止该谱系的能力。
