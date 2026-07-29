ALTER TABLE agent_schedules
  ALTER COLUMN approval_mode SET DEFAULT 'request_approval';

ALTER TABLE agent_schedules
  DROP CONSTRAINT IF EXISTS agent_schedules_approval_mode_check;

ALTER TABLE agent_schedules
  ADD CONSTRAINT agent_schedules_approval_mode_check
  CHECK (approval_mode IN ('request_approval', 'approve_for_me', 'full_access'));
