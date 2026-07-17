#!/usr/bin/env bash
# shellcheck disable=SC2016 # GitHub/Compose/PowerShell expressions are literal test fixtures.

set -euo pipefail

readonly fork_repo="GentleKingson/fight-the-landlord"
readonly game_image="gentlekingson/fight-the-landlord"
readonly douzero_image="gentlekingson/fight-the-landlord-douzero"

fail() {
  printf 'release identity check failed: %s\n' "$*" >&2
  exit 1
}

require_literal() {
  local file="$1"
  local value="$2"
  grep -Fq -- "$value" "$file" || fail "$file does not contain required value: $value"
}

reject_regex() {
  local file="$1"
  local pattern="$2"
  if grep -Eiq -- "$pattern" "$file"; then
    grep -Ein -- "$pattern" "$file" >&2 || true
    fail "$file still contains an upstream deployment source"
  fi
}

require_literal README.md "https://github.com/${fork_repo}/actions/workflows/test.yml"
require_literal README.md "https://github.com/${fork_repo}/actions/workflows/release.yml"
require_literal README.md "https://raw.githubusercontent.com/${fork_repo}/main/install.sh"
require_literal README.md "https://raw.githubusercontent.com/${fork_repo}/main/install.ps1"
require_literal README.md "https://raw.githubusercontent.com/${fork_repo}/main/docker-compose.yml"
require_literal README.md "https://hub.docker.com/r/${game_image}"
require_literal README.md "https://github.com/palemoky/fight-the-landlord"

reject_regex README.md 'raw\.githubusercontent\.com/palemoky/fight-the-landlord'
reject_regex README.md 'github\.com/palemoky/fight-the-landlord/actions/'
reject_regex README.md 'hub\.docker\.com/r/palemoky/fight-the-landlord'
reject_regex README.md 'docker/image-size/palemoky/fight-the-landlord'

require_literal .env.example "GAME_IMAGE=${game_image}"
require_literal .env.example "DOUZERO_IMAGE=${douzero_image}"
require_literal docker-compose.yml '${GAME_IMAGE:-gentlekingson/fight-the-landlord}'
require_literal docker-compose.yml '${DOUZERO_IMAGE:-gentlekingson/fight-the-landlord-douzero}'

for installer in install.sh install.ps1; do
  require_literal "$installer" "$fork_repo"
  reject_regex "$installer" 'palemoky/fight-the-landlord'
done
require_literal install.sh 'CHECKSUM_URL="${DOWNLOAD_URL}.sha256"'
require_literal install.sh 'error "无法下载校验和文件: $CHECKSUM_URL"'
reject_regex install.sh '跳过校验'
require_literal install.ps1 '$checksumUrl = "$downloadUrl.sha256"'
require_literal install.ps1 'Get-FileHash -Algorithm SHA256'

require_literal internal/update/update.go "Repo         = \"${fork_repo}\""
reject_regex internal/update/update.go 'Repo[[:space:]]*=.*palemoky/fight-the-landlord'

require_literal douzero/README.md "docker build -t ${douzero_image}:latest"
reject_regex douzero/README.md 'docker build -t palemoky/fight-the-landlord'

require_literal .github/workflows/release.yml 'IMAGE_NAME: gentlekingson/${{ github.event.repository.name }}'
require_literal .github/workflows/release.yml 'images: ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}'
require_literal .github/workflows/release.yml 'images: ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}-douzero'
if [[ "$(grep -Fc 'type=raw,value=latest' .github/workflows/release.yml)" -ne 2 ]]; then
  fail "release workflow must publish latest for both semver-tagged images"
fi
reject_regex .github/workflows/release.yml 'type=raw,value=latest,enable=\{\{is_default_branch\}\}'
require_literal .github/workflows/release.yml 'GO_TRIXIE_DIGEST: sha256:'
require_literal .github/workflows/release.yml '"golang:${GO_VERSION}-trixie@${GO_TRIXIE_DIGEST}"'

printf 'Release identity checks passed for %s\n' "$fork_repo"
