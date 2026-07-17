-- One Agent entity supports both private user Agents and Workspace-shared Agents.
-- Existing Agents are preserved as Workspace-shared custom Agents.

ALTER TABLE agents
  ADD COLUMN IF NOT EXISTS owner_type TEXT NOT NULL DEFAULT 'workspace',
  ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'workspace',
  ADD COLUMN IF NOT EXISTS agent_kind TEXT NOT NULL DEFAULT 'custom';

CREATE OR REPLACE FUNCTION tma_normalize_agent_ownership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.owner_type = 'workspace' AND COALESCE(NEW.owner_id, '') = '' THEN
    NEW.owner_id := NEW.workspace_id;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS agents_normalize_ownership ON agents;
CREATE TRIGGER agents_normalize_ownership
  BEFORE INSERT OR UPDATE OF workspace_id, owner_type, owner_id ON agents
  FOR EACH ROW
  EXECUTE FUNCTION tma_normalize_agent_ownership();

UPDATE agents
SET owner_type = 'workspace',
    owner_id = workspace_id,
    visibility = 'workspace',
    agent_kind = CASE WHEN id LIKE 'agt_general%' THEN 'general' ELSE 'custom' END
WHERE owner_id = '';

ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_owner_type_check;
ALTER TABLE agents ADD CONSTRAINT agents_owner_type_check
  CHECK (owner_type IN ('user', 'workspace'));

ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_visibility_check;
ALTER TABLE agents ADD CONSTRAINT agents_visibility_check
  CHECK (visibility IN ('private', 'workspace'));

ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_kind_check;
ALTER TABLE agents ADD CONSTRAINT agents_kind_check
  CHECK (agent_kind IN ('general', 'custom'));

ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_ownership_visibility_check;
ALTER TABLE agents ADD CONSTRAINT agents_ownership_visibility_check
  CHECK (
    (owner_type = 'user' AND owner_id <> '' AND visibility = 'private')
    OR
    (owner_type = 'workspace' AND owner_id = workspace_id AND visibility = 'workspace')
  );

CREATE INDEX IF NOT EXISTS idx_agents_owner
  ON agents(workspace_id, owner_type, owner_id)
  WHERE archived_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_personal_general_unique
  ON agents(workspace_id, owner_id, agent_kind)
  WHERE owner_type = 'user' AND agent_kind = 'general';

DROP POLICY IF EXISTS agents_workspace_isolation ON agents;

CREATE POLICY agents_workspace_isolation
  ON agents
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_type = 'workspace'
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_type = 'workspace'
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  );

-- Clean up the abandoned scope/template draft if it was applied in a development DB.
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_template_version_fkey;
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_scope_type_check;
ALTER TABLE agents
  DROP COLUMN IF EXISTS scope_type,
  DROP COLUMN IF EXISTS scope_id,
  DROP COLUMN IF EXISTS owner_principal_id,
  DROP COLUMN IF EXISTS created_from_template_id,
  DROP COLUMN IF EXISTS created_from_template_version;

DROP TABLE IF EXISTS agent_template_versions;
DROP TABLE IF EXISTS agent_templates;
DROP SEQUENCE IF EXISTS tma_agent_template_id_seq;
