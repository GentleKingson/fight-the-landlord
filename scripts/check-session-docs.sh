#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

fail() {
  echo "session documentation check failed: $*" >&2
  exit 1
}

require_text() {
  local file="$1"
  local text="$2"
  grep -Fq -- "$text" "$file" || fail "$file must contain: $text"
}

grep -Eq 'reconnectTimeout[[:space:]]*=[[:space:]]*2 \* time\.Minute' \
  internal/server/session/player.go || fail "runtime reconnect window must remain 2 minutes"
grep -Eq 'deadSessionRetention[[:space:]]*=[[:space:]]*10 \* time\.Minute' \
  internal/server/session/player.go || fail "dead-session retention must remain explicitly scoped"

if rg -n 'sessionExpireTime' internal/server/session >/dev/null; then
  fail "misleading sessionExpireTime identifier remains"
fi

if grep -En '(强制|绝对).{0,16}(10 分钟|十分钟).{0,16}(TTL|有效期|过期)|10 分钟.{0,16}(凭证 TTL|绝对)' \
  README.md docs/deployment.md .env.example config.yaml >/dev/null; then
  fail "deployment documentation still describes an absolute 10-minute credential TTL"
fi

require_text docs/deployment.md '连接保持在线期间，重连凭证不因墙钟时间过期'
require_text docs/deployment.md '完整 2 分钟恢复窗口'
require_text docs/deployment.md '重复离线通知不会延长截止时间'
require_text docs/deployment.md '服务端会回滚轮换'
require_text docs/deployment.md '调用 `/session/revoke` 立即'
require_text docs/deployment.md '`GAME_OFFLINE_WAIT_TIMEOUT` 控制牌局中轮到离线玩家行动时的等待秒数'

require_text README.md '物理连接断开后有完整 2 分钟'
require_text README.md '`GAME_OFFLINE_WAIT_TIMEOUT` 只控制'
require_text .env.example '不是会话重连窗口'
require_text .env.example '物理断线后固定保留 2 分钟'
require_text config.yaml 'offline_wait_timeout: 30'
require_text config.yaml '不是会话重连窗口'
require_text config.yaml '物理断线后固定保留 2 分钟'

echo "Session documentation checks passed"
