CREATE TABLE IF NOT EXISTS subagent_task_groups (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  owner_id TEXT NOT NULL,
  parent_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  parent_turn_id TEXT NOT NULL DEFAULT '',
  strategy TEXT NOT NULL DEFAULT 'all_completed',
  quorum INTEGER NOT NULL DEFAULT 0,
  fail_fast BOOLEAN NOT NULL DEFAULT FALSE,
  planned_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT subagent_task_groups_strategy_check CHECK (strategy IN ('all_completed', 'any_completed', 'quorum'))
);

CREATE INDEX IF NOT EXISTS idx_subagent_task_groups_parent
  ON subagent_task_groups(parent_session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS subagent_task_group_items (
  group_id TEXT NOT NULL REFERENCES subagent_task_groups(id) ON DELETE CASCADE,
  item_index INTEGER NOT NULL,
  agent_id TEXT NOT NULL REFERENCES agents(id),
  environment_id TEXT NOT NULL REFERENCES environments(id),
  session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  title TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL DEFAULT '',
  priority INTEGER NOT NULL DEFAULT 0,
  initial_state TEXT NOT NULL DEFAULT 'created',
  error_type TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, item_index),
  CONSTRAINT subagent_task_group_items_state_check CHECK (initial_state IN ('created', 'started', 'queued', 'rejected'))
);

CREATE INDEX IF NOT EXISTS idx_subagent_task_group_items_session
  ON subagent_task_group_items(session_id)
  WHERE session_id IS NOT NULL;

CREATE SEQUENCE IF NOT EXISTS tma_subagent_task_group_id_seq;
