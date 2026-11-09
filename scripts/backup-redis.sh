#!/usr/bin/env bash
set -euo pipefail
umask 077

# Usage: backup-redis.sh /secure/backups/ddz.rdb [-h host] [-p port] [--tls ...]
# Supply authentication through REDISCLI_AUTH, never through a logged argument.
backup_path=${1:?provide a new backup file path}
shift
if [[ -e "$backup_path" || -e "$backup_path.sha256" ]]; then
  echo "Refusing to overwrite an existing backup" >&2
  exit 1
fi
backup_tmp=$(mktemp "${backup_path}.XXXXXX")
trap 'rm -f "$backup_tmp"' EXIT
redis-cli "$@" --rdb "$backup_tmp"
if command -v redis-check-rdb >/dev/null 2>&1; then
  redis-check-rdb "$backup_tmp"
fi
mv "$backup_tmp" "$backup_path"
sha256sum "$backup_path" > "$backup_path.sha256"
echo "Backup saved: $backup_path"
