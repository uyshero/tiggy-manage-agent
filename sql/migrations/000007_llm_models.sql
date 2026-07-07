CREATE TABLE IF NOT EXISTS llm_models (
  provider_id TEXT NOT NULL REFERENCES llm_providers(id) ON DELETE CASCADE,
  model TEXT NOT NULL,
  context_window_tokens INTEGER NOT NULL DEFAULT 128000,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (provider_id, model)
);

INSERT INTO llm_models (provider_id, model, context_window_tokens)
VALUES ('fake', 'fake-demo', 128000)
ON CONFLICT (provider_id, model) DO NOTHING;

INSERT INTO llm_models (provider_id, model, context_window_tokens)
SELECT DISTINCT llm_provider, llm_model, 128000
FROM agent_config_versions
WHERE llm_provider IS NOT NULL AND llm_model IS NOT NULL
ON CONFLICT (provider_id, model) DO NOTHING;
