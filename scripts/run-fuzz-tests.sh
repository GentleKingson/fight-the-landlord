#!/usr/bin/env bash

set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

fuzz_time="${FUZZ_TIME:-3s}"
fuzz_timeout="${FUZZ_TIMEOUT:-10m}"
fuzz_parallel="${FUZZ_PARALLEL:-1}"

duration_pattern='^[1-9][0-9]*(ms|s|m|h)$'
[[ "$fuzz_time" =~ $duration_pattern ]] || {
  printf 'FUZZ_TIME must be a positive Go duration such as 3s or 1m: %s\n' "$fuzz_time" >&2
  exit 2
}
[[ "$fuzz_timeout" =~ $duration_pattern ]] || {
  printf 'FUZZ_TIMEOUT must be a positive Go duration such as 10m: %s\n' "$fuzz_timeout" >&2
  exit 2
}
[[ "$fuzz_parallel" =~ ^[1-9][0-9]*$ ]] || {
  printf 'FUZZ_PARALLEL must be a positive integer: %s\n' "$fuzz_parallel" >&2
  exit 2
}

packages=(
  ./internal/config
  ./internal/protocol/codec
  ./internal/protocol/convert/payload
  ./internal/game/card
  ./internal/game/rule
  ./internal/server/session
  ./internal/server
)
required_targets=(
  "./internal/config:FuzzEnvironmentValueParsing"
  "./internal/protocol/codec:FuzzMessageCodecRoundTrip"
  "./internal/protocol/convert/payload:FuzzPlayCardsPayloadRoundTrip"
  "./internal/game/card:FuzzRankFromChar"
  "./internal/game/card:FuzzFindCardsInHand"
  "./internal/game/rule:FuzzLegalResponseProperties"
  "./internal/game/rule:FuzzParseHand"
  "./internal/server/session:FuzzReconnectCredentialInput"
  "./internal/server:FuzzClientIncomingFrame"
  "./internal/server:FuzzWebSessionOriginAndTrustedProxy"
  "./internal/server:FuzzSessionRevokeJSON"
)
if (( $# > 0 )); then
  packages=("$@")
fi

target_count=0
discovered_targets=()
for package in "${packages[@]}"; do
  listing="$(go test -tags=ci -run='^$' -list='^Fuzz[A-Za-z0-9_]+$' "$package")"
  while IFS= read -r target; do
    [[ "$target" =~ ^Fuzz[A-Za-z0-9_]+$ ]] || continue
    target_count=$((target_count + 1))
    discovered_targets+=("${package}:${target}")
  done <<< "$listing"
done

if (( target_count == 0 )); then
  printf 'No fuzz targets discovered\n' >&2
  exit 1
fi

manifest_failed=0
for required in "${required_targets[@]}"; do
  matches=0
  for discovered in "${discovered_targets[@]}"; do
    if [[ "$discovered" == "$required" ]]; then
      matches=$((matches + 1))
    fi
  done
  if (( matches != 1 )); then
    printf 'Required fuzz target must be discovered exactly once: %s (found %d)\n' \
      "$required" "$matches" >&2
    manifest_failed=1
  fi
done
if (( manifest_failed != 0 )); then
  exit 1
fi

for discovered in "${discovered_targets[@]}"; do
  package="${discovered%%:*}"
  target="${discovered#*:}"
  printf '\n==> fuzz %s %s for %s\n' "$package" "$target" "$fuzz_time"
  go test -tags=ci -run='^$' -fuzz="^${target}$" -fuzztime="$fuzz_time" \
    -parallel="$fuzz_parallel" -timeout="$fuzz_timeout" "$package"
done

printf '\nCompleted %d fuzz targets\n' "$target_count"
