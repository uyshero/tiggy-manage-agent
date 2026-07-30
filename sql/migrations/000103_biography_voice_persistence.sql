-- Biography Voice production persistence. Metadata stays in PostgreSQL;
-- recordings are stored in the configured private object store.

CREATE TABLE IF NOT EXISTS biography_users (
    id TEXT PRIMARY KEY,
    oidc_issuer TEXT NOT NULL,
    oidc_subject TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    last_login_at TIMESTAMPTZ NOT NULL,
    UNIQUE (oidc_issuer, oidc_subject)
);

CREATE TABLE IF NOT EXISTS biography_projects (
    owner_id TEXT NOT NULL REFERENCES biography_users(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL,
    progress JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (owner_id, project_id)
);

CREATE INDEX IF NOT EXISTS biography_projects_owner_updated_idx
    ON biography_projects (owner_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS biography_recording_sessions (
    owner_id TEXT NOT NULL REFERENCES biography_users(id) ON DELETE CASCADE,
    recording_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL DEFAULT '',
    chapter_title TEXT NOT NULL DEFAULT '',
    transcript TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL CHECK (duration_ms >= 0),
    title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    content_type TEXT NOT NULL DEFAULT 'audio/wav',
    object_bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    PRIMARY KEY (owner_id, recording_id)
);

CREATE INDEX IF NOT EXISTS biography_recordings_owner_project_created_idx
    ON biography_recording_sessions (owner_id, project_id, created_at DESC);

CREATE TABLE IF NOT EXISTS biography_audit_events (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES biography_users(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    project_id TEXT,
    recording_id TEXT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS biography_audit_owner_created_idx
    ON biography_audit_events (owner_id, created_at DESC);

-- Retain an explicit checkpoint record so scheduled backups can report a
-- verifiable snapshot time and object-store prefix for restore operations.
CREATE TABLE IF NOT EXISTS biography_backup_checkpoints (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('started', 'completed', 'failed')),
    database_snapshot_ref TEXT,
    object_snapshot_ref TEXT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ
);
