CREATE TABLE IF NOT EXISTS agent_loop_states (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  owner_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  revision BIGINT NOT NULL,
  phase TEXT NOT NULL,
  state_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (session_id, turn_id),
  FOREIGN KEY (session_id, turn_id)
    REFERENCES session_turns(session_id, id)
    ON DELETE CASCADE,
  CONSTRAINT agent_loop_states_revision_check CHECK (revision >= 0),
  CONSTRAINT agent_loop_states_phase_check CHECK (
    phase IN (
      'preparing',
      'awaiting_model',
      'preflighting_tools',
      'paused',
      'executing_tools',
      'validating_completion',
      'completed',
      'failed',
      'canceled'
    )
  ),
  CONSTRAINT agent_loop_states_json_check CHECK (jsonb_typeof(state_json) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_agent_loop_states_workspace_phase
  ON agent_loop_states(workspace_id, phase, updated_at DESC);

ALTER TABLE agent_loop_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_loop_states FORCE ROW LEVEL SECURITY;

CREATE POLICY agent_loop_states_session_isolation
  ON agent_loop_states
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = agent_loop_states.session_id
        AND sessions.workspace_id = agent_loop_states.workspace_id
        AND sessions.owner_id = agent_loop_states.owner_id
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = agent_loop_states.session_id
        AND sessions.workspace_id = agent_loop_states.workspace_id
        AND sessions.owner_id = agent_loop_states.owner_id
    )
  );
