ALTER TABLE skill_asset_retention_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_asset_retention_policies FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_asset_retention_policies_scope_isolation
  ON skill_asset_retention_policies;

CREATE POLICY skill_asset_retention_policies_scope_isolation
  ON skill_asset_retention_policies
  FOR ALL
  USING (
    (
      scope_type = 'workspace'
      AND workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    )
    OR (
      scope_type = 'organization'
      AND organization_id = (
        SELECT workspaces.org_id
        FROM workspaces
        WHERE workspaces.id = NULLIF(current_setting('tma.workspace_id', true), '')
      )
    )
  )
  WITH CHECK (
    (
      scope_type = 'workspace'
      AND workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
      AND organization_id IS NULL
    )
    OR (
      scope_type = 'organization'
      AND workspace_id IS NULL
      AND organization_id = (
        SELECT workspaces.org_id
        FROM workspaces
        WHERE workspaces.id = NULLIF(current_setting('tma.workspace_id', true), '')
      )
    )
  );

ALTER TABLE skill_asset_retention_policy_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_asset_retention_policy_versions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_asset_retention_policy_versions_policy_isolation
  ON skill_asset_retention_policy_versions;

CREATE POLICY skill_asset_retention_policy_versions_policy_isolation
  ON skill_asset_retention_policy_versions
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM skill_asset_retention_policies
      WHERE skill_asset_retention_policies.id = skill_asset_retention_policy_versions.policy_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM skill_asset_retention_policies
      WHERE skill_asset_retention_policies.id = skill_asset_retention_policy_versions.policy_id
    )
  );

ALTER TABLE skill_asset_gc_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_asset_gc_runs FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_asset_gc_runs_workspace_isolation
  ON skill_asset_gc_runs;

CREATE POLICY skill_asset_gc_runs_workspace_isolation
  ON skill_asset_gc_runs
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      policy_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM skill_asset_retention_policies
        JOIN skill_asset_retention_policy_versions
          ON skill_asset_retention_policy_versions.policy_id = skill_asset_retention_policies.id
          AND skill_asset_retention_policy_versions.version = skill_asset_gc_runs.policy_version
        WHERE skill_asset_retention_policies.id = skill_asset_gc_runs.policy_id
      )
    )
  );

ALTER TABLE skill_asset_gc_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_asset_gc_items FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_asset_gc_items_run_isolation
  ON skill_asset_gc_items;

CREATE POLICY skill_asset_gc_items_run_isolation
  ON skill_asset_gc_items
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM skill_asset_gc_runs
      WHERE skill_asset_gc_runs.id = skill_asset_gc_items.run_id
        AND skill_asset_gc_runs.workspace_id = skill_asset_gc_items.workspace_id
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM skill_asset_gc_runs
      WHERE skill_asset_gc_runs.id = skill_asset_gc_items.run_id
        AND skill_asset_gc_runs.workspace_id = skill_asset_gc_items.workspace_id
    )
    AND (
      status = 'deleted'
      OR EXISTS (
        SELECT 1
        FROM object_refs
        WHERE object_refs.id = skill_asset_gc_items.object_ref_id
          AND object_refs.workspace_id = skill_asset_gc_items.workspace_id
      )
    )
    AND (
      skill_id = ''
      OR EXISTS (
        SELECT 1
        FROM skills
        WHERE skills.id = skill_asset_gc_items.skill_id
          AND skills.workspace_id = skill_asset_gc_items.workspace_id
      )
    )
  );

ALTER TABLE skill_asset_gc_tombstones ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_asset_gc_tombstones FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_asset_gc_tombstones_run_isolation
  ON skill_asset_gc_tombstones;

CREATE POLICY skill_asset_gc_tombstones_run_isolation
  ON skill_asset_gc_tombstones
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM skill_asset_gc_runs
      WHERE skill_asset_gc_runs.id = skill_asset_gc_tombstones.run_id
        AND skill_asset_gc_runs.workspace_id = skill_asset_gc_tombstones.workspace_id
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM skill_asset_gc_runs
      WHERE skill_asset_gc_runs.id = skill_asset_gc_tombstones.run_id
        AND skill_asset_gc_runs.workspace_id = skill_asset_gc_tombstones.workspace_id
    )
  );
