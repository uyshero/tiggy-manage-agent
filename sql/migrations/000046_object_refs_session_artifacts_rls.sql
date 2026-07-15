ALTER TABLE object_refs ENABLE ROW LEVEL SECURITY;
ALTER TABLE object_refs FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS object_refs_workspace_isolation ON object_refs;

CREATE POLICY object_refs_workspace_isolation
  ON object_refs
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  );

ALTER TABLE session_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_artifacts FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_artifacts_workspace_isolation ON session_artifacts;

CREATE POLICY session_artifacts_workspace_isolation
  ON session_artifacts
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  );
