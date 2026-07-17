#!/usr/bin/env bash

set -euo pipefail

command -v docker >/dev/null
command -v jq >/dev/null
command -v python3 >/dev/null

# This value is only used to render the Compose model. Production receives the
# real value from its secret manager when Compose creates redis_password.
export REDIS_PASSWORD="${REDIS_PASSWORD:-compose-security-check-only}"
export REDIS_DEBUG_PORT=6379

default_config="$(docker compose --env-file .env.example config --format json)"

jq -e '
  (.services.redis.ports // []) == [] and
  (.services.redis.image | contains("@sha256:")) and
  .services["poker-server"].image == "gentlekingson/fight-the-landlord:latest"
' <<<"$default_config" >/dev/null

jq -e '
  .services["poker-server"].ports == [{
    "mode": "ingress",
    "host_ip": "127.0.0.1",
    "target": 1780,
    "published": "1780",
    "protocol": "tcp"
  }] and
  (.services["poker-server"].healthcheck.test | join(" ") | contains("/readyz"))
' <<<"$default_config" >/dev/null

jq -e '
  .secrets.redis_password.environment == "REDIS_PASSWORD" and
  (.services.redis.secrets | any(.source == "redis_password")) and
  ((.services.redis.environment // {}) | has("REDIS_PASSWORD") | not) and
  (.services.redis.command | join(" ") | contains("--aclfile")) and
  (.services.redis.command | join(" ") | contains("chown redis:redis")) and
  (.services.redis.command | join(" ") | contains("/usr/local/bin/docker-entrypoint.sh redis-server"))
' <<<"$default_config" >/dev/null

grep -Eq '^ARG PYTHON_BUILDER_IMAGE=.*@sha256:[0-9a-f]{64}$' douzero/Dockerfile
grep -Eq '^ARG PYTHON_RUNTIME_IMAGE=.*@sha256:[0-9a-f]{64}$' douzero/Dockerfile
grep -Eq '^ARG UV_IMAGE=.*@sha256:[0-9a-f]{64}$' douzero/Dockerfile
grep -Eq '^ARG GO_DIGEST=sha256:[0-9a-f]{64}$' Dockerfile
grep -Eq '^ARG NODE_DIGEST=sha256:[0-9a-f]{64}$' Dockerfile
grep -Eq '^ARG RUNTIME_IMAGE=.*@sha256:[0-9a-f]{64}$' Dockerfile
grep -Fq 'http://127.0.0.1:1780/readyz' Dockerfile
grep -Eq '^  log_format: "json"$' config.yaml

# Model artifacts must come from an immutable Hub commit and every expected
# digest must remain an explicit SHA-256 value.
grep -Eq '^HF_REVISION = "[0-9a-f]{40}"$' douzero/model_assets.py
test "$(grep -Ec '^    "landlord(_down|_up)?": "[0-9a-f]{64}",$' douzero/model_assets.py)" -eq 3
if grep -R -n -E 'huggingface\.co/.*/resolve/(main|master)/' douzero/Dockerfile douzero/*.py; then
  echo >&2 "mutable Hugging Face model reference detected"
  exit 1
fi
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s douzero -p 'test_model_assets.py'

jq -e '
  .services["poker-server"].environment.SERVER_ENV == "production" and
  .services["poker-server"].environment.REDIS_PASSWORD == env.REDIS_PASSWORD
' <<<"$default_config" >/dev/null

digest_config="$(
  GAME_IMAGE_REF='gentlekingson/fight-the-landlord@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
  DOUZERO_IMAGE_REF='gentlekingson/fight-the-landlord-douzero@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' \
  docker compose --env-file .env.example --profile douzero config --format json
)"
jq -e '
  .services["poker-server"].image == "gentlekingson/fight-the-landlord@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" and
  .services.douzero.image == "gentlekingson/fight-the-landlord-douzero@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
' <<<"$digest_config" >/dev/null

debug_config="$(docker compose --env-file .env.example --profile redis-debug config --format json)"

jq -e '
  .services["redis-debug"].profiles == ["redis-debug"] and
  .services["redis-debug"].ports == [{
    "mode": "ingress",
    "host_ip": "127.0.0.1",
    "target": 6379,
    "published": "6379",
    "protocol": "tcp"
  }] and
  (.services["redis-debug"].labels["io.gentlekingson.fight-the-landlord.purpose"] |
    contains("DO NOT USE IN PRODUCTION"))
' <<<"$debug_config" >/dev/null

echo "Compose deployment security checks passed"
