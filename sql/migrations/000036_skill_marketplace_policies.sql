CREATE TABLE IF NOT EXISTS skill_marketplace_policies (
  id TEXT PRIMARY KEY,
  scope_type TEXT NOT NULL,
  organization_id TEXT REFERENCES organizations(id),
  workspace_id TEXT REFERENCES workspaces(id),
  status TEXT NOT NULL DEFAULT 'active',
  current_version INTEGER NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ,
  CONSTRAINT skill_marketplace_policies_scope_check CHECK (
    (scope_type = 'organization' AND organization_id IS NOT NULL AND workspace_id IS NULL) OR
    (scope_type = 'workspace' AND workspace_id IS NOT NULL AND organization_id IS NULL)
  ),
  CONSTRAINT skill_marketplace_policies_status_check CHECK (status IN ('active', 'archived')),
  CONSTRAINT skill_marketplace_policies_version_check CHECK (current_version > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_marketplace_policy_active_org
  ON skill_marketplace_policies(organization_id)
  WHERE status = 'active' AND scope_type = 'organization';

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_marketplace_policy_active_workspace
  ON skill_marketplace_policies(workspace_id)
  WHERE status = 'active' AND scope_type = 'workspace';

CREATE TABLE IF NOT EXISTS skill_marketplace_policy_versions (
  id TEXT PRIMARY KEY,
  policy_id TEXT NOT NULL REFERENCES skill_marketplace_policies(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  config_json JSONB NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (policy_id, version),
  CONSTRAINT skill_marketplace_policy_versions_version_check CHECK (version > 0)
);

CREATE INDEX IF NOT EXISTS idx_skill_marketplace_policy_versions_policy
  ON skill_marketplace_policy_versions(policy_id, version DESC);

CREATE SEQUENCE IF NOT EXISTS tma_skill_marketplace_policy_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_skill_marketplace_policy_version_id_seq;
