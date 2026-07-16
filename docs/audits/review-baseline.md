# Web Client Integration Review Baseline

Recorded on 2026-07-15 (Asia/Hong_Kong) before remediation changes.

## Repository state

- Base branch: `agent/import-upstream-pr-65-web-client`
- Fix branch: `codex/web-client-system-remediation`
- Base commit: `9b98c237c40a55a60808cb96f89cbdc70144cb90`
  (`feat(web): import web client from upstream PR #65`)
- Worktree: clean before the branch and this document were created
- Difference from `main`: one commit ahead, zero behind; 41 Web files and
  9,004 inserted lines

## Toolchain

| Tool | Result |
| --- | --- |
| Go | Unavailable in the host `PATH` (`go: command not found`) |
| Node.js | `v24.14.0` |
| npm | `11.18.0` |
| Docker CLI | `29.6.1` |
| Docker Compose | `v5.2.0` |

The sandbox could not access the Docker daemon without elevated execution.

## Untouched baseline commands

| Command | Result |
| --- | --- |
| `go test ./...` | Not run: the host has no Go executable in `PATH` |
| `cd web && npm ci` | Passed after registry network access was permitted; 169 locked packages installed |
| `cd web && npm test -- --run` | Passed: 4 files, 18 tests |
| `cd web && npm run build` | Passed: TypeScript project build and Vite production build |
| `cd web && npm run test:e2e` | Passed with local-server/browser permission: 9 demo-only tests in 3 viewport projects |

The E2E run emits Node warnings because `FORCE_COLOR` overrides `NO_COLOR`.
The first sandbox-only E2E attempt could not bind `127.0.0.1:5174`; it passed
unchanged once local server and browser execution were permitted.

## Known baseline gaps

- The browser codec loads copied `.proto` files and constructs schema text at
  runtime rather than consuming generated artifacts from the canonical Go
  schema.
- No Go-to-TypeScript or TypeScript-to-Go golden codec suite exists.
- All Playwright coverage uses demo query parameters; no test opens a socket to
  the real Go server.
- Reconnect identity rotation, invalid/expired tokens, repeated reconnects,
  refreshes, StrictMode cleanup, and multi-tab conflicts are untested.
- Web hand validation and hints do not share the complete Go rules semantics.
- Command acknowledgement, duplicate submission, timeout, network loss,
  malformed-frame, half-open socket, and reconnect-storm behavior are untested.
- Snapshot ordering/idempotence, server-deadline timers, and the complete
  remaining-card calculation lack reducer-level coverage.
- Chat isolation, IME composition, keyboard card selection, drawer focus
  management, and automated accessibility checks are untested.
- The production image does not yet prove SPA fallback, WebSocket proxying,
  `/health`, `/version`, or client/server compatibility gating.

## Acceptance matrix

| Area | Required evidence | Baseline |
| --- | --- | --- |
| Canonical protocol | deterministic generation/check plus bidirectional golden fixtures | Missing |
| Reconnect | atomic Go identity restoration and repeatable Web state-machine tests | Missing |
| Rules | every Go-equivalent hand type, comparison, and legal hint covered | Missing |
| Commands/transport | observable send results, pending/error states, heartbeat and retry tests | Missing |
| Snapshot/state | complete versioned snapshot, ordered reducers, deadline timer, idempotent counter | Missing |
| Chat/accessibility | scoped chat plus keyboard/pointer/focus/a11y automation | Missing |
| Production | SPA/WebSocket hosting, version gate, CI, image and container smoke tests | Missing |
| Real integration | three browser contexts complete a game, reconnect, chat, settle, and restart/leave | Missing |

The final review must record every command actually run, including any
environment-limited checks, and must not treat demo-only coverage as real-server
evidence.
