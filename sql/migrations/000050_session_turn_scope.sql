ALTER TABLE session_turns
  ADD COLUMN IF NOT EXISTS workspace_id TEXT,
  ADD COLUMN IF NOT EXISTS owner_id TEXT;

UPDATE session_turns AS turn
SET workspace_id = session.workspace_id,
    owner_id = session.owner_id
FROM sessions AS session
WHERE session.id = turn.session_id
  AND (turn.workspace_id IS NULL OR turn.owner_id IS NULL);

ALTER TABLE session_turns
  ALTER COLUMN workspace_id SET NOT NULL,
  ALTER COLUMN owner_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_session_turns_workspace_claimable
  ON session_turns(workspace_id, status, lease_expires_at, started_at)
  WHERE status = 'running';
