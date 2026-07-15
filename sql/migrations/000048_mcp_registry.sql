CREATE TABLE IF NOT EXISTS mcp_registry_servers (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  identifier TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  current_version INTEGER NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT mcp_registry_servers_status_check CHECK (status IN ('active', 'disabled', 'archived')),
  CONSTRAINT mcp_registry_servers_version_check CHECK (current_version > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_registry_servers_workspace_identifier
  ON mcp_registry_servers(workspace_id, identifier)
  WHERE status <> 'archived';

CREATE TABLE IF NOT EXISTS mcp_registry_server_versions (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES mcp_registry_servers(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  config_json JSONB NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (server_id, version),
  CONSTRAINT mcp_registry_server_versions_version_check CHECK (version > 0)
);

CREATE INDEX IF NOT EXISTS idx_mcp_registry_server_versions_server
  ON mcp_registry_server_versions(server_id, version DESC);

CREATE SEQUENCE IF NOT EXISTS tma_mcp_registry_server_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_mcp_registry_version_id_seq;
