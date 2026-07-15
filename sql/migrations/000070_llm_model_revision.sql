ALTER TABLE llm_models
  ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'llm_models_revision_positive'
      AND conrelid = 'public.llm_models'::regclass
  ) THEN
    ALTER TABLE public.llm_models
      ADD CONSTRAINT llm_models_revision_positive CHECK (revision > 0);
  END IF;
END
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
DECLARE
  current_context_window_tokens INTEGER;
  current_capability_type TEXT;
  current_is_default_vision BOOLEAN;
BEGIN
  IF requested_is_default_vision THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_vision'));
  END IF;

  SELECT context_window_tokens, capability_type, is_default_vision
  INTO current_context_window_tokens, current_capability_type, current_is_default_vision
  FROM public.llm_models
  WHERE provider_id = requested_provider_id AND model = requested_model
  FOR UPDATE;

  IF FOUND THEN
    IF requested_is_default_vision THEN
      UPDATE public.llm_models
      SET is_default_vision = FALSE,
          revision = revision + 1,
          updated_at = CURRENT_TIMESTAMP
      WHERE is_default_vision = TRUE
        AND (provider_id, model) <> (requested_provider_id, requested_model);
    END IF;

    UPDATE public.llm_models
    SET context_window_tokens = requested_context_window_tokens,
        capability_type = requested_capability_type,
        is_default_vision = requested_is_default_vision,
        revision = revision + CASE
          WHEN ROW(current_context_window_tokens, current_capability_type, current_is_default_vision)
            IS DISTINCT FROM ROW(requested_context_window_tokens, requested_capability_type, requested_is_default_vision)
          THEN 1 ELSE 0 END,
        updated_at = CASE
          WHEN ROW(current_context_window_tokens, current_capability_type, current_is_default_vision)
            IS DISTINCT FROM ROW(requested_context_window_tokens, requested_capability_type, requested_is_default_vision)
          THEN CURRENT_TIMESTAMP ELSE updated_at END
    WHERE provider_id = requested_provider_id AND model = requested_model;
    RETURN;
  END IF;

  IF requested_is_default_vision THEN
    UPDATE public.llm_models
    SET is_default_vision = FALSE,
        revision = revision + 1,
        updated_at = CURRENT_TIMESTAMP
    WHERE is_default_vision = TRUE;
  END IF;

  INSERT INTO public.llm_models (
    provider_id, model, context_window_tokens, capability_type,
    is_default_vision, revision, created_at, updated_at
  ) VALUES (
    requested_provider_id, requested_model, requested_context_window_tokens,
    requested_capability_type, requested_is_default_vision, 1,
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  );
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_create_llm_model(
  requested_provider_id TEXT,
  requested_model TEXT,
  requested_context_window_tokens INTEGER,
  requested_capability_type TEXT,
  requested_is_default_vision BOOLEAN
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  INSERT INTO public.llm_models (
    provider_id, model, context_window_tokens, capability_type,
    is_default_vision, revision, created_at, updated_at
  ) VALUES (
    requested_provider_id, requested_model, requested_context_window_tokens,
    requested_capability_type, FALSE, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  )
  ON CONFLICT (provider_id, model) DO NOTHING;

  IF NOT FOUND THEN
    RETURN FALSE;
  END IF;

  IF requested_is_default_vision THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_vision'));
    UPDATE public.llm_models
    SET is_default_vision = FALSE,
        revision = revision + 1,
        updated_at = CURRENT_TIMESTAMP
    WHERE is_default_vision = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
    UPDATE public.llm_models
    SET is_default_vision = TRUE
    WHERE provider_id = requested_provider_id AND model = requested_model;
  END IF;
  RETURN TRUE;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_update_llm_model(
  requested_provider_id TEXT,
  requested_model TEXT,
  requested_context_window_tokens INTEGER,
  requested_capability_type TEXT,
  requested_is_default_vision BOOLEAN,
  requested_revision BIGINT
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  IF requested_is_default_vision THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_vision'));
  END IF;

  UPDATE public.llm_models
  SET context_window_tokens = requested_context_window_tokens,
      capability_type = requested_capability_type,
      is_default_vision = FALSE,
      revision = revision + 1,
      updated_at = CURRENT_TIMESTAMP
  WHERE provider_id = requested_provider_id
    AND model = requested_model
    AND revision = requested_revision;

  IF NOT FOUND THEN
    RETURN FALSE;
  END IF;

  IF requested_is_default_vision THEN
    UPDATE public.llm_models
    SET is_default_vision = FALSE,
        revision = revision + 1,
        updated_at = CURRENT_TIMESTAMP
    WHERE is_default_vision = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
    UPDATE public.llm_models
    SET is_default_vision = TRUE
    WHERE provider_id = requested_provider_id AND model = requested_model;
  END IF;
  RETURN TRUE;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_delete_llm_model(
  requested_provider_id TEXT,
  requested_model TEXT,
  requested_revision BIGINT
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  DELETE FROM public.llm_models
  WHERE provider_id = requested_provider_id
    AND model = requested_model
    AND revision = requested_revision;
  RETURN FOUND;
END;
$$;
