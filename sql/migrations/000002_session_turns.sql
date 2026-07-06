CREATE TABLE IF NOT EXISTS session_turns (
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  id TEXT NOT NULL,
  status TEXT NOT NULL,
  user_event_id TEXT REFERENCES session_events(id) ON DELETE SET NULL,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ,
  interrupt_requested_at TIMESTAMPTZ,
  error_message TEXT,
  PRIMARY KEY (session_id, id),
  CONSTRAINT session_turns_status_check CHECK (
    status IN (
      'running',
      'interrupted',
      'completed',
      'failed'
    )
  )
);

CREATE INDEX IF NOT EXISTS idx_session_turns_session_status
  ON session_turns(session_id, status);

CREATE INDEX IF NOT EXISTS idx_session_turns_user_event_id
  ON session_turns(user_event_id);
