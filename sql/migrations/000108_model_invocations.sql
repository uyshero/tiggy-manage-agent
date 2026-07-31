CREATE SEQUENCE IF NOT EXISTS tma_model_invocation_id_seq;

CREATE TABLE IF NOT EXISTS model_invocations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  principal_id TEXT NOT NULL,
  auth_type TEXT NOT NULL DEFAULT '',
  request_id TEXT NOT NULL,
  capability TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  provider_type TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL,
  status TEXT NOT NULL,
  error_code TEXT NOT NULL DEFAULT '',
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  total_tokens BIGINT NOT NULL DEFAULT 0,
  cached_input_tokens BIGINT NOT NULL DEFAULT 0,
  reasoning_tokens BIGINT NOT NULL DEFAULT 0,
  input_items BIGINT NOT NULL DEFAULT 0,
  output_items BIGINT NOT NULL DEFAULT 0,
  input_bytes BIGINT NOT NULL DEFAULT 0,
  output_bytes BIGINT NOT NULL DEFAULT 0,
  input_characters BIGINT NOT NULL DEFAULT 0,
  output_characters BIGINT NOT NULL DEFAULT 0,
  input_audio_ms BIGINT NOT NULL DEFAULT 0,
  output_audio_ms BIGINT NOT NULL DEFAULT 0,
  latency_ms BIGINT NOT NULL DEFAULT 0,
  started_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT model_invocations_principal_check CHECK (btrim(principal_id) <> ''),
  CONSTRAINT model_invocations_request_check CHECK (btrim(request_id) <> ''),
  CONSTRAINT model_invocations_route_check CHECK (btrim(provider_id) <> '' AND btrim(model) <> ''),
  CONSTRAINT model_invocations_capability_check CHECK (capability IN ('generate', 'embedding', 'rerank', 'speech_to_text', 'text_to_speech')),
  CONSTRAINT model_invocations_status_check CHECK (status IN ('completed', 'failed', 'canceled')),
  CONSTRAINT model_invocations_error_check CHECK ((status = 'failed' AND btrim(error_code) <> '') OR (status <> 'failed' AND error_code = '')),
  CONSTRAINT model_invocations_usage_check CHECK (
    input_tokens >= 0 AND output_tokens >= 0 AND total_tokens >= 0 AND
    cached_input_tokens >= 0 AND reasoning_tokens >= 0 AND
    input_items >= 0 AND output_items >= 0 AND input_bytes >= 0 AND output_bytes >= 0 AND
    input_characters >= 0 AND output_characters >= 0 AND input_audio_ms >= 0 AND output_audio_ms >= 0 AND latency_ms >= 0
  ),
  CONSTRAINT model_invocations_time_check CHECK (completed_at >= started_at)
);

CREATE INDEX IF NOT EXISTS model_invocations_workspace_started_idx
  ON model_invocations(workspace_id, started_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS model_invocations_workspace_route_idx
  ON model_invocations(workspace_id, provider_id, model, started_at DESC);

CREATE INDEX IF NOT EXISTS model_invocations_workspace_principal_idx
  ON model_invocations(workspace_id, principal_id, started_at DESC);

ALTER TABLE model_invocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_invocations FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS model_invocations_workspace_isolation ON model_invocations;
CREATE POLICY model_invocations_workspace_isolation
  ON model_invocations
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
