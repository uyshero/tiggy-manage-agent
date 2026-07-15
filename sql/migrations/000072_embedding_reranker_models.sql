ALTER TABLE llm_models
  ADD COLUMN IF NOT EXISTS capabilities_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS is_default_embedding BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS is_default_reranker BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE llm_models
  DROP CONSTRAINT IF EXISTS llm_models_capability_type_check;

ALTER TABLE llm_models
  ADD CONSTRAINT llm_models_capability_type_check
  CHECK (capability_type IN (
    'text', 'text_image', 'image_generation', 'video_generation', 'embedding', 'reranker'
  ));

ALTER TABLE llm_models
  DROP CONSTRAINT IF EXISTS llm_models_capabilities_object,
  DROP CONSTRAINT IF EXISTS llm_models_default_embedding_type,
  DROP CONSTRAINT IF EXISTS llm_models_default_reranker_type;

ALTER TABLE llm_models
  ADD CONSTRAINT llm_models_capabilities_object
    CHECK (jsonb_typeof(capabilities_json) = 'object'),
  ADD CONSTRAINT llm_models_default_embedding_type
    CHECK (NOT is_default_embedding OR capability_type = 'embedding'),
  ADD CONSTRAINT llm_models_default_reranker_type
    CHECK (NOT is_default_reranker OR capability_type = 'reranker');

CREATE UNIQUE INDEX IF NOT EXISTS llm_models_single_default_embedding_idx
  ON llm_models (is_default_embedding)
  WHERE is_default_embedding = TRUE;

CREATE UNIQUE INDEX IF NOT EXISTS llm_models_single_default_reranker_idx
  ON llm_models (is_default_reranker)
  WHERE is_default_reranker = TRUE;

CREATE OR REPLACE FUNCTION tma_control_upsert_llm_model(
  requested_provider_id TEXT,
  requested_model TEXT,
  requested_context_window_tokens INTEGER,
  requested_capability_type TEXT,
  requested_capabilities_json JSONB,
  requested_is_default_vision BOOLEAN,
  requested_is_default_embedding BOOLEAN,
  requested_is_default_reranker BOOLEAN
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
  current_capabilities_json JSONB;
  current_is_default_vision BOOLEAN;
  current_is_default_embedding BOOLEAN;
  current_is_default_reranker BOOLEAN;
  model_exists BOOLEAN;
BEGIN
  IF requested_is_default_vision THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_vision'));
  END IF;
  IF requested_is_default_embedding THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_embedding'));
  END IF;
  IF requested_is_default_reranker THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_reranker'));
  END IF;

  SELECT context_window_tokens, capability_type, capabilities_json,
         is_default_vision, is_default_embedding, is_default_reranker
  INTO current_context_window_tokens, current_capability_type, current_capabilities_json,
       current_is_default_vision, current_is_default_embedding, current_is_default_reranker
  FROM public.llm_models
  WHERE provider_id = requested_provider_id AND model = requested_model
  FOR UPDATE;
  model_exists := FOUND;

  IF requested_is_default_vision THEN
    UPDATE public.llm_models
    SET is_default_vision = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_vision = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
  END IF;
  IF requested_is_default_embedding THEN
    UPDATE public.llm_models
    SET is_default_embedding = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_embedding = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
  END IF;
  IF requested_is_default_reranker THEN
    UPDATE public.llm_models
    SET is_default_reranker = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_reranker = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
  END IF;

  IF model_exists THEN
    UPDATE public.llm_models
    SET context_window_tokens = requested_context_window_tokens,
        capability_type = requested_capability_type,
        capabilities_json = requested_capabilities_json,
        is_default_vision = requested_is_default_vision,
        is_default_embedding = requested_is_default_embedding,
        is_default_reranker = requested_is_default_reranker,
        revision = revision + CASE
          WHEN ROW(current_context_window_tokens, current_capability_type, current_capabilities_json,
                   current_is_default_vision, current_is_default_embedding, current_is_default_reranker)
            IS DISTINCT FROM
               ROW(requested_context_window_tokens, requested_capability_type, requested_capabilities_json,
                   requested_is_default_vision, requested_is_default_embedding, requested_is_default_reranker)
          THEN 1 ELSE 0 END,
        updated_at = CASE
          WHEN ROW(current_context_window_tokens, current_capability_type, current_capabilities_json,
                   current_is_default_vision, current_is_default_embedding, current_is_default_reranker)
            IS DISTINCT FROM
               ROW(requested_context_window_tokens, requested_capability_type, requested_capabilities_json,
                   requested_is_default_vision, requested_is_default_embedding, requested_is_default_reranker)
          THEN CURRENT_TIMESTAMP ELSE updated_at END
    WHERE provider_id = requested_provider_id AND model = requested_model;
    RETURN;
  END IF;

  INSERT INTO public.llm_models (
    provider_id, model, context_window_tokens, capability_type, capabilities_json,
    is_default_vision, is_default_embedding, is_default_reranker,
    revision, created_at, updated_at
  ) VALUES (
    requested_provider_id, requested_model, requested_context_window_tokens,
    requested_capability_type, requested_capabilities_json,
    requested_is_default_vision, requested_is_default_embedding, requested_is_default_reranker,
    1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  );
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_create_llm_model(
  requested_provider_id TEXT,
  requested_model TEXT,
  requested_context_window_tokens INTEGER,
  requested_capability_type TEXT,
  requested_capabilities_json JSONB,
  requested_is_default_vision BOOLEAN,
  requested_is_default_embedding BOOLEAN,
  requested_is_default_reranker BOOLEAN
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
  IF requested_is_default_embedding THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_embedding'));
  END IF;
  IF requested_is_default_reranker THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_reranker'));
  END IF;

  INSERT INTO public.llm_models (
    provider_id, model, context_window_tokens, capability_type, capabilities_json,
    is_default_vision, is_default_embedding, is_default_reranker,
    revision, created_at, updated_at
  ) VALUES (
    requested_provider_id, requested_model, requested_context_window_tokens,
    requested_capability_type, requested_capabilities_json,
    FALSE, FALSE, FALSE,
    1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  )
  ON CONFLICT (provider_id, model) DO NOTHING;
  IF NOT FOUND THEN
    RETURN FALSE;
  END IF;

  IF requested_is_default_vision THEN
    UPDATE public.llm_models
    SET is_default_vision = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_vision = TRUE;
  END IF;
  IF requested_is_default_embedding THEN
    UPDATE public.llm_models
    SET is_default_embedding = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_embedding = TRUE;
  END IF;
  IF requested_is_default_reranker THEN
    UPDATE public.llm_models
    SET is_default_reranker = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_reranker = TRUE;
  END IF;
  UPDATE public.llm_models
  SET is_default_vision = requested_is_default_vision,
      is_default_embedding = requested_is_default_embedding,
      is_default_reranker = requested_is_default_reranker
  WHERE provider_id = requested_provider_id AND model = requested_model;
  RETURN TRUE;
END;
$$;

CREATE OR REPLACE FUNCTION tma_control_update_llm_model(
  requested_provider_id TEXT,
  requested_model TEXT,
  requested_context_window_tokens INTEGER,
  requested_capability_type TEXT,
  requested_capabilities_json JSONB,
  requested_is_default_vision BOOLEAN,
  requested_is_default_embedding BOOLEAN,
  requested_is_default_reranker BOOLEAN,
  requested_revision BIGINT
)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
DECLARE
  revision_matches BOOLEAN;
BEGIN
  IF requested_is_default_vision THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_vision'));
  END IF;
  IF requested_is_default_embedding THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_embedding'));
  END IF;
  IF requested_is_default_reranker THEN
    PERFORM pg_advisory_xact_lock(hashtext('tma.llm_models.default_reranker'));
  END IF;

  SELECT TRUE INTO revision_matches
  FROM public.llm_models
  WHERE provider_id = requested_provider_id
    AND model = requested_model
    AND revision = requested_revision
  FOR UPDATE;
  IF NOT FOUND THEN
    RETURN FALSE;
  END IF;

  IF requested_is_default_vision THEN
    UPDATE public.llm_models
    SET is_default_vision = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_vision = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
  END IF;
  IF requested_is_default_embedding THEN
    UPDATE public.llm_models
    SET is_default_embedding = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_embedding = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
  END IF;
  IF requested_is_default_reranker THEN
    UPDATE public.llm_models
    SET is_default_reranker = FALSE, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE is_default_reranker = TRUE
      AND (provider_id, model) <> (requested_provider_id, requested_model);
  END IF;

  UPDATE public.llm_models
  SET context_window_tokens = requested_context_window_tokens,
      capability_type = requested_capability_type,
      capabilities_json = requested_capabilities_json,
      is_default_vision = requested_is_default_vision,
      is_default_embedding = requested_is_default_embedding,
      is_default_reranker = requested_is_default_reranker,
      revision = revision + 1,
      updated_at = CURRENT_TIMESTAMP
  WHERE provider_id = requested_provider_id
    AND model = requested_model
    AND revision = requested_revision;
  RETURN FOUND;
END;
$$;
