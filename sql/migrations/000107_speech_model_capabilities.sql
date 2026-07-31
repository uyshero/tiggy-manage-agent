ALTER TABLE llm_models
  DROP CONSTRAINT IF EXISTS llm_models_capability_type_check;

ALTER TABLE llm_models
  ADD CONSTRAINT llm_models_capability_type_check
  CHECK (capability_type IN (
    'text', 'text_image', 'image_generation', 'video_generation',
    'embedding', 'reranker', 'speech_to_text', 'text_to_speech'
  ));

COMMENT ON COLUMN llm_models.capabilities_json IS
  'Capability-specific model metadata. Speech models use protocol, resource_id, default_voice, audio_format, sample_rate_hz, and optional upstream_model.';
