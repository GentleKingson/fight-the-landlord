# Follow-up Remediation Baseline

Recorded on 2026-07-15 (Asia/Hong_Kong) from a clean checkout before any
follow-up implementation changes.

This document records only commands executed for this follow-up. Statements in
`final-review.md` were not used as test evidence.

## Repository state

- Starting branch: `codex/web-client-system-remediation`
- Follow-up branch: `codex/web-client-remediation-followup`
- Starting commit: `ffd7740de7b40fdc48d2afd8d0074c117fa74cff`
- `git status --short --branch`: clean, on the follow-up branch
- `git diff main...HEAD --stat`: 162 files changed, 23,126 insertions and
  1,039 deletions

## Toolchain observed in this run

| Tool | Actual result |
| --- | --- |
| Go | `go1.26.5 darwin/arm64`; the host initially had no `go` in `PATH`, so the official archive was downloaded to `/tmp`, SHA-256 verified, and explicitly added to `PATH` |
| Node.js | `v24.14.0` |
| npm | `11.18.0` |
| Docker CLI/Engine | `29.6.1`; Docker Desktop `4.81.0` |
| Docker Compose | `v5.2.0` |

The first `docker version` attempt found the CLI but not the daemon. Docker
Desktop was started, after which client and server version checks succeeded.

## Commands and results

| Command | Result from this run |
| --- | --- |
| `make proto-check` with explicit Go/protoc tool paths | Passed. Go protobuf, message-type mapping, and TypeScript generated codec had no diff. |
| `go test -tags=ci -race -p 4 -count=1 ./...` | Passed across all packages. No race report or failed test. Packages with no tests were reported normally. |
| `cd web && npm ci` | Passed: 258 packages installed, 259 audited, 0 vulnerabilities. |
| `npm run proto:check` | Passed. |
| `npm run typecheck` | Passed with no TypeScript diagnostic. |
| `npm run lint` | Passed with `--max-warnings=0`. |
| `npm test -- --run` | Passed: 17 files, 211 tests. |
| `npm run build` | Passed: Vite transformed 126 modules. |
| `npm run test:e2e` | Passed: 15 Chromium tests over desktop, mobile portrait, and mobile landscape in 13.2s. |
| `npm run test:e2e:real` | Passed: one three-context real-server game, test 20.5s and run 33.0s. |
| `docker compose --env-file .env.example config --quiet` | Passed. |
| `docker build --build-arg VERSION=followup -t fight-the-landlord:followup .` | Passed. Image manifest list: `sha256:54bd2bc142e6c9acc36bb0e15dba7c2a622ccba42cf9a3caf5078762ab385ac2`. |

## Warnings and observed noise

- `npm ci` and the Docker Web build reported deprecated transitive package
  `whatwg-encoding@3.1.1`.
- npm reported three install scripts not yet covered by its local
  `allowScripts` policy: `esbuild@0.25.12`, `fsevents@2.3.2`, and
  `fsevents@2.3.3`.
- npm in the Node 22 builder reported that npm 12 is available.
- Playwright workers reported that `NO_COLOR` is ignored because the host also
  sets `FORCE_COLOR`.
- The real E2E intentionally closed a browser WebSocket during reconnect. Vite
  logged `ws proxy socket error: read ECONNRESET`; the reconnect succeeded and
  the game completed.
- No Go race, TypeScript, ESLint, generated-file, or build warning was emitted.

## What the passing baseline does not prove

The current suite passes, but it does not exercise the follow-up merge gates:

- The connection semaphore is not tested as an active-connection lease, nor is
  `max_connections: 0` tested as unlimited.
- There is no high-contention `SendMessage`/`Close`, broadcast/close,
  shutdown/send, or reconnect-replacement stress test.
- External packages still have direct Room membership/state access; there is no
  comprehensive disconnect-during-deal/bid/redeal/game-over race suite.
- Matcher tests do not inject failures at every room assembly step and do not
  prove server-authoritative queue expiry or inflight cancellation rollback.
- The real E2E does not refresh or reconnect on the settlement screen, perform
  a second reconnect, or start a rematch.
- WebSocket protocol compatibility is not negotiated by the server, and
  commands do not yet have end-to-end request IDs and bounded idempotency.
- The default Compose configuration still publishes Redis, trusted proxy CIDRs
  are not enforced, and production response/security headers and readiness are
  not covered.
- Configuration parsing still accepts malformed environment values silently,
  and long-lived managers do not all expose deterministic shutdown.
- Playwright real-server coverage starts Vite and `go run`; it does not execute
  the shipped Docker image directly. Firefox/WebKit and required fault flows are
  absent.

These are coverage gaps, not claims that the implementation is safe. Each
follow-up phase must first add a targeted failing test and then change the
implementation until that test and the relevant race suite pass.
