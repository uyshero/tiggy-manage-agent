#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
POSTGRES_USER="${TMA_POSTGRES_TEST_USER:-tma}"
POSTGRES_PASSWORD="${TMA_POSTGRES_TEST_PASSWORD:-tma}"
POSTGRES_HOST="${TMA_POSTGRES_TEST_HOST:-localhost}"
POSTGRES_PORT="${TMA_POSTGRES_TEST_PORT:-5432}"
BENCHTIME="${TMA_POSTGRES_BENCHTIME:-50x}"
COUNT="${TMA_POSTGRES_BENCH_COUNT:-1}"
GOCACHE="${GOCACHE:-$ROOT_DIR/.gocache}"
BENCHMARK="${TMA_POSTGRES_BENCHMARK:-^BenchmarkPostgres(AgentLoopFastCommit|SessionEventAppend)$}"
CPU_PROFILE="${TMA_POSTGRES_CPU_PROFILE:-}"
MEM_PROFILE="${TMA_POSTGRES_MEM_PROFILE:-}"
PROFILE_OUTPUT_DIR="${TMA_POSTGRES_PROFILE_OUTPUT_DIR:-}"
BENCH_DATABASE="tma_bench_$(date +%Y%m%d%H%M%S)_$$"
BENCH_DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$POSTGRES_HOST:$POSTGRES_PORT/$BENCH_DATABASE?sslmode=disable"

cleanup() {
	docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
		-c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$BENCH_DATABASE' AND pid <> pg_backend_pid();" \
		>/dev/null 2>&1 || true
	docker compose exec -T postgres dropdb --if-exists -U "$POSTGRES_USER" "$BENCH_DATABASE" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

cd "$ROOT_DIR"
docker compose up -d postgres >/dev/null
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$BENCH_DATABASE"
docker compose exec -T postgres sh -c '
	set -eu
	for file in /migrations/*.sql; do
		psql -v ON_ERROR_STOP=1 --single-transaction -U "$1" -d "$2" -f "$file" >/dev/null 2>&1
	done
' sh "$POSTGRES_USER" "$BENCH_DATABASE"

set -- go test ./internal/managedagents \
	-run '^$' \
	-bench "$BENCHMARK" \
	-benchmem \
	-benchtime="$BENCHTIME" \
	-count="$COUNT"
if [ -n "$CPU_PROFILE" ]; then
	set -- "$@" -cpuprofile="$CPU_PROFILE"
fi
if [ -n "$MEM_PROFILE" ]; then
	set -- "$@" -memprofile="$MEM_PROFILE"
fi
if [ -n "$PROFILE_OUTPUT_DIR" ]; then
	mkdir -p "$PROFILE_OUTPUT_DIR"
	set -- "$@" -outputdir="$PROFILE_OUTPUT_DIR" -o "$PROFILE_OUTPUT_DIR/managedagents.test"
fi

TMA_RUN_POSTGRES_TESTS=1 TMA_DATABASE_URL="$BENCH_DATABASE_URL" GOCACHE="$GOCACHE" "$@"
