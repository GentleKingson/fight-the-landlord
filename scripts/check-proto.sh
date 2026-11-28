#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [[ ${1:-} == --if-changed ]]; then
  paths=(internal/protocol/proto internal/protocol/pb internal/protocol/convert/msgtype
    web/scripts web/package.json web/package-lock.json web/src/protocol/proto.generated.ts
    go.mod go.sum Makefile scripts/check-proto.sh .agents/setup)
  if base=$(git merge-base HEAD "${CHECK_BASE:-origin/main}" 2>/dev/null) &&
    git diff --quiet "$base" -- "${paths[@]}" &&
    [[ -z $(git ls-files --others --exclude-standard -- "${paths[@]}") ]]; then
    echo 'Protocol inputs and outputs unchanged; skipping regeneration check.'
    exit 0
  fi
elif (($#)); then
  echo 'Usage: check-proto.sh [--if-changed]' >&2
  exit 2
fi

for tool in go gofmt protoc protoc-gen-go npm; do
  if ! command -v "$tool" >/dev/null; then
    echo "Missing $tool; install protocol tools first (or run .agents/setup in an orb)." >&2
    exit 1
  fi
done
if [[ ! -x web/node_modules/.bin/pbjs ]]; then
  echo 'Missing pbjs; run npm --prefix web ci first.' >&2
  exit 1
fi

# Reuse make proto in an isolated tree, comparing to the working files, not HEAD.
mkdir -p .amp/in/orb-backup
scratch=$(mktemp -d "$PWD/.amp/in/orb-backup/check-proto.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/internal/protocol/convert/msgtype" "$scratch/web/src/protocol"
cp Makefile go.mod go.sum "$scratch/"
cp -R internal/protocol/proto "$scratch/internal/protocol/"
cp internal/protocol/convert/msgtype/gen.go "$scratch/internal/protocol/convert/msgtype/"
cp web/package.json "$scratch/web/"
cp -R web/scripts "$scratch/web/"
ln -s "$PWD/web/node_modules" "$scratch/web/node_modules"
make --no-print-directory -s -C "$scratch" proto

drift=0
diff -ru internal/protocol/pb "$scratch/internal/protocol/pb" || drift=1
diff -u internal/protocol/convert/msgtype/mapping.go "$scratch/internal/protocol/convert/msgtype/mapping.go" || drift=1
diff -u web/src/protocol/proto.generated.ts "$scratch/web/src/protocol/proto.generated.ts" || drift=1
if ((drift)); then
  echo 'Protocol generation drift detected. Run make proto and review the generated changes.' >&2
  exit 1
fi
echo 'Go protobuf, message mapping, and TS protocol files are synchronized.'
