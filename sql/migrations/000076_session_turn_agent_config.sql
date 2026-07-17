ALTER TABLE session_turns
  ADD COLUMN IF NOT EXISTS agent_id TEXT,
  ADD COLUMN IF NOT EXISTS agent_config_version INTEGER;

UPDATE session_turns AS turn
SET agent_id = session.agent_id,
    agent_config_version = session.agent_config_version
FROM sessions AS session
WHERE session.id = turn.session_id
  AND (turn.agent_id IS NULL OR turn.agent_config_version IS NULL);

ALTER TABLE session_turns
  ALTER COLUMN agent_id SET NOT NULL,
  ALTER COLUMN agent_config_version SET NOT NULL;

ALTER TABLE session_turns
  DROP CONSTRAINT IF EXISTS session_turns_agent_config_version_fkey;

ALTER TABLE session_turns
  ADD CONSTRAINT session_turns_agent_config_version_fkey
  FOREIGN KEY (agent_id, agent_config_version)
  REFERENCES agent_config_versions(agent_id, version);

CREATE INDEX IF NOT EXISTS idx_session_turns_agent_config
  ON session_turns(agent_id, agent_config_version, started_at DESC);
