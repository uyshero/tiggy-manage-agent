-- TMA P1 schema.
-- Apply with:
--   psql "$TMA_DATABASE_URL" -f sql/migrations/000001_init.sql

CREATE TABLE IF NOT EXISTS organizations (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES organizations(id),
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  name TEXT NOT NULL,
  current_config_version INTEGER NOT NULL,
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS llm_providers (
  id TEXT PRIMARY KEY,
  provider_type TEXT NOT NULL,
  base_url TEXT NOT NULL DEFAULT '',
  api_key_env TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS llm_models (
  provider_id TEXT NOT NULL REFERENCES llm_providers(id) ON DELETE CASCADE,
  model TEXT NOT NULL,
  context_window_tokens INTEGER NOT NULL DEFAULT 128000,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (provider_id, model)
);

CREATE TABLE IF NOT EXISTS agent_config_versions (
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  llm_provider TEXT NOT NULL DEFAULT 'fake' REFERENCES llm_providers(id),
  llm_model TEXT NOT NULL,
  system TEXT NOT NULL DEFAULT '',
  tools_json JSONB NOT NULL DEFAULT 'null'::jsonb,
  skills_json JSONB NOT NULL DEFAULT 'null'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (agent_id, version)
);

CREATE TABLE IF NOT EXISTS environments (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  name TEXT NOT NULL,
  config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  agent_id TEXT NOT NULL REFERENCES agents(id),
  agent_config_version INTEGER NOT NULL,
  environment_id TEXT NOT NULL REFERENCES environments(id),
  status TEXT NOT NULL,
  title TEXT,
  sandbox_id TEXT,
  runtime_settings_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  worker_node_id TEXT,
  idle_expires_at TIMESTAMPTZ,
  running_since TIMESTAMPTZ,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ,
  CONSTRAINT sessions_status_check CHECK (
    status IN (
      'provisioning',
      'idle',
      'running',
      'interrupting',
      'compacting',
      'failed',
      'terminated'
    )
  )
);

CREATE TABLE IF NOT EXISTS session_events (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  seq BIGINT NOT NULL,
  type TEXT NOT NULL,
  payload_json JSONB NOT NULL DEFAULT 'null'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, seq)
);

CREATE TABLE IF NOT EXISTS session_summaries (
  session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  summary_text TEXT NOT NULL DEFAULT '',
  source_until_seq BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS llm_usage_records (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  agent_id TEXT NOT NULL REFERENCES agents(id),
  agent_config_version INTEGER NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  turn_id TEXT NOT NULL,
  provider_id TEXT NOT NULL REFERENCES llm_providers(id),
  provider_type TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL,
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  total_tokens BIGINT NOT NULL DEFAULT 0,
  cached_input_tokens BIGINT NOT NULL DEFAULT 0,
  reasoning_tokens BIGINT NOT NULL DEFAULT 0,
  latency_ms BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  error_message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_workspaces_org_id
  ON workspaces(org_id);

CREATE INDEX IF NOT EXISTS idx_agents_workspace_id
  ON agents(workspace_id);

CREATE INDEX IF NOT EXISTS idx_environments_workspace_id
  ON environments(workspace_id);

CREATE INDEX IF NOT EXISTS idx_sessions_workspace_status
  ON sessions(workspace_id, status);

CREATE INDEX IF NOT EXISTS idx_sessions_agent_id
  ON sessions(agent_id);

CREATE INDEX IF NOT EXISTS idx_session_events_session_seq
  ON session_events(session_id, seq);

CREATE INDEX IF NOT EXISTS idx_llm_usage_session_turn
  ON llm_usage_records(session_id, turn_id);

CREATE INDEX IF NOT EXISTS idx_llm_usage_provider_model
  ON llm_usage_records(provider_id, model);

CREATE INDEX IF NOT EXISTS idx_llm_usage_created_at
  ON llm_usage_records(created_at);

INSERT INTO organizations (id, name)
VALUES ('org_default', 'Default Organization')
ON CONFLICT (id) DO NOTHING;

INSERT INTO workspaces (id, org_id, name)
VALUES ('wksp_default', 'org_default', 'Default Workspace')
ON CONFLICT (id) DO NOTHING;

INSERT INTO llm_providers (id, provider_type, base_url, api_key_env, enabled)
VALUES ('fake', 'fake', '', '', TRUE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO llm_models (provider_id, model, context_window_tokens)
VALUES ('fake', 'fake-demo', 128000)
ON CONFLICT (provider_id, model) DO NOTHING;
