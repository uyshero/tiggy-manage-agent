#!/bin/sh
set -eu

: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${TMA_RUNTIME_DB_PASSWORD:?TMA_RUNTIME_DB_PASSWORD is required}"

escaped_password="$(printf '%s' "$TMA_RUNTIME_DB_PASSWORD" | sed "s/'/''/g")"
if [ "$(psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "SELECT 1 FROM pg_roles WHERE rolname = 'tma_runtime'")" != "1" ]; then
  psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -c "CREATE ROLE tma_runtime LOGIN PASSWORD '$escaped_password' NOSUPERUSER NOBYPASSRLS NOINHERIT"
fi

psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  -f /opt/tma/sql/runtime-grants.sql

