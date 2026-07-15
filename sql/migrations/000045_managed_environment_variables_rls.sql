ALTER TABLE managed_environment_variables ENABLE ROW LEVEL SECURITY;
ALTER TABLE managed_environment_variables FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS managed_environment_variables_workspace_isolation
  ON managed_environment_variables;

CREATE POLICY managed_environment_variables_workspace_isolation
  ON managed_environment_variables
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  );
