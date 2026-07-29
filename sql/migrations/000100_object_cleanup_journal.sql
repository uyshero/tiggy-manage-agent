CREATE TABLE IF NOT EXISTS object_cleanup_journal (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  object_ref_id TEXT NOT NULL DEFAULT '',
  storage_provider TEXT NOT NULL,
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  object_version TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL,
  safe_to_delete BOOLEAN NOT NULL DEFAULT FALSE,
  status TEXT NOT NULL DEFAULT 'blocked',
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  object_was_missing BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  CONSTRAINT object_cleanup_journal_status_check CHECK (
    status IN ('pending', 'processing', 'completed', 'blocked', 'dead_letter')
  ),
  CONSTRAINT object_cleanup_journal_attempt_check CHECK (attempt_count >= 0),
  CONSTRAINT object_cleanup_journal_identity_check CHECK (
    btrim(storage_provider) <> '' AND btrim(bucket) <> '' AND btrim(object_key) <> ''
  ),
  CONSTRAINT object_cleanup_journal_safety_check CHECK (safe_to_delete OR status = 'blocked')
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_object_cleanup_journal_active_object
  ON object_cleanup_journal(workspace_id, storage_provider, bucket, object_key, object_version)
  WHERE status IN ('pending', 'processing', 'blocked', 'dead_letter');

CREATE INDEX IF NOT EXISTS idx_object_cleanup_journal_claim
  ON object_cleanup_journal(workspace_id, next_attempt_at, created_at)
  WHERE status = 'pending' AND safe_to_delete;

CREATE INDEX IF NOT EXISTS idx_object_cleanup_journal_expired_lease
  ON object_cleanup_journal(workspace_id, lease_expires_at, created_at)
  WHERE status = 'processing';

CREATE INDEX IF NOT EXISTS idx_object_cleanup_journal_workspace_status
  ON object_cleanup_journal(workspace_id, status, created_at DESC);

CREATE SEQUENCE IF NOT EXISTS tma_object_cleanup_journal_id_seq;

ALTER TABLE object_cleanup_journal ENABLE ROW LEVEL SECURITY;
ALTER TABLE object_cleanup_journal FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS object_cleanup_journal_workspace_isolation ON object_cleanup_journal;
CREATE POLICY object_cleanup_journal_workspace_isolation
  ON object_cleanup_journal
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

