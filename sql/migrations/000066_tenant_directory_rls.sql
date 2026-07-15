CREATE OR REPLACE FUNCTION tma_list_workspace_ids()
RETURNS TABLE(workspace_id TEXT)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  RETURN QUERY SELECT workspaces.id FROM public.workspaces ORDER BY workspaces.id;
END;
$$;

CREATE OR REPLACE FUNCTION tma_workspace_exists(requested_workspace_id TEXT)
RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  RETURN EXISTS (SELECT 1 FROM public.workspaces WHERE id = requested_workspace_id);
END;
$$;

CREATE OR REPLACE FUNCTION tma_organization_exists(requested_organization_id TEXT)
RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  RETURN EXISTS (SELECT 1 FROM public.organizations WHERE id = requested_organization_id);
END;
$$;

CREATE OR REPLACE FUNCTION tma_workspace_organization_id(requested_workspace_id TEXT)
RETURNS TEXT
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
DECLARE
  organization_id TEXT;
BEGIN
  SELECT org_id INTO organization_id
  FROM public.workspaces
  WHERE id = requested_workspace_id;
  RETURN organization_id;
END;
$$;

CREATE OR REPLACE FUNCTION tma_workspaces_share_organization(left_workspace_id TEXT, right_workspace_id TEXT)
RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1
    FROM public.workspaces left_workspace
    JOIN public.workspaces right_workspace
      ON right_workspace.org_id = left_workspace.org_id
    WHERE left_workspace.id = left_workspace_id
      AND right_workspace.id = right_workspace_id
  );
END;
$$;

ALTER TABLE workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspaces FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS workspaces_current_workspace_read ON workspaces;

CREATE POLICY workspaces_current_workspace_read
  ON workspaces
  FOR SELECT
  USING (id = NULLIF(current_setting('tma.workspace_id', true), ''));

ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE organizations FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS organizations_current_workspace_read ON organizations;

CREATE POLICY organizations_current_workspace_read
  ON organizations
  FOR SELECT
  USING (
    id = tma_workspace_organization_id(NULLIF(current_setting('tma.workspace_id', true), ''))
  );

DROP POLICY IF EXISTS skill_marketplace_entries_organization_catalog_read
  ON skill_marketplace_entries;

CREATE POLICY skill_marketplace_entries_organization_catalog_read
  ON skill_marketplace_entries
  FOR SELECT
  USING (
    status = 'published'
    AND tma_workspaces_share_organization(
      skill_marketplace_entries.workspace_id,
      NULLIF(current_setting('tma.workspace_id', true), '')
    )
  );

DROP POLICY IF EXISTS skill_marketplace_policies_scope_isolation
  ON skill_marketplace_policies;

CREATE POLICY skill_marketplace_policies_scope_isolation
  ON skill_marketplace_policies
  FOR ALL
  USING (
    (scope_type = 'workspace' AND workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
    OR (
      scope_type = 'organization'
      AND organization_id = tma_workspace_organization_id(NULLIF(current_setting('tma.workspace_id', true), ''))
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
      AND organization_id = tma_workspace_organization_id(NULLIF(current_setting('tma.workspace_id', true), ''))
    )
  );

DROP POLICY IF EXISTS skill_asset_retention_policies_scope_isolation
  ON skill_asset_retention_policies;

CREATE POLICY skill_asset_retention_policies_scope_isolation
  ON skill_asset_retention_policies
  FOR ALL
  USING (
    (scope_type = 'workspace' AND workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
    OR (
      scope_type = 'organization'
      AND organization_id = tma_workspace_organization_id(NULLIF(current_setting('tma.workspace_id', true), ''))
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
      AND organization_id = tma_workspace_organization_id(NULLIF(current_setting('tma.workspace_id', true), ''))
    )
  );
