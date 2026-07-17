# Production database migrations

TMA maintains two database artifacts for different deployment cases:

- `sql/baselines/000080_baseline.sql` initializes a new, empty PostgreSQL database at schema version `000080`.
- `sql/migrations/*.sql` remains the authoritative ordered history for upgrading an existing database.

Do not apply the baseline to a database that already contains TMA tables or data. Do not delete, edit, renumber, or squash migration files that have been used by any environment.

## New production database

Apply the baseline as one transaction and stop on the first error:

```bash
psql "$TMA_DATABASE_URL" \
  -v ON_ERROR_STOP=1 \
  --single-transaction \
  -f sql/baselines/000080_baseline.sql
```

The deployment should then start the application only after the SQL command succeeds. Database credentials used for migration need DDL privileges; runtime application credentials should use the minimum privileges required by TMA.

## Existing database

Apply only migrations newer than the version already deployed. A baseline is not an upgrade script and must never be run over an existing schema.

Before the first public production release, configure the deployment migration tool to record the current baseline as version `000080`. Subsequent changes must be added as `000081_*.sql`, `000082_*.sql`, and so on. Production should use a migration tool with a version ledger and advisory locking instead of invoking `make migrate-up` concurrently from application replicas.

## Generate and verify

Regenerate the checked-in baseline after intentionally changing a migration that has not yet shipped:

```bash
make generate-sql-baseline
```

Verify that the baseline and all sequential migrations produce identical PostgreSQL schemas, then run the PostgreSQL integration suites against the baseline-created database:

```bash
TMA_POSTGRES_TEST_PORT=55432 make verify-sql-baseline
```

The verification uses temporary databases and removes them when complete. It does not modify the regular `tma` database.

