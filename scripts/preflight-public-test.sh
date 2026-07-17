#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"

usage() {
  cat <<'EOF'
Usage: ./scripts/preflight-public-test.sh --env-file PATH [options]

Validate the effective Docker Compose configuration before a small public test.

Options:
  --env-file PATH       Production environment file (required)
  --compose-file PATH   Compose file; may be repeated for overrides
                        (default: docker-compose.yml in the repository root)
  --profile NAME        Enable a Compose profile while validating; may be repeated
  -h, --help            Show this help
EOF
}

error_and_exit() {
  printf 'ERROR [%s] %s\n' "$1" "$2"
  printf 'ERROR preflight failed with 1 error(s)\n'
  exit 1
}

env_file=""
compose_files=()
profiles=()

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --env-file)
      [[ "$#" -ge 2 ]] || error_and_exit usage "--env-file requires a path"
      env_file="$2"
      shift 2
      ;;
    --compose-file|-f)
      [[ "$#" -ge 2 ]] || error_and_exit usage "--compose-file requires a path"
      compose_files+=("$2")
      shift 2
      ;;
    --profile)
      [[ "$#" -ge 2 ]] || error_and_exit usage "--profile requires a name"
      profiles+=("$2")
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      error_and_exit usage "unknown argument: $1"
      ;;
  esac
done

[[ -n "$env_file" ]] || error_and_exit env_file "--env-file is required"
[[ -f "$env_file" ]] || error_and_exit env_file "environment file does not exist: $env_file"
printf 'PASS [env_file] environment file exists\n'

if [[ "${#compose_files[@]}" -eq 0 ]]; then
  compose_files+=("$repo_root/docker-compose.yml")
fi

for compose_file in "${compose_files[@]}"; do
  [[ -f "$compose_file" ]] || error_and_exit compose_file "Compose file does not exist: $compose_file"
done
printf 'PASS [compose_file] Compose file set exists\n'

command -v docker >/dev/null 2>&1 || error_and_exit dependency "docker is required"
docker compose version >/dev/null 2>&1 || error_and_exit dependency "Docker Compose v2 is required"
command -v python3 >/dev/null 2>&1 || error_and_exit dependency "python3 is required"

umask 077
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/public-test-preflight.XXXXXX")"
trap 'rm -rf "$temp_dir"' EXIT

compose_base=(docker compose)
for compose_file in "${compose_files[@]}"; do
  compose_base+=(--file "$compose_file")
done
compose_base+=(--env-file "$env_file")

compose_active=("${compose_base[@]}")
if [[ "${#profiles[@]}" -gt 0 ]]; then
  for profile in "${profiles[@]}"; do
    compose_active+=(--profile "$profile")
  done
fi

render_or_fail() {
  local output_file="$1"
  local error_code="$2"
  shift 2

  if ! "$@" >"$output_file" 2>"$temp_dir/compose.stderr"; then
    # Compose diagnostics can contain interpolated secret values. Keep the
    # public preflight output useful without echoing those diagnostics.
    error_and_exit "$error_code" "Docker Compose could not render this configuration"
  fi
}

render_or_fail \
  "$temp_dir/environment" \
  compose_environment \
  "${compose_base[@]}" config --environment

render_or_fail \
  "$temp_dir/active.json" \
  compose_render \
  "${compose_active[@]}" config --format json

# Inspect inactive profile services for static hazards such as published Redis
# or DouZero ports, host networking, and privileged containers.
render_or_fail \
  "$temp_dir/all.json" \
  compose_render \
  "${compose_base[@]}" --profile '*' config --format json

# The uninterpolated model lets the validator distinguish a secret reference
# from a literal password without ever printing the effective password.
render_or_fail \
  "$temp_dir/raw.json" \
  compose_render \
  "${compose_base[@]}" --profile '*' config --no-interpolate --format json

if python3 - \
  "$temp_dir/active.json" \
  "$temp_dir/all.json" \
  "$temp_dir/raw.json" \
  "$temp_dir/environment" <<'PY'
import ipaddress
import json
import re
import sys
from pathlib import Path
from urllib.parse import urlsplit


active_path, all_path, raw_path, environment_path = map(Path, sys.argv[1:])
active = json.loads(active_path.read_text(encoding="utf-8"))
all_profiles = json.loads(all_path.read_text(encoding="utf-8"))
raw = json.loads(raw_path.read_text(encoding="utf-8"))

effective_environment = {}
for line in environment_path.read_text(encoding="utf-8").splitlines():
    key, separator, value = line.partition("=")
    if separator and key:
        effective_environment[key] = value

errors = 0
warnings = 0


def emit(level, code, message):
    global errors, warnings
    if level == "ERROR":
        errors += 1
    elif level == "WARNING":
        warnings += 1
    print(f"{level} [{code}] {message}")


def passed(code, message):
    emit("PASS", code, message)


def error(code, message):
    emit("ERROR", code, message)


def warning(code, message):
    emit("WARNING", code, message)


def services(model):
    value = model.get("services", {})
    return value if isinstance(value, dict) else {}


def service_environment(service):
    value = service.get("environment", {}) if isinstance(service, dict) else {}
    if isinstance(value, dict):
        return {str(key): "" if item is None else str(item) for key, item in value.items()}
    if isinstance(value, list):
        result = {}
        for item in value:
            key, separator, item_value = str(item).partition("=")
            result[key] = item_value if separator else ""
        return result
    return {}


def find_game_service(model):
    model_services = services(model)
    if "poker-server" in model_services:
        return "poker-server", model_services["poker-server"]
    candidates = []
    for name, service in model_services.items():
        environment = service_environment(service)
        if "SERVER_ENV" in environment and "SECURITY_ALLOWED_ORIGINS" in environment:
            candidates.append((name, service))
    if len(candidates) == 1:
        return candidates[0]
    return None, None


def find_named_service(model, preferred_name, image_prefix):
    model_services = services(model)
    if preferred_name in model_services:
        return preferred_name, model_services[preferred_name]
    candidates = []
    for name, service in model_services.items():
        image = str(service.get("image", ""))
        image_name = image.rsplit("/", 1)[-1].lower()
        if preferred_name in name.lower() or image_name.startswith(image_prefix):
            candidates.append((name, service))
    if len(candidates) == 1:
        return candidates[0]
    return None, None


def service_ports(service):
    value = service.get("ports", []) if isinstance(service, dict) else []
    return value if isinstance(value, list) else []


def port_host(port):
    if isinstance(port, dict):
        return str(port.get("host_ip", ""))
    text = str(port)
    if text.startswith("[") and "]" in text:
        return text[1:text.index("]")]
    pieces = text.split(":")
    return pieces[0] if len(pieces) >= 3 else ""


def is_loopback_host(host):
    host = host.strip().strip("[]")
    if host.lower() == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def has_public_port(service):
    return any(not is_loopback_host(port_host(port)) for port in service_ports(service))


def parse_boolean(value):
    normalized = str(value).strip().lower()
    if normalized in {"1", "true", "yes", "on"}:
        return True
    if normalized in {"0", "false", "no", "off"}:
        return False
    return None


def check_positive_integer(environment, key, code, upper_bound=None):
    raw_value = environment.get(key, "")
    try:
        value = int(raw_value)
    except (TypeError, ValueError):
        error(code, f"{key} must be an integer")
        return
    if value <= 0:
        error(code, f"{key} must be greater than zero")
    elif upper_bound is not None and value > upper_bound:
        error(code, f"{key} must not exceed {upper_bound} for a small public test")
    else:
        passed(code, f"{key} is enabled and within the public-test range")


def image_has_fixed_reference(image):
    image = str(image).strip()
    if re.search(r"@sha256:[0-9a-fA-F]{64}$", image):
        return True
    final_component = image.rsplit("/", 1)[-1]
    if ":" not in final_component:
        return False
    tag = final_component.rsplit(":", 1)[-1].lower()
    return bool(tag) and tag != "latest"


game_name, game = find_game_service(active)
if game is None:
    error("game_service", "could not identify the game service in the active Compose model")
    game_environment = {}
else:
    passed("game_service", f"identified game service {game_name}")
    game_environment = service_environment(game)

if game_environment.get("SERVER_ENV", "").strip().lower() == "production":
    passed("server_env", "SERVER_ENV is production")
else:
    error("server_env", "SERVER_ENV must be production")

admin_key = game_environment.get("ADMIN_KEY")
if admin_key is None:
    admin_key = effective_environment.get("ADMIN_KEY", "")

admin_key_length = len(admin_key.encode("utf-8"))
if not admin_key:
    error("admin_key_empty", "ADMIN_KEY must be non-empty in production")
elif admin_key != admin_key.strip() or any(char in admin_key for char in "\x00\r\n"):
    error("admin_key_format", "ADMIN_KEY must not contain surrounding whitespace or control characters")
elif admin_key_length < 32:
    error("admin_key_short", "ADMIN_KEY must be at least 32 bytes")
elif admin_key_length > 1024:
    error("admin_key_long", "ADMIN_KEY must not exceed 1024 bytes")
else:
    passed("admin_key", "ADMIN_KEY meets the production length and format requirements")

password = game_environment.get("REDIS_PASSWORD")
if password is None:
    password = effective_environment.get("REDIS_PASSWORD", "")

normalized_password = password.strip().lower()
example_passwords = {
    "changeme",
    "change-me",
    "change_me",
    "example",
    "example-password",
    "password",
    "redis-password",
    "replace-me",
    "secret",
    "test",
    "your-password",
    "your-secure-password",
}
looks_like_example = (
    normalized_password in example_passwords
    or normalized_password.startswith(("example-", "replace-", "your-"))
    or normalized_password.startswith("<")
    or normalized_password.endswith(">")
)
if not password or not password.strip():
    error("redis_password_empty", "REDIS_PASSWORD must be non-empty")
elif looks_like_example:
    error("redis_password_example", "REDIS_PASSWORD must not use an example or placeholder value")
else:
    passed("redis_password", "REDIS_PASSWORD is non-empty and is not a known example value")

# Reject literal Redis passwords in service environment or redis-server command
# fields. Values are deliberately not included in diagnostics.
hardcoded_password = False
for service in services(raw).values():
    for key, value in service_environment(service).items():
        if key.upper() == "REDIS_PASSWORD" and value and "$" not in value:
            hardcoded_password = True
    command = service.get("command", []) if isinstance(service, dict) else []
    command_text = " ".join(map(str, command)) if isinstance(command, list) else str(command)
    requirepass_pattern = re.compile(
        r"(?:^|\s)(?:--)?requirepass(?:=|\s+)(\"[^\"]*\"|'[^']*'|\S+)",
        re.IGNORECASE,
    )
    for match in requirepass_pattern.finditer(command_text):
        password_argument = match.group(1).strip("\"'")
        if "$" not in password_argument and "/run/secrets/" not in password_argument:
            hardcoded_password = True

if hardcoded_password:
    error("redis_password_hardcoded", "Compose must reference REDIS_PASSWORD or a secret, not a literal password")
else:
    passed("redis_password_hardcoded", "Compose does not hardcode a Redis password")

origins_value = game_environment.get("SECURITY_ALLOWED_ORIGINS", "")
origins = [origin.strip() for origin in origins_value.split(",") if origin.strip()]
if not origins:
    error("allowed_origins", "SECURITY_ALLOWED_ORIGINS must be non-empty")
elif any("*" in origin for origin in origins):
    error("origin_wildcard", "production origins must not contain a wildcard")
else:
    origin_errors = []
    for origin in origins:
        try:
            parsed = urlsplit(origin)
            host = parsed.hostname or ""
            if parsed.username or parsed.password:
                origin_errors.append("origins must not contain credentials")
            elif parsed.query or parsed.fragment or parsed.path not in {"", "/"}:
                origin_errors.append("origins must not contain a path, query, or fragment")
            elif parsed.scheme == "https" and host:
                continue
            elif parsed.scheme == "http" and is_loopback_host(host):
                continue
            elif parsed.scheme == "http":
                origin_errors.append("public HTTP origins are forbidden")
            else:
                origin_errors.append("origins must use https:// (HTTP is allowed only for loopback)")
        except ValueError:
            origin_errors.append("origin URL is invalid")
    if origin_errors:
        unique_errors = "; ".join(dict.fromkeys(origin_errors))
        error("origin_http_public" if "public HTTP" in unique_errors else "allowed_origins", unique_errors)
    else:
        passed("allowed_origins", "allowed origins are explicit and public origins use HTTPS")

trusted_proxy_value = game_environment.get("SECURITY_TRUSTED_PROXY_CIDRS", "")
trusted_proxy_cidrs = [item.strip() for item in trusted_proxy_value.split(",") if item.strip()]
trusted_proxy_valid = bool(trusted_proxy_cidrs)
for cidr in trusted_proxy_cidrs:
    try:
        network = ipaddress.ip_network(cidr, strict=False)
        if network.prefixlen == 0:
            trusted_proxy_valid = False
    except ValueError:
        trusted_proxy_valid = False

if trusted_proxy_valid:
    passed("trusted_proxies", "trusted proxy CIDRs are explicitly configured")
else:
    error("trusted_proxies", "SECURITY_TRUSTED_PROXY_CIDRS must contain explicit non-global CIDRs")

check_positive_integer(
    game_environment,
    "SECURITY_RATE_LIMIT_PER_SECOND",
    "connection_rate_second",
)
check_positive_integer(
    game_environment,
    "SECURITY_RATE_LIMIT_PER_MINUTE",
    "connection_rate_minute",
)
check_positive_integer(
    game_environment,
    "SECURITY_MESSAGE_LIMIT_PER_SECOND",
    "message_rate",
)
check_positive_integer(
    game_environment,
    "SERVER_MAX_CONNECTIONS",
    "max_connections",
    upper_bound=1000,
)

if game is not None:
    game_ports = service_ports(game)
    if not game_ports:
        passed("backend_bind", "game backend is not published on a host port")
    elif all(port_host(port) == "127.0.0.1" for port in game_ports):
        passed("backend_bind", "game backend host ports bind only to 127.0.0.1")
    else:
        error("backend_bind", "game backend host ports must bind only to 127.0.0.1")

all_services = services(all_profiles)
redis_name, redis_service = find_named_service(all_profiles, "redis", "redis:")
if redis_service is None:
    error("redis_service", "could not identify the Redis service")
elif service_ports(redis_service):
    error("redis_ports", f"Redis service {redis_name} must not publish host ports")
else:
    passed("redis_ports", "Redis does not publish a host port")

douzero_name, douzero_service = find_named_service(all_profiles, "douzero", "fight-the-landlord-douzero")
if douzero_service is not None and service_ports(douzero_service):
    error("douzero_ports", f"DouZero service {douzero_name} must not publish host ports")
else:
    passed("douzero_ports", "DouZero does not publish a host port")

host_network_services = sorted(
    name
    for name, service in all_services.items()
    if str(service.get("network_mode", "")).strip().lower() == "host"
)
if host_network_services:
    error("host_network", "host network mode is forbidden: " + ", ".join(host_network_services))
else:
    passed("host_network", "no service uses host network mode")

privileged_services = sorted(
    name for name, service in all_services.items() if service.get("privileged") is True
)
if privileged_services:
    error("privileged", "privileged containers are forbidden: " + ", ".join(privileged_services))
else:
    passed("privileged", "no service is privileged")

if game is not None and image_has_fixed_reference(game.get("image", "")):
    passed("game_image", "game image uses a digest or an explicit non-latest tag")
else:
    error("game_image_latest", "game image must not use an implicit or explicit latest tag")

douzero_enabled = parse_boolean(game_environment.get("DOUZERO_ENABLED", "false"))
if douzero_enabled is None:
    error("douzero_enabled", "DOUZERO_ENABLED must be a boolean")
elif douzero_enabled:
    active_douzero_name, active_douzero = find_named_service(
        active, "douzero", "fight-the-landlord-douzero"
    )
    if active_douzero is None:
        error("douzero_profile", "DouZero is enabled but its Compose profile/service is not active")
    elif not image_has_fixed_reference(active_douzero.get("image", "")):
        error("douzero_image_latest", "enabled DouZero must use a digest or explicit non-latest tag")
    else:
        passed("douzero_image", f"enabled DouZero service {active_douzero_name} uses a fixed reference")
else:
    passed("douzero_image", "DouZero is disabled; no inference image is deployed")

metrics_enabled = parse_boolean(
    game_environment.get("OBSERVABILITY_METRICS_ENABLED", "false")
)
if metrics_enabled is None:
    error("metrics_enabled", "OBSERVABILITY_METRICS_ENABLED must be a boolean")
elif not metrics_enabled:
    passed("metrics_public", "metrics are disabled")
else:
    metrics_path = game_environment.get("OBSERVABILITY_METRICS_PATH", "").strip()
    public_metrics_service = any(
        "metric" in name.lower() and has_public_port(service)
        for name, service in services(active).items()
    )
    if not metrics_path:
        error("metrics_path", "enabled metrics require OBSERVABILITY_METRICS_PATH")
    elif (game is not None and has_public_port(game)) or public_metrics_service:
        error("metrics_public", "enabled metrics are reachable through a publicly bound host port")
    else:
        passed("metrics_public", "Compose does not directly publish the metrics listener publicly")
        warning(
            "metrics_proxy_acl",
            "metrics share the game listener; verify the TLS reverse proxy denies the metrics path publicly",
        )

if errors:
    print(f"ERROR preflight failed with {errors} error(s) and {warnings} warning(s)")
    sys.exit(1)

print(f"PASS preflight passed with {warnings} warning(s)")
PY
then
  exit 0
else
  exit 1
fi
