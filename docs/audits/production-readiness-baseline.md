# Production Readiness Baseline

Recorded on 2026-07-17 before remediation work began.

## Source baseline

- Branch: `main`
- Commit: `cd3409d1ef69aed68cb5db62b4127d25609db5ce`
- Remote: `https://github.com/GentleKingson/fight-the-landlord.git`
- Worktree: clean before the remediation branch was created
- Remediation branch: `codex/production-readiness-remediation`
- Go module and generated protobuf import path intentionally remain
  `github.com/palemoky/fight-the-landlord` for compatibility.

## Toolchain

| Tool | Baseline |
| --- | --- |
| Go | Not installed on the host; repository tests used `golang:1.26` (`go1.26.5 linux/arm64`) |
| Node.js | `v24.14.0` |
| npm | `11.18.0` |
| Docker client/server | `29.6.1` / `29.6.1` |
| Docker Compose | `v5.2.0` |
| Playwright | `1.60.0` |
| actionlint | `1.7.12` |
| golangci-lint | Not installed on the host; CI declares `v2.11.1` |

The host also lacks `protoc`. The Go generation check was therefore run in the
Go container with the official `protoc 3.13.0` Linux ARM64 archive (SHA-256
`5f6f59be05ce91425195dc689f5faa59284efb4799526b6f92a7a91efe5702fd`) and
`protoc-gen-go v1.36.11`; the Web generation check ran with the host Node.js
toolchain. Both generated trees were unchanged.

## Release identity and deployment sources

The checked-out repository is the `GentleKingson` fork, but every default
deployment path still selects the upstream project:

- `docker-compose.yml` and `.env.example` default to
  `palemoky/fight-the-landlord` and
  `palemoky/fight-the-landlord-douzero`.
- The README Docker badge, Test badge, Release badge, logo/screenshots,
  installation commands, Compose download URLs, and Go Report Card point to
  `palemoky/fight-the-landlord`.
- `install.sh` and `install.ps1` query and download releases from
  `palemoky/fight-the-landlord`.
- The CLI updater also uses `palemoky/fight-the-landlord` as its release source.
- The release workflow derives the Docker Hub namespace from
  `vars.DOCKERHUB_USERNAME`; it does not encode a fork-owned default.

Upstream author attribution is legitimate and must remain distinguishable from
the fork's deployment identity. At baseline the fork has no published GitHub
Release and the target `gentlekingson/fight-the-landlord` Docker Hub repositories
are not yet available, so fork installers and images cannot be claimed usable
until the first release is published.

## Reconnect sessions

Runtime behavior and documentation disagree:

- A session is valid without a wall-clock expiry while its owning connection is
  online.
- The first physical disconnect starts a complete two-minute recovery window.
- Duplicate `SetOffline` calls do not extend the deadline.
- Restore atomically consumes and rotates the token. Only one concurrent
  consumer can succeed.
- A failed restore response/rebind rolls the rotation back so the old token can
  be retried.
- Explicit revocation immediately invalidates the presented credential.
- `sessionExpireTime` is a separate ten-minute retention period for cleaning up
  already-dead offline session records, but its name is misleading.

`docs/deployment.md` instead describes an absolute ten-minute credential TTL.
`config.yaml` and `.env.example` expose the in-game offline wait timeout, which is
separate from the two-minute identity recovery window.

The Web client persists `{ player_id, reconnect_token }` in the
`ddz_next_reconnect` `localStorage` entry and sends both fields in the protobuf
reconnect command and JSON revoke request. The CLI/TUI uses the same explicit
protocol token and must remain compatible. No HttpOnly reconnect cookie exists.

## GitHub Actions supply chain

All baseline third-party `uses:` references are listed below. Only cosign is
pinned to an immutable full SHA.

```text
actions/checkout@v7
actions/setup-go@v6
actions/cache/restore@v6
golangci/golangci-lint-action@v9
actions/upload-artifact@v7
actions/cache/save@v6
codecov/codecov-action@v7
actions/setup-node@v6
anchore/sbom-action@v0
docker/setup-qemu-action@v4
actions/download-artifact@v8
softprops/action-gh-release@v3
docker/setup-buildx-action@v4
sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6
docker/login-action@v4
docker/metadata-action@v6
docker/build-push-action@v7
peter-evans/dockerhub-description@v5
palemoky/xiaomi-speaker-action@v1
```

Repeated uses of the same Action are omitted above. The Xiaomi notification runs
inside the privileged image build/sign job and can affect the release result.
Dependabot already includes a `github-actions` entry, but there is no CODEOWNERS
file and no CI pinning check.

## Observability

- `/health` exists and reports the legacy aggregate health response.
- `/livez` exists and remains healthy during graceful shutdown.
- `/readyz` exists and checks shutdown state and Redis readiness.
- `/version` exists with `Cache-Control: no-store`.
- `/metrics` does not exist.
- Server logging uses the standard library's human-readable text output. Several
  messages rely on emoji prefixes; there is no production JSON format or common
  structured field contract.
- There are no Prometheus connection, room, game, matcher, command, Redis, or bot
  metrics. No metric labels exist yet, so there is also no high-cardinality label
  policy enforced in code.

## Test and performance gates

Baseline CI runs Go lint/gofmt/protobuf generation, Go tests with `-race` and
coverage collection, Web protocol/type/lint/unit/benchmark/build checks, mock
Playwright tests, and a final-image production E2E job. Codecov upload is
non-blocking. There is no local coverage threshold, short fuzz gate, property
test gate, load smoke test, nightly soak workflow, or repeatable capacity report.

Observed baseline results:

| Check | Result |
| --- | --- |
| Go generated protocol check | Pass in container with canonical tool versions; no diff |
| `go test -tags=ci -race -p 4 -count=1 ./...` | Pass in `golang:1.26` |
| Go atomic coverage | Pass; total statements `51.0%` |
| `npm ci` | Pass using a writable temporary npm cache |
| `npm run proto:check` | Pass |
| `npm run typecheck` | Pass |
| `npm run lint` | Pass |
| `npm test -- --run` | Pass: 18 files, 244 tests |
| `npm run bench:rules` | Pass: 62.2 ops/s enumeration, 59.8 ops/s Hint on this host |
| `npm run build` | Pass |
| `docker compose --env-file .env.example config --quiet` | Pass |
| `./scripts/check-compose-security.sh` | Pass |

The first containerized Go attempt was interrupted by a full Docker data disk;
after inactive prior test images were removed, the clean rerun passed. This was
an environment capacity failure, not a repository test failure.

## Deployment topology limits

Room ownership, matcher queues, live client bindings, sessions, and reconnect
transactions are process-local. Redis supplies selected persistence and
readiness, but there is no distributed Room/Matcher owner election, cross-instance
session recovery, shared command idempotency cache, or routing affinity contract.
The supported baseline is therefore one game-server instance. Redis
Sentinel/Cluster or managed high availability, multi-region operation, and
cross-instance reconnect are not implemented.
