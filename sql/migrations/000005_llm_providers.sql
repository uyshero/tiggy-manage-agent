CREATE TABLE IF NOT EXISTS llm_providers (
  id TEXT PRIMARY KEY,
  provider_type TEXT NOT NULL,
  base_url TEXT NOT NULL DEFAULT '',
  api_key_env TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO llm_providers (id, provider_type, base_url, api_key_env, enabled)
VALUES ('fake', 'fake', '', '', TRUE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO llm_providers (id, provider_type, base_url, api_key_env, enabled)
SELECT DISTINCT
  llm_provider,
  CASE WHEN llm_provider = 'fake' THEN 'fake' ELSE 'openai' END,
  '',
  CASE WHEN llm_provider = 'fake' THEN '' ELSE 'TMA_LLM_API_KEY' END,
  TRUE
FROM agent_config_versions
WHERE llm_provider IS NOT NULL
ON CONFLICT (id) DO NOTHING;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'agent_config_versions_llm_provider_fkey'
  ) THEN
    ALTER TABLE agent_config_versions
      ADD CONSTRAINT agent_config_versions_llm_provider_fkey
      FOREIGN KEY (llm_provider) REFERENCES llm_providers(id);
  END IF;
END $$;
