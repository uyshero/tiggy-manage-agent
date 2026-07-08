ALTER TABLE session_interventions
  ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_session_interventions_pending_expires_at
  ON session_interventions(session_id, status, expires_at)
  WHERE status = 'pending';
