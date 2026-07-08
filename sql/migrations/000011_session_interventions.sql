CREATE TABLE IF NOT EXISTS session_interventions (
  session_id TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  call_id TEXT NOT NULL,
  tool_identifier TEXT NOT NULL,
  api_name TEXT NOT NULL,
  arguments_json JSONB,
  intervention_mode TEXT NOT NULL,
  reason TEXT,
  status TEXT NOT NULL DEFAULT 'pending',
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  decided_at TIMESTAMPTZ,
  decision_reason TEXT,
  PRIMARY KEY (session_id, turn_id, call_id),
  FOREIGN KEY (session_id, turn_id)
    REFERENCES session_turns(session_id, id)
    ON DELETE CASCADE,
  CONSTRAINT session_interventions_status_check CHECK (
    status IN (
      'pending',
      'approved',
      'rejected'
    )
  )
);

CREATE INDEX IF NOT EXISTS idx_session_interventions_session_status
  ON session_interventions(session_id, status, requested_at);
