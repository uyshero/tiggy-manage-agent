ALTER TABLE agent_deliberations ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_deliberations FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agent_deliberations_session_isolation
  ON agent_deliberations;

CREATE POLICY agent_deliberations_session_isolation
  ON agent_deliberations
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
      WHERE sessions.id = agent_deliberations.parent_session_id
        AND sessions.workspace_id = agent_deliberations.workspace_id
        AND sessions.owner_id = agent_deliberations.owner_id
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
      WHERE sessions.id = agent_deliberations.parent_session_id
        AND sessions.workspace_id = agent_deliberations.workspace_id
        AND sessions.owner_id = agent_deliberations.owner_id
    )
    AND EXISTS (
      SELECT 1
      FROM agents
      WHERE agents.id = agent_deliberations.moderator_agent_id
        AND agents.workspace_id = agent_deliberations.workspace_id
    )
    AND EXISTS (
      SELECT 1
      FROM environments
      WHERE environments.id = agent_deliberations.moderator_environment_id
        AND environments.workspace_id = agent_deliberations.workspace_id
    )
    AND (
      agent_deliberations.final_group_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM subagent_task_groups
        WHERE subagent_task_groups.id = agent_deliberations.final_group_id
          AND subagent_task_groups.workspace_id = agent_deliberations.workspace_id
          AND subagent_task_groups.owner_id = agent_deliberations.owner_id
      )
    )
  );

ALTER TABLE agent_deliberation_participants ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_deliberation_participants FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agent_deliberation_participants_parent_isolation
  ON agent_deliberation_participants;

CREATE POLICY agent_deliberation_participants_parent_isolation
  ON agent_deliberation_participants
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM agent_deliberations
      WHERE agent_deliberations.id = agent_deliberation_participants.deliberation_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM agent_deliberations
      JOIN agents ON agents.id = agent_deliberation_participants.agent_id
      JOIN environments ON environments.id = agent_deliberation_participants.environment_id
      WHERE agent_deliberations.id = agent_deliberation_participants.deliberation_id
        AND agents.workspace_id = agent_deliberations.workspace_id
        AND environments.workspace_id = agent_deliberations.workspace_id
    )
  );

ALTER TABLE agent_deliberation_rounds ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_deliberation_rounds FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agent_deliberation_rounds_parent_isolation
  ON agent_deliberation_rounds;

CREATE POLICY agent_deliberation_rounds_parent_isolation
  ON agent_deliberation_rounds
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM agent_deliberations
      WHERE agent_deliberations.id = agent_deliberation_rounds.deliberation_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM agent_deliberations
      JOIN subagent_task_groups task_groups
        ON task_groups.id = agent_deliberation_rounds.task_group_id
      WHERE agent_deliberations.id = agent_deliberation_rounds.deliberation_id
        AND task_groups.workspace_id = agent_deliberations.workspace_id
        AND task_groups.owner_id = agent_deliberations.owner_id
        AND (
          agent_deliberation_rounds.moderator_group_id IS NULL
          OR EXISTS (
            SELECT 1
            FROM subagent_task_groups moderator_groups
            WHERE moderator_groups.id = agent_deliberation_rounds.moderator_group_id
              AND moderator_groups.workspace_id = agent_deliberations.workspace_id
              AND moderator_groups.owner_id = agent_deliberations.owner_id
          )
        )
    )
  );

ALTER TABLE agent_deliberation_contributions ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_deliberation_contributions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agent_deliberation_contributions_parent_isolation
  ON agent_deliberation_contributions;

CREATE POLICY agent_deliberation_contributions_parent_isolation
  ON agent_deliberation_contributions
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM agent_deliberations
      WHERE agent_deliberations.id = agent_deliberation_contributions.deliberation_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM agent_deliberations
      JOIN subagent_task_groups task_groups
        ON task_groups.id = agent_deliberation_contributions.task_group_id
      WHERE agent_deliberations.id = agent_deliberation_contributions.deliberation_id
        AND task_groups.workspace_id = agent_deliberations.workspace_id
        AND task_groups.owner_id = agent_deliberations.owner_id
        AND (
          agent_deliberation_contributions.session_id IS NULL
          OR EXISTS (
            SELECT 1
            FROM sessions
            WHERE sessions.id = agent_deliberation_contributions.session_id
              AND sessions.workspace_id = agent_deliberations.workspace_id
              AND sessions.owner_id = agent_deliberations.owner_id
          )
        )
    )
  );
