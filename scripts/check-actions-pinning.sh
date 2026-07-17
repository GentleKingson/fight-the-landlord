#!/usr/bin/env bash

set -euo pipefail

failed=0

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

check_reference() {
  local file="$1"
  local line_number="$2"
  local raw="$3"
  local value comment=""

  if [[ "$raw" == *#* ]]; then
    value="${raw%%#*}"
    comment="${raw#*#}"
  else
    value="$raw"
  fi

  value="$(trim "$value")"
  comment="$(trim "$comment")"

  if [[ ( "$value" == \"*\" && "$value" == *\" ) || ( "$value" == \'*\' && "$value" == *\' ) ]]; then
    value="${value:1:${#value}-2}"
  fi

  if [[ "$value" == ./* ]]; then
    return
  fi

  if [[ "$value" == docker://* ]]; then
    if [[ ! "$value" =~ ^docker://[^@[:space:]]+@sha256:[0-9a-fA-F]{64}$ ]]; then
      printf '%s:%s: Docker action must use an immutable sha256 digest: %s\n' \
        "$file" "$line_number" "$value" >&2
      failed=1
    fi
    return
  fi

  if [[ ! "$value" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[^@[:space:]]+)?@[0-9a-fA-F]{40}$ ]]; then
    printf '%s:%s: action must be pinned to a full 40-character commit SHA: %s\n' \
      "$file" "$line_number" "$value" >&2
    failed=1
    return
  fi

  if [[ ! "$comment" =~ ^v[0-9] ]]; then
    printf '%s:%s: pinned action must retain a readable version comment: %s\n' \
      "$file" "$line_number" "$value" >&2
    failed=1
  fi
}

main() {
  local workflow_dir="${1:-.github/workflows}"
  local found=0
  local file record line_number raw

  if [[ ! -d "$workflow_dir" ]]; then
    printf 'Workflow directory does not exist: %s\n' "$workflow_dir" >&2
    exit 1
  fi

  while IFS= read -r file; do
    while IFS= read -r record; do
      found=1
      line_number="${record%%:*}"
      raw="${record#*:}"
      raw="${raw#*:}"
      check_reference "$file" "$line_number" "$raw"
    done < <(grep -nE "^[[:space:]]*(-[[:space:]]*)?([\"']?uses[\"']?)[[:space:]]*:" "$file" || true)
  done < <(find "$workflow_dir" -type f \( -name '*.yml' -o -name '*.yaml' \) -print | sort)

  if [[ "$found" -eq 0 ]]; then
    printf 'No GitHub Actions references found under %s\n' "$workflow_dir" >&2
    exit 1
  fi

  if [[ "$failed" -ne 0 ]]; then
    exit 1
  fi

  printf 'GitHub Actions pinning checks passed\n'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
