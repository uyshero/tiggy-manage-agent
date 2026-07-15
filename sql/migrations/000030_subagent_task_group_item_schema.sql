ALTER TABLE subagent_task_group_items
  ADD COLUMN IF NOT EXISTS expected_result_schema JSONB NOT NULL DEFAULT 'null'::jsonb;
