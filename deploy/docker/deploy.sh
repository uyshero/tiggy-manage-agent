#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$ROOT/deploy/docker/docker-compose.production.yml"
ENV_FILE="$ROOT/deploy/docker/.env.production"
WITH_WORKER=0
WITH_BROWSER=0
BUILD=1
PREPARE_HOST=1
DRY_RUN=0
WAIT_SECONDS=120

usage() {
  cat <<'EOF'
Usage: deploy/docker/deploy.sh [options]

Options:
  --env-file PATH       Production environment file.
  --with-worker         Start the optional local_system Worker.
  --with-browser        Start Browser Gateway and browser extension Worker.
  --no-build            Use prebuilt images without building locally.
  --no-prepare-host     Do not create/chown cloud_sandbox host directories.
  --wait-seconds N      Server health timeout (default: 120).
  --dry-run             Validate configuration without changing Docker state.
  -h, --help            Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file) ENV_FILE="${2:?missing --env-file value}"; shift 2 ;;
    --with-worker) WITH_WORKER=1; shift ;;
    --with-browser) WITH_BROWSER=1; shift ;;
    --no-build) BUILD=0; shift ;;
    --no-prepare-host) PREPARE_HOST=0; shift ;;
    --wait-seconds) WAIT_SECONDS="${2:?missing --wait-seconds value}"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown option: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

command -v docker >/dev/null || { echo 'docker is required' >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo 'docker compose v2 is required' >&2; exit 1; }
[[ -f "$ENV_FILE" ]] || {
  printf 'production env file not found: %s\nCreate it from deploy/docker/.env.production.example.\n' "$ENV_FILE" >&2
  exit 1
}
if grep -En 'replace-with|example\.com' "$ENV_FILE" >/dev/null; then
  echo 'production env file still contains template values:' >&2
  grep -En 'replace-with|example\.com' "$ENV_FILE" >&2
  exit 1
fi
[[ "$WAIT_SECONDS" =~ ^[1-9][0-9]*$ ]] || { echo '--wait-seconds must be a positive integer' >&2; exit 2; }

env_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "$ENV_FILE" | tail -1
}

ENV_FILE="$(cd "$(dirname "$ENV_FILE")" && pwd)/$(basename "$ENV_FILE")"
compose=(docker compose --project-name tma-production --env-file "$ENV_FILE" -f "$COMPOSE_FILE")
TMA_ENV_FILE="$ENV_FILE" "${compose[@]}" config --quiet

configured_gid="$(env_value TMA_DOCKER_GID)"
workspace_root="$(env_value TMA_CLOUD_SANDBOX_ROOT)"
data_root="$(env_value TMA_CLOUD_SANDBOX_DATA_ROOT)"
sandbox_image="$(env_value TMA_CLOUD_SANDBOX_IMAGE)"
[[ "$configured_gid" =~ ^[0-9]+$ ]] || { echo 'TMA_DOCKER_GID must be numeric' >&2; exit 1; }
[[ "$workspace_root" = /* && "$data_root" = /* ]] || { echo 'sandbox roots must be absolute paths' >&2; exit 1; }
[[ "$workspace_root" == /var/lib/tma/* && "$data_root" == /var/lib/tma/* ]] || {
  echo 'Compose cloud_sandbox roots must be children of /var/lib/tma' >&2
  exit 1
}
[[ -n "$sandbox_image" ]] || { echo 'TMA_CLOUD_SANDBOX_IMAGE is required' >&2; exit 1; }
if [[ "$WITH_BROWSER" -eq 1 && -z "$(env_value TMA_BROWSER_GATEWAY_SERVICE_SECRET)" ]]; then
  echo 'TMA_BROWSER_GATEWAY_SERVICE_SECRET is required with --with-browser' >&2
  exit 1
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo 'Docker deployment configuration is valid.'
  exit 0
fi

if [[ "$PREPARE_HOST" -eq 1 ]]; then
  [[ "$(uname -s)" == "Linux" ]] || {
    echo 'cloud_sandbox Docker production deployment requires a dedicated Linux host' >&2
    exit 1
  }
  [[ -S /var/run/docker.sock ]] || { echo '/var/run/docker.sock is unavailable' >&2; exit 1; }
  socket_gid="$(stat -c '%g' /var/run/docker.sock)"
  [[ "$configured_gid" == "$socket_gid" ]] || {
    printf 'TMA_DOCKER_GID=%s does not match Docker socket GID %s\n' "$configured_gid" "$socket_gid" >&2
    exit 1
  }
  install_cmd=(install -d -o 10001 -g 10001 -m 0700 "$workspace_root" "$data_root")
  if [[ "$(id -u)" -eq 0 ]]; then
    "${install_cmd[@]}"
  elif command -v sudo >/dev/null; then
    sudo "${install_cmd[@]}"
  else
    echo 'root or sudo is required to prepare sandbox directories' >&2
    exit 1
  fi
fi

profile_args=()
services=(server)
if [[ "$WITH_WORKER" -eq 1 ]]; then
	profile_args+=(--profile worker)
  services+=(worker)
fi
if [[ "$WITH_BROWSER" -eq 1 ]]; then
  profile_args+=(--profile browser)
  services+=(browser-gateway browser-worker)
fi
build_args=(--build)
if [[ "$BUILD" -eq 0 ]]; then
  build_args=(--no-build)
fi
if ! docker image inspect "$sandbox_image" >/dev/null 2>&1; then
  docker pull "$sandbox_image"
fi

TMA_ENV_FILE="$ENV_FILE" "${compose[@]}" "${profile_args[@]}" up -d "${build_args[@]}" "${services[@]}"

deadline=$((SECONDS + WAIT_SECONDS))
until TMA_ENV_FILE="$ENV_FILE" "${compose[@]}" exec -T server wget -q -O - http://127.0.0.1:8080/health >/dev/null 2>&1; do
  if (( SECONDS >= deadline )); then
    echo 'tma-server did not become healthy; recent logs:' >&2
    TMA_ENV_FILE="$ENV_FILE" "${compose[@]}" logs --tail=120 server >&2 || true
    exit 1
  fi
  sleep 2
done

if [[ "$WITH_BROWSER" -eq 1 ]]; then
  until TMA_ENV_FILE="$ENV_FILE" "${compose[@]}" exec -T browser-gateway wget -q -O - http://127.0.0.1:8090/v2/extensions/browser/health >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      echo 'tma-browser-gateway did not become healthy; recent logs:' >&2
      TMA_ENV_FILE="$ENV_FILE" "${compose[@]}" logs --tail=120 browser-gateway >&2 || true
      exit 1
    fi
    sleep 2
  done
fi

bind_address="$(env_value TMA_HTTP_BIND)"
http_port="$(env_value TMA_HTTP_PORT)"
printf 'TMA Docker deployment is healthy: http://%s:%s/health\n' "${bind_address:-127.0.0.1}" "${http_port:-8080}"
