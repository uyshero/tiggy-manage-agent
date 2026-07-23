#!/bin/sh
set -eu

REPOSITORY_ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
DOTENV_PATH="${TMA_DOTENV_PATH:-$REPOSITORY_ROOT/.env}"
HTTP_ADDR="${TMA_AGENT_CORE_COMPETITOR_HTTP_ADDR:-:18089}"
PID_FILE="${TMA_AGENT_CORE_COMPETITOR_PID_FILE:-$REPOSITORY_ROOT/.tma-agent-core-competitor.pid}"
POSTGRES_CONTAINER="${TMA_AGENT_CORE_STAGING_POSTGRES_CONTAINER:-tma-postgres}"

database_url=$(sed -n 's/^TMA_DATABASE_URL=//p' "$DOTENV_PATH" | head -1)
database_url=${database_url#\"}
database_url=${database_url%\"}
postgres_port=$(docker port "$POSTGRES_CONTAINER" 5432/tcp | head -1 | awk -F: '{print $NF}')
if [ -z "$database_url" ] || [ -z "$postgres_port" ]; then
  echo "cannot resolve competitor database URL" >&2
  exit 1
fi
database_url=$(printf '%s' "$database_url" | sed -E "s#(localhost|127\\.0\\.0\\.1|\\[::1\\]):[0-9]+/#\\1:$postgres_port/#")

cd "$REPOSITORY_ROOT"
export TMA_HTTP_ADDR="$HTTP_ADDR"
export TMA_DATABASE_URL="$database_url"
export TMA_AUTH_MODE=disabled
export TMA_AUTH_OIDC_WEB_LOGIN_ENABLED=false
exec bin/tma-server --pid-file "$PID_FILE"
