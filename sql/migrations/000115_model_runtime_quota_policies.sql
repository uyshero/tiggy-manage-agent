CREATE SEQUENCE IF NOT EXISTS tma_model_runtime_quota_policy_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_model_runtime_quota_policy_version_id_seq;

ALTER TABLE service_identities
  ADD CONSTRAINT service_identities_workspace_id_id_key UNIQUE (workspace_id, id);

CREATE TABLE IF NOT EXISTS model_runtime_quota_policies (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scope TEXT NOT NULL,
  app_id TEXT,
  plan TEXT NOT NULL,
  config_json JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  revision BIGINT NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL,
  updated_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ,
  CONSTRAINT model_runtime_quota_policies_scope_check CHECK (scope IN ('workspace', 'application')),
  CONSTRAINT model_runtime_quota_policies_target_check CHECK (
    (scope = 'workspace' AND app_id IS NULL) OR (scope = 'application' AND app_id IS NOT NULL)
  ),
  CONSTRAINT model_runtime_quota_policies_app_fk FOREIGN KEY (workspace_id, app_id)
    REFERENCES service_identities(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT model_runtime_quota_policies_plan_check CHECK (length(btrim(plan)) BETWEEN 1 AND 64),
  CONSTRAINT model_runtime_quota_policies_config_check CHECK (jsonb_typeof(config_json) = 'object'),
  CONSTRAINT model_runtime_quota_policies_status_check CHECK (status IN ('active', 'archived')),
  CONSTRAINT model_runtime_quota_policies_revision_check CHECK (revision > 0),
  CONSTRAINT model_runtime_quota_policies_actor_check CHECK (btrim(created_by) <> '' AND btrim(updated_by) <> ''),
  CONSTRAINT model_runtime_quota_policies_archive_check CHECK ((status = 'archived') = (archived_at IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS model_runtime_quota_policies_workspace_unique
  ON model_runtime_quota_policies(workspace_id)
  WHERE scope = 'workspace';
CREATE UNIQUE INDEX IF NOT EXISTS model_runtime_quota_policies_application_unique
  ON model_runtime_quota_policies(workspace_id, app_id)
  WHERE scope = 'application';
CREATE INDEX IF NOT EXISTS model_runtime_quota_policies_workspace_status_idx
  ON model_runtime_quota_policies(workspace_id, status, scope, app_id);

CREATE TABLE IF NOT EXISTS model_runtime_quota_policy_versions (
  id TEXT PRIMARY KEY,
  policy_id TEXT NOT NULL REFERENCES model_runtime_quota_policies(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scope TEXT NOT NULL,
  app_id TEXT,
  plan TEXT NOT NULL,
  config_json JSONB NOT NULL,
  status TEXT NOT NULL,
  revision BIGINT NOT NULL,
  changed_by TEXT NOT NULL,
  changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT model_runtime_quota_policy_versions_scope_check CHECK (scope IN ('workspace', 'application')),
  CONSTRAINT model_runtime_quota_policy_versions_target_check CHECK (
    (scope = 'workspace' AND app_id IS NULL) OR (scope = 'application' AND app_id IS NOT NULL)
  ),
  CONSTRAINT model_runtime_quota_policy_versions_plan_check CHECK (length(btrim(plan)) BETWEEN 1 AND 64),
  CONSTRAINT model_runtime_quota_policy_versions_config_check CHECK (jsonb_typeof(config_json) = 'object'),
  CONSTRAINT model_runtime_quota_policy_versions_status_check CHECK (status IN ('active', 'archived')),
  CONSTRAINT model_runtime_quota_policy_versions_revision_check CHECK (revision > 0),
  CONSTRAINT model_runtime_quota_policy_versions_actor_check CHECK (btrim(changed_by) <> ''),
  UNIQUE (policy_id, revision)
);

CREATE INDEX IF NOT EXISTS model_runtime_quota_policy_versions_workspace_policy_idx
  ON model_runtime_quota_policy_versions(workspace_id, policy_id, revision DESC);

ALTER TABLE model_runtime_quota_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_runtime_quota_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE model_runtime_quota_policy_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_runtime_quota_policy_versions FORCE ROW LEVEL SECURITY;

CREATE POLICY model_runtime_quota_policies_workspace_isolation ON model_runtime_quota_policies
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

CREATE POLICY model_runtime_quota_policy_versions_workspace_isolation ON model_runtime_quota_policy_versions
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
