ALTER TABLE llm_providers
  ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'llm_providers_revision_positive'
      AND conrelid = 'public.llm_providers'::regclass
  ) THEN
    ALTER TABLE public.llm_providers
      ADD CONSTRAINT llm_providers_revision_positive CHECK (revision > 0);
  END IF;
END
$$;

CREATE OR REPLACE FUNCTION tma_control_upsert_llm_provider(
  requested_id TEXT,
  requested_provider_type TEXT,
  requested_base_url TEXT,
  requested_api_key_env TEXT,
  requested_enabled BOOLEAN
)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  INSERT INTO public.llm_providers (
    id, provider_type, base_url, api_key_env, enabled, revision, created_at, updated_at
  ) VALUES (
    requested_id, requested_provider_type, requested_base_url, requested_api_key_env,
    requested_enabled, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  )
  ON CONFLICT (id) DO UPDATE SET
    provider_type = EXCLUDED.provider_type,
    base_url = EXCLUDED.base_url,
    api_key_env = EXCLUDED.api_key_env,
    enabled = EXCLUDED.enabled,
    revision = CASE
      WHEN ROW(
        public.llm_providers.provider_type, public.llm_providers.base_url,
        public.llm_providers.api_key_env, public.llm_providers.enabled
      ) IS DISTINCT FROM ROW(
        EXCLUDED.provider_type, EXCLUDED.base_url, EXCLUDED.api_key_env, EXCLUDED.enabled
      ) THEN public.llm_providers.revision + 1
      ELSE public.llm_providers.revision
    END,
    updated_at = CASE
      WHEN ROW(
        public.llm_providers.provider_type, public.llm_providers.base_url,
        public.llm_providers.api_key_env, public.llm_providers.enabled
      ) IS DISTINCT FROM ROW(
        EXCLUDED.provider_type, EXCLUDED.base_url, EXCLUDED.api_key_env, EXCLUDED.enabled
      ) THEN CURRENT_TIMESTAMP
      ELSE public.llm_providers.updated_at
    END;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_create_llm_provider(
  requested_id TEXT,
  requested_provider_type TEXT,
  requested_base_url TEXT,
  requested_api_key_env TEXT,
  requested_enabled BOOLEAN
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  INSERT INTO public.llm_providers (
    id, provider_type, base_url, api_key_env, enabled, revision, created_at, updated_at
  ) VALUES (
    requested_id, requested_provider_type, requested_base_url, requested_api_key_env,
    requested_enabled, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  )
  ON CONFLICT (id) DO NOTHING;
  RETURN FOUND;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_update_llm_provider(
  requested_id TEXT,
  requested_provider_type TEXT,
  requested_base_url TEXT,
  requested_api_key_env TEXT,
  requested_enabled BOOLEAN,
  requested_revision BIGINT
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  UPDATE public.llm_providers
  SET provider_type = requested_provider_type,
      base_url = requested_base_url,
      api_key_env = requested_api_key_env,
      enabled = requested_enabled,
      revision = revision + 1,
      updated_at = CURRENT_TIMESTAMP
  WHERE id = requested_id AND revision = requested_revision;
  RETURN FOUND;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_set_llm_provider_enabled(
  requested_id TEXT,
  requested_enabled BOOLEAN
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  UPDATE public.llm_providers
  SET revision = revision + CASE WHEN enabled IS DISTINCT FROM requested_enabled THEN 1 ELSE 0 END,
      updated_at = CASE WHEN enabled IS DISTINCT FROM requested_enabled THEN CURRENT_TIMESTAMP ELSE updated_at END,
      enabled = requested_enabled
  WHERE id = requested_id;
  RETURN FOUND;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_set_llm_provider_enabled(
  requested_id TEXT,
  requested_enabled BOOLEAN,
  requested_revision BIGINT
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  UPDATE public.llm_providers
  SET revision = revision + CASE WHEN enabled IS DISTINCT FROM requested_enabled THEN 1 ELSE 0 END,
      updated_at = CASE WHEN enabled IS DISTINCT FROM requested_enabled THEN CURRENT_TIMESTAMP ELSE updated_at END,
      enabled = requested_enabled
  WHERE id = requested_id AND revision = requested_revision;
  RETURN FOUND;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_delete_llm_provider(
  requested_id TEXT,
  requested_revision BIGINT
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  DELETE FROM public.llm_providers
  WHERE id = requested_id AND revision = requested_revision;
  RETURN FOUND;
END;
$$;
