#!/bin/sh
set -eu

REPOSITORY_ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
BASELINE="${1:-$REPOSITORY_ROOT/sql/baselines/000085_baseline.sql}"
POSTGRES_USER="${TMA_POSTGRES_TEST_USER:-tma}"
POSTGRES_PASSWORD="${TMA_POSTGRES_TEST_PASSWORD:-tma}"
POSTGRES_HOST="${TMA_POSTGRES_TEST_HOST:-localhost}"
POSTGRES_PORT="${TMA_POSTGRES_TEST_PORT:-5432}"
RUN_ID="$(date +%Y%m%d%H%M%S)_$$"
MIGRATIONS_DATABASE="tma_migrations_$RUN_ID"
BASELINE_DATABASE="tma_baseline_$RUN_ID"
TEMP_DIR="$(mktemp -d)"

if [ ! -f "$BASELINE" ]; then
	printf 'baseline does not exist: %s\n' "$BASELINE" >&2
	exit 1
fi

cleanup() {
	for database in "$MIGRATIONS_DATABASE" "$BASELINE_DATABASE"; do
		docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
			-c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$database' AND pid <> pg_backend_pid();" \
			>/dev/null 2>&1 || true
		docker compose exec -T postgres dropdb --if-exists -U "$POSTGRES_USER" "$database" >/dev/null 2>&1 || true
	done
	rm -rf "$TEMP_DIR"
}
trap cleanup EXIT INT TERM

docker compose up -d postgres >/dev/null
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$MIGRATIONS_DATABASE"
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$BASELINE_DATABASE"

docker compose exec -T postgres sh -c '
	set -eu
	for file in /migrations/*.sql; do
		psql -v ON_ERROR_STOP=1 --single-transaction -U "$1" -d "$2" -f "$file" >/dev/null
	done
' sh "$POSTGRES_USER" "$MIGRATIONS_DATABASE"

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 --single-transaction \
	-U "$POSTGRES_USER" -d "$BASELINE_DATABASE" <"$BASELINE" >/dev/null

docker compose exec -T postgres pg_dump --schema-only --no-owner --no-privileges \
	-U "$POSTGRES_USER" "$MIGRATIONS_DATABASE" \
	| sed -e '/^\\restrict /d' -e '/^\\unrestrict /d' >"$TEMP_DIR/migrations.sql"
docker compose exec -T postgres pg_dump --schema-only --no-owner --no-privileges \
	-U "$POSTGRES_USER" "$BASELINE_DATABASE" \
	| sed -e '/^\\restrict /d' -e '/^\\unrestrict /d' >"$TEMP_DIR/baseline.sql"

if ! diff -u "$TEMP_DIR/migrations.sql" "$TEMP_DIR/baseline.sql"; then
	printf '%s\n' 'baseline schema differs from sequential migrations' >&2
	exit 1
fi

BASELINE_DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$POSTGRES_HOST:$POSTGRES_PORT/$BASELINE_DATABASE?sslmode=disable"
TMA_RUN_POSTGRES_TESTS=1 TMA_DATABASE_URL="$BASELINE_DATABASE_URL" \
	go test ./internal/managedagents -run Postgres -count=1
TMA_RUN_POSTGRES_TESTS=1 TMA_DATABASE_URL="$BASELINE_DATABASE_URL" \
	go test ./internal/httpapi -run PostgresV2 -count=1

printf 'verified baseline schema and PostgreSQL integration tests: %s\n' "$BASELINE"
