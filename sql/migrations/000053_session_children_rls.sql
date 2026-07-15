ALTER TABLE session_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_events FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_events_session_isolation ON session_events;

CREATE POLICY session_events_session_isolation
  ON session_events
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_events.session_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_events.session_id
    )
  );

ALTER TABLE session_summaries ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_summaries FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_summaries_session_isolation ON session_summaries;

CREATE POLICY session_summaries_session_isolation
  ON session_summaries
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_summaries.session_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_summaries.session_id
    )
  );

ALTER TABLE session_turns ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_turns FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_turns_session_isolation ON session_turns;

CREATE POLICY session_turns_session_isolation
  ON session_turns
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_turns.session_id
        AND sessions.workspace_id = session_turns.workspace_id
        AND sessions.owner_id = session_turns.owner_id
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_turns.session_id
        AND sessions.workspace_id = session_turns.workspace_id
        AND sessions.owner_id = session_turns.owner_id
    )
  );

ALTER TABLE session_interventions ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_interventions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_interventions_session_isolation
  ON session_interventions;

CREATE POLICY session_interventions_session_isolation
  ON session_interventions
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_interventions.session_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_interventions.session_id
    )
  );
