ALTER TABLE llm_models
  DROP CONSTRAINT IF EXISTS llm_models_capability_type_check;

ALTER TABLE llm_models
  ADD CONSTRAINT llm_models_capability_type_check
  CHECK (capability_type IN (
    'text', 'text_image', 'image_generation', 'video_generation',
    'embedding', 'reranker', 'speech_to_text', 'text_to_speech',
    'multimodal_realtime'
  ));

COMMENT ON COLUMN llm_models.capabilities_json IS
  'Capability-specific model metadata. Multimodal realtime models declare protocol plus bounded input/output media formats, output modalities, track count, and frame size.';
