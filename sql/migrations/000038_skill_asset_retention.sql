CREATE TABLE IF NOT EXISTS skill_asset_retention_policies (
  id TEXT PRIMARY KEY,
  scope_type TEXT NOT NULL,
  organization_id TEXT REFERENCES organizations(id),
  workspace_id TEXT REFERENCES workspaces(id),
  status TEXT NOT NULL DEFAULT 'active',
  current_version INTEGER NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ,
  CONSTRAINT skill_asset_retention_policies_scope_check CHECK (
    (scope_type = 'organization' AND organization_id IS NOT NULL AND workspace_id IS NULL) OR
    (scope_type = 'workspace' AND workspace_id IS NOT NULL AND organization_id IS NULL)
  ),
  CONSTRAINT skill_asset_retention_policies_status_check CHECK (status IN ('active', 'archived')),
  CONSTRAINT skill_asset_retention_policies_version_check CHECK (current_version > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_asset_retention_policy_active_org
  ON skill_asset_retention_policies(organization_id)
  WHERE status = 'active' AND scope_type = 'organization';

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_asset_retention_policy_active_workspace
  ON skill_asset_retention_policies(workspace_id)
  WHERE status = 'active' AND scope_type = 'workspace';

CREATE TABLE IF NOT EXISTS skill_asset_retention_policy_versions (
  id TEXT PRIMARY KEY,
  policy_id TEXT NOT NULL REFERENCES skill_asset_retention_policies(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  config_json JSONB NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (policy_id, version),
  CONSTRAINT skill_asset_retention_policy_versions_version_check CHECK (version > 0)
);

CREATE INDEX IF NOT EXISTS idx_skill_asset_retention_policy_versions_policy
  ON skill_asset_retention_policy_versions(policy_id, version DESC);

CREATE TABLE IF NOT EXISTS skill_asset_gc_runs (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  policy_source TEXT NOT NULL,
  policy_id TEXT,
  policy_version INTEGER NOT NULL DEFAULT 0,
  policy_revision TEXT NOT NULL,
  retention_days INTEGER NOT NULL,
  delete_limit INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  candidate_count INTEGER NOT NULL DEFAULT 0,
  deleted_count INTEGER NOT NULL DEFAULT 0,
  skipped_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  bytes_deleted BIGINT NOT NULL DEFAULT 0,
  requested_by TEXT NOT NULL,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  CONSTRAINT skill_asset_gc_runs_status_check CHECK (status IN ('running', 'succeeded', 'partial', 'failed')),
  CONSTRAINT skill_asset_gc_runs_counts_check CHECK (
    candidate_count >= 0 AND deleted_count >= 0 AND skipped_count >= 0 AND failed_count >= 0 AND bytes_deleted >= 0
  )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_asset_gc_runs_active_workspace
  ON skill_asset_gc_runs(workspace_id) WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_skill_asset_gc_runs_workspace_started
  ON skill_asset_gc_runs(workspace_id, started_at DESC);

CREATE TABLE IF NOT EXISTS skill_asset_gc_items (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES skill_asset_gc_runs(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  skill_id TEXT NOT NULL DEFAULT '',
  skill_identifier TEXT NOT NULL DEFAULT '',
  skill_version_id TEXT NOT NULL DEFAULT '',
  skill_version INTEGER NOT NULL DEFAULT 0,
  asset_path TEXT NOT NULL DEFAULT '',
  object_ref_id TEXT NOT NULL,
  storage_provider TEXT NOT NULL,
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  object_version TEXT NOT NULL DEFAULT '',
  content_type TEXT NOT NULL DEFAULT '',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  scan_provider TEXT NOT NULL DEFAULT '',
  scan_version TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'candidate',
  reason TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  object_was_missing BOOLEAN NOT NULL DEFAULT FALSE,
  error_message TEXT NOT NULL DEFAULT '',
  eligible_at TIMESTAMPTZ NOT NULL,
  object_created_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ,
  UNIQUE (run_id, object_ref_id),
  CONSTRAINT skill_asset_gc_items_status_check CHECK (status IN ('candidate', 'deleting', 'deleted', 'skipped', 'failed')),
  CONSTRAINT skill_asset_gc_items_size_check CHECK (size_bytes >= 0),
  CONSTRAINT skill_asset_gc_items_attempts_check CHECK (attempts >= 0)
);

CREATE INDEX IF NOT EXISTS idx_skill_asset_gc_items_run_status
  ON skill_asset_gc_items(run_id, status, created_at);

CREATE TABLE IF NOT EXISTS skill_asset_gc_tombstones (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES skill_asset_gc_runs(id),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  skill_id TEXT NOT NULL DEFAULT '',
  skill_version_id TEXT NOT NULL DEFAULT '',
  asset_path TEXT NOT NULL DEFAULT '',
  object_ref_id TEXT NOT NULL UNIQUE,
  storage_provider TEXT NOT NULL,
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  object_version TEXT NOT NULL DEFAULT '',
  content_type TEXT NOT NULL DEFAULT '',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  scan_provider TEXT NOT NULL DEFAULT '',
  scan_version TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL,
  object_was_missing BOOLEAN NOT NULL DEFAULT FALSE,
  deleted_by TEXT NOT NULL,
  deleted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT skill_asset_gc_tombstones_size_check CHECK (size_bytes >= 0)
);

CREATE INDEX IF NOT EXISTS idx_skill_asset_gc_tombstones_workspace_deleted
  ON skill_asset_gc_tombstones(workspace_id, deleted_at DESC);

CREATE SEQUENCE IF NOT EXISTS tma_skill_asset_retention_policy_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_skill_asset_retention_policy_version_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_skill_asset_gc_run_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_skill_asset_gc_item_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_skill_asset_gc_tombstone_id_seq;
