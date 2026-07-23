ALTER TABLE agents
  ADD COLUMN IF NOT EXISTS environment_id TEXT REFERENCES environments(id);

CREATE INDEX IF NOT EXISTS idx_agents_environment_id
  ON agents(environment_id)
  WHERE environment_id IS NOT NULL;
