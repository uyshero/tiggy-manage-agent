ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS parent_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS parent_turn_id TEXT,
  ADD COLUMN IF NOT EXISTS spawn_depth INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_sessions_parent_session_id
  ON sessions(parent_session_id, created_at DESC);
