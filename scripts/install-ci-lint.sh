#!/usr/bin/env bash
set -euo pipefail

# Build the pinned tool through Go's module proxy/checksum verification instead
# of downloading a GitHub Release on every ephemeral runner.
version="${1:?usage: install-ci-lint.sh vX.Y.Z /absolute/install/directory}"
install_dir="${2:?an absolute installation directory is required}"
module=github.com/golangci/golangci-lint/v2
package="$module/cmd/golangci-lint"

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ || "$install_dir" != /* ]]; then
  echo 'Expected an exact vX.Y.Z version and an absolute installation directory' >&2
  exit 1
fi

valid_binary() {
  local binary="$1"
  [[ -x "$binary" ]] || return 1
  # Source builds need not embed a release version in their human-readable
  # output. Go build metadata records the actual module and version used.
  go version -m "$binary" | awk -v module="$module" -v package="$package" -v version="$version" '
    $1 == "path" && $2 == package { command_matches = 1 }
    $1 == "mod" && $2 == module && $3 == version { version_matches = 1 }
    END { exit !(command_matches && version_matches) }
  ' || return 1
  "$binary" version >/dev/null 2>&1
}

mkdir -p "$install_dir"
if valid_binary "$install_dir/golangci-lint"; then
  echo "Using cached golangci-lint $version"
else
  # Only publish a complete, verified installation. Never cache partial output.
  staging_dir="$(mktemp -d "$install_dir/.install.XXXXXX")"
  trap 'rm -rf "$staging_dir"' EXIT
  installed=false
  for attempt in 1 2 3; do
    echo "Installing golangci-lint $version through Go modules (attempt $attempt/3)"
    if GOBIN="$staging_dir" GOWORK=off go install "$package@$version"; then
      installed=true
      break
    fi
    if [[ "$attempt" -lt 3 ]]; then
      sleep "$((attempt * 5))"
    fi
  done
  if [[ "$installed" != true ]] || ! valid_binary "$staging_dir/golangci-lint"; then
    echo "Failed to install and verify golangci-lint $version" >&2
    exit 1
  fi
  mv "$staging_dir/golangci-lint" "$install_dir/golangci-lint"
fi

if [[ -n "${GITHUB_PATH:-}" ]]; then
  printf '%s\n' "$install_dir" >> "$GITHUB_PATH"
fi
"$install_dir/golangci-lint" version
