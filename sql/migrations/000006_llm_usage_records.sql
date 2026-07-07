CREATE TABLE IF NOT EXISTS llm_usage_records (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  agent_id TEXT NOT NULL REFERENCES agents(id),
  agent_config_version INTEGER NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  turn_id TEXT NOT NULL,
  provider_id TEXT NOT NULL REFERENCES llm_providers(id),
  provider_type TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL,
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  total_tokens BIGINT NOT NULL DEFAULT 0,
  cached_input_tokens BIGINT NOT NULL DEFAULT 0,
  reasoning_tokens BIGINT NOT NULL DEFAULT 0,
  latency_ms BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  error_message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE SEQUENCE IF NOT EXISTS tma_llm_usage_id_seq;

INSERT INTO llm_usage_records (
  id,
  workspace_id,
  agent_id,
  agent_config_version,
  session_id,
  turn_id,
  provider_id,
  provider_type,
  model,
  input_tokens,
  output_tokens,
  total_tokens,
  cached_input_tokens,
  reasoning_tokens,
  latency_ms,
  status,
  error_message,
  created_at
)
SELECT
  'llmu_legacy',
  'wksp_default',
  'agt_legacy',
  1,
  'sesn_legacy',
  'turn_legacy',
  'fake',
  '',
  'fake-demo',
  0,
  0,
  0,
  0,
  0,
  0,
  'completed',
  '',
  now()
WHERE FALSE;

CREATE INDEX IF NOT EXISTS idx_llm_usage_session_turn
  ON llm_usage_records(session_id, turn_id);

CREATE INDEX IF NOT EXISTS idx_llm_usage_provider_model
  ON llm_usage_records(provider_id, model);

CREATE INDEX IF NOT EXISTS idx_llm_usage_created_at
  ON llm_usage_records(created_at);
