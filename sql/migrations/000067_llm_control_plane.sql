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
  INSERT INTO public.llm_providers (id, provider_type, base_url, api_key_env, enabled, created_at)
  VALUES (requested_id, requested_provider_type, requested_base_url, requested_api_key_env, requested_enabled, CURRENT_TIMESTAMP)
  ON CONFLICT (id) DO UPDATE SET
    provider_type = EXCLUDED.provider_type,
    base_url = EXCLUDED.base_url,
    api_key_env = EXCLUDED.api_key_env,
    enabled = EXCLUDED.enabled;
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
  SET enabled = requested_enabled
  WHERE id = requested_id;
  RETURN FOUND;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_delete_llm_provider(requested_id TEXT)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  DELETE FROM public.llm_providers WHERE id = requested_id;
  RETURN FOUND;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_upsert_llm_model(
  requested_provider_id TEXT,
  requested_model TEXT,
  requested_context_window_tokens INTEGER,
  requested_capability_type TEXT,
  requested_is_default_vision BOOLEAN
)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  IF requested_is_default_vision THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_vision'));
    UPDATE public.llm_models
    SET is_default_vision = FALSE, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_vision = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
  END IF;

  INSERT INTO public.llm_models (
    provider_id, model, context_window_tokens, capability_type,
    is_default_vision, created_at, updated_at
  ) VALUES (
    requested_provider_id, requested_model, requested_context_window_tokens,
    requested_capability_type, requested_is_default_vision,
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  )
  ON CONFLICT (provider_id, model) DO UPDATE SET
    context_window_tokens = EXCLUDED.context_window_tokens,
    capability_type = EXCLUDED.capability_type,
    is_default_vision = EXCLUDED.is_default_vision,
    updated_at = EXCLUDED.updated_at;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_delete_llm_model(
  requested_provider_id TEXT,
  requested_model TEXT
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  DELETE FROM public.llm_models
  WHERE provider_id = requested_provider_id AND model = requested_model;
  RETURN FOUND;
END;
$$;
