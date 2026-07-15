ALTER TABLE skills
  ADD COLUMN IF NOT EXISTS source_type TEXT NOT NULL DEFAULT 'inline',
  ADD COLUMN IF NOT EXISTS source_locator TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS source_path TEXT NOT NULL DEFAULT '';

ALTER TABLE skill_versions
  ADD COLUMN IF NOT EXISTS source_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS source_revision TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS source_url TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'skills_source_type_check'
  ) THEN
    ALTER TABLE skills
      ADD CONSTRAINT skills_source_type_check
      CHECK (source_type IN ('inline', 'github', 'plugin', 'builtin'));
  END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_skills_source
  ON skills(source_type, source_locator, source_path);
