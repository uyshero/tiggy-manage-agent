CREATE TABLE IF NOT EXISTS workspace_tool_permission_policies (
  workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  policy_json JSONB NOT NULL DEFAULT '{"permission_rules":[]}'::jsonb,
  revision BIGINT NOT NULL DEFAULT 1,
  updated_by TEXT NOT NULL DEFAULT 'system',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT workspace_tool_permission_policies_object_check
    CHECK (jsonb_typeof(policy_json) = 'object'),
  CONSTRAINT workspace_tool_permission_policies_rules_check
    CHECK (
      NOT (policy_json ? 'permission_rules')
      OR jsonb_typeof(policy_json->'permission_rules') = 'array'
    ),
  CONSTRAINT workspace_tool_permission_policies_revision_check
    CHECK (revision > 0)
);

ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS runtime_settings_revision BIGINT NOT NULL DEFAULT 1;

ALTER TABLE sessions
  DROP CONSTRAINT IF EXISTS sessions_runtime_settings_revision_check;

ALTER TABLE sessions
  ADD CONSTRAINT sessions_runtime_settings_revision_check
  CHECK (runtime_settings_revision > 0);

ALTER TABLE workspace_tool_permission_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_tool_permission_policies FORCE ROW LEVEL SECURITY;

CREATE POLICY workspace_tool_permission_policies_isolation
  ON workspace_tool_permission_policies
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
