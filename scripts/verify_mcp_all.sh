#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

POSTGRES_USER="${TMA_POSTGRES_TEST_USER:-tma}"
POSTGRES_PASSWORD="${TMA_POSTGRES_TEST_PASSWORD:-tma}"
POSTGRES_HOST="${TMA_POSTGRES_TEST_HOST:-localhost}"
POSTGRES_PORT="${TMA_POSTGRES_TEST_PORT:-5432}"
VERIFY_DATABASE="tma_verify_mcp_all_$(date +%Y%m%d%H%M%S)_$$"
DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$POSTGRES_HOST:$POSTGRES_PORT/$VERIFY_DATABASE?sslmode=disable"

cleanup() {
  docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
    -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$VERIFY_DATABASE' AND pid <> pg_backend_pid();" \
    >/dev/null 2>&1 || true
  docker compose exec -T postgres dropdb --if-exists -U "$POSTGRES_USER" "$VERIFY_DATABASE" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

for command in docker python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "missing command: $command" >&2
    exit 1
  fi
done

echo "Creating shared isolated database for MCP stdio/HTTP: $VERIFY_DATABASE"
docker compose up -d postgres >/dev/null
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$VERIFY_DATABASE"
docker compose exec -T postgres sh -c '
  set -eu
  for file in /migrations/*.sql; do
    psql -v ON_ERROR_STOP=1 --single-transaction -U "$1" -d "$2" -f "$file" >/dev/null
  done
' sh "$POSTGRES_USER" "$VERIFY_DATABASE"

echo "Running MCP stdio verification"
TMA_DATABASE_URL="$DATABASE_URL" scripts/verify_mcp_stdio.sh

echo "Running MCP Streamable HTTP verification"
TMA_DATABASE_URL="$DATABASE_URL" scripts/verify_mcp_http.sh

echo "Running MCP Registry verification"
scripts/verify_mcp_registry.sh

echo "Running MCP RuntimeGuard verification"
scripts/verify_mcp_runtime_guard.sh

echo "All MCP end-to-end verifications passed"
