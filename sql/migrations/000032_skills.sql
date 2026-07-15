CREATE TABLE IF NOT EXISTS skills (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  identifier TEXT NOT NULL,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  owner_type TEXT NOT NULL DEFAULT 'workspace',
  source_plugin_id TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ,
  UNIQUE (workspace_id, identifier),
  CONSTRAINT skills_owner_type_check CHECK (owner_type IN ('builtin', 'workspace', 'plugin')),
  CONSTRAINT skills_status_check CHECK (status IN ('active', 'archived'))
);

CREATE TABLE IF NOT EXISTS skill_versions (
  id TEXT PRIMARY KEY,
  skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  content_format TEXT NOT NULL DEFAULT 'hybrid',
  manifest_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  content_text TEXT NOT NULL DEFAULT '',
  assets_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  checksum_sha256 TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (skill_id, version),
  CONSTRAINT skill_versions_version_check CHECK (version > 0),
  CONSTRAINT skill_versions_content_format_check CHECK (content_format IN ('markdown', 'json', 'hybrid'))
);

CREATE TABLE IF NOT EXISTS session_turn_skill_usages (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  turn_id TEXT NOT NULL,
  agent_id TEXT NOT NULL REFERENCES agents(id),
  agent_config_version INTEGER NOT NULL,
  skill_id TEXT NOT NULL REFERENCES skills(id),
  skill_identifier TEXT NOT NULL,
  skill_version INTEGER NOT NULL,
  requested_mode TEXT NOT NULL,
  rendered_mode TEXT NOT NULL DEFAULT '',
  priority INTEGER NOT NULL DEFAULT 100,
  estimated_tokens INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  failure_reason TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, turn_id, skill_id),
  CONSTRAINT session_turn_skill_usage_status_check CHECK (status IN ('resolved', 'degraded', 'skipped', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_skills_workspace_status
  ON skills(workspace_id, status, identifier);

CREATE INDEX IF NOT EXISTS idx_skill_versions_skill
  ON skill_versions(skill_id, version DESC);

CREATE INDEX IF NOT EXISTS idx_session_turn_skill_usages_turn
  ON session_turn_skill_usages(session_id, turn_id);

CREATE SEQUENCE IF NOT EXISTS tma_skill_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_skill_version_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_skill_usage_id_seq;
