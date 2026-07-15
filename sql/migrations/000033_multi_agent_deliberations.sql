CREATE TABLE IF NOT EXISTS agent_deliberations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  owner_id TEXT NOT NULL,
  parent_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  parent_turn_id TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT NOT NULL DEFAULT '',
  objective TEXT NOT NULL,
  strategy TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  phase TEXT NOT NULL DEFAULT 'round1_running',
  max_participants INTEGER NOT NULL,
  max_rounds INTEGER NOT NULL DEFAULT 2,
  max_tokens BIGINT NOT NULL DEFAULT 0,
  max_seconds INTEGER NOT NULL DEFAULT 0,
  moderator_agent_id TEXT NOT NULL REFERENCES agents(id),
  moderator_environment_id TEXT NOT NULL REFERENCES environments(id),
  plan_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  final_group_id TEXT REFERENCES subagent_task_groups(id) ON DELETE SET NULL,
  final_result_json JSONB NOT NULL DEFAULT 'null'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  canceled_at TIMESTAMPTZ,
  cancel_reason TEXT NOT NULL DEFAULT '',
  CONSTRAINT agent_deliberations_status_check CHECK (status IN ('running', 'completed', 'failed', 'canceled')),
  CONSTRAINT agent_deliberations_rounds_check CHECK (max_rounds = 2),
  CONSTRAINT agent_deliberations_participants_check CHECK (max_participants BETWEEN 2 AND 8)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_deliberations_idempotency
  ON agent_deliberations(parent_session_id, idempotency_key)
  WHERE idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_agent_deliberations_parent
  ON agent_deliberations(parent_session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_deliberation_participants (
  deliberation_id TEXT NOT NULL REFERENCES agent_deliberations(id) ON DELETE CASCADE,
  participant_index INTEGER NOT NULL,
  role_id TEXT NOT NULL,
  role_title TEXT NOT NULL,
  goal TEXT NOT NULL,
  agent_id TEXT NOT NULL REFERENCES agents(id),
  environment_id TEXT NOT NULL REFERENCES environments(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (deliberation_id, participant_index),
  UNIQUE (deliberation_id, role_id)
);

CREATE TABLE IF NOT EXISTS agent_deliberation_rounds (
  deliberation_id TEXT NOT NULL REFERENCES agent_deliberations(id) ON DELETE CASCADE,
  round_number INTEGER NOT NULL,
  round_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  task_group_id TEXT NOT NULL REFERENCES subagent_task_groups(id) ON DELETE CASCADE,
  moderator_group_id TEXT REFERENCES subagent_task_groups(id) ON DELETE SET NULL,
  summary_json JSONB NOT NULL DEFAULT 'null'::jsonb,
  questions_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  PRIMARY KEY (deliberation_id, round_number),
  CONSTRAINT agent_deliberation_round_number_check CHECK (round_number BETWEEN 1 AND 2),
  CONSTRAINT agent_deliberation_round_status_check CHECK (status IN ('running', 'moderating', 'completed', 'failed', 'canceled'))
);

CREATE TABLE IF NOT EXISTS agent_deliberation_contributions (
  deliberation_id TEXT NOT NULL,
  round_number INTEGER NOT NULL,
  participant_index INTEGER NOT NULL,
  task_group_id TEXT NOT NULL REFERENCES subagent_task_groups(id) ON DELETE CASCADE,
  item_index INTEGER NOT NULL,
  session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  status TEXT NOT NULL,
  contribution_text TEXT NOT NULL DEFAULT '',
  contribution_json JSONB NOT NULL DEFAULT 'null'::jsonb,
  retry_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (deliberation_id, round_number, participant_index),
  FOREIGN KEY (deliberation_id, round_number)
    REFERENCES agent_deliberation_rounds(deliberation_id, round_number) ON DELETE CASCADE,
  FOREIGN KEY (deliberation_id, participant_index)
    REFERENCES agent_deliberation_participants(deliberation_id, participant_index) ON DELETE CASCADE
);

CREATE SEQUENCE IF NOT EXISTS tma_agent_deliberation_id_seq;
