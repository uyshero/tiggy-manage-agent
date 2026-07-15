ALTER TABLE mcp_registry_servers ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_registry_servers FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS mcp_registry_servers_workspace_isolation
  ON mcp_registry_servers;

CREATE POLICY mcp_registry_servers_workspace_isolation
  ON mcp_registry_servers
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  );

ALTER TABLE mcp_registry_server_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_registry_server_versions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS mcp_registry_server_versions_workspace_isolation
  ON mcp_registry_server_versions;

CREATE POLICY mcp_registry_server_versions_workspace_isolation
  ON mcp_registry_server_versions
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM mcp_registry_servers
      WHERE mcp_registry_servers.id = mcp_registry_server_versions.server_id
        AND mcp_registry_servers.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM mcp_registry_servers
      WHERE mcp_registry_servers.id = mcp_registry_server_versions.server_id
        AND mcp_registry_servers.workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    )
  );
