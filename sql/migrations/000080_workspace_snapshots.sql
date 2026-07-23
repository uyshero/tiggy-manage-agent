CREATE SEQUENCE IF NOT EXISTS tma_workspace_snapshot_id_seq;

CREATE TABLE IF NOT EXISTS workspace_snapshots (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  sequence BIGINT NOT NULL,
  object_ref_id TEXT NOT NULL REFERENCES object_refs(id),
  checksum_sha256 TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  file_count INTEGER NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, sequence),
  CONSTRAINT workspace_snapshots_sequence_check CHECK (sequence > 0),
  CONSTRAINT workspace_snapshots_size_check CHECK (size_bytes >= 0),
  CONSTRAINT workspace_snapshots_file_count_check CHECK (file_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_workspace_snapshots_session_latest
  ON workspace_snapshots(session_id, sequence DESC);

ALTER TABLE workspace_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_snapshots FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS workspace_snapshots_session_isolation ON workspace_snapshots;
CREATE POLICY workspace_snapshots_session_isolation
  ON workspace_snapshots
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1 FROM sessions
      WHERE sessions.id = workspace_snapshots.session_id
        AND sessions.workspace_id = workspace_snapshots.workspace_id
        AND (
          NULLIF(current_setting('tma.owner_id', true), '') IS NULL
          OR sessions.owner_id = NULLIF(current_setting('tma.owner_id', true), '')
        )
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1 FROM sessions
      WHERE sessions.id = workspace_snapshots.session_id
        AND sessions.workspace_id = workspace_snapshots.workspace_id
        AND (
          NULLIF(current_setting('tma.owner_id', true), '') IS NULL
          OR sessions.owner_id = NULLIF(current_setting('tma.owner_id', true), '')
        )
    )
  );
