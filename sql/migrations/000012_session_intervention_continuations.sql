ALTER TABLE session_interventions
  ADD COLUMN IF NOT EXISTS continuation_messages_json JSONB,
  ADD COLUMN IF NOT EXISTS continuation_round INTEGER NOT NULL DEFAULT 0;
