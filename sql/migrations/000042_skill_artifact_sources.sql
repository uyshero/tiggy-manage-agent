ALTER TABLE skills
  DROP CONSTRAINT IF EXISTS skills_source_type_check;

ALTER TABLE skills
  ADD CONSTRAINT skills_source_type_check
  CHECK (source_type IN ('inline', 'github', 'artifact', 'catalog', 'plugin', 'builtin'));
