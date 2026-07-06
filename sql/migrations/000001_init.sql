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
  current_version INTEGER NOT NULL,
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_versions (
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  model TEXT NOT NULL,
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
  agent_version INTEGER NOT NULL,
  environment_id TEXT NOT NULL REFERENCES environments(id),
  status TEXT NOT NULL,
  title TEXT,
  sandbox_id TEXT,
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

INSERT INTO organizations (id, name)
VALUES ('org_default', 'Default Organization')
ON CONFLICT (id) DO NOTHING;

INSERT INTO workspaces (id, org_id, name)
VALUES ('wksp_default', 'org_default', 'Default Workspace')
ON CONFLICT (id) DO NOTHING;

