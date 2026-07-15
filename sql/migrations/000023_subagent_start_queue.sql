CREATE TABLE IF NOT EXISTS subagent_start_requests (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  owner_id TEXT NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  parent_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  parent_turn_id TEXT NOT NULL DEFAULT '',
  payload_json JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  priority INTEGER NOT NULL DEFAULT 0,
  workspace_active_limit INTEGER NOT NULL DEFAULT 0,
  user_active_limit INTEGER NOT NULL DEFAULT 0,
  queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  started_at TIMESTAMPTZ,
  turn_id TEXT,
  CONSTRAINT subagent_start_requests_status_check CHECK (status IN ('pending', 'started', 'canceled', 'expired'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_subagent_start_requests_session_pending
  ON subagent_start_requests(session_id)
  WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_subagent_start_requests_pending
  ON subagent_start_requests(status, priority DESC, queued_at ASC)
  WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_subagent_start_requests_workspace_pending
  ON subagent_start_requests(workspace_id, status, queued_at ASC);

CREATE INDEX IF NOT EXISTS idx_subagent_start_requests_owner_pending
  ON subagent_start_requests(workspace_id, owner_id, status, queued_at ASC);

CREATE SEQUENCE IF NOT EXISTS tma_subagent_start_request_id_seq;
