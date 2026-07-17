# Small Public Test Baseline

Recorded on 2026-07-17 (Asia/Hong_Kong) before public-test readiness
implementation began.

## Source baseline

- Branch: `main`
- Commit: `b03fcf682a75890be11a61d735c8dcd8b48ff3d0`
- Remote: `https://github.com/GentleKingson/fight-the-landlord.git`
- Worktree: clean before the remediation branch was created
- Remediation branch: `codex/small-public-test-readiness`
- Intended topology: one Linux host, one game-server process, one Redis,
  optional DouZero, 10-50 testers, and at most 100 concurrent connections

## Existing capabilities

- The server has `/livez`, Redis-backed `/readyz`, `/version`, optional
  Prometheus metrics, structured logging, WebSocket Origin checks, trusted-proxy
  CIDRs, connection/message/chat limits, bounded command idempotency, and
  rotating reconnect credentials.
- Graceful shutdown already rejects new WebSocket upgrades, marks readiness
  false, and waits for active games up to a configured timeout. The existing
  maintenance flag is only entered during shutdown; it is not an operator
  controlled three-state admission system.
- Redis stores room snapshots and player statistics. Compose uses a named
  volume, requires an ACL password, enables AOF, has a health check, and does
  not publish the Redis port in its default profile. A loopback-only
  `redis-debug` profile exists for explicit local debugging.
- The game container is published on host loopback by default, waits for Redis
  health, and uses `/readyz` for its container health check. DouZero is an
  optional Compose profile and has no host port.
- The Web client sends leaderboard `type`, `offset`, and `limit`, and keeps the
  returned leaderboard type. Existing load/chaos tools cover connections,
  reconnects, room lifecycle, matcher operations, telemetry, and selected
  faults, but they do not play complete games.
- Release CI builds tagged images and release artifacts, generates an SBOM,
  signs images, and verifies release identity. Deployment references can be
  overridden with complete image references.

## Findings

### Leaderboards and settlement

- The handler validates `limit` and `offset` but passes only `limit` to storage.
  Storage hard-codes `total` and offset zero, while the response echoes the
  requested type. A daily or weekly response can therefore contain total-board
  data under the wrong label.
- Daily and weekly sorted sets store each player's lifetime score instead of
  score earned in that period.
- Player JSON, total ranking, daily ranking, and weekly ranking are updated by
  separate Redis commands. Concurrent or partial updates can disagree.
- Settlement has no game-result idempotency key. Re-dispatching a completed
  result can count and score the same game again.
- Leaderboard time reads call `time.Now` directly, preventing deterministic
  day/week boundary tests.

### Public network boundary

- Default Compose does not expose Redis or DouZero and publishes the cleartext
  game backend on `127.0.0.1`, but no deployment preflight verifies rendered
  Compose output. A local edit can still publish sensitive ports, use host
  networking, expose `/metrics`, or bind the backend publicly.
- Production configuration rejects wildcard Origins and requires a Redis
  password. It accepts explicit trusted-proxy CIDRs, but there is no public-test
  check requiring the deployment to make its proxy trust decision explicit or
  requiring public Origins to use HTTPS.
- `/metrics` shares the application listener. Documentation tells reverse
  proxies to restrict it, but the application has no separate metrics listener
  and no preflight proof that the public proxy route is denied.
- `.env.example` and Compose still fall back to a bare `latest` application
  image tag. Complete digest references are supported but not enforced or
  demonstrated as the production default.

### Operations and moderation

- There is no operator command, local admin listener, or authenticated admin
  route for drain, maintenance, resume, disconnect, mute, ban, or unban.
- The existing maintenance boolean blocks new WebSocket connections and is set
  during graceful shutdown. It cannot stop only new rooms/matches while
  allowing current games and reconnects to finish.
- Chat has per-client rate and cooldown controls, but no player mute with an
  expiry. IP rate limiting has an internal temporary filter, but there is no
  player-ID temporary ban or operator-controlled disconnect.
- No non-sensitive audit event contract exists for operator actions.

### Redis backup, upgrade, and rollback

- Redis persistence is enabled, but the repository has no supported backup or
  restore scripts, checksum/manifest format, retention control, isolated
  restore verification, or rollback procedure.
- Deployment documentation describes Compose and TLS proxying but does not
  provide a complete drain, backup, fixed-digest upgrade, validation, and
  rollback runbook for this single-node test topology.

### DouZero resilience

- DouZero has an HTTP timeout and falls back to the heuristic engine for
  transport/decode/service errors, missing cards, and a pass when play is
  mandatory.
- It does not reject non-2xx responses before decoding, fully validate the
  returned hand type or beat relation, bind an asynchronous decision to the
  authoritative game/turn IDs, or retry one legal fallback when the session
  rejects a submitted action.
- A stale or invalid decision can therefore be submitted after the turn moves,
  and a rejected action can leave the game waiting for its turn timer. There is
  no fixed-reason invalid-action metric.

### Complete-game evidence

- Existing Web E2E covers real games, and the load harness reports latency,
  Redis errors, RSS, goroutines, and connection cleanup. The load harness
  explicitly does not complete games.
- There is no 18-player, short-duration game smoke that covers ready, bidding,
  legal play, settlement, rematch, random disconnect/reconnect, leaderboard
  reconciliation, duplicate settlement, or final room cleanup in one report.

## Scope of this change

This branch will fix leaderboard correctness, add a rendered-configuration
preflight, implement only the local operational controls needed for a small
test, support pinned single-node deployment and Redis backup/restore, harden
DouZero fallbacks, add a bounded complete-game smoke, and document deployment,
maintenance, troubleshooting, upgrade, and rollback.

Service restart may terminate active games. The operational contract is to
enter draining first, stop new game admission, notify users, wait until active
games reach zero, back up Redis, and then restart.

## Explicitly out of scope

- Multi-instance deployment, Kubernetes, Redis Sentinel/Cluster, distributed
  ownership, cross-instance rooms, cross-instance reconnect, or active-game
  recovery after process restart
- OAuth, JWT, registration, email, password recovery, a durable account system,
  an administrator Web UI, or complex RBAC
- Reports, appeals, a moderation platform, replay, spectating, tournaments,
  ranked seasons, payments, virtual currency, or a store
- Cross-region disaster recovery or automated remote backup upload

These may be reconsidered after test evidence justifies them, but none is part
of this branch.

## Baseline verification

The host has Node.js `v24.14.0`, npm `11.18.0`, Docker `29.6.1`, Docker
Compose `v5.2.0`, and actionlint `1.7.12`. Host Go and golangci-lint are not
installed, so Go tests ran against an isolated `origin/main` archive with the
`golang:1.26` toolchain image.

| Command | Baseline result |
| --- | --- |
| `go test ./internal/config ./internal/server ./internal/server/handler ./internal/server/storage ./internal/bot -count=1` | Pass |
| `cd web && npm run typecheck` | Pass |
| `cd web && npm test -- --run` | Pass: 19 files, 257 tests |
| `docker compose --env-file .env.example config --quiet` | Pass |

The commands above were executed against commit `b03fcf6`, not against files
being modified concurrently on the remediation branch.
