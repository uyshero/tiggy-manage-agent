ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS effective_llm_provider TEXT,
  ADD COLUMN IF NOT EXISTS effective_llm_model TEXT;

-- Early development databases could retain Agents or Sessions after their
-- referenced config row was removed. Preserve those resources with the seeded
-- deterministic model before enforcing the reference through the trigger.
INSERT INTO agent_config_versions (
  agent_id, version, llm_provider, llm_model, system,
  tools_json, mcp_json, skills_json, created_at
)
SELECT
  missing.agent_id, missing.version, 'fake', 'fake-demo', '',
  'null'::jsonb, 'null'::jsonb, 'null'::jsonb, CURRENT_TIMESTAMP
FROM (
  SELECT id AS agent_id, current_config_version AS version
  FROM agents
  UNION
  SELECT agent_id, agent_config_version
  FROM sessions
) AS missing
WHERE NOT EXISTS (
  SELECT 1
  FROM agent_config_versions AS existing
  WHERE existing.agent_id = missing.agent_id
    AND existing.version = missing.version
)
ON CONFLICT (agent_id, version) DO NOTHING;

CREATE OR REPLACE FUNCTION tma_set_session_effective_llm()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
DECLARE
  configured_provider TEXT;
  configured_model TEXT;
BEGIN
  SELECT config.llm_provider, config.llm_model
  INTO configured_provider, configured_model
  FROM public.agent_config_versions AS config
  WHERE config.agent_id = NEW.agent_id
    AND config.version = NEW.agent_config_version;

  IF configured_provider IS NULL OR configured_model IS NULL THEN
    RAISE EXCEPTION 'session agent configuration %/% does not exist', NEW.agent_id, NEW.agent_config_version
      USING ERRCODE = '23503';
  END IF;

  NEW.effective_llm_provider := COALESCE(
    NULLIF(btrim(COALESCE(NEW.runtime_settings_json->>'llm_provider', '')), ''),
    configured_provider
  );
  NEW.effective_llm_model := COALESCE(
    NULLIF(btrim(COALESCE(NEW.runtime_settings_json->>'llm_model', '')), ''),
    configured_model
  );
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS sessions_set_effective_llm ON sessions;
CREATE TRIGGER sessions_set_effective_llm
BEFORE INSERT OR UPDATE OF agent_id, agent_config_version, runtime_settings_json
ON sessions
FOR EACH ROW
EXECUTE FUNCTION tma_set_session_effective_llm();

UPDATE sessions
SET runtime_settings_json = runtime_settings_json;

ALTER TABLE sessions
  ALTER COLUMN effective_llm_provider SET NOT NULL,
  ALTER COLUMN effective_llm_model SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_config_versions_llm_model_reference
  ON agent_config_versions (llm_provider, llm_model);

CREATE INDEX IF NOT EXISTS idx_sessions_effective_llm_model_reference
  ON sessions (effective_llm_provider, effective_llm_model);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'agent_config_versions_llm_model_fkey'
      AND conrelid = 'public.agent_config_versions'::regclass
  ) THEN
    ALTER TABLE public.agent_config_versions
      ADD CONSTRAINT agent_config_versions_llm_model_fkey
      FOREIGN KEY (llm_provider, llm_model)
      REFERENCES public.llm_models(provider_id, model)
      NOT VALID;
  END IF;
END
$$;

ALTER TABLE agent_config_versions
  VALIDATE CONSTRAINT agent_config_versions_llm_model_fkey;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sessions_effective_llm_model_fkey'
      AND conrelid = 'public.sessions'::regclass
  ) THEN
    ALTER TABLE public.sessions
      ADD CONSTRAINT sessions_effective_llm_model_fkey
      FOREIGN KEY (effective_llm_provider, effective_llm_model)
      REFERENCES public.llm_models(provider_id, model)
      NOT VALID;
  END IF;
END
$$;

ALTER TABLE sessions
  VALIDATE CONSTRAINT sessions_effective_llm_model_fkey;
