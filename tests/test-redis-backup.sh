#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
project_name="redis-backup-test-$$"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/fight-landlord-redis-backup-test.XXXXXX")"
lock_dir="/tmp/fight-landlord-redis-ops-${project_name}.lock"
export REDIS_PASSWORD="integration-${project_name}-not-a-production-secret"

compose=(docker compose --project-name "$project_name" --file "$repo_root/docker-compose.yml" --env-file "$repo_root/.env.example")

cleanup() {
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$lock_dir"
  rm -rf "$temp_dir"
}
trap cleanup EXIT

redis_cli() {
  # shellcheck disable=SC2016 # Expanded by the container shell.
  "${compose[@]}" exec -T redis sh -eu -c '
    export REDISCLI_AUTH="$(cat /run/secrets/redis_password)"
    exec redis-cli --raw "$@"
  ' sh "$@"
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print tolower($1)}'
  else
    shasum -a 256 "$1" | awk '{print tolower($1)}'
  fi
}

"${compose[@]}" up -d --wait redis
initial_redis_cid="$("${compose[@]}" ps -q redis)"
redis_cli SET player:stats:backup-test original >/dev/null
redis_cli ZADD leaderboard:score 42 backup-test >/dev/null
redis_cli SADD leaderboard:settlement:game-1 backup-test >/dev/null

mkdir "$lock_dir"
printf 'held-by-test\n' >"$lock_dir/token"
if "$repo_root/scripts/backup-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  --output-dir "$temp_dir/backups" >"$temp_dir/locked.out" 2>&1; then
  echo "backup unexpectedly bypassed the project operation lock" >&2
  exit 1
fi
grep -q 'another Redis backup/restore is active' "$temp_dir/locked.out"
rm -rf "$lock_dir"

"$repo_root/scripts/backup-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  --output-dir "$temp_dir/backups" \
  --keep 2

archive="$(find "$temp_dir/backups" -maxdepth 1 -name 'redis-backup-*.tar.gz' -print | head -n 1)"
[[ -n "$archive" && -f "${archive}.sha256" ]]
tar -xOzf "$archive" metadata.txt | grep -qx 'MIN_SETTLEMENT_KEYS=0'
tar -xOzf "$archive" metadata.txt | grep -qx 'SETTLEMENT_KEYS_AT_BACKUP=1'

if "$repo_root/scripts/restore-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  "$archive" >"$temp_dir/unconfirmed.out" 2>&1; then
  echo "restore unexpectedly accepted a missing confirmation flag" >&2
  exit 1
fi
grep -q 'restore requires --confirm-restore' "$temp_dir/unconfirmed.out"

if "$repo_root/scripts/restore-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  --backup-dir "$temp_dir/backups" \
  --confirm-restore "$archive" >"$temp_dir/running.out" 2>&1; then
  echo "restore unexpectedly accepted a running Redis" >&2
  exit 1
fi
grep -q 'Redis, redis-debug, or poker-server is running' "$temp_dir/running.out"

redis_cli SET player:stats:backup-test changed >/dev/null
redis_cli SET should-disappear yes >/dev/null
"${compose[@]}" stop redis

cp "$archive" "$temp_dir/tampered.tar.gz"
printf '%064d  tampered.tar.gz\n' 0 >"$temp_dir/tampered.tar.gz.sha256"
if "$repo_root/scripts/restore-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  --backup-dir "$temp_dir/backups" \
  --confirm-restore "$temp_dir/tampered.tar.gz" >"$temp_dir/tampered.out" 2>&1; then
  echo "restore unexpectedly accepted a bad checksum" >&2
  exit 1
fi
grep -q 'SHA-256 verification failed' "$temp_dir/tampered.out"

if REDIS_DB=1 "$repo_root/scripts/restore-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  --backup-dir "$temp_dir/backups" \
  --confirm-restore "$archive" >"$temp_dir/db-mismatch.out" 2>&1; then
  echo "restore unexpectedly accepted a mismatched target Redis DB" >&2
  exit 1
fi
grep -q 'does not match target Redis DB' "$temp_dir/db-mismatch.out"

# A structurally valid archive with an impossible critical-key lower bound
# forces a post-start failure and exercises the automatic raw-volume rollback.
mkdir "$temp_dir/rollback-fixture"
tar -xzf "$archive" -C "$temp_dir/rollback-fixture"
awk '
  /^MIN_PLAYER_STATS_KEYS=/ { print "MIN_PLAYER_STATS_KEYS=999"; next }
  { print }
' "$temp_dir/rollback-fixture/metadata.txt" >"$temp_dir/rollback-fixture/metadata.new"
mv "$temp_dir/rollback-fixture/metadata.new" "$temp_dir/rollback-fixture/metadata.txt"
rollback_fixture="$temp_dir/rollback-check.tar.gz"
tar -C "$temp_dir/rollback-fixture" -czf "$rollback_fixture" dump.rdb metadata.txt
printf '%s  %s\n' "$(sha256_file "$rollback_fixture")" "$(basename "$rollback_fixture")" >"${rollback_fixture}.sha256"

if "$repo_root/scripts/restore-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  --backup-dir "$temp_dir/backups" \
  --confirm-restore "$rollback_fixture" >"$temp_dir/rollback.out" 2>&1; then
  echo "restore unexpectedly passed the forced critical-key failure" >&2
  exit 1
fi
grep -q 'Rollback completed' "$temp_dir/rollback.out"

"${compose[@]}" up -d --wait redis
rollback_redis_cid="$("${compose[@]}" ps -q redis)"
[[ -n "$rollback_redis_cid" && "$rollback_redis_cid" != "$initial_redis_cid" ]]
[[ "$(redis_cli GET player:stats:backup-test)" == changed ]]
[[ "$(redis_cli EXISTS should-disappear)" == 1 ]]
"${compose[@]}" stop redis

"$repo_root/scripts/restore-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  --backup-dir "$temp_dir/backups" \
  --confirm-restore "$archive"

[[ "$(redis_cli GET player:stats:backup-test)" == original ]]
[[ "$(redis_cli EXISTS leaderboard:score)" == 1 ]]
[[ "$(redis_cli SISMEMBER leaderboard:settlement:game-1 backup-test)" == 1 ]]
[[ "$(redis_cli EXISTS should-disappear)" == 0 ]]
find "$temp_dir/backups" -maxdepth 1 -name 'pre-restore-redis-volume-*.tar.gz' -print -quit | grep -q .

# Exercise the explicit running-service path and the re-entrant project lock
# used by its automatic online pre-backup.
"$repo_root/scripts/restore-redis.sh" \
  --compose-file "$repo_root/docker-compose.yml" \
  --env-file "$repo_root/.env.example" \
  --project-name "$project_name" \
  --backup-dir "$temp_dir/backups" \
  --stop-running \
  --confirm-restore "$archive"
[[ "$(redis_cli GET player:stats:backup-test)" == original ]]

printf 'Redis backup/restore integration test passed.\n'
