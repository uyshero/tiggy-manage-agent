CREATE TABLE IF NOT EXISTS worker_work (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  worker_id TEXT REFERENCES workers(id),
  environment_id TEXT REFERENCES environments(id),
  session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
  turn_id TEXT NOT NULL DEFAULT '',
  work_type TEXT NOT NULL DEFAULT 'tool_execution',
  status TEXT NOT NULL DEFAULT 'pending',
  payload_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  result_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  error_message TEXT NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  CONSTRAINT worker_work_status_check CHECK (status IN ('pending', 'leased', 'running', 'completed', 'failed', 'canceled')),
  CONSTRAINT worker_work_type_check CHECK (work_type IN ('tool_execution', 'sandbox_command', 'artifact_sync'))
);

CREATE INDEX IF NOT EXISTS idx_worker_work_worker_status
  ON worker_work(worker_id, status, created_at);

CREATE INDEX IF NOT EXISTS idx_worker_work_workspace_status
  ON worker_work(workspace_id, status, created_at);

CREATE INDEX IF NOT EXISTS idx_worker_work_session_turn
  ON worker_work(session_id, turn_id);

CREATE SEQUENCE IF NOT EXISTS tma_worker_work_id_seq;

SELECT setval(
  'tma_worker_work_id_seq',
  GREATEST((SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) FROM worker_work), 1),
  (SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) > 0 FROM worker_work)
);

