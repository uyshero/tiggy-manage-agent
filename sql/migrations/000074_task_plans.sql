CREATE TABLE IF NOT EXISTS session_task_plans (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  owner_id TEXT NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  created_turn_id TEXT NOT NULL DEFAULT '',
  updated_turn_id TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  goal TEXT NOT NULL,
  handling_mode TEXT NOT NULL DEFAULT 'tracked',
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  CONSTRAINT session_task_plans_mode_check CHECK (handling_mode IN ('tracked', 'planned')),
  CONSTRAINT session_task_plans_status_check CHECK (status IN ('active', 'completed', 'canceled', 'superseded'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_task_plans_one_active
  ON session_task_plans(session_id)
  WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_session_task_plans_session
  ON session_task_plans(session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS session_task_items (
  id TEXT PRIMARY KEY,
  plan_id TEXT NOT NULL REFERENCES session_task_plans(id) ON DELETE CASCADE,
  item_index INTEGER NOT NULL,
  description TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  evidence TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  UNIQUE(plan_id, item_index),
  CONSTRAINT session_task_items_status_check CHECK (status IN ('pending', 'in_progress', 'completed', 'blocked'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_task_items_one_in_progress
  ON session_task_items(plan_id)
  WHERE status = 'in_progress';

CREATE SEQUENCE IF NOT EXISTS tma_task_plan_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_task_item_id_seq;

ALTER TABLE session_task_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_task_plans FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_task_plans_session_isolation
  ON session_task_plans;

CREATE POLICY session_task_plans_session_isolation
  ON session_task_plans
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1 FROM sessions
      WHERE sessions.id = session_task_plans.session_id
        AND sessions.workspace_id = session_task_plans.workspace_id
        AND sessions.owner_id = session_task_plans.owner_id
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1 FROM sessions
      WHERE sessions.id = session_task_plans.session_id
        AND sessions.workspace_id = session_task_plans.workspace_id
        AND sessions.owner_id = session_task_plans.owner_id
    )
  );

ALTER TABLE session_task_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_task_items FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS session_task_items_plan_isolation
  ON session_task_items;

CREATE POLICY session_task_items_plan_isolation
  ON session_task_items
  FOR ALL
  USING (
    EXISTS (
      SELECT 1 FROM session_task_plans plans
      WHERE plans.id = session_task_items.plan_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1 FROM session_task_plans plans
      WHERE plans.id = session_task_items.plan_id
    )
  );
