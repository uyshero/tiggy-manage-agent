ALTER TABLE managed_environment_variables
  ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT '';

ALTER TABLE managed_environment_variables
  DROP CONSTRAINT IF EXISTS managed_environment_variables_pkey;

ALTER TABLE managed_environment_variables
  ADD CONSTRAINT managed_environment_variables_pkey
  PRIMARY KEY (workspace_id, owner_id, name);

DROP INDEX IF EXISTS idx_managed_environment_variables_workspace;

CREATE INDEX idx_managed_environment_variables_workspace
  ON managed_environment_variables(workspace_id, owner_id, name);

DROP POLICY IF EXISTS managed_environment_variables_workspace_isolation
  ON managed_environment_variables;
DROP POLICY IF EXISTS managed_environment_variables_owner_insert
  ON managed_environment_variables;
DROP POLICY IF EXISTS managed_environment_variables_owner_update
  ON managed_environment_variables;
DROP POLICY IF EXISTS managed_environment_variables_owner_delete
  ON managed_environment_variables;

CREATE POLICY managed_environment_variables_workspace_isolation
  ON managed_environment_variables
  FOR SELECT
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      owner_id = ''
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  );

CREATE POLICY managed_environment_variables_owner_insert
  ON managed_environment_variables
  FOR INSERT
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND owner_id = COALESCE(NULLIF(current_setting('tma.owner_id', true), ''), '')
  );

CREATE POLICY managed_environment_variables_owner_update
  ON managed_environment_variables
  FOR UPDATE
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND owner_id = COALESCE(NULLIF(current_setting('tma.owner_id', true), ''), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND owner_id = COALESCE(NULLIF(current_setting('tma.owner_id', true), ''), '')
  );

CREATE POLICY managed_environment_variables_owner_delete
  ON managed_environment_variables
  FOR DELETE
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND owner_id = COALESCE(NULLIF(current_setting('tma.owner_id', true), ''), '')
  );
