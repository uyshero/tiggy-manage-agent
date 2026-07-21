CREATE SEQUENCE IF NOT EXISTS tma_agent_schedule_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_agent_schedule_run_id_seq;

CREATE TABLE IF NOT EXISTS agent_schedules (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  owner_id TEXT NOT NULL,
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  environment_id TEXT NOT NULL REFERENCES environments(id),
  name TEXT NOT NULL,
  prompt TEXT NOT NULL,
  cron_expression TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'UTC',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  next_run_at TIMESTAMPTZ,
  last_run_at TIMESTAMPTZ,
  last_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  last_run_status TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT agent_schedules_name_check CHECK (btrim(name) <> ''),
  CONSTRAINT agent_schedules_prompt_check CHECK (btrim(prompt) <> ''),
  CONSTRAINT agent_schedules_last_status_check CHECK (
    last_run_status IS NULL OR last_run_status IN ('pending', 'dispatched', 'failed')
  )
);

CREATE INDEX IF NOT EXISTS idx_agent_schedules_agent
  ON agent_schedules(agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_schedules_due
  ON agent_schedules(next_run_at, id)
  WHERE enabled AND next_run_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_schedule_runs (
  id TEXT PRIMARY KEY,
  schedule_id TEXT NOT NULL REFERENCES agent_schedules(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  scheduled_for TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  error_message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (schedule_id, scheduled_for),
  CONSTRAINT agent_schedule_runs_status_check CHECK (status IN ('pending', 'dispatched', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_agent_schedule_runs_schedule
  ON agent_schedule_runs(schedule_id, scheduled_for DESC);

ALTER TABLE agent_schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_schedules FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_schedules_workspace_isolation ON agent_schedules
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), ''))
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), ''))
  );

ALTER TABLE agent_schedule_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_schedule_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_schedule_runs_workspace_isolation ON agent_schedule_runs
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (SELECT 1 FROM agent_schedules s WHERE s.id = schedule_id)
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (SELECT 1 FROM agent_schedules s WHERE s.id = schedule_id)
  );
