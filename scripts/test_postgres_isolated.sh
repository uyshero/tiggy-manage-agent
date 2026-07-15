#!/bin/sh
set -eu

POSTGRES_USER="${TMA_POSTGRES_TEST_USER:-tma}"
POSTGRES_PASSWORD="${TMA_POSTGRES_TEST_PASSWORD:-tma}"
POSTGRES_HOST="${TMA_POSTGRES_TEST_HOST:-localhost}"
POSTGRES_PORT="${TMA_POSTGRES_TEST_PORT:-5432}"
TEST_DATABASE="tma_test_$(date +%Y%m%d%H%M%S)_$$"
TEST_DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$POSTGRES_HOST:$POSTGRES_PORT/$TEST_DATABASE?sslmode=disable"

cleanup() {
  docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
    -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$TEST_DATABASE' AND pid <> pg_backend_pid();" \
    >/dev/null 2>&1 || true
  docker compose exec -T postgres dropdb --if-exists -U "$POSTGRES_USER" "$TEST_DATABASE" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker compose up -d postgres >/dev/null
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$TEST_DATABASE"
docker compose exec -T postgres sh -c '
  set -eu
  for file in /migrations/*.sql; do
    psql -v ON_ERROR_STOP=1 --single-transaction -U "$1" -d "$2" -f "$file" >/dev/null
  done
' sh "$POSTGRES_USER" "$TEST_DATABASE"

TMA_RUN_POSTGRES_TESTS=1 \
TMA_DATABASE_URL="$TEST_DATABASE_URL" \
go test ./internal/managedagents -run Postgres -count=1 "$@"

TMA_RUN_POSTGRES_TESTS=1 \
TMA_DATABASE_URL="$TEST_DATABASE_URL" \
go test ./internal/httpapi -run PostgresV2 -count=1 "$@"
