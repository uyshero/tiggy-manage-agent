DROP INDEX IF EXISTS idx_session_interventions_pending_expires_at;

ALTER TABLE session_interventions
  DROP COLUMN IF EXISTS expires_at;
