ALTER TABLE session_turns
  ADD COLUMN IF NOT EXISTS idempotency_key TEXT,
  ADD COLUMN IF NOT EXISTS request_hash TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_turns_idempotency_key
  ON session_turns(session_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

ALTER TABLE session_events
  ADD COLUMN IF NOT EXISTS turn_id TEXT;

UPDATE session_events
SET turn_id = NULLIF(payload_json->>'turn_id', '')
WHERE turn_id IS NULL
  AND jsonb_typeof(payload_json) = 'object'
  AND payload_json ? 'turn_id';

CREATE INDEX IF NOT EXISTS idx_session_events_session_turn_seq
  ON session_events(session_id, turn_id, seq)
  WHERE turn_id IS NOT NULL AND turn_id <> '';
