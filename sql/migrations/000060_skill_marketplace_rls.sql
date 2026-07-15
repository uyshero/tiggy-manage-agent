ALTER TABLE skill_marketplace_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_marketplace_entries FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_marketplace_entries_workspace_isolation
  ON skill_marketplace_entries;

CREATE POLICY skill_marketplace_entries_workspace_isolation
  ON skill_marketplace_entries
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM skills
      JOIN skill_versions
        ON skill_versions.skill_id = skills.id
        AND skill_versions.version = skill_marketplace_entries.skill_version
      WHERE skills.id = skill_marketplace_entries.skill_id
        AND skills.workspace_id = skill_marketplace_entries.workspace_id
    )
  );

ALTER TABLE skill_marketplace_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_marketplace_policies FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_marketplace_policies_scope_isolation
  ON skill_marketplace_policies;

CREATE POLICY skill_marketplace_policies_scope_isolation
  ON skill_marketplace_policies
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

ALTER TABLE skill_marketplace_policy_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_marketplace_policy_versions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_marketplace_policy_versions_policy_isolation
  ON skill_marketplace_policy_versions;

CREATE POLICY skill_marketplace_policy_versions_policy_isolation
  ON skill_marketplace_policy_versions
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM skill_marketplace_policies
      WHERE skill_marketplace_policies.id = skill_marketplace_policy_versions.policy_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM skill_marketplace_policies
      WHERE skill_marketplace_policies.id = skill_marketplace_policy_versions.policy_id
    )
  );
