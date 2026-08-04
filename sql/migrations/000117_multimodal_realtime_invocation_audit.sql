ALTER TABLE model_invocations
  ADD COLUMN IF NOT EXISTS input_video_frames BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS output_video_frames BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS input_video_dropped BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS output_video_dropped BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS input_video_ms BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS output_video_ms BIGINT NOT NULL DEFAULT 0;

ALTER TABLE model_invocations DROP CONSTRAINT IF EXISTS model_invocations_capability_check;
ALTER TABLE model_invocations
  ADD CONSTRAINT model_invocations_capability_check CHECK (
    capability IN ('generate', 'embedding', 'rerank', 'speech_to_text', 'text_to_speech', 'multimodal_realtime')
  );

ALTER TABLE model_invocations DROP CONSTRAINT IF EXISTS model_invocations_usage_check;
ALTER TABLE model_invocations
  ADD CONSTRAINT model_invocations_usage_check CHECK (
    input_tokens >= 0 AND output_tokens >= 0 AND total_tokens >= 0 AND
    cached_input_tokens >= 0 AND reasoning_tokens >= 0 AND
    input_items >= 0 AND output_items >= 0 AND input_bytes >= 0 AND output_bytes >= 0 AND
    input_characters >= 0 AND output_characters >= 0 AND input_audio_ms >= 0 AND output_audio_ms >= 0 AND
    input_video_frames >= 0 AND output_video_frames >= 0 AND
    input_video_dropped >= 0 AND output_video_dropped >= 0 AND
    input_video_ms >= 0 AND output_video_ms >= 0 AND latency_ms >= 0
  );

ALTER TABLE model_invocation_quota_buckets DROP CONSTRAINT IF EXISTS model_invocation_quota_capability_check;
ALTER TABLE model_invocation_quota_buckets
  ADD CONSTRAINT model_invocation_quota_capability_check CHECK (
    capability IN ('generate', 'embedding', 'rerank', 'speech_to_text', 'text_to_speech', 'multimodal_realtime')
  );
