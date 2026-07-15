ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS tags_json JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX IF NOT EXISTS idx_sessions_workspace_pinned_created
  ON sessions(workspace_id, pinned_at DESC NULLS LAST, created_at DESC);
