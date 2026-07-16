ALTER TABLE session_interventions
  ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'tool_approval',
  ADD COLUMN IF NOT EXISTS request_json JSONB,
  ADD COLUMN IF NOT EXISTS response_json JSONB,
  ADD COLUMN IF NOT EXISTS responded_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

ALTER TABLE session_interventions
  DROP CONSTRAINT IF EXISTS session_interventions_kind_check;

ALTER TABLE session_interventions
  ADD CONSTRAINT session_interventions_kind_check CHECK (
    kind IN ('tool_approval', 'clarification', 'plan_approval', 'upload_request')
  );

ALTER TABLE session_interventions
  DROP CONSTRAINT IF EXISTS session_interventions_status_check;

ALTER TABLE session_interventions
  ADD CONSTRAINT session_interventions_status_check CHECK (
    status IN (
      'pending',
      'approved',
      'rejected',
      'answered',
      'skipped',
      'canceled',
      'expired'
    )
  );

ALTER TABLE session_turns
  DROP CONSTRAINT IF EXISTS session_turns_status_check;

ALTER TABLE session_turns
  ADD CONSTRAINT session_turns_status_check CHECK (
    status IN (
      'running',
      'waiting_approval',
      'waiting_human',
      'interrupted',
      'completed',
      'failed'
    )
  );

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_interventions_one_pending_clarification
  ON session_interventions(session_id, turn_id)
  WHERE kind = 'clarification' AND status = 'pending';

