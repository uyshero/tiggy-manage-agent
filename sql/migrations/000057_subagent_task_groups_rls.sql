ALTER TABLE subagent_task_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE subagent_task_groups FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS subagent_task_groups_session_isolation
  ON subagent_task_groups;

CREATE POLICY subagent_task_groups_session_isolation
  ON subagent_task_groups
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
      WHERE sessions.id = subagent_task_groups.parent_session_id
        AND sessions.workspace_id = subagent_task_groups.workspace_id
        AND sessions.owner_id = subagent_task_groups.owner_id
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
      WHERE sessions.id = subagent_task_groups.parent_session_id
        AND sessions.workspace_id = subagent_task_groups.workspace_id
        AND sessions.owner_id = subagent_task_groups.owner_id
    )
  );

ALTER TABLE subagent_task_group_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE subagent_task_group_items FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS subagent_task_group_items_group_isolation
  ON subagent_task_group_items;

CREATE POLICY subagent_task_group_items_group_isolation
  ON subagent_task_group_items
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM subagent_task_groups task_groups
      JOIN agents ON agents.id = subagent_task_group_items.agent_id
      JOIN environments ON environments.id = subagent_task_group_items.environment_id
      WHERE task_groups.id = subagent_task_group_items.group_id
		AND agents.workspace_id = task_groups.workspace_id
		AND environments.workspace_id = task_groups.workspace_id
        AND (
          subagent_task_group_items.session_id IS NULL
          OR EXISTS (
            SELECT 1
            FROM sessions
            WHERE sessions.id = subagent_task_group_items.session_id
              AND sessions.workspace_id = task_groups.workspace_id
			  AND sessions.owner_id = task_groups.owner_id
          )
        )
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM subagent_task_groups task_groups
      JOIN agents ON agents.id = subagent_task_group_items.agent_id
      JOIN environments ON environments.id = subagent_task_group_items.environment_id
      WHERE task_groups.id = subagent_task_group_items.group_id
		AND agents.workspace_id = task_groups.workspace_id
		AND environments.workspace_id = task_groups.workspace_id
        AND (
          subagent_task_group_items.session_id IS NULL
          OR EXISTS (
            SELECT 1
            FROM sessions
            WHERE sessions.id = subagent_task_group_items.session_id
              AND sessions.workspace_id = task_groups.workspace_id
			  AND sessions.owner_id = task_groups.owner_id
          )
        )
    )
  );
