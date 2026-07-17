#!/usr/bin/env bash

set -euo pipefail

command -v docker >/dev/null
command -v jq >/dev/null

# This value is only used to render the Compose model. Production receives the
# real value from its secret manager when Compose creates redis_password.
export REDIS_PASSWORD="${REDIS_PASSWORD:-compose-security-check-only}"
export REDIS_DEBUG_PORT=6379

default_config="$(docker compose --env-file .env.example config --format json)"

jq -e '
  (.services.redis.ports // []) == [] and
  (.services.redis.image | contains("@sha256:"))
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

jq -e '
  .services["poker-server"].environment.SERVER_ENV == "production" and
  .services["poker-server"].environment.REDIS_PASSWORD == env.REDIS_PASSWORD
' <<<"$default_config" >/dev/null

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
