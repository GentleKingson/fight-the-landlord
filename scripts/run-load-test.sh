#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

json_out="${LOAD_TEST_JSON_OUT:-artifacts/load/load-test.json}"
markdown_out="${LOAD_TEST_MARKDOWN_OUT:-artifacts/load/load-test.md}"
mkdir -p "$(dirname "$json_out")" "$(dirname "$markdown_out")"

args=(
  --url "${LOAD_TEST_URL:-ws://127.0.0.1:1780/ws}"
  --metrics-url "${LOAD_TEST_METRICS_URL:-auto}"
  --connections "${LOAD_TEST_CONNECTIONS:-100}"
  --connect-concurrency "${LOAD_TEST_CONNECT_CONCURRENCY:-10}"
  --duration "${LOAD_TEST_DURATION:-60s}"
  --cooldown "${LOAD_TEST_COOLDOWN:-2s}"
  --reconnects "${LOAD_TEST_RECONNECTS:-10}"
  --room-operations "${LOAD_TEST_ROOM_OPERATIONS:-10}"
  --match-operations "${LOAD_TEST_MATCH_OPERATIONS:-0}"
  --match-timeouts "${LOAD_TEST_MATCH_TIMEOUTS:-0}"
  --match-timeout-wait "${LOAD_TEST_MATCH_TIMEOUT_WAIT:-35s}"
  --operation-timeout "${LOAD_TEST_OPERATION_TIMEOUT:-10s}"
  --client-version "${LOAD_TEST_CLIENT_VERSION:-ci}"
  --min-connection-success-rate "${LOAD_TEST_MIN_CONNECTION_SUCCESS_RATE:-1}"
  --min-reconnect-success-rate "${LOAD_TEST_MIN_RECONNECT_SUCCESS_RATE:-1}"
  --min-room-success-rate "${LOAD_TEST_MIN_ROOM_SUCCESS_RATE:-1}"
  --min-match-success-rate "${LOAD_TEST_MIN_MATCH_SUCCESS_RATE:-1}"
  --min-idle-success-rate "${LOAD_TEST_MIN_IDLE_SUCCESS_RATE:-1}"
  --max-p99-ms "${LOAD_TEST_MAX_P99_MS:-0}"
  --max-server-rss-bytes "${LOAD_TEST_MAX_SERVER_RSS_BYTES:-0}"
  --max-server-goroutines "${LOAD_TEST_MAX_SERVER_GOROUTINES:-0}"
  --max-final-goroutines-delta "${LOAD_TEST_MAX_FINAL_GOROUTINES_DELTA:--1}"
  --max-redis-errors "${LOAD_TEST_MAX_REDIS_ERRORS:--1}"
  --max-final-connections-delta "${LOAD_TEST_MAX_FINAL_CONNECTIONS_DELTA:--1}"
  --json-out "$json_out"
  --markdown-out "$markdown_out"
)

exec go run ./tests/load "${args[@]}" "$@"
