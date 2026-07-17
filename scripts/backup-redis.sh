#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
invocation_dir="$(pwd -P)"

compose_file="$repo_root/docker-compose.yml"
env_file="$repo_root/.env"
project_name=""
output_dir="${REDIS_BACKUP_DIR:-}"
keep_count="${REDIS_BACKUP_KEEP:-}"
redis_db="${REDIS_DB:-}"
timeout_seconds=120
output_dir_from_cli=false
keep_count_from_cli=false
redis_db_from_cli=false

usage() {
  cat <<'EOF'
Usage: scripts/backup-redis.sh [options]

Create a checksummed Redis RDB archive from the Compose deployment.

Options:
  --compose-file PATH  Compose file (default: docker-compose.yml)
  --env-file PATH      Compose env file (default: .env)
  --project-name NAME  Compose project name
  --output-dir PATH    Backup directory (default: backups/redis)
  --keep COUNT         Number of standard archives to retain (default: 7)
  --redis-db NUMBER    Application Redis database used for key checks
  --timeout SECONDS    Health and BGSAVE timeout (default: 120)
  -h, --help           Show this help

REDIS_PASSWORD must be available to the existing Compose deployment. The
script reads it only through the container-mounted secret and never prints it.
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
    --output-dir)
      need_value "$@"
      output_dir="$2"
      output_dir_from_cli=true
      shift 2
      ;;
    --keep)
      need_value "$@"
      keep_count="$2"
      keep_count_from_cli=true
      shift 2
      ;;
    --redis-db)
      need_value "$@"
      redis_db="$2"
      redis_db_from_cli=true
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
    *)
      die "unknown argument: $1"
      ;;
  esac
done

absolute_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$invocation_dir" "$1" ;;
  esac
}

compose_file="$(absolute_path "$compose_file")"
env_file="$(absolute_path "$env_file")"

[[ -f "$compose_file" ]] || die "Compose file not found: $compose_file"
[[ -f "$env_file" ]] || die "env file not found: $env_file"

# Read only the named scalar instead of sourcing an untrusted dotenv file.
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

if [[ -z "$output_dir" && "$output_dir_from_cli" == false ]]; then
  output_dir="$(dotenv_value REDIS_BACKUP_DIR)"
fi
if [[ -z "$keep_count" && "$keep_count_from_cli" == false ]]; then
  keep_count="$(dotenv_value REDIS_BACKUP_KEEP)"
fi
if [[ -z "$redis_db" && "$redis_db_from_cli" == false ]]; then
  redis_db="$(dotenv_value REDIS_DB)"
fi

output_dir="${output_dir:-$repo_root/backups/redis}"
keep_count="${keep_count:-7}"
redis_db="${redis_db:-0}"
output_dir="$(absolute_path "$output_dir")"

[[ "$keep_count" =~ ^[1-9][0-9]*$ ]] || die "--keep must be a positive integer"
[[ "$redis_db" =~ ^[0-9]+$ ]] || die "--redis-db must be a non-negative integer"
[[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]] || die "--timeout must be a positive integer"

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

compose=(docker compose --file "$compose_file" --env-file "$env_file")
if [[ -n "$project_name" ]]; then
  compose+=(--project-name "$project_name")
fi

"${compose[@]}" config --quiet >/dev/null
"${compose[@]}" config --services | grep -Fxq redis || die "Compose service 'redis' was not found"

compose_project="$("${compose[@]}" config | awk '$1 == "name:" { print $2; exit }')"
[[ "$compose_project" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]] || die "could not determine a safe Compose project name"
lock_dir="/tmp/fight-landlord-redis-ops-${compose_project}.lock"
lock_token="${REDIS_OPS_LOCK_TOKEN:-}"
lock_owned=false

release_lock() {
  if [[ "$lock_owned" == true ]]; then
    rm -f "$lock_dir/token" "$lock_dir/owner"
    rmdir "$lock_dir" 2>/dev/null || true
    lock_owned=false
  fi
}

if [[ -n "$lock_token" && -f "$lock_dir/token" && "$(<"$lock_dir/token")" == "$lock_token" ]]; then
  : # Re-entrant call from restore-redis.sh while it owns the project lock.
elif mkdir "$lock_dir" 2>/dev/null; then
  lock_owned=true
  lock_token="$$-${RANDOM}-$(date +%s)"
  printf '%s\n' "$lock_token" >"$lock_dir/token"
  printf 'pid=%s\nstarted_at_utc=%s\n' "$$" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >"$lock_dir/owner"
else
  die "another Redis backup/restore is active for project $compose_project (lock: $lock_dir)"
fi
trap release_lock EXIT

redis_cid="$("${compose[@]}" ps -q redis)"
[[ -n "$redis_cid" ]] || die "Redis is not running"
[[ "$(docker inspect --format '{{.State.Running}}' "$redis_cid")" == true ]] || die "Redis is not running"

redis_cli() {
  # shellcheck disable=SC2016 # Expanded by the container shell, not this script.
  "${compose[@]}" exec -T redis sh -eu -c '
    password="$(cat /run/secrets/redis_password)"
    test -n "$password"
    export REDISCLI_AUTH="$password"
    exec redis-cli --raw "$@"
  ' sh "$@"
}

wait_for_health() {
  local deadline=$(( $(date +%s) + timeout_seconds ))
  local health pong

  while (( $(date +%s) < deadline )); do
    health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}' "$redis_cid" 2>/dev/null || true)"
    pong="$(redis_cli -n "$redis_db" PING 2>/dev/null || true)"
    if [[ "$health" == healthy && "$pong" == PONG ]]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_health || die "Redis did not become healthy within ${timeout_seconds}s"

# These are lower bounds taken before BGSAVE. Player statistics and the total
# leaderboard are durable application keys, so the subsequent RDB must contain
# at least this state even if the live service writes more while the save runs.
scan_count() {
  redis_cli -n "$redis_db" --scan --pattern "$1" | LC_ALL=C sort -u | awk 'END { print NR + 0 }'
}
min_player_stats_keys="$(scan_count 'player:stats:*')"
min_settlement_keys="$(scan_count 'leaderboard:settlement:*')"
require_leaderboard_total="$(redis_cli -n "$redis_db" EXISTS leaderboard:score)"

# Do not race a save already in progress. Then request a fresh server-side RDB
# snapshot and wait for Redis to report a successful completion.
deadline=$(( $(date +%s) + timeout_seconds ))
while :; do
  persistence_info="$(redis_cli INFO persistence | tr -d '\r')"
  if grep -q '^rdb_bgsave_in_progress:0$' <<<"$persistence_info" &&
     grep -q '^aof_rewrite_in_progress:0$' <<<"$persistence_info" &&
     grep -q '^aof_rewrite_scheduled:0$' <<<"$persistence_info"; then
    break
  fi
  (( $(date +%s) < deadline )) || die "timed out waiting for Redis persistence work"
  sleep 1
done

while :; do
  if bgsave_result="$(redis_cli BGSAVE 2>&1)"; then
    break
  fi
  case "$bgsave_result" in
    *"in progress"*|*"scheduled"*) ;;
    *) die "Redis rejected BGSAVE" ;;
  esac
  (( $(date +%s) < deadline )) || die "Redis did not accept BGSAVE within ${timeout_seconds}s"
  sleep 1
done

deadline=$(( $(date +%s) + timeout_seconds ))
while :; do
  persistence_info="$(redis_cli INFO persistence | tr -d '\r')"
  if grep -q '^rdb_bgsave_in_progress:0$' <<<"$persistence_info"; then
    grep -q '^rdb_last_bgsave_status:ok$' <<<"$persistence_info" || die "Redis reported a failed BGSAVE"
    break
  fi
  (( $(date +%s) < deadline )) || die "BGSAVE did not finish within ${timeout_seconds}s"
  sleep 1
done

mkdir -p "$output_dir"
chmod 700 "$output_dir"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/fight-landlord-redis-backup.XXXXXX")"
archive_partial=""
checksum_partial=""
published_checksum=""
cleanup() {
  rm -rf "$temp_dir"
  [[ -z "$archive_partial" ]] || rm -f "$archive_partial"
  [[ -z "$checksum_partial" ]] || rm -f "$checksum_partial"
  [[ -z "$published_checksum" ]] || rm -f "$published_checksum"
  release_lock
}
trap cleanup EXIT

docker cp "${redis_cid}:/data/dump.rdb" "$temp_dir/dump.rdb"
[[ -s "$temp_dir/dump.rdb" ]] || die "Redis snapshot /data/dump.rdb is empty"
chmod 600 "$temp_dir/dump.rdb"

redis_version="$(redis_cli INFO server | tr -d '\r' | awk -F: '$1 == "redis_version" { print $2; exit }')"
[[ -n "$redis_version" ]] || die "could not read the Redis version"
database_keys="$(redis_cli -n "$redis_db" DBSIZE)"

source_version="unknown"
if command -v git >/dev/null 2>&1 && git -C "$repo_root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  source_version="$(git -C "$repo_root" describe --tags --always --dirty 2>/dev/null || git -C "$repo_root" rev-parse HEAD)"
fi
redis_image="$(docker inspect --format '{{.Config.Image}}' "$redis_cid")"
game_image="not-running"
game_image_id="not-running"
game_cid="$("${compose[@]}" ps --all -q poker-server 2>/dev/null || true)"
if [[ -n "$game_cid" ]]; then
  game_image="$(docker inspect --format '{{.Config.Image}}' "$game_cid")"
  game_image_id="$(docker inspect --format '{{.Image}}' "$game_cid")"
fi
project_version="$game_image"
[[ "$project_version" != not-running ]] || project_version="$source_version"
rdb_sha256="$(sha256_file "$temp_dir/dump.rdb")"
created_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

cat >"$temp_dir/metadata.txt" <<EOF
FORMAT_VERSION=1
CREATED_AT_UTC=$created_at
PROJECT_VERSION=$project_version
SOURCE_VERSION=$source_version
GAME_IMAGE=$game_image
GAME_IMAGE_ID=$game_image_id
REDIS_IMAGE=$redis_image
REDIS_VERSION=$redis_version
REDIS_DB=$redis_db
DATABASE_KEYS=$database_keys
MIN_PLAYER_STATS_KEYS=$min_player_stats_keys
MIN_SETTLEMENT_KEYS=$min_settlement_keys
REQUIRE_LEADERBOARD_TOTAL=$require_leaderboard_total
RDB_SHA256=$rdb_sha256
EOF
chmod 600 "$temp_dir/metadata.txt"

timestamp="$(date -u '+%Y%m%dT%H%M%SZ')"
archive="$output_dir/redis-backup-${timestamp}-$$.tar.gz"
checksum_file="${archive}.sha256"
[[ ! -e "$archive" && ! -e "$checksum_file" ]] || die "backup path already exists"

archive_partial="${archive}.partial"
checksum_partial="${checksum_file}.partial"
tar -C "$temp_dir" -czf "$archive_partial" dump.rdb metadata.txt
chmod 600 "$archive_partial"
archive_sha256="$(sha256_file "$archive_partial")"
printf '%s  %s\n' "$archive_sha256" "$(basename "$archive")" >"$checksum_partial"
chmod 600 "$checksum_partial"
mv "$checksum_partial" "$checksum_file"
checksum_partial=""
published_checksum="$checksum_file"
mv "$archive_partial" "$archive"
archive_partial=""
published_checksum=""

# File names sort by UTC creation time, so deleting from the front preserves
# the newest requested number of standard archives. Pre-restore volume
# snapshots use another prefix and are intentionally not included.
backup_files=()
for candidate in "$output_dir"/redis-backup-*.tar.gz; do
  [[ -f "$candidate" && -f "${candidate}.sha256" ]] && backup_files+=("$candidate")
done
while (( ${#backup_files[@]} > keep_count )); do
  oldest="${backup_files[0]}"
  rm -f "$oldest" "${oldest}.sha256"
  backup_files=("${backup_files[@]:1}")
done

printf 'Redis backup completed.\n'
printf 'Archive: %s\n' "$archive"
printf 'Checksum: %s\n' "$checksum_file"
