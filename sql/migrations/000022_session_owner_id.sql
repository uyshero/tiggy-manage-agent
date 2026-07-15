ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS owner_id TEXT;

UPDATE sessions
SET owner_id = created_by
WHERE owner_id IS NULL OR owner_id = '';

ALTER TABLE sessions
  ALTER COLUMN owner_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_workspace_owner_status
  ON sessions(workspace_id, owner_id, status);
