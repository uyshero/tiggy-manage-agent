# Biography Voice Production Storage

The biography voice gateway uses PostgreSQL for user-owned metadata and the
configured private object store for recording bytes. Do not use
`TMA_BIOGRAPHY_DATA_DIR` in a production deployment.

## Required configuration

```dotenv
TMA_BIOGRAPHY_AUTH_MODE=oidc
TMA_BIOGRAPHY_DATABASE_URL=postgres://.../tma
TMA_BIOGRAPHY_OBJECT_STORE_PROVIDER=s3
TMA_BIOGRAPHY_OBJECT_STORE_ENDPOINT=https://object.example
TMA_BIOGRAPHY_OBJECT_STORE_REGION=cn-beijing-1
TMA_BIOGRAPHY_OBJECT_STORE_BUCKET=biography-private
TMA_BIOGRAPHY_OBJECT_STORE_ACCESS_KEY_ENV=BIOGRAPHY_OBJECT_STORE_ACCESS_KEY
TMA_BIOGRAPHY_OBJECT_STORE_SECRET_KEY_ENV=BIOGRAPHY_OBJECT_STORE_SECRET_KEY
```

Apply `sql/migrations/000103_biography_voice_persistence.sql` before starting
the gateway. `make migrate-up` applies all repository migrations to the local
compose database.

## Ownership and concurrency

Every metadata query is scoped by the authenticated OIDC user ID. The gateway
uses a PostgreSQL transaction advisory lock keyed by user and project before
writing progress, so concurrent gateway replicas cannot interleave project
updates. `biography_audit_events` records login, progress save, recording
writes and deletion without storing audio or access tokens.

## Backup and restore

Enable bucket versioning and an encrypted, cross-region replication or backup
destination for the private recording bucket. Take a daily encrypted PostgreSQL
logical backup and retain it for at least the same duration as the object-store
versions. A backup is valid only when its database snapshot and object-store
version marker are stored together in `biography_backup_checkpoints` by the
backup job.

Run a quarterly restore drill into an isolated environment: restore the
database snapshot, restore the bucket version marker, verify owner-scoped
recording playback and chapter progress, then delete the drill environment.
Never use production OIDC tokens or real recordings during automated tests.
