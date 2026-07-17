# Changelog

All notable changes to the maintained fork are documented here. The project has not
yet published a fork release; entries remain under `Unreleased` until a tag workflow
successfully produces verifiable artifacts.

## Unreleased

### Security

- Move browser reconnect credentials from localStorage to an HttpOnly,
  SameSite=Strict cookie flow while retaining explicit CLI/TUI tokens.
- Require strict Origin checks for browser session commit and revocation.
- Pin every third-party GitHub Action to a verified full commit SHA and add workflow
  ownership and automated update checks.
- Require SHA-256 verification in macOS, Linux, and Windows release installers.
- Pin DouZero ONNX downloads to an immutable Hugging Face commit and verify every
  model SHA-256 before build or startup.

### Added

- Prometheus connection, room, game, matcher, command, Redis, readiness, and bot
  metrics with bounded labels.
- JSON/text structured logging with credential-key redaction.
- Repeatable WebSocket load and soak tooling with JSON and Markdown evidence.
- Coverage regression checks, fuzz targets, and deterministic Go/Web rule properties.
- Production security, observability, performance, and release verification guides.

### Changed

- Align documentation, installers, Compose defaults, and release workflows on
  `GentleKingson/fight-the-landlord` and the `gentlekingson` Docker Hub namespace.
- Support verified digest references in Compose, bind the plaintext backend to
  loopback by default, and make image/Compose health checks use readiness.
- Clarify that online sessions have no wall-clock expiry and receive a full two-minute
  reconnect window after physical disconnect.
- Pass documented capacity-limit overrides through Compose and make digest-based
  upgrade/rollback procedures authoritative over mutable tags.
- Treat the HttpOnly migration as an identity boundary: legacy localStorage tokens
  are deleted rather than imported, so browsers without a new Cookie may need a new
  identity; CLI/TUI protocol fields remain compatible.

### Known Limitations

- Room and Matcher ownership remains single-instance.
- Redis Sentinel/Cluster or managed high availability is not configured.
- Published SBOMs use BuildKit's final-stage default unless build-stage scanning is
  explicitly enabled; lockfiles and dependency scanning remain required.
- Cross-instance and cross-region reconnect are not implemented.
- No multi-hour or multi-day production soak has been completed; the automated
  workflow currently tops out at 30 minutes.
- Full accounts and moderation, replay, spectator, and tournament systems remain
  outside the current single-instance game service.
