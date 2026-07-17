#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
preflight="$repo_root/scripts/preflight-public-test.sh"
fixtures="$script_dir/fixtures/preflight-public-test"
base_compose="$fixtures/compose.yml"
tests_run=0

isolated_command() {
  env -i \
    HOME="${HOME:-/tmp}" \
    PATH="$PATH" \
    TMPDIR="${TMPDIR:-/tmp}" \
    "$@"
}

run_success() {
  local name="$1"
  shift
  local output
  output="$(mktemp "${TMPDIR:-/tmp}/preflight-test.XXXXXX")"

  if ! isolated_command "$preflight" "$@" >"$output" 2>&1; then
    printf 'FAIL: %s unexpectedly failed\n' "$name" >&2
    sed -n '1,160p' "$output" >&2
    rm -f "$output"
    exit 1
  fi
  if ! grep -Eq '^PASS preflight passed with [0-9]+ warning\(s\)$' "$output"; then
    printf 'FAIL: %s did not print a PASS summary\n' "$name" >&2
    sed -n '1,160p' "$output" >&2
    rm -f "$output"
    exit 1
  fi
  if grep -q '^ERROR' "$output"; then
    printf 'FAIL: %s printed an ERROR\n' "$name" >&2
    sed -n '1,160p' "$output" >&2
    rm -f "$output"
    exit 1
  fi

  rm -f "$output"
  tests_run=$((tests_run + 1))
}

run_failure() {
  local name="$1"
  local expected_code="$2"
  shift 2
  local output
  output="$(mktemp "${TMPDIR:-/tmp}/preflight-test.XXXXXX")"

  if isolated_command "$preflight" "$@" >"$output" 2>&1; then
    printf 'FAIL: %s unexpectedly passed\n' "$name" >&2
    sed -n '1,160p' "$output" >&2
    rm -f "$output"
    exit 1
  fi
  if ! grep -Fq "ERROR [$expected_code]" "$output"; then
    printf 'FAIL: %s did not report ERROR [%s]\n' "$name" "$expected_code" >&2
    sed -n '1,160p' "$output" >&2
    rm -f "$output"
    exit 1
  fi
  if ! grep -Eq '^ERROR preflight failed with [1-9][0-9]* error\(s\)' "$output"; then
    printf 'FAIL: %s did not print an ERROR summary\n' "$name" >&2
    sed -n '1,160p' "$output" >&2
    rm -f "$output"
    exit 1
  fi

  rm -f "$output"
  tests_run=$((tests_run + 1))
}

run_success \
  "valid public-test configuration" \
  --env-file "$fixtures/valid.env" \
  --compose-file "$base_compose"

run_failure \
  "Redis host-port exposure" \
  redis_ports \
  --env-file "$fixtures/valid.env" \
  --compose-file "$base_compose" \
  --compose-file "$fixtures/redis-exposed.override.yml"

run_failure \
  "public HTTP Origin" \
  origin_http_public \
  --env-file "$fixtures/http-public-origin.env" \
  --compose-file "$base_compose"

run_failure \
  "wildcard Origin" \
  origin_wildcard \
  --env-file "$fixtures/wildcard-origin.env" \
  --compose-file "$base_compose"

run_failure \
  "empty Redis password" \
  redis_password_empty \
  --env-file "$fixtures/empty-password.env" \
  --compose-file "$base_compose"

run_failure \
  "example Redis password" \
  redis_password_example \
  --env-file "$fixtures/example-password.env" \
  --compose-file "$base_compose"

run_failure \
  "bare latest game image" \
  game_image_latest \
  --env-file "$fixtures/latest.env" \
  --compose-file "$base_compose"

run_failure \
  "host network mode" \
  host_network \
  --env-file "$fixtures/valid.env" \
  --compose-file "$base_compose" \
  --compose-file "$fixtures/host-network.override.yml"

run_failure \
  "public metrics listener" \
  metrics_public \
  --env-file "$fixtures/metrics-exposed.env" \
  --compose-file "$base_compose"

printf 'PASS: %d public-test preflight fixture tests passed\n' "$tests_run"
