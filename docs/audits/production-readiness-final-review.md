# Production Readiness Final Review

Recorded on 2026-07-17 after the production-readiness remediation and its
isolated production validation.

## Audit identity

| Item | Value |
| --- | --- |
| Baseline branch | `main` |
| Baseline SHA | `cd3409d1ef69aed68cb5db62b4127d25609db5ce` |
| Remediation branch | `codex/production-readiness-remediation` |
| Reviewed implementation SHA | `11e4795dd7a4ad03bd4ede4886b1da78abf6ed98` |
| Go module compatibility path | `github.com/palemoky/fight-the-landlord` (intentionally unchanged) |
| Local release image | `fight-the-landlord:production-readiness` |

This report is committed after the reviewed implementation SHA, so its own
commit cannot truthfully be included in the content it adds. The implementation
SHA above is the exact revision used for the final image build and clean
production E2E run.

## Decision

The local blocking gates passed: generated files, static policy checks,
golangci-lint, gofmt, full Go race/coverage, 11 fuzz targets, Web strict checks,
unit tests, mock E2E, final-image production E2E, endpoint probes, and the
100-connection load baseline. The branch is suitable for a Draft PR.

It must remain Draft until the new PR's remote CI is green. It must not be
described as a published or signed release: the fork still has no tag or GitHub
Release, both intended Docker Hub repositories return 404, and no artifact for
the reviewed implementation SHA exists remotely.

## Stage closure

| Stage | Root cause and closure | Main files and evidence |
| --- | --- | --- |
| 0 - Baseline | Fork identity, reconnect behavior, mutable Actions, localStorage credentials, missing metrics, and missing capacity/fuzz gates were not recorded together. A reproducible baseline now pins the source SHA, toolchain, defaults, and test state. | `docs/audits/production-readiness-baseline.md`; baseline Go race and Web/Compose gates passed. |
| 1 - Fork identity | Documentation, installers, updater, Compose, and release metadata could deploy the upstream `palemoky` project. Defaults now consistently target `GentleKingson`/`gentlekingson`; upstream references remain only for attribution or protocol/module compatibility. | `README.md`, `install.sh`, `install.ps1`, `docker-compose.yml`, `.env.example`, `internal/update/update.go`, `scripts/check-release-identity.sh`. |
| 2 - Session documentation | Documentation incorrectly treated ten-minute dead-record retention as an absolute reconnect-token TTL. Online credentials remain valid; first disconnect starts an unextendable two-minute recovery window; successful restore consumes and rotates; failed publication rolls back. | `internal/server/session/player.go`, session tests, `docs/deployment.md`, `scripts/check-session-docs.sh`; the ambiguous constant is now `deadSessionRetention`. |
| 3 - Supply chain | Third-party Actions used mutable tags, release notification shared a privileged job, binary downloads lacked enforced verification, and a cross-build image was mutable. Every Action is pinned to a verified 40-hex SHA with a version comment; notification is isolated and non-blocking; CODEOWNERS/Dependabot and pinning checks are present; installers fail closed on SHA-256; cross-build and runtime images are digest-pinned. | `.github/workflows/*.yml`, `.github/CODEOWNERS`, `scripts/check-actions-pinning.sh`, installers, `Dockerfile`, `douzero/Dockerfile`. |
| 4 - Web credentials | A usable reconnect token was readable by same-origin JavaScript in `localStorage`. Browser reconnect now uses opaque HttpOnly cookies and a two-phase ticket/commit/refresh flow with exact predecessor/owner binding, rotation rollback, lineage revocation, and command-authority barriers. CLI/TUI explicit protobuf tokens remain compatible. | `internal/server/{connection,web,web_session}.go`, session/handler code, `web/src/transport/wsClient.ts`, `web/src/stores/appStore.ts`, Go/Web/E2E regression tests. |
| 5 - Observability | Only health text and unstructured logs existed. A private Prometheus registry, `/metrics`, bounded labels, Redis instrumentation, lifecycle metrics, JSON logs, and secret/payload redaction tests were added. | `internal/observability`, server/match/room/bot integrations, `docs/observability.md`; focused race tests and structured-log parse/redaction tests passed. |
| 6 - Capacity and faults | There was no repeatable connection, reconnect, room, matcher, slow-client, Redis, shutdown, or DouZero fault harness. Configurable Go load/chaos clients, JSON/Markdown reports, PR smoke, nightly soak, cleanup, and threshold failure semantics now exist. Compose also pins Redis/model inputs and defaults to a loopback game port. | `tests/load`, `tests/chaosclient`, `scripts/run-{load,soak,chaos}-test.sh`, `.github/workflows/soak.yml`, `docs/performance-testing.md`. |
| 7 - Coverage/fuzz/properties | Codecov upload was not a local threshold, and codec/rule/session boundaries lacked fuzz and invariant gates. A baseline-pinned local coverage check, 11 required fuzz targets, rule/state properties, and Web randomized/golden coverage now block regressions. | `scripts/check-coverage.sh`, `scripts/run-fuzz-tests.sh`, `.github/workflows/fuzz.yml`, Go `*_fuzz_test.go`/property tests, `web/tests/ruleProperties.test.ts`. |
| 8 - Operations docs | Install, proxy, Redis, observability, release, migration, and rollback guidance drifted from runtime behavior. The operational set now documents the fork, cookie boundary, TLS/trusted-proxy requirements, metrics ACLs, digest deployment, SBOM/cosign verification, capacity testing, and explicit HA limits. | `README.md`, `docs/{deployment,security,observability,performance-testing,release-verification,web-session-security}.md`, `SECURITY.md`, `CHANGELOG.md`. |
| 9 - Full validation | The final gate found one E2E harness defect: Node `fetch` rejected the local self-signed TLS certificate, so restart health always appeared unavailable; it also asserted a notice no longer emitted. The test now uses Playwright's TLS-aware request context and proves a fresh HttpOnly cookie is issued after the process-local session is lost. | `web/tests/e2e-production/faults.spec.ts`; targeted restart E2E passed, then the clean 14-test production suite passed. |
| 10 - Final audit | Release claims previously had no single evidence boundary. This report separates local evidence from remote publication state and records unresolved risks. | This file. |

## Session security

- `ddz_web_session` is an opaque random credential with `HttpOnly`,
  `SameSite=Strict`, `Path=/`, and a seven-day bounded `Max-Age`.
- `Secure` is set for direct TLS or only when the direct peer is in the trusted
  proxy CIDR set and supplies the exact `X-Forwarded-Proto: https` value.
- Browser upgrade obtains an owner nonce when no known predecessor is present.
  A 30-second ticket is committed by same-origin POST, then refreshed to confirm
  delivery before the predecessor is retired.
- Commit/refresh/revoke accept POST only, require an allowed nonempty Origin,
  reject unknown/oversized JSON, and return `Cache-Control: no-store`.
- Reconnect rotation is single-consumer. Failed delivery, room publication, or
  ticket creation rolls the old credential back; logout revokes the complete
  observed lineage and drains in-flight commands before returning.
- Web code deletes `ddz_next_reconnect` but never reads or migrates its token.
  Users without a new cookie can receive a new identity after upgrade. Rolling
  back to the old Web client cannot recover the HttpOnly credential.
- CLI/TUI retains the explicit reconnect-token protocol. The protobuf change is
  additive and generated Go/Web codec outputs are synchronized.

## Prometheus metrics

The registry exposes the required bounded application families (plus standard
Go/process collectors):

```text
fight_landlord_websocket_connections_current
fight_landlord_websocket_connections_total
fight_landlord_websocket_rejected_total
fight_landlord_slow_client_disconnects_total
fight_landlord_reconnect_attempts_total
fight_landlord_reconnect_success_total
fight_landlord_reconnect_failure_total{reason}
fight_landlord_rooms_current
fight_landlord_room_cleanup_total
fight_landlord_games_current
fight_landlord_games_started_total
fight_landlord_games_finished_total
fight_landlord_game_duration_seconds
fight_landlord_match_queue_current
fight_landlord_match_wait_seconds
fight_landlord_match_cancelled_total{reason}
fight_landlord_match_transaction_rollback_total{stage}
fight_landlord_commands_total{type,result}
fight_landlord_command_latency_seconds{type}
fight_landlord_protocol_errors_total{reason}
fight_landlord_idempotency_cache_hits_total
fight_landlord_idempotency_conflicts_total
fight_landlord_redis_operation_seconds{operation}
fight_landlord_redis_errors_total{operation}
fight_landlord_readiness_status
fight_landlord_bot_decision_seconds{engine}
fight_landlord_bot_timeouts_total{engine}
fight_landlord_bot_fallback_total{from,to}
```

No metric label accepts player, room, game, request, nickname, or other
unbounded identity values. Production JSON logs use bounded event metadata and
tests reject reconnect tokens, cookies, Redis passwords, chat bodies, and
nickname/payload detail.

## Final verification

### Static and generated checks

| Check | Result |
| --- | --- |
| `git diff --check` | Pass |
| Release identity, session docs, Actions pinning, Compose security | Pass |
| `actionlint .github/workflows/*.yml` | Pass |
| `bash -n scripts/*.sh` | Pass |
| golangci-lint v2.11.1 with `--build-tags=ci` | Pass, `0 issues` |
| gofmt over tracked Go files | Pass, no output |
| Protobuf 3.13.0 + `protoc-gen-go v1.36.11` regeneration | Pass, no generated diff |
| Web protocol generation check | Pass |
| `docker compose --env-file .env.example config --quiet` | Pass |

All third-party workflow `uses:` entries are immutable full SHAs. The checked
set includes checkout/setup/cache/artifact actions, Codecov, golangci-lint,
Docker build/login/metadata/QEMU actions, SBOM, cosign installer, GitHub Release,
Docker Hub description, and the isolated notification action.

### Go and coverage

`go test -tags=ci -race -p 4 -count=1 -coverprofile=coverage.out
-covermode=atomic ./...` passed in `go1.26.5 linux/arm64`.

| Scope | Baseline minimum | Final |
| --- | ---: | ---: |
| Total | 51.03% | 55.06% |
| config | 91.14% | 92.75% |
| match | 75.87% | 77.41% |
| room | 71.25% | 72.07% |
| rule | 90.30% | 93.80% |
| codec | 59.80% | 62.04% |
| payload | 73.01% | 73.28% |
| server | 75.57% | 78.64% |
| handler | 59.36% | 62.62% |
| session | 81.12% | 84.23% |

### Fuzz and properties

The required manifest discovered every target exactly once. A clean isolated
run used `FUZZ_TIME=30s`, `FUZZ_TIMEOUT=20m`, and `FUZZ_PARALLEL=1`; all 11
targets passed:

```text
FuzzEnvironmentValueParsing
FuzzMessageCodecRoundTrip
FuzzPlayCardsPayloadRoundTrip
FuzzRankFromChar
FuzzFindCardsInHand
FuzzLegalResponseProperties
FuzzParseHand
FuzzReconnectCredentialInput
FuzzClientIncomingFrame
FuzzWebSessionOriginAndTrustedProxy
FuzzSessionRevokeJSON
```

Property tests cover owned/legal hints, legal responses, card conservation,
category/bomb ordering, rocket dominance, codec/state round trips, idempotent
single commit, request-ID isolation, concurrent physical-generation replay, and
non-regressing reconnect game/turn/settlement state. Web golden/random rule
properties and the rules benchmark passed.

Two preliminary all-target invocations returned `context deadline exceeded`
from the Go fuzz engine for the codec target at the 30-second stop boundary.
The target then passed alone for 30 seconds and the final fresh-container
all-target run completed 11/11 without reducing fuzz time, widening the
per-package timeout, or changing the target. This runner flake remains recorded
rather than being hidden.

### Web

| Check | Result |
| --- | --- |
| `npm ci` with a writable temporary cache | Pass; 302 packages audited, 0 vulnerabilities |
| Protocol, TypeScript strict check, ESLint | Pass |
| Vitest | 19 files, 257 tests passed |
| Rules benchmark | Pass; 48.72 ops/s enumeration, 52.78 ops/s Hint on this host |
| Production build | Pass |
| Mock Playwright E2E | 15/15 passed |
| Final-image Playwright E2E | 14/14 passed in 2.0 minutes |

The production suite used HTTPS through the local reverse proxy. Chromium ran
deployment headers/endpoints, Origin rejection, Redis isolation, matching,
mid-game disconnects, SIGTERM/restart, Redis pause/recovery, two complete games,
repeat reconnect, and settlement restore. Firefox and WebKit both ran real
connection/card smoke and the complete HttpOnly cookie lifecycle test.

### Image and endpoints

The final image was rebuilt from the reviewed implementation SHA with
`VERSION=production-readiness` and the pinned Node, Go, and distroless base
digests.

| Item | Local value |
| --- | --- |
| Image/index ID and local RepoDigest | `sha256:5d28d1073f792a0b8db3c1ec3507b062459b350ea4ac526b724b5598c20539d1` |
| Linux/arm64 application manifest | `sha256:e82e745d6ae1afae4c2d802778812fb03815074bbc4c187c3ef83b1f0454190b` |
| Image config | `sha256:181368edf8e70834da9b069d5d8fdada129a58894fec205e79ad6027246cb63f` |
| Local BuildKit attestation manifest | `sha256:8250f0c558ce84bb978f785d237acd91b2915ff7378aeec5bc36404c0c04e91b` |
| Runtime identity | Linux/arm64, user `65532`, version label `production-readiness` |

`/health`, `/livez`, `/readyz`, `/version`, `/metrics`, and `/` each returned
HTTP 200 from the isolated final-image stack. `/version` reported both server
and Web client as `production-readiness`. Redis had no published host port.
After the tests, Compose removed both containers, its network, and its volume;
the filtered container count was zero.

These are local Docker values, not a registry-published digest. The BuildKit
attestation entry is not a substitute for extracting and validating the release
workflow's per-platform SBOM and SLSA provenance.

### Capacity baseline

The final image passed a 100-connection, 60-second run with strict cleanup
thresholds (`final_connections_delta=0`, `final_goroutines_delta<=10`, and
`redis_errors=0`). Reports were generated as JSON and Markdown outside the
repository.

| Metric | Result |
| --- | ---: |
| Connections | 100/100, 0 rejected |
| Idle checks | 100/100 |
| Reconnects | 10/10 |
| Room scenarios | 10/10 |
| Rooms created/joined/left | 10/10/20 |
| All-operation latency p50/p95/p99/max | 7.50/15.13/32.88/33.84 ms |
| Peak server RSS | 39,309,312 bytes |
| Peak server goroutines | 214 |
| Baseline/final goroutines | 13/13 |
| Baseline/final connections | 0/0 |
| Redis error delta | 0 |
| Slow-client disconnect delta | 0 |
| Telemetry errors | 0 |

The short load does not play complete games, inject Redis faults, exercise
DouZero, or prove multi-hour stability. Those paths are covered separately by
the deterministic tests/production fault suite or remain soak work.

## Remote artifact state

Remote state was rechecked on 2026-07-17:

- GitHub tags: none.
- GitHub Releases: none.
- Docker Hub `gentlekingson/fight-the-landlord`: HTTP 404.
- Docker Hub `gentlekingson/fight-the-landlord-douzero`: HTTP 404.
- GitHub Actions retains 15 artifacts for `main` and an older remediation
  branch, but none is for `11e4795dd7a4ad03bd4ede4886b1da78abf6ed98`.
- No remote digest, SBOM, provenance, cosign signature, or release binary exists
  for this implementation. Private vulnerability reporting is disabled.

The tag workflow is configured to build linux/amd64 and linux/arm64 images with
`sbom: true`, maximum provenance, and keyless cosign signatures. Those controls
are unverified until the Docker Hub repositories/credentials are provisioned
and the first reviewed tag completes. GitHub Release binaries receive fail-closed
SHA-256 sidecars but are not cosign-signed.

## Deployment and rollback

Production must supply a nonempty Redis secret, an explicit Origin allowlist,
trusted proxy CIDRs matching only the final TLS hop, and a loopback/private
metrics policy at the reverse proxy. Deploy immutable `GAME_IMAGE_REF` and
`DOUZERO_IMAGE_REF` digests after validating cosign, per-platform SBOM, and
provenance. Do not deploy the current nonexistent Docker Hub tags.

Upgrade Web and server atomically. The localStorage token is deleted, not
migrated, so a browser may receive a new identity. Rollback must restore the
previous verified image digest, minimum client version, and proxy configuration
together. Rollback cannot recover process-local rooms or translate an HttpOnly
cookie back into the old JavaScript credential format.

## Remaining risks

1. Room ownership, matcher queues, sessions, command cache, and reconnect
   authority remain single-process. There is no multi-instance election,
   affinity contract, or cross-instance recovery.
2. Redis supports one plaintext address/password/database endpoint. TLS,
   Sentinel/Cluster, managed failover, and cross-region durability are not
   implemented.
3. Multi-region deployment, cross-region reconnect, and regional disaster
   recovery are not implemented.
4. The local image validation is linux/arm64 only. Remote multi-architecture
   manifests, per-platform SBOM/provenance, signatures, and release credentials
   remain unverified.
5. No multi-hour or multi-day soak was run in this review; the new nightly and
   workflow-dispatch framework must establish longer baselines.
6. A complete account system, moderation/user governance, replay, spectator,
   and tournament systems remain out of scope.
7. DouZero is optional and deterministic fault/fallback tests pass, but this
   final gate did not build or publish the complete DouZero multi-arch image.
8. The local npm cache ownership issue and finite Docker VM disk required an
   isolated npm cache and pruning unused build cache. They are host maintenance
   concerns, not repository test bypasses.

## Ready recommendation

Keep the PR as Draft on creation. Convert it to Ready only after the branch's
GitHub checks pass and the maintainer has reviewed the migration/rollback plan.
Do not publish a production tag until Docker Hub repositories and secrets exist
and a release candidate proves remote digest, per-platform SBOM/provenance, and
cosign verification fail closed.
