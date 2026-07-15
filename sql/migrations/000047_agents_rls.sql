ALTER TABLE agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE agents FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agents_workspace_isolation ON agents;

CREATE POLICY agents_workspace_isolation
  ON agents
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  );

ALTER TABLE agent_config_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_config_versions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agent_config_versions_workspace_isolation
  ON agent_config_versions;

CREATE POLICY agent_config_versions_workspace_isolation
  ON agent_config_versions
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM agents
      WHERE agents.id = agent_config_versions.agent_id
        AND agents.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM agents
      WHERE agents.id = agent_config_versions.agent_id
        AND agents.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    )
  );
