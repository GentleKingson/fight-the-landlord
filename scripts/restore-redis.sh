#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
invocation_dir="$(pwd -P)"

compose_file="$repo_root/docker-compose.yml"
env_file="$repo_root/.env"
project_name=""
backup_dir="${REDIS_BACKUP_DIR:-}"
target_redis_db="${REDIS_DB:-}"
checksum_file=""
timeout_seconds=120
confirmed=false
stop_running=false
archive=""
backup_dir_from_cli=false

usage() {
  cat <<'EOF'
Usage: scripts/restore-redis.sh [options] --confirm-restore ARCHIVE

Restore a checksummed RDB archive into the Compose Redis named volume.

Options:
  --confirm-restore   Required non-interactive overwrite confirmation
  --stop-running      Back up, then stop running poker-server/Redis services
  --checksum PATH     Checksum file (default: ARCHIVE.sha256)
  --compose-file PATH Compose file (default: docker-compose.yml)
  --env-file PATH     Compose env file (default: .env)
  --project-name NAME Compose project name
  --backup-dir PATH   Directory for the pre-restore volume snapshot
  --timeout SECONDS   Health timeout (default: 120)
  -h, --help          Show this help

Without --stop-running, the script refuses to touch a running Redis or game
service. It always creates an offline volume snapshot before replacement.
EOF
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

need_value() {
  [[ $# -ge 2 && -n "$2" ]] || die "$1 requires a value"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --confirm-restore)
      confirmed=true
      shift
      ;;
    --stop-running)
      stop_running=true
      shift
      ;;
    --checksum)
      need_value "$@"
      checksum_file="$2"
      shift 2
      ;;
    --compose-file)
      need_value "$@"
      compose_file="$2"
      shift 2
      ;;
    --env-file)
      need_value "$@"
      env_file="$2"
      shift 2
      ;;
    --project-name)
      need_value "$@"
      project_name="$2"
      shift 2
      ;;
    --backup-dir)
      need_value "$@"
      backup_dir="$2"
      backup_dir_from_cli=true
      shift 2
      ;;
    --timeout)
      need_value "$@"
      timeout_seconds="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      die "unknown argument: $1"
      ;;
    *)
      [[ -z "$archive" ]] || die "only one archive may be restored"
      archive="$1"
      shift
      ;;
  esac
done

[[ "$confirmed" == true ]] || die "restore requires --confirm-restore"
[[ -n "$archive" ]] || die "an archive path is required"
[[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]] || die "--timeout must be a positive integer"

absolute_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$invocation_dir" "$1" ;;
  esac
}

compose_file="$(absolute_path "$compose_file")"
env_file="$(absolute_path "$env_file")"
archive="$(absolute_path "$archive")"
[[ -n "$checksum_file" ]] && checksum_file="$(absolute_path "$checksum_file")"

[[ -f "$compose_file" ]] || die "Compose file not found: $compose_file"
[[ -f "$env_file" ]] || die "env file not found: $env_file"
[[ -f "$archive" ]] || die "archive not found: $archive"
checksum_file="${checksum_file:-${archive}.sha256}"
[[ -f "$checksum_file" ]] || die "checksum file not found: $checksum_file"

dotenv_value() {
  local key="$1"
  awk -v key="$key" '
    /^[[:space:]]*#/ { next }
    {
      equals = index($0, "=")
      if (equals == 0) next
      name = substr($0, 1, equals - 1)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", name)
      if (name != key) next
      value = substr($0, equals + 1)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if (value ~ /^".*"$/ || value ~ /^\047.*\047$/) {
        value = substr(value, 2, length(value) - 2)
      }
      result = value
    }
    END { if (result != "") print result }
  ' "$env_file"
}

if [[ -z "$backup_dir" && "$backup_dir_from_cli" == false ]]; then
  backup_dir="$(dotenv_value REDIS_BACKUP_DIR)"
fi
if [[ -z "$target_redis_db" ]]; then
  target_redis_db="$(dotenv_value REDIS_DB)"
fi
backup_dir="${backup_dir:-$repo_root/backups/redis}"
backup_dir="$(absolute_path "$backup_dir")"
target_redis_db="${target_redis_db:-0}"
[[ "$target_redis_db" =~ ^[0-9]+$ ]] || die "target REDIS_DB must be a non-negative integer"

for command_name in docker tar awk date sort; do
  command -v "$command_name" >/dev/null 2>&1 || die "required command not found: $command_name"
done

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print tolower($1)}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print tolower($1)}'
  else
    die "sha256sum or shasum is required"
  fi
}

expected_archive_sha="$(awk 'NF { print tolower($1); exit }' "$checksum_file")"
[[ "$expected_archive_sha" =~ ^[0-9a-f]{64}$ ]] || die "checksum file does not contain a SHA-256 value"
actual_archive_sha="$(sha256_file "$archive")"
[[ "$actual_archive_sha" == "$expected_archive_sha" ]] || die "archive SHA-256 verification failed"

archive_entries="$(tar -tzf "$archive")" || die "archive is not a readable gzip tar file"
entry_count="$(printf '%s\n' "$archive_entries" | awk 'NF { count++ } END { print count + 0 }')"
dump_count="$(printf '%s\n' "$archive_entries" | awk '$0 == "dump.rdb" { count++ } END { print count + 0 }')"
metadata_count="$(printf '%s\n' "$archive_entries" | awk '$0 == "metadata.txt" { count++ } END { print count + 0 }')"
[[ "$entry_count" == 2 && "$dump_count" == 1 && "$metadata_count" == 1 ]] || \
  die "archive must contain exactly dump.rdb and metadata.txt"

temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/fight-landlord-redis-restore.XXXXXX")"
rollback_armed=false
rollback_snapshot=""
rollback_partial=""
redis_was_running=false
poker_was_running=false
redis_debug_was_running=false
lock_dir=""
lock_token="${REDIS_OPS_LOCK_TOKEN:-}"
lock_owned=false

compose=(docker compose --file "$compose_file" --env-file "$env_file")
if [[ -n "$project_name" ]]; then
  compose+=(--project-name "$project_name")
fi
compose_debug=("${compose[@]}" --profile redis-debug)

restore_volume_snapshot() {
  local snapshot="$1"
  local snapshot_dir snapshot_name
  snapshot_dir="$(cd "$(dirname "$snapshot")" && pwd -P)"
  snapshot_name="$(basename "$snapshot")"
  # shellcheck disable=SC2016 # Expanded by the container shell.
  "${compose[@]}" run --rm --no-deps -T --user 0:0 \
    --entrypoint sh --volume "$snapshot_dir:/rollback:ro" redis -eu -c '
      find /data -mindepth 1 -maxdepth 1 -exec rm -rf {} \;
      tar -xzf "/rollback/$1" -C /data
      chown -R redis:redis /data
    ' sh "$snapshot_name" >/dev/null
}

remove_redis_container() {
  # A stopped container can retain a stale unhealthy state after its mounted
  # volume is replaced. Recreate it without touching the named data volume.
  "${compose[@]}" rm --force --stop redis >/dev/null
}

redis_cli() {
  # shellcheck disable=SC2016 # Expanded by the container shell.
  "${compose[@]}" exec -T redis sh -eu -c '
    password="$(cat /run/secrets/redis_password)"
    test -n "$password"
    export REDISCLI_AUTH="$password"
    exec redis-cli --raw "$@"
  ' sh "$@"
}

release_lock() {
  if [[ "$lock_owned" == true ]]; then
    rm -f "$lock_dir/token" "$lock_dir/owner"
    rmdir "$lock_dir" 2>/dev/null || true
    lock_owned=false
  fi
}

wait_for_health() {
  local deadline=$(( $(date +%s) + timeout_seconds ))
  local redis_cid health pong
  while (( $(date +%s) < deadline )); do
    redis_cid="$("${compose[@]}" ps -q redis 2>/dev/null || true)"
    if [[ -n "$redis_cid" ]]; then
      health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}' "$redis_cid" 2>/dev/null || true)"
      pong="$(redis_cli PING 2>/dev/null || true)"
      if [[ "$health" == healthy && "$pong" == PONG ]]; then
        return 0
      fi
    fi
    sleep 1
  done
  return 1
}

rollback() {
  local rollback_status=0
  printf 'Restore failed; attempting to restore the pre-restore volume snapshot.\n' >&2
  "${compose[@]}" stop redis >/dev/null 2>&1 || true
  remove_redis_container || rollback_status=$?
  if [[ "$rollback_status" -eq 0 ]]; then
    restore_volume_snapshot "$rollback_snapshot" || rollback_status=$?
  fi
  if [[ "$redis_was_running" == true && "$rollback_status" -eq 0 ]]; then
    "${compose[@]}" up -d redis >/dev/null 2>&1 || rollback_status=$?
    if [[ "$rollback_status" -eq 0 ]]; then
      wait_for_health >/dev/null 2>&1 || rollback_status=$?
    fi
  fi
  if [[ "$rollback_status" -eq 0 ]]; then
    printf 'Rollback completed. The poker-server service remains stopped.\n' >&2
  else
    printf 'WARNING: automatic rollback was incomplete; preserve %s for manual recovery.\n' "$rollback_snapshot" >&2
  fi
}

cleanup() {
  local status=$?
  trap - EXIT
  if [[ "$status" -ne 0 && "$rollback_armed" == true && -n "$rollback_snapshot" ]]; then
    rollback
  fi
  [[ -z "$rollback_partial" ]] || rm -f "$rollback_partial"
  rm -rf "$temp_dir"
  release_lock
  exit "$status"
}
trap cleanup EXIT

tar -xOzf "$archive" dump.rdb >"$temp_dir/dump.rdb"
tar -xOzf "$archive" metadata.txt >"$temp_dir/metadata.txt"
[[ -s "$temp_dir/dump.rdb" && -s "$temp_dir/metadata.txt" ]] || die "archive contains an empty required file"
chmod 600 "$temp_dir/dump.rdb" "$temp_dir/metadata.txt"

metadata_value() {
  awk -v key="$1" '
    index($0, key "=") == 1 {
      print substr($0, length(key) + 2)
      exit
    }
  ' "$temp_dir/metadata.txt"
}

format_version="$(metadata_value FORMAT_VERSION)"
metadata_rdb_sha="$(metadata_value RDB_SHA256 | tr '[:upper:]' '[:lower:]')"
metadata_redis_db="$(metadata_value REDIS_DB)"
min_player_stats="$(metadata_value MIN_PLAYER_STATS_KEYS)"
min_settlements="$(metadata_value MIN_SETTLEMENT_KEYS)"
require_leaderboard="$(metadata_value REQUIRE_LEADERBOARD_TOTAL)"

[[ "$format_version" == 1 ]] || die "unsupported backup format version: ${format_version:-missing}"
[[ "$metadata_rdb_sha" =~ ^[0-9a-f]{64}$ ]] || die "backup metadata has an invalid RDB checksum"
[[ "$(sha256_file "$temp_dir/dump.rdb")" == "$metadata_rdb_sha" ]] || die "embedded RDB SHA-256 verification failed"
[[ "$metadata_redis_db" =~ ^[0-9]+$ ]] || die "backup metadata has an invalid Redis DB"
[[ "$metadata_redis_db" == "$target_redis_db" ]] || \
  die "backup Redis DB $metadata_redis_db does not match target Redis DB $target_redis_db"
[[ "$min_player_stats" =~ ^[0-9]+$ ]] || die "backup metadata has an invalid player key count"
[[ "$min_settlements" =~ ^[0-9]+$ ]] || die "backup metadata has an invalid settlement key count"
[[ "$require_leaderboard" =~ ^[01]$ ]] || die "backup metadata has an invalid leaderboard key check"

"${compose[@]}" config --quiet >/dev/null
"${compose[@]}" config --services | grep -Fxq redis || die "Compose service 'redis' was not found"

compose_project="$("${compose[@]}" config | awk '$1 == "name:" { print $2; exit }')"
[[ "$compose_project" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]] || die "could not determine a safe Compose project name"
lock_dir="/tmp/fight-landlord-redis-ops-${compose_project}.lock"
if [[ -n "$lock_token" && -f "$lock_dir/token" && "$(<"$lock_dir/token")" == "$lock_token" ]]; then
  :
elif mkdir "$lock_dir" 2>/dev/null; then
  lock_owned=true
  lock_token="$$-${RANDOM}-$(date +%s)"
  printf '%s\n' "$lock_token" >"$lock_dir/token"
  printf 'pid=%s\nstarted_at_utc=%s\n' "$$" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >"$lock_dir/owner"
else
  die "another Redis backup/restore is active for project $compose_project (lock: $lock_dir)"
fi

service_running() {
  local service="$1"
  local container_id
  local -a runner=("${compose[@]}")
  [[ "$service" != redis-debug ]] || runner=("${compose_debug[@]}")
  while IFS= read -r container_id; do
    [[ -n "$container_id" ]] || continue
    if [[ "$(docker inspect --format '{{.State.Running}}' "$container_id" 2>/dev/null || true)" == true ]]; then
      return 0
    fi
  done < <("${runner[@]}" ps --all -q "$service" 2>/dev/null || true)
  return 1
}

service_running redis && redis_was_running=true
service_running poker-server && poker_was_running=true
service_running redis-debug && redis_debug_was_running=true

if [[ "$stop_running" != true && ( "$redis_was_running" == true || "$poker_was_running" == true || "$redis_debug_was_running" == true ) ]]; then
  die "Redis, redis-debug, or poker-server is running; stop all three first or pass --stop-running"
fi

if [[ "$stop_running" == true && "$redis_was_running" == true ]]; then
  # This online RDB is an additional operator-visible recovery point. The raw
  # snapshot below remains the authoritative automatic rollback source.
  backup_args=(--compose-file "$compose_file" --env-file "$env_file" --output-dir "$backup_dir" --redis-db "$target_redis_db")
  [[ -z "$project_name" ]] || backup_args+=(--project-name "$project_name")
  REDIS_OPS_LOCK_TOKEN="$lock_token" "$script_dir/backup-redis.sh" "${backup_args[@]}"
fi

if [[ "$stop_running" == true && "$poker_was_running" == true ]]; then
  "${compose[@]}" stop poker-server
fi
if [[ "$stop_running" == true && "$redis_debug_was_running" == true ]]; then
  "${compose_debug[@]}" stop redis-debug
fi
if [[ "$stop_running" == true && "$redis_was_running" == true ]]; then
  "${compose[@]}" stop redis
fi

service_running redis && die "Redis is still running"
service_running poker-server && die "poker-server is still running"
service_running redis-debug && die "redis-debug is still running"

# Always recreate Redis after changing /data. Besides applying the current
# Compose configuration, this resets Docker health state before the restored
# dataset is validated.
remove_redis_container

# Validate the RDB with the pinned Redis image before reading or changing the
# target volume.
if ! "${compose[@]}" run --rm --no-deps -T --user 0:0 \
  --entrypoint redis-check-rdb --volume "$temp_dir:/restore:ro" \
  redis /restore/dump.rdb >/dev/null; then
  die "redis-check-rdb rejected the archive"
fi

mkdir -p "$backup_dir"
chmod 700 "$backup_dir"
timestamp="$(date -u '+%Y%m%dT%H%M%SZ')"
rollback_snapshot="$backup_dir/pre-restore-redis-volume-${timestamp}-$$.tar.gz"
rollback_partial="${rollback_snapshot}.partial"
"${compose[@]}" run --rm --no-deps -T --user 0:0 --entrypoint sh redis \
  -eu -c 'tar -C /data -czf - .' >"$rollback_partial"
chmod 600 "$rollback_partial"
tar -tzf "$rollback_partial" >/dev/null || die "could not verify the pre-restore volume snapshot"
mv "$rollback_partial" "$rollback_snapshot"
rollback_partial=""
rollback_sha="$(sha256_file "$rollback_snapshot")"
printf '%s  %s\n' "$rollback_sha" "$(basename "$rollback_snapshot")" >"${rollback_snapshot}.sha256"
chmod 600 "${rollback_snapshot}.sha256"
rollback_armed=true

"${compose[@]}" run --rm --no-deps -T --user 0:0 \
  --entrypoint sh --volume "$temp_dir:/restore:ro" redis -eu -c '
    find /data -mindepth 1 -maxdepth 1 -exec rm -rf {} \;
    cp /restore/dump.rdb /data/dump.rdb
    chmod 600 /data/dump.rdb
    chown -R redis:redis /data
  '

# Redis gives an existing AOF precedence over dump.rdb. Convert the verified
# RDB to a fresh multipart AOF through a private Unix socket before starting
# the normal service with --appendonly yes; otherwise an empty AOF could hide
# the restored dataset on first boot.
# shellcheck disable=SC2016 # Expanded by the one-off container shell.
"${compose[@]}" run --rm --no-deps -T --user 0:0 --entrypoint sh redis \
  -eu -c '
    socket=/tmp/fight-landlord-restore.sock
    logfile=/tmp/fight-landlord-restore.log
    server_pid=""

    stop_private_redis() {
      if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
        redis-cli -s "$socket" SHUTDOWN NOSAVE >/dev/null 2>&1 || kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
      fi
    }
    trap stop_private_redis EXIT INT TERM

    /usr/local/bin/docker-entrypoint.sh redis-server \
      --port 0 \
      --protected-mode yes \
      --unixsocket "$socket" \
      --unixsocketperm 700 \
      --dir /data \
      --dbfilename dump.rdb \
      --appendonly no \
      --save "" \
      --logfile "$logfile" &
    server_pid=$!

    deadline=$(( $(date +%s) + $1 ))
    while [ ! -S "$socket" ]; do
      kill -0 "$server_pid" 2>/dev/null || {
        cat "$logfile" >&2 || true
        exit 1
      }
      [ "$(date +%s)" -lt "$deadline" ] || {
        echo >&2 "timed out loading the restored RDB"
        exit 1
      }
      sleep 1
    done

    [ "$(redis-cli -s "$socket" --raw PING)" = PONG ]
    redis-cli -s "$socket" --raw CONFIG SET appendonly yes >/dev/null

    while :; do
      persistence="$(redis-cli -s "$socket" --raw INFO persistence | tr -d "\r")"
      if printf "%s\n" "$persistence" | grep -q "^aof_rewrite_in_progress:0$" &&
         printf "%s\n" "$persistence" | grep -q "^aof_rewrite_scheduled:0$"; then
        printf "%s\n" "$persistence" | grep -q "^aof_enabled:1$"
        printf "%s\n" "$persistence" | grep -q "^aof_last_bgrewrite_status:ok$"
        break
      fi
      [ "$(date +%s)" -lt "$deadline" ] || {
        echo >&2 "timed out converting the restored RDB to AOF"
        exit 1
      }
      sleep 1
    done

    redis-cli -s "$socket" SHUTDOWN NOSAVE >/dev/null
    wait "$server_pid"
    server_pid=""
    trap - EXIT INT TERM
    test -s /data/appendonlydir/appendonly.aof.manifest
  ' sh "$timeout_seconds"

"${compose[@]}" up -d redis

wait_for_health || die "restored Redis did not become healthy within ${timeout_seconds}s"

persistence_info="$(redis_cli INFO persistence | tr -d '\r')"
grep -q '^aof_enabled:1$' <<<"$persistence_info" || die "restored Redis does not have AOF enabled"
grep -q '^aof_last_write_status:ok$' <<<"$persistence_info" || die "restored Redis reported an AOF write failure"

scan_count() {
  redis_cli -n "$metadata_redis_db" --scan --pattern "$1" | LC_ALL=C sort -u | awk 'END { print NR + 0 }'
}

actual_player_stats="$(scan_count 'player:stats:*')"
actual_settlements="$(scan_count 'leaderboard:settlement:*')"
actual_leaderboard="$(redis_cli -n "$metadata_redis_db" EXISTS leaderboard:score)"
(( actual_player_stats >= min_player_stats )) || \
  die "critical key check failed for player:stats:* (minimum $min_player_stats, got $actual_player_stats)"
(( actual_settlements >= min_settlements )) || \
  die "critical key check failed for leaderboard:settlement:* (minimum $min_settlements, got $actual_settlements)"
if [[ "$require_leaderboard" == 1 && "$actual_leaderboard" != 1 ]]; then
  die "critical key check failed for leaderboard:score"
fi

restored_keys="$(redis_cli -n "$metadata_redis_db" DBSIZE)"
rollback_armed=false

printf 'Redis restore completed; poker-server was not started.\n'
printf 'Restored database: %s\n' "$metadata_redis_db"
printf 'Restored key count: %s\n' "$restored_keys"
printf 'Pre-restore volume snapshot: %s\n' "$rollback_snapshot"
