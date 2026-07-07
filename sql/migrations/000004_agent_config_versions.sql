DO $$
BEGIN
  IF to_regclass('public.agent_config_versions') IS NULL
     AND to_regclass('public.agent_versions') IS NOT NULL THEN
    ALTER TABLE agent_versions RENAME TO agent_config_versions;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'agents' AND column_name = 'current_version'
  ) AND NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'agents' AND column_name = 'current_config_version'
  ) THEN
    ALTER TABLE agents RENAME COLUMN current_version TO current_config_version;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'sessions' AND column_name = 'agent_version'
  ) AND NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'sessions' AND column_name = 'agent_config_version'
  ) THEN
    ALTER TABLE sessions RENAME COLUMN agent_version TO agent_config_version;
  END IF;

  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'agent_config_versions' AND column_name = 'model'
  ) AND NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'agent_config_versions' AND column_name = 'llm_model'
  ) THEN
    ALTER TABLE agent_config_versions RENAME COLUMN model TO llm_model;
  END IF;
END $$;

ALTER TABLE agent_config_versions
  ADD COLUMN IF NOT EXISTS llm_provider TEXT NOT NULL DEFAULT 'fake';
