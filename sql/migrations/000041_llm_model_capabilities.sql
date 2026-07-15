ALTER TABLE llm_models
  ADD COLUMN IF NOT EXISTS capability_type TEXT NOT NULL DEFAULT 'text',
  ADD COLUMN IF NOT EXISTS is_default_vision BOOLEAN NOT NULL DEFAULT FALSE;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'llm_models_capability_type_check'
  ) THEN
    ALTER TABLE llm_models
      ADD CONSTRAINT llm_models_capability_type_check
      CHECK (capability_type IN ('text', 'text_image', 'image_generation', 'video_generation'));
  END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS llm_models_single_default_vision_idx
  ON llm_models (is_default_vision)
  WHERE is_default_vision = TRUE;

