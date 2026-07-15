ALTER TABLE subagent_start_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE subagent_start_requests FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS subagent_start_requests_session_isolation
  ON subagent_start_requests;

CREATE POLICY subagent_start_requests_session_isolation
  ON subagent_start_requests
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
      WHERE sessions.id = subagent_start_requests.session_id
        AND sessions.workspace_id = subagent_start_requests.workspace_id
        AND sessions.owner_id = subagent_start_requests.owner_id
    )
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = subagent_start_requests.parent_session_id
        AND sessions.workspace_id = subagent_start_requests.workspace_id
        AND sessions.owner_id = subagent_start_requests.owner_id
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
      WHERE sessions.id = subagent_start_requests.session_id
        AND sessions.workspace_id = subagent_start_requests.workspace_id
        AND sessions.owner_id = subagent_start_requests.owner_id
    )
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = subagent_start_requests.parent_session_id
        AND sessions.workspace_id = subagent_start_requests.workspace_id
        AND sessions.owner_id = subagent_start_requests.owner_id
    )
  );
