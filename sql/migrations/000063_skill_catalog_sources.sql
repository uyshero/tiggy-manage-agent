ALTER TABLE skills
  DROP CONSTRAINT IF EXISTS skills_source_type_check;

ALTER TABLE skills
  ADD CONSTRAINT skills_source_type_check
  CHECK (source_type IN ('inline', 'github', 'artifact', 'catalog', 'plugin', 'builtin'));

CREATE OR REPLACE FUNCTION tma_skill_catalog_version_visible(
  requested_skill_id TEXT,
  requested_skill_version INTEGER
)
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
      FROM public.skill_marketplace_entries entry
      JOIN public.workspaces publisher_workspace
        ON publisher_workspace.id = entry.workspace_id
      JOIN public.workspaces consumer_workspace
        ON consumer_workspace.id = NULLIF(current_setting('tma.workspace_id', true), '')
        AND consumer_workspace.org_id = publisher_workspace.org_id
      WHERE entry.skill_id = requested_skill_id
        AND (requested_skill_version IS NULL OR entry.skill_version = requested_skill_version)
        AND entry.status = 'published'
    );
END;
$$;

DROP POLICY IF EXISTS skills_published_catalog_read ON skills;

CREATE POLICY skills_published_catalog_read
  ON skills
  FOR SELECT
  USING (
    status = 'active'
    AND tma_skill_catalog_version_visible(skills.id, NULL)
  );

DROP POLICY IF EXISTS skill_versions_skill_isolation ON skill_versions;

CREATE POLICY skill_versions_skill_isolation
  ON skill_versions
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM skills
      WHERE skills.id = skill_versions.skill_id
        AND skills.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM skills
      WHERE skills.id = skill_versions.skill_id
        AND skills.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
        AND (
          skill_versions.package_object_ref_id IS NULL
          OR EXISTS (
            SELECT 1
            FROM object_refs
            WHERE object_refs.id = skill_versions.package_object_ref_id
              AND object_refs.workspace_id = skills.workspace_id
          )
        )
        AND (
          skill_versions.skill_md_object_ref_id IS NULL
          OR EXISTS (
            SELECT 1
            FROM object_refs
            WHERE object_refs.id = skill_versions.skill_md_object_ref_id
              AND object_refs.workspace_id = skills.workspace_id
          )
        )
    )
  );

DROP POLICY IF EXISTS skill_versions_published_catalog_read ON skill_versions;

CREATE POLICY skill_versions_published_catalog_read
  ON skill_versions
  FOR SELECT
  USING (
    tma_skill_catalog_version_visible(skill_versions.skill_id, skill_versions.version)
  );

DROP POLICY IF EXISTS skill_version_package_files_version_isolation
  ON skill_version_package_files;

CREATE POLICY skill_version_package_files_version_isolation
  ON skill_version_package_files
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM skill_versions
      JOIN skills ON skills.id = skill_versions.skill_id
      WHERE skill_versions.id = skill_version_package_files.skill_version_id
        AND skills.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM skill_versions
      JOIN skills ON skills.id = skill_versions.skill_id
      JOIN object_refs ON object_refs.id = skill_version_package_files.object_ref_id
      WHERE skill_versions.id = skill_version_package_files.skill_version_id
        AND skills.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
        AND object_refs.workspace_id = skills.workspace_id
    )
  );

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

DROP POLICY IF EXISTS skill_marketplace_entries_organization_catalog_read
  ON skill_marketplace_entries;

CREATE POLICY skill_marketplace_entries_organization_catalog_read
  ON skill_marketplace_entries
  FOR SELECT
  USING (
    status = 'published'
    AND EXISTS (
      SELECT 1
      FROM workspaces publisher_workspace
      JOIN workspaces consumer_workspace
        ON consumer_workspace.id = NULLIF(current_setting('tma.workspace_id', true), '')
        AND consumer_workspace.org_id = publisher_workspace.org_id
      WHERE publisher_workspace.id = skill_marketplace_entries.workspace_id
    )
  );
