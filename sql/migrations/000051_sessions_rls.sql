ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS sessions_workspace_owner_isolation ON sessions;

CREATE POLICY sessions_workspace_owner_isolation
  ON sessions
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  );
