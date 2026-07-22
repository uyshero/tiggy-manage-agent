ALTER TABLE agent_schedules
  ADD COLUMN IF NOT EXISTS session_mode TEXT NOT NULL DEFAULT 'new_session',
  ADD COLUMN IF NOT EXISTS target_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS approval_mode TEXT NOT NULL DEFAULT 'approve_for_me';

ALTER TABLE agent_schedules DROP CONSTRAINT IF EXISTS agent_schedules_session_mode_check;
ALTER TABLE agent_schedules ADD CONSTRAINT agent_schedules_session_mode_check
  CHECK (session_mode IN ('new_session', 'existing_session'));
ALTER TABLE agent_schedules DROP CONSTRAINT IF EXISTS agent_schedules_approval_mode_check;
ALTER TABLE agent_schedules ADD CONSTRAINT agent_schedules_approval_mode_check
  CHECK (approval_mode IN ('approve_for_me', 'full_access'));
ALTER TABLE agent_schedules DROP CONSTRAINT IF EXISTS agent_schedules_target_session_check;
ALTER TABLE agent_schedules ADD CONSTRAINT agent_schedules_target_session_check
  CHECK (
    (session_mode = 'new_session' AND target_session_id IS NULL)
    OR session_mode = 'existing_session'
  );

ALTER TABLE agent_schedules DROP CONSTRAINT IF EXISTS agent_schedules_last_status_check;
ALTER TABLE agent_schedules ADD CONSTRAINT agent_schedules_last_status_check CHECK (
  last_run_status IS NULL OR last_run_status IN ('pending', 'waiting_session', 'dispatching', 'dispatched', 'failed')
);

ALTER TABLE agent_schedule_runs
  ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
ALTER TABLE agent_schedule_runs DROP CONSTRAINT IF EXISTS agent_schedule_runs_status_check;
ALTER TABLE agent_schedule_runs ADD CONSTRAINT agent_schedule_runs_status_check
  CHECK (status IN ('pending', 'waiting_session', 'dispatching', 'dispatched', 'failed'));

CREATE INDEX IF NOT EXISTS idx_agent_schedule_runs_dispatch_queue
  ON agent_schedule_runs(workspace_id, scheduled_for, id)
  WHERE status IN ('pending', 'waiting_session', 'dispatching');
CREATE INDEX IF NOT EXISTS idx_agent_schedules_target_session
  ON agent_schedules(target_session_id, id)
  WHERE session_mode = 'existing_session';
