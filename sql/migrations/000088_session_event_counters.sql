CREATE TABLE IF NOT EXISTS session_event_counters (
  session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  last_seq BIGINT NOT NULL DEFAULT 0,
  CONSTRAINT session_event_counters_last_seq_check CHECK (last_seq >= 0)
);

INSERT INTO session_event_counters (session_id, last_seq)
SELECT sessions.id, COALESCE(MAX(session_events.seq), 0)
FROM sessions
LEFT JOIN session_events ON session_events.session_id = sessions.id
GROUP BY sessions.id
ON CONFLICT (session_id) DO UPDATE
SET last_seq = GREATEST(session_event_counters.last_seq, EXCLUDED.last_seq);

ALTER TABLE session_event_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_event_counters FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_event_counters_session_isolation ON session_event_counters;
CREATE POLICY session_event_counters_session_isolation
  ON session_event_counters
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_event_counters.session_id
        AND sessions.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
        AND (
          NULLIF(current_setting('tma.owner_id', true), '') IS NULL
          OR sessions.owner_id = NULLIF(current_setting('tma.owner_id', true), '')
        )
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_event_counters.session_id
        AND sessions.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
        AND (
          NULLIF(current_setting('tma.owner_id', true), '') IS NULL
          OR sessions.owner_id = NULLIF(current_setting('tma.owner_id', true), '')
        )
    )
  );
