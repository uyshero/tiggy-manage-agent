#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
MIGRATION_DIR="$ROOT_DIR/sql/migrations"
POSTGRES_SERVICE="${TMA_POSTGRES_MIGRATION_SERVICE:-postgres}"
POSTGRES_USER="${TMA_POSTGRES_MIGRATION_USER:-tma}"
POSTGRES_DATABASE="${TMA_POSTGRES_MIGRATION_DATABASE:-tma}"
BASELINE_VERSION="${TMA_MIGRATION_BASELINE_VERSION:-}"
LOCK_TIMEOUT_SECONDS="${TMA_MIGRATION_LOCK_TIMEOUT_SECONDS:-60}"
LOCK_PROCESS_PID=""
HOST_TOKEN="$(hostname | cksum | awk '{print $1}')"
LOCK_APP_NAME="tma-migrate-$HOST_TOKEN-$(date +%s)-$$"

checksum_file() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		sha256sum "$1" | awk '{print $1}'
	fi
}

psql_value() {
	docker compose exec -T "$POSTGRES_SERVICE" psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DATABASE" -Atc "$1"
}

release_migration_lock() {
	if [ -z "$LOCK_PROCESS_PID" ]; then
		return
	fi
	docker compose exec -T "$POSTGRES_SERVICE" psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DATABASE" -Atc \
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = '$LOCK_APP_NAME' AND pid <> pg_backend_pid()" \
		>/dev/null 2>&1 || true
	kill "$LOCK_PROCESS_PID" >/dev/null 2>&1 || true
	wait "$LOCK_PROCESS_PID" >/dev/null 2>&1 || true
	LOCK_PROCESS_PID=""
}

acquire_migration_lock() {
	docker compose exec -T -e PGAPPNAME="$LOCK_APP_NAME" "$POSTGRES_SERVICE" \
		psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DATABASE" -Atc \
		"SELECT pg_advisory_lock(hashtext(current_database()), hashtext('tma_schema_migrations')); SELECT pg_sleep(2147483647);" \
		>/dev/null 2>&1 &
	LOCK_PROCESS_PID=$!
	deadline=$(( $(date +%s) + LOCK_TIMEOUT_SECONDS ))
	while [ "$(date +%s)" -le "$deadline" ]; do
		if ! kill -0 "$LOCK_PROCESS_PID" 2>/dev/null; then
			wait "$LOCK_PROCESS_PID" >/dev/null 2>&1 || true
			LOCK_PROCESS_PID=""
			printf '%s\n' 'migration advisory lock connection exited before acquiring the lock' >&2
			exit 1
		fi
		granted_count="$(psql_value "SELECT count(*) FROM pg_locks AS locks JOIN pg_stat_activity AS activity ON activity.pid = locks.pid WHERE activity.application_name = '$LOCK_APP_NAME' AND locks.locktype = 'advisory' AND locks.granted")"
		if [ "$granted_count" -gt 0 ]; then
			printf 'acquired migration advisory lock: %s\n' "$LOCK_APP_NAME"
			return
		fi
		sleep 1
	done
	printf 'timed out after %s seconds waiting for the migration advisory lock\n' "$LOCK_TIMEOUT_SECONDS" >&2
	exit 1
}

trap 'release_migration_lock' EXIT
trap 'exit 1' HUP INT TERM

case "$BASELINE_VERSION" in
	"" | [0-9][0-9][0-9][0-9][0-9][0-9]) ;;
	*)
		printf 'TMA_MIGRATION_BASELINE_VERSION must be a six-digit migration version\n' >&2
		exit 1
		;;
esac
case "$LOCK_TIMEOUT_SECONDS" in
	'' | *[!0-9]*)
		printf 'TMA_MIGRATION_LOCK_TIMEOUT_SECONDS must be a positive integer\n' >&2
		exit 1
		;;
esac
if [ "$LOCK_TIMEOUT_SECONDS" -lt 1 ]; then
	printf 'TMA_MIGRATION_LOCK_TIMEOUT_SECONDS must be greater than zero\n' >&2
	exit 1
fi

acquire_migration_lock

docker compose exec -T "$POSTGRES_SERVICE" psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DATABASE" <<'SQL'
CREATE TABLE IF NOT EXISTS tma_schema_migrations (
  version TEXT PRIMARY KEY,
  checksum_sha256 TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT tma_schema_migrations_version_check CHECK (version ~ '^[0-9]{6}$'),
  CONSTRAINT tma_schema_migrations_checksum_check CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$')
);
SQL

ledger_count="$(psql_value 'SELECT count(*) FROM tma_schema_migrations')"
existing_table_count="$(psql_value "SELECT count(*) FROM pg_class WHERE relnamespace = 'public'::regnamespace AND relkind IN ('r', 'p') AND relname <> 'tma_schema_migrations'")"

if [ "$ledger_count" -eq 0 ] && [ "$existing_table_count" -gt 0 ]; then
	if [ -z "$BASELINE_VERSION" ]; then
		printf '%s\n' 'existing schema has no migration ledger; refusing to replay historical migrations' >&2
		printf '%s\n' 'after backup and schema verification, rerun with TMA_MIGRATION_BASELINE_VERSION=NNNNNN' >&2
		exit 1
	fi

	baseline_found=false
	baseline_values=""
	separator=""
	for file in "$MIGRATION_DIR"/*.sql; do
		filename="$(basename "$file")"
		version="${filename%%_*}"
		checksum="$(checksum_file "$file")"
		baseline_values="$baseline_values$separator('$version', '$checksum')"
		separator=","
		if [ "$version" = "$BASELINE_VERSION" ]; then
			baseline_found=true
			break
		fi
	done
	if [ "$baseline_found" != true ]; then
		printf 'baseline migration %s does not exist\n' "$BASELINE_VERSION" >&2
		exit 1
	fi
	docker compose exec -T "$POSTGRES_SERVICE" psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DATABASE" \
		-c "INSERT INTO tma_schema_migrations (version, checksum_sha256) VALUES $baseline_values ON CONFLICT (version) DO NOTHING" >/dev/null
	printf 'baselined existing schema through %s\n' "$BASELINE_VERSION"
fi

applied_migrations="$(psql_value "SELECT version || ' ' || checksum_sha256 FROM tma_schema_migrations ORDER BY version")"
for file in "$MIGRATION_DIR"/*.sql; do
	filename="$(basename "$file")"
	version="${filename%%_*}"
	checksum="$(checksum_file "$file")"
	stored_checksum="$(printf '%s\n' "$applied_migrations" | awk -v version="$version" '$1 == version { print $2 }')"
	if [ -n "$stored_checksum" ]; then
		if [ "$stored_checksum" != "$checksum" ]; then
			printf 'migration %s checksum changed after it was applied\n' "$filename" >&2
			exit 1
		fi
		continue
	fi

	container_file="/migrations/$filename"
	docker compose exec -T "$POSTGRES_SERVICE" psql -v ON_ERROR_STOP=1 --single-transaction \
		-U "$POSTGRES_USER" -d "$POSTGRES_DATABASE" -f "$container_file" \
		-c "INSERT INTO tma_schema_migrations (version, checksum_sha256) VALUES ('$version', '$checksum')"
	applied_migrations="$applied_migrations
$version $checksum"
	printf 'applied migration %s\n' "$filename"
done

printf 'migration ledger is current\n'
