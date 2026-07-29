ALTER TABLE workbench_projects
  ADD COLUMN IF NOT EXISTS runtime_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS runtime_status TEXT NOT NULL DEFAULT 'unconfigured',
  ADD COLUMN IF NOT EXISTS runtime_url TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS runtime_error TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS runtime_started_at TIMESTAMPTZ;

ALTER TABLE workbench_projects
  DROP CONSTRAINT IF EXISTS workbench_projects_runtime_status_check;

ALTER TABLE workbench_projects
  ADD CONSTRAINT workbench_projects_runtime_status_check
  CHECK (runtime_status IN ('unconfigured', 'starting', 'running', 'stopped', 'error'));
