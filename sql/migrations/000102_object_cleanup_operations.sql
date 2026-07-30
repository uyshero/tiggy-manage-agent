ALTER TABLE object_cleanup_journal
  ADD COLUMN IF NOT EXISTS size_bytes BIGINT NOT NULL DEFAULT 0;

ALTER TABLE object_cleanup_journal
  DROP CONSTRAINT IF EXISTS object_cleanup_journal_size_check;

ALTER TABLE object_cleanup_journal
  ADD CONSTRAINT object_cleanup_journal_size_check CHECK (size_bytes >= 0);

CREATE INDEX IF NOT EXISTS idx_object_cleanup_journal_workspace_reason
  ON object_cleanup_journal(workspace_id, reason, created_at DESC);
