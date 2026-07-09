CREATE TABLE IF NOT EXISTS object_refs (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  storage_provider TEXT NOT NULL DEFAULT 's3',
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  object_version TEXT NOT NULL DEFAULT '',
  content_type TEXT NOT NULL DEFAULT '',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  etag TEXT NOT NULL DEFAULT '',
  visibility TEXT NOT NULL DEFAULT 'workspace',
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT NOT NULL DEFAULT 'system',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT object_refs_size_check CHECK (size_bytes >= 0),
  CONSTRAINT object_refs_visibility_check CHECK (visibility IN ('session', 'workspace'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_object_refs_location
  ON object_refs(storage_provider, bucket, object_key, object_version);

CREATE INDEX IF NOT EXISTS idx_object_refs_workspace_created
  ON object_refs(workspace_id, created_at);

CREATE SEQUENCE IF NOT EXISTS tma_object_ref_id_seq;

SELECT setval(
  'tma_object_ref_id_seq',
  GREATEST((SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) FROM object_refs), 1),
  (SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) > 0 FROM object_refs)
);

CREATE TABLE IF NOT EXISTS session_artifacts (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  environment_id TEXT REFERENCES environments(id),
  object_ref_id TEXT NOT NULL REFERENCES object_refs(id),
  turn_id TEXT NOT NULL DEFAULT '',
  tool_call_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  artifact_type TEXT NOT NULL DEFAULT 'file',
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT NOT NULL DEFAULT 'system',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT session_artifacts_type_check CHECK (artifact_type IN ('file', 'snapshot', 'asset'))
);

CREATE INDEX IF NOT EXISTS idx_session_artifacts_session_created
  ON session_artifacts(session_id, created_at);

CREATE INDEX IF NOT EXISTS idx_session_artifacts_workspace_created
  ON session_artifacts(workspace_id, created_at);

CREATE INDEX IF NOT EXISTS idx_session_artifacts_object_ref
  ON session_artifacts(object_ref_id);

CREATE INDEX IF NOT EXISTS idx_session_artifacts_turn
  ON session_artifacts(session_id, turn_id);

CREATE SEQUENCE IF NOT EXISTS tma_session_artifact_id_seq;

SELECT setval(
  'tma_session_artifact_id_seq',
  GREATEST((SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) FROM session_artifacts), 1),
  (SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) > 0 FROM session_artifacts)
);
