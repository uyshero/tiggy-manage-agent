-- Each utterance remains independently addressable after an interview session
-- is backed up. Session rows contain only summary metadata.

CREATE TABLE IF NOT EXISTS biography_recording_segments (
    owner_id TEXT NOT NULL,
    recording_id TEXT NOT NULL,
    segment_id TEXT NOT NULL,
    transcript TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL CHECK (duration_ms >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    content_type TEXT NOT NULL DEFAULT 'audio/wav',
    transcription_status TEXT NOT NULL DEFAULT 'ready'
        CHECK (transcription_status IN ('ready', 'needs_retry')),
    object_bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    PRIMARY KEY (owner_id, recording_id, segment_id),
    FOREIGN KEY (owner_id, recording_id)
        REFERENCES biography_recording_sessions(owner_id, recording_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS biography_recording_segments_owner_session_created_idx
    ON biography_recording_segments (owner_id, recording_id, created_at);
