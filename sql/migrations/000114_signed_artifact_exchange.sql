CREATE SEQUENCE IF NOT EXISTS tma_artifact_exchange_id_seq;

CREATE TABLE IF NOT EXISTS artifact_exchanges (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  app_id TEXT,
  owner_id TEXT NOT NULL,
  direction TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  session_id TEXT,
  object_ref_id TEXT,
  artifact_id TEXT,
  filename TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  artifact_type TEXT NOT NULL DEFAULT 'file',
  environment_id TEXT,
  turn_id TEXT NOT NULL DEFAULT '',
  tool_call_id TEXT NOT NULL DEFAULT '',
  visibility TEXT NOT NULL DEFAULT 'session',
  content_type TEXT NOT NULL DEFAULT '',
  expected_size_bytes BIGINT,
  max_size_bytes BIGINT NOT NULL,
  expected_checksum_sha256 TEXT NOT NULL DEFAULT '',
  token_hash BYTEA NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  claimed_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  error_message TEXT NOT NULL DEFAULT '',
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT artifact_exchanges_direction_check CHECK (direction IN ('import', 'export')),
  CONSTRAINT artifact_exchanges_status_check CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'expired')),
  CONSTRAINT artifact_exchanges_filename_check CHECK (length(btrim(filename)) BETWEEN 1 AND 512),
  CONSTRAINT artifact_exchanges_artifact_type_check CHECK (artifact_type IN ('file', 'snapshot', 'asset')),
  CONSTRAINT artifact_exchanges_visibility_check CHECK (visibility IN ('workspace', 'session')),
  CONSTRAINT artifact_exchanges_size_check CHECK (
    max_size_bytes >= 0
    AND expected_size_bytes IS NULL OR expected_size_bytes >= 0 AND expected_size_bytes <= max_size_bytes
  ),
  CONSTRAINT artifact_exchanges_checksum_check CHECK (
    expected_checksum_sha256 = '' OR expected_checksum_sha256 ~ '^[0-9a-f]{64}$'
  ),
  CONSTRAINT artifact_exchanges_token_hash_check CHECK (octet_length(token_hash) = 32),
  CONSTRAINT artifact_exchanges_metadata_check CHECK (jsonb_typeof(metadata_json) = 'object'),
  CONSTRAINT artifact_exchanges_target_check CHECK (
    (direction = 'import' AND session_id IS NOT NULL AND (
      (status = 'completed' AND object_ref_id IS NOT NULL AND artifact_id IS NOT NULL)
      OR (status <> 'completed' AND object_ref_id IS NULL AND artifact_id IS NULL)
    ))
    OR
    (direction = 'export' AND object_ref_id IS NOT NULL)
  )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_artifact_exchanges_token_hash
  ON artifact_exchanges(token_hash);

CREATE INDEX IF NOT EXISTS idx_artifact_exchanges_workspace_owner
  ON artifact_exchanges(workspace_id, owner_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_artifact_exchanges_expiry
  ON artifact_exchanges(expires_at, id)
  WHERE status = 'pending';

ALTER TABLE artifact_exchanges ENABLE ROW LEVEL SECURITY;
ALTER TABLE artifact_exchanges FORCE ROW LEVEL SECURITY;

CREATE POLICY artifact_exchanges_workspace_isolation ON artifact_exchanges
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
