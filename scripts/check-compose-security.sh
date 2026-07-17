#!/usr/bin/env bash

set -euo pipefail

command -v docker >/dev/null
command -v jq >/dev/null
command -v python3 >/dev/null

# Render every fixture from the checked-in Compose file and env example. Shell
# variables take precedence over --env-file, so remove every model variable
# before applying the few overrides a fixture intentionally exercises.
compose_variable_unsets=()
while read -r variable; do
  [[ "$variable" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || {
    echo >&2 "unexpected Compose variable name: $variable"
    exit 1
  }
  compose_variable_unsets+=(-u "$variable")
done < <(
  COMPOSE_PROJECT_NAME=compose-security-check \
    COMPOSE_PROFILES='' \
    docker compose \
      --file docker-compose.yml \
      --env-file .env.example \
      config --variables --format json | jq -r 'keys[]'
)

test "${#compose_variable_unsets[@]}" -gt 0

compose_security_password="compose-security-check-only"

render_compose_config() {
  local -a isolated_environment=(
    env
    "${compose_variable_unsets[@]}"
    -u COMPOSE_FILE
    -u COMPOSE_PROFILES
    -u COMPOSE_PROJECT_NAME
    -u COMPOSE_ENV_FILES
    -u COMPOSE_DISABLE_ENV_FILE
    COMPOSE_PROJECT_NAME=compose-security-check
    REDIS_PASSWORD="$compose_security_password"
  )

  while [[ "$#" -gt 0 && "$1" == *=* ]]; do
    isolated_environment+=("$1")
    shift
  done

  "${isolated_environment[@]}" \
    docker compose \
      --file docker-compose.yml \
      --env-file .env.example \
      "$@" \
      config --format json
}

default_config="$(render_compose_config)"

jq -e '
  (.services.redis.ports // []) == [] and
  (.services.redis.image | contains("@sha256:")) and
  .services["poker-server"].image == "gentlekingson/fight-the-landlord:v0.6.0-rc.1" and
  (.services | has("douzero") | not)
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
  .services.redis.restart == "unless-stopped" and
  .services["poker-server"].restart == "unless-stopped" and
  .services["poker-server"].depends_on.redis.condition == "service_healthy" and
  (.services.redis.healthcheck.test | join(" ") | contains("redis-cli ping")) and
  (.services.redis.volumes | any(.type == "volume" and .source == "redis-data" and .target == "/data")) and
  .volumes["redis-data"].driver == "local" and
  (.services.redis.command | join(" ") | contains("--appendonly yes"))
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
grep -Eq '^  host: "127\.0\.0\.1"$' config.yaml
grep -Eq '^  max_connections: 100$' config.yaml
grep -Eq '^  douzero_enabled: false$' config.yaml
if grep -Eq '^    - "\*"$' config.yaml; then
  echo >&2 "wildcard Origin found in shipped config.yaml"
  exit 1
fi
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

jq -e --arg redis_password "$compose_security_password" '
  .services["poker-server"].environment.SERVER_ENV == "production" and
  .services["poker-server"].environment.REDIS_PASSWORD == $redis_password and
  .services["poker-server"].environment.SERVER_MAX_CONNECTIONS == "100" and
  .services["poker-server"].environment.SECURITY_RATE_LIMIT_PER_SECOND == "10" and
  .services["poker-server"].environment.SECURITY_RATE_LIMIT_PER_MINUTE == "60" and
  .services["poker-server"].environment.SECURITY_MESSAGE_LIMIT_PER_SECOND == "20"
' <<<"$default_config" >/dev/null

load_config="$(
  render_compose_config \
    SERVER_MAX_CONNECTIONS=2000 \
    SECURITY_RATE_LIMIT_PER_SECOND=2000 \
    SECURITY_RATE_LIMIT_PER_MINUTE=100000 \
    SECURITY_MESSAGE_LIMIT_PER_SECOND=100
)"
jq -e '
  .services["poker-server"].environment.SERVER_MAX_CONNECTIONS == "2000" and
  .services["poker-server"].environment.SECURITY_RATE_LIMIT_PER_SECOND == "2000" and
  .services["poker-server"].environment.SECURITY_RATE_LIMIT_PER_MINUTE == "100000" and
  .services["poker-server"].environment.SECURITY_MESSAGE_LIMIT_PER_SECOND == "100"
' <<<"$load_config" >/dev/null

digest_config="$(
  render_compose_config \
    GAME_IMAGE_REF='gentlekingson/fight-the-landlord@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
    DOUZERO_IMAGE_REF='gentlekingson/fight-the-landlord-douzero@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' \
    --profile douzero
)"
jq -e '
  .services["poker-server"].image == "gentlekingson/fight-the-landlord@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" and
  .services.douzero.image == "gentlekingson/fight-the-landlord-douzero@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
' <<<"$digest_config" >/dev/null

debug_config="$(render_compose_config --profile redis-debug)"

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
