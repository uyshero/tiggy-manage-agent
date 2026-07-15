ALTER TABLE subagent_task_groups
  ADD COLUMN IF NOT EXISTS parent_group_id TEXT,
  ADD COLUMN IF NOT EXISTS parent_item_index INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_subagent_task_groups_parent_group
  ON subagent_task_groups(parent_group_id, parent_item_index, created_at DESC)
  WHERE parent_group_id IS NOT NULL AND parent_group_id <> '';
