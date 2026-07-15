ALTER TABLE subagent_task_groups
  ADD COLUMN IF NOT EXISTS result_reducer TEXT NOT NULL DEFAULT 'concat_text';
