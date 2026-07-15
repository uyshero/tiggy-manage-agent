ALTER TABLE agent_config_versions
  ADD COLUMN IF NOT EXISTS mcp_json JSONB NOT NULL DEFAULT 'null'::jsonb;
