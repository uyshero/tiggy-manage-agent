ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS runtime_settings_json JSONB NOT NULL DEFAULT '{}'::jsonb;
