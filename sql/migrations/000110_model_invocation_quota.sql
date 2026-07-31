CREATE TABLE IF NOT EXISTS model_invocation_quota_buckets (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  quota_scope TEXT NOT NULL,
  actor_id TEXT NOT NULL DEFAULT '',
  capability TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  model TEXT NOT NULL,
  window_started_at TIMESTAMPTZ NOT NULL,
  request_count INTEGER NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, quota_scope, actor_id, capability, provider_id, model),
  CONSTRAINT model_invocation_quota_scope_check CHECK (quota_scope IN ('workspace', 'identity')),
  CONSTRAINT model_invocation_quota_actor_check CHECK (
    (quota_scope = 'workspace' AND actor_id = '') OR
    (quota_scope = 'identity' AND btrim(actor_id) <> '')
  ),
  CONSTRAINT model_invocation_quota_capability_check CHECK (
    capability IN ('generate', 'embedding', 'rerank', 'speech_to_text', 'text_to_speech')
  ),
  CONSTRAINT model_invocation_quota_route_check CHECK (btrim(provider_id) <> '' AND btrim(model) <> ''),
  CONSTRAINT model_invocation_quota_count_check CHECK (request_count > 0)
);

CREATE INDEX IF NOT EXISTS model_invocation_quota_buckets_updated_idx
  ON model_invocation_quota_buckets(updated_at);

ALTER TABLE model_invocation_quota_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_invocation_quota_buckets FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS model_invocation_quota_buckets_workspace_isolation ON model_invocation_quota_buckets;
CREATE POLICY model_invocation_quota_buckets_workspace_isolation
  ON model_invocation_quota_buckets
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
