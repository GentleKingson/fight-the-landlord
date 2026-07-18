#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

usage() {
  cat <<'USAGE'
Usage: ./scripts/run-public-test-smoke.sh [smoke|public-test] [options]

Presets:
  smoke       10 minutes (default)
  public-test 1 hour

Options:
  --duration DURATION       Override the preset duration (for example 30s)
  --players COUNT           Simulated players, a multiple of 3 from 3 to 48
  --disconnect-rate RATE    Reconnect probability per player action, 0 through 1
  --douzero true|false      Record whether the target has DouZero enabled
  --help                    Show this help

Target and report paths are configured with PUBLIC_TEST_URL,
PUBLIC_TEST_METRICS_URL, and PUBLIC_TEST_OUTPUT_DIR.
USAGE
}

preset="smoke"
duration=""
players="18"
disconnect_rate="0.02"
douzero="false"

if [[ ${1:-} != "" && ${1:-} != --* ]]; then
  preset="$1"
  shift
fi

while (($#)); do
  case "$1" in
    --duration)
      [[ $# -ge 2 ]] || { echo "ERROR: --duration requires a value" >&2; exit 2; }
      duration="$2"
      shift 2
      ;;
    --players)
      [[ $# -ge 2 ]] || { echo "ERROR: --players requires a value" >&2; exit 2; }
      players="$2"
      shift 2
      ;;
    --disconnect-rate)
      [[ $# -ge 2 ]] || { echo "ERROR: --disconnect-rate requires a value" >&2; exit 2; }
      disconnect_rate="$2"
      shift 2
      ;;
    --douzero)
      [[ $# -ge 2 ]] || { echo "ERROR: --douzero requires true or false" >&2; exit 2; }
      douzero="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "ERROR: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$preset" in
  smoke)
    preset_duration="10m"
    ;;
  public-test)
    preset_duration="1h"
    ;;
  *)
    echo "ERROR: preset must be exactly smoke or public-test" >&2
    exit 2
    ;;
esac

[[ "$players" =~ ^[0-9]+$ ]] || { echo "ERROR: --players must be an integer" >&2; exit 2; }
if ((players < 3 || players > 48 || players % 3 != 0)); then
  echo "ERROR: --players must be a multiple of 3 from 3 to 48" >&2
  exit 2
fi
case "$douzero" in
  true|false) ;;
  *)
    echo "ERROR: --douzero must be true or false" >&2
    exit 2
    ;;
esac

duration="${duration:-$preset_duration}"
output_dir="${PUBLIC_TEST_OUTPUT_DIR:-artifacts/public-test}"
json_out="${PUBLIC_TEST_JSON_OUT:-$output_dir/$preset-report.json}"
markdown_out="${PUBLIC_TEST_MARKDOWN_OUT:-$output_dir/$preset-report.md}"
mkdir -p "$(dirname "$json_out")" "$(dirname "$markdown_out")"

args=(
  --preset "$preset"
  --url "${PUBLIC_TEST_URL:-ws://127.0.0.1:1780/ws}"
  --metrics-url "${PUBLIC_TEST_METRICS_URL:-auto}"
  --duration "$duration"
  --players "$players"
  --disconnect-rate "$disconnect_rate"
  --douzero "$douzero"
  --operation-timeout "${PUBLIC_TEST_OPERATION_TIMEOUT:-10s}"
  --cooldown "${PUBLIC_TEST_COOLDOWN:-35s}"
  --metrics-interval "${PUBLIC_TEST_METRICS_INTERVAL:-1s}"
  --client-version "${PUBLIC_TEST_CLIENT_VERSION:-public-test-smoke}"
  --seed "${PUBLIC_TEST_SEED:-1}"
  --json-out "$json_out"
  --markdown-out "$markdown_out"
)

binary="$(mktemp "${TMPDIR:-/tmp}/fight-landlord-public-test.XXXXXX")"
trap 'rm -f "$binary"' EXIT
go build -o "$binary" ./tests/publictest
"$binary" "${args[@]}"
