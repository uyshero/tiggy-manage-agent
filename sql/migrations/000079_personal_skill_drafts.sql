ALTER TABLE skills
  ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'workspace',
  ADD COLUMN IF NOT EXISTS forked_from_skill_id TEXT REFERENCES skills(id),
  ADD COLUMN IF NOT EXISTS forked_from_version INTEGER;

UPDATE skills
SET owner_id = workspace_id,
    visibility = 'workspace'
WHERE owner_id = '';

ALTER TABLE skills DROP CONSTRAINT IF EXISTS skills_owner_type_check;
ALTER TABLE skills ADD CONSTRAINT skills_owner_type_check
  CHECK (owner_type IN ('user', 'workspace', 'builtin', 'plugin'));

ALTER TABLE skills DROP CONSTRAINT IF EXISTS skills_visibility_check;
ALTER TABLE skills ADD CONSTRAINT skills_visibility_check
  CHECK (
    (owner_type = 'user' AND owner_id <> '' AND visibility = 'private')
    OR
    (owner_type <> 'user' AND owner_id = workspace_id AND visibility = 'workspace')
  );

ALTER TABLE skills DROP CONSTRAINT IF EXISTS skills_workspace_id_identifier_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_skills_shared_identifier_unique
  ON skills(workspace_id, identifier)
  WHERE owner_type <> 'user';

CREATE UNIQUE INDEX IF NOT EXISTS idx_skills_personal_identifier_unique
  ON skills(workspace_id, owner_id, identifier)
  WHERE owner_type = 'user';

CREATE TABLE IF NOT EXISTS skill_drafts (
  skill_id TEXT PRIMARY KEY REFERENCES skills(id) ON DELETE CASCADE,
  revision BIGINT NOT NULL DEFAULT 1,
  content_format TEXT NOT NULL DEFAULT 'hybrid',
  manifest_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  content_text TEXT NOT NULL DEFAULT '',
  assets_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  updated_by TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT skill_drafts_revision_check CHECK (revision > 0),
  CONSTRAINT skill_drafts_content_format_check CHECK (content_format IN ('markdown', 'json', 'hybrid'))
);

DROP POLICY IF EXISTS skills_workspace_isolation ON skills;
CREATE POLICY skills_workspace_isolation
  ON skills
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_type <> 'user'
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_type <> 'user'
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  );

ALTER TABLE skill_drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE skill_drafts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS skill_drafts_skill_isolation ON skill_drafts;
CREATE POLICY skill_drafts_skill_isolation
  ON skill_drafts
  FOR ALL
  USING (EXISTS (SELECT 1 FROM skills WHERE skills.id = skill_drafts.skill_id))
  WITH CHECK (EXISTS (SELECT 1 FROM skills WHERE skills.id = skill_drafts.skill_id));
