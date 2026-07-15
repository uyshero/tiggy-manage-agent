ALTER TABLE skills ENABLE ROW LEVEL SECURITY;
ALTER TABLE skills FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skills_workspace_isolation ON skills;

CREATE POLICY skills_workspace_isolation
  ON skills
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  );

ALTER TABLE skill_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_versions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_versions_skill_isolation ON skill_versions;

CREATE POLICY skill_versions_skill_isolation
  ON skill_versions
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM skills
      WHERE skills.id = skill_versions.skill_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM skills
      WHERE skills.id = skill_versions.skill_id
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

ALTER TABLE skill_version_package_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_version_package_files FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS skill_version_package_files_version_isolation
  ON skill_version_package_files;

CREATE POLICY skill_version_package_files_version_isolation
  ON skill_version_package_files
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM skill_versions
      WHERE skill_versions.id = skill_version_package_files.skill_version_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM skill_versions
      JOIN skills ON skills.id = skill_versions.skill_id
      JOIN object_refs ON object_refs.id = skill_version_package_files.object_ref_id
      WHERE skill_versions.id = skill_version_package_files.skill_version_id
        AND object_refs.workspace_id = skills.workspace_id
    )
  );

ALTER TABLE session_turn_skill_usages ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_turn_skill_usages FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_turn_skill_usages_session_isolation
  ON session_turn_skill_usages;

CREATE POLICY session_turn_skill_usages_session_isolation
  ON session_turn_skill_usages
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_turn_skill_usages.session_id
        AND sessions.workspace_id = session_turn_skill_usages.workspace_id
        AND sessions.agent_id = session_turn_skill_usages.agent_id
        AND sessions.agent_config_version = session_turn_skill_usages.agent_config_version
    )
    AND EXISTS (
      SELECT 1
      FROM skills
      JOIN skill_versions
        ON skill_versions.skill_id = skills.id
        AND skill_versions.version = session_turn_skill_usages.skill_version
      WHERE skills.id = session_turn_skill_usages.skill_id
        AND skills.workspace_id = session_turn_skill_usages.workspace_id
        AND skills.identifier = session_turn_skill_usages.skill_identifier
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = session_turn_skill_usages.session_id
        AND sessions.workspace_id = session_turn_skill_usages.workspace_id
        AND sessions.agent_id = session_turn_skill_usages.agent_id
        AND sessions.agent_config_version = session_turn_skill_usages.agent_config_version
    )
    AND EXISTS (
      SELECT 1
      FROM skills
      JOIN skill_versions
        ON skill_versions.skill_id = skills.id
        AND skill_versions.version = session_turn_skill_usages.skill_version
      WHERE skills.id = session_turn_skill_usages.skill_id
        AND skills.workspace_id = session_turn_skill_usages.workspace_id
        AND skills.identifier = session_turn_skill_usages.skill_identifier
    )
  );
