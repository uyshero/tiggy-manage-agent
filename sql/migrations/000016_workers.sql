CREATE TABLE IF NOT EXISTS workers (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  name TEXT NOT NULL,
  worker_type TEXT NOT NULL DEFAULT 'local',
  status TEXT NOT NULL DEFAULT 'offline',
  capabilities_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  registered_by TEXT NOT NULL DEFAULT 'system',
  registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ,
  lease_expires_at TIMESTAMPTZ,
  archived_at TIMESTAMPTZ,
  CONSTRAINT workers_type_check CHECK (worker_type IN ('local', 'shared', 'cloud')),
  CONSTRAINT workers_status_check CHECK (status IN ('online', 'offline', 'draining', 'archived'))
);

CREATE INDEX IF NOT EXISTS idx_workers_workspace_status
  ON workers(workspace_id, status);

CREATE INDEX IF NOT EXISTS idx_workers_lease_expires
  ON workers(lease_expires_at);

CREATE SEQUENCE IF NOT EXISTS tma_worker_id_seq;

SELECT setval(
  'tma_worker_id_seq',
  GREATEST((SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) FROM workers), 1),
  (SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) > 0 FROM workers)
);

