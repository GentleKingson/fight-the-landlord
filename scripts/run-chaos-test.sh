#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

output_dir="${CHAOS_OUTPUT_DIR:-artifacts/soak/chaos}"
server_port="${CHAOS_SERVER_PORT:-1784}"
server_url="http://127.0.0.1:${server_port}"
websocket_url="ws://127.0.0.1:${server_port}/ws"
server_binary="${CHAOS_SERVER_BINARY:-${RUNNER_TEMP:-/tmp}/fight-landlord-soak-server}"
server_pid_file="${CHAOS_SERVER_PID_FILE:-${RUNNER_TEMP:-/tmp}/fight-landlord-soak-server.pid}"
server_log="${CHAOS_SERVER_LOG:-artifacts/soak/server.log}"
redis_container="${CHAOS_REDIS_CONTAINER_ID:-}"
chaos_scope="${CHAOS_SCOPE:-}"
private_dir="${RUNNER_TEMP:-/tmp}/fight-landlord-chaos-${RANDOM}"
client_binary="$private_dir/chaosclient"
checkpoint="$private_dir/restart-checkpoint.json"
restart_result="$output_dir/restart-result.json"

mkdir -p "$output_dir" "$private_dir" "$(dirname "$server_log")"
chmod 700 "$private_dir"
rm -f "$restart_result" "$output_dir/chaos-report.json" "$output_dir/chaos-report.md"

overall_status="failed"
matcher_status="not_run"
slow_client_status="not_run"
douzero_status="not_run"
redis_pause_status="not_run"
redis_restart_status="not_run"
server_restart_status="not_run"
redis_pause_http_status=""
redis_restart_http_status=""
redis_error_before="0"
redis_error_after=""
hold_client_pid=""
redis_paused="false"
redis_stopped="false"

metric_sum() {
  local metric="$1"
  local metrics
  metrics="$(curl --fail --silent --max-time 3 "$server_url/metrics")" || return 1
  awk -v name="$metric" \
    '$1 == name || index($1, name "{") == 1 { total += $2 } END { print total + 0 }' \
    <<<"$metrics"
}

http_status() {
  curl --silent --output /dev/null --max-time 3 --write-out '%{http_code}' "$1" || true
}

wait_ready() {
  local _
  for _ in $(seq 1 30); do
    if curl --fail --silent --max-time 3 "$server_url/readyz" >/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_process_exit() {
  local pid="$1"
  local _
  for _ in $(seq 1 30); do
    if ! kill -0 "$pid" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

write_report() {
  if [ -z "$redis_error_after" ]; then
    redis_error_after="$(metric_sum fight_landlord_redis_errors_total 2>/dev/null || printf '0\n')"
  fi
  local redis_delta
  redis_delta="$(awk -v before="$redis_error_before" -v after="$redis_error_after" 'BEGIN { delta = after - before; if (delta < 0) delta = 0; printf "%.0f", delta }')"
  local restart_json='null'
  if [ -f "$restart_result" ]; then
    restart_json="$(jq -c . "$restart_result")"
  fi

  jq -n \
    --arg status "$overall_status" \
    --arg matcher "$matcher_status" \
    --arg slow "$slow_client_status" \
    --arg douzero "$douzero_status" \
    --arg redis_pause "$redis_pause_status" \
    --arg redis_pause_http "$redis_pause_http_status" \
    --arg redis_restart "$redis_restart_status" \
    --arg redis_restart_http "$redis_restart_http_status" \
    --arg server_restart "$server_restart_status" \
    --argjson redis_error_delta "$redis_delta" \
    --argjson restart "$restart_json" \
    '{
      schema_version: 1,
      status: $status,
      generated_at: (now | todateiso8601),
      scenarios: {
        matcher_concurrent_cancel_and_timeout: {status: $matcher},
        slow_client_no_read_delayed_read_and_buffer_exhaustion: {status: $slow},
        douzero_normal_timeout_refused_invalid_and_fallback: {status: $douzero},
        redis_pause_and_recovery: {status: $redis_pause, unavailable_http_status: $redis_pause_http},
        redis_stop_start_and_recovery: {status: $redis_restart, unavailable_http_status: $redis_restart_http},
        planned_sigterm_restart_and_client_recovery: ({status: $server_restart} + ($restart // {}))
      },
      redis_error_count: $redis_error_delta,
      server_crash_count: (if $restart == null then null else $restart.unexpected_crash_count end),
      limitations: [
        "Redis injection covers one local Redis process; Sentinel, Cluster, managed failover, and cross-instance ownership remain untested.",
        "The restart probe creates an active waiting room, not an in-progress game. This single-process implementation intentionally loses in-memory rooms, matcher entries, and reconnect sessions after restart.",
        "Recovery means the client receives a fresh usable session after the old process-bound token and waiting room are rejected; it does not claim cross-process session restoration.",
        "Slow-reader faults deterministically exercise the server outbound queue with no reader and a delayed consumer; they do not emulate a specific WAN bandwidth profile.",
        "DouZero faults use a controlled mock HTTP service and validate heuristic fallback, not the accuracy or capacity of the Python model."
      ]
    }' > "$output_dir/chaos-report.json"

  jq -r '
    "# Chaos Test Report\n\n" +
    "- Status: **" + .status + "**\n" +
    "- Redis error delta: `" + (.redis_error_count | tostring) + "`\n" +
    "- Unexpected server crashes: `" + ((.server_crash_count // "n/a") | tostring) + "`\n\n" +
    "| Scenario | Status |\n| --- | --- |\n" +
    ([.scenarios | to_entries[] | "| " + (.key | gsub("_"; " ")) + " | " + .value.status + " |"] | join("\n")) +
    "\n\n## Restart Semantics\n\n" +
    "The old process-bound session and active waiting room are expected **not** to survive a single-process restart. The probe must reject both, then prove that the fresh session can ping successfully.\n\n" +
    "## Limitations\n\n" +
    ([.limitations[] | "- " + .] | join("\n")) + "\n"
  ' "$output_dir/chaos-report.json" > "$output_dir/chaos-report.md"
}

cleanup() {
  local exit_code=$?
  set +e
  if [ -n "$hold_client_pid" ] && kill -0 "$hold_client_pid" 2>/dev/null; then
    kill -TERM "$hold_client_pid" 2>/dev/null || true
    wait "$hold_client_pid" 2>/dev/null || true
  fi
  if [ "$redis_paused" = "true" ]; then
    docker unpause "$redis_container" >/dev/null 2>&1 || true
  fi
  if [ "$redis_stopped" = "true" ]; then
    docker start "$redis_container" >/dev/null 2>&1 || true
  fi
  write_report
  rm -f "$client_binary" "$checkpoint"
  rmdir "$private_dir" 2>/dev/null || true
  exit "$exit_code"
}
trap cleanup EXIT

# Install cleanup/reporting before validating fault targets so even a refused
# preflight leaves explicit evidence and removes the private working directory.
command -v docker >/dev/null
command -v go >/dev/null
command -v jq >/dev/null
command -v curl >/dev/null
test -n "$redis_container"
test -x "$server_binary"
test -f "$server_pid_file"
[[ "$chaos_scope" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$ ]] || {
  printf 'CHAOS_SCOPE must identify an isolated test run\n' >&2
  exit 2
}
redis_scope="$(docker inspect --format '{{ index .Config.Labels "fight-landlord.chaos-scope" }}' "$redis_container")"
if [ "$redis_scope" != "$chaos_scope" ]; then
  printf 'refusing to fault Redis outside chaos scope %s\n' "$chaos_scope" >&2
  exit 2
fi
server_pid="$(cat "$server_pid_file")"
[[ "$server_pid" =~ ^[1-9][0-9]*$ ]] || {
  printf 'invalid isolated server PID\n' >&2
  exit 2
}
server_executable="$(readlink -f "/proc/$server_pid/exe" 2>/dev/null || true)"
expected_executable="$(readlink -f "$server_binary" 2>/dev/null || true)"
if [ -z "$server_executable" ] || [ "$server_executable" != "$expected_executable" ]; then
  printf 'refusing to signal a PID that is not the isolated server binary\n' >&2
  exit 2
fi

go test -race ./internal/game/match -count=1
matcher_status="passed"

go test -race ./internal/server -run '^TestSlowClientFaultMatrix$' -count=1
slow_client_status="passed"

go test -race ./internal/bot -run '^TestDouZeroFaultMatrix$' -count=1
douzero_status="passed"

redis_error_before="$(metric_sum fight_landlord_redis_errors_total)"
docker pause "$redis_container" >/dev/null
redis_paused="true"
redis_pause_http_status="$(http_status "$server_url/readyz")"
test "$redis_pause_http_status" != "200"
test "$(http_status "$server_url/livez")" = "200"
docker unpause "$redis_container" >/dev/null
redis_paused="false"
wait_ready
redis_pause_status="passed"

docker stop --time 1 "$redis_container" >/dev/null
redis_stopped="true"
redis_restart_http_status="$(http_status "$server_url/readyz")"
test "$redis_restart_http_status" != "200"
test "$(http_status "$server_url/livez")" = "200"
docker start "$redis_container" >/dev/null
redis_stopped="false"
wait_ready
redis_restart_status="passed"
redis_error_after="$(metric_sum fight_landlord_redis_errors_total)"

go build -o "$client_binary" ./tests/chaosclient
"$client_binary" hold-room \
  --url "$websocket_url" \
  --state "$checkpoint" \
  > "$output_dir/hold-client.log" 2>&1 &
hold_client_pid=$!
for _ in $(seq 1 30); do
  if [ -s "$checkpoint" ]; then
    break
  fi
  if ! kill -0 "$hold_client_pid" 2>/dev/null; then
    cat "$output_dir/hold-client.log"
    exit 1
  fi
  sleep 1
done
test -s "$checkpoint"

old_server_pid="$(cat "$server_pid_file")"
kill -TERM "$old_server_pid"
wait_process_exit "$old_server_pid"
kill -TERM "$hold_client_pid"
wait "$hold_client_pid"
hold_client_pid=""

"$server_binary" >> "$server_log" 2>&1 &
new_server_pid=$!
printf '%s\n' "$new_server_pid" > "$server_pid_file"
wait_ready

"$client_binary" probe-restart \
  --url "$websocket_url" \
  --state "$checkpoint" \
  --output "$restart_result"
server_restart_status="passed"

rm -f "$checkpoint"
overall_status="passed"
