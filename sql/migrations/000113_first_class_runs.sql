CREATE TABLE IF NOT EXISTS session_runs (
  session_id TEXT NOT NULL,
  id TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  agent_config_version INTEGER NOT NULL,
  status TEXT NOT NULL,
  user_event_id TEXT REFERENCES session_events(id) ON DELETE SET NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  current_attempt_id TEXT,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ,
  interrupt_requested_at TIMESTAMPTZ,
  error_message TEXT,
  idempotency_key TEXT,
  request_hash TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (session_id, id),
  CONSTRAINT session_runs_turn_fkey
    FOREIGN KEY (session_id, turn_id) REFERENCES session_turns(session_id, id) ON DELETE CASCADE,
  CONSTRAINT session_runs_agent_config_version_fkey
    FOREIGN KEY (agent_id, agent_config_version) REFERENCES agent_config_versions(agent_id, version),
  CONSTRAINT session_runs_status_check CHECK (
    status IN ('running', 'waiting_approval', 'waiting_human', 'interrupted', 'completed', 'failed')
  ),
  CONSTRAINT session_runs_attempt_count_check CHECK (attempt_count >= 0),
  UNIQUE (session_id, turn_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_runs_idempotency_key
  ON session_runs(session_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_session_runs_session_started
  ON session_runs(session_id, started_at, id);

CREATE INDEX IF NOT EXISTS idx_session_runs_agent_config
  ON session_runs(agent_id, agent_config_version, started_at DESC);

CREATE TABLE IF NOT EXISTS session_run_attempts (
  session_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  id TEXT NOT NULL,
  attempt_number INTEGER NOT NULL,
  workspace_id TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  status TEXT NOT NULL,
  lease_owner TEXT,
  lease_expires_at TIMESTAMPTZ,
  last_heartbeat_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ,
  error_message TEXT,
  migration_snapshot BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (session_id, run_id, id),
  CONSTRAINT session_run_attempts_run_fkey
    FOREIGN KEY (session_id, run_id) REFERENCES session_runs(session_id, id) ON DELETE CASCADE,
  CONSTRAINT session_run_attempts_number_check CHECK (attempt_number > 0),
  CONSTRAINT session_run_attempts_status_check CHECK (
    status IN ('running', 'suspended', 'completed', 'failed', 'interrupted', 'abandoned')
  ),
  UNIQUE (session_id, run_id, attempt_number)
);

CREATE INDEX IF NOT EXISTS idx_session_run_attempts_run_number
  ON session_run_attempts(session_id, run_id, attempt_number);

CREATE INDEX IF NOT EXISTS idx_session_run_attempts_active_lease
  ON session_run_attempts(workspace_id, status, lease_expires_at)
  WHERE status = 'running';

INSERT INTO session_runs (
  session_id, id, turn_id, workspace_id, owner_id, agent_id, agent_config_version,
  status, user_event_id, attempt_count, current_attempt_id, started_at, ended_at,
  interrupt_requested_at, error_message, idempotency_key, request_hash
)
SELECT
  turn.session_id, turn.id, turn.id, turn.workspace_id, turn.owner_id,
  turn.agent_id, turn.agent_config_version, turn.status, turn.user_event_id,
  turn.attempt_count,
  CASE WHEN turn.attempt_count > 0 THEN 'attempt_' || lpad(turn.attempt_count::TEXT, 6, '0') END,
  turn.started_at, turn.ended_at, turn.interrupt_requested_at, turn.error_message,
  turn.idempotency_key, turn.request_hash
FROM session_turns turn
ON CONFLICT (session_id, id) DO NOTHING;

INSERT INTO session_run_attempts (
  session_id, run_id, id, attempt_number, workspace_id, owner_id, status,
  lease_owner, lease_expires_at, last_heartbeat_at, started_at, ended_at,
  error_message, migration_snapshot
)
SELECT
  turn.session_id, turn.id,
  'attempt_' || lpad(turn.attempt_count::TEXT, 6, '0'), turn.attempt_count,
  turn.workspace_id, turn.owner_id,
  CASE
    WHEN turn.status = 'completed' THEN 'completed'
    WHEN turn.status = 'failed' THEN 'failed'
    WHEN turn.status = 'interrupted' THEN 'interrupted'
    WHEN turn.status IN ('waiting_approval', 'waiting_human') THEN 'suspended'
    WHEN NULLIF(turn.lease_owner, '') IS NOT NULL THEN 'running'
    ELSE 'abandoned'
  END,
  NULLIF(turn.lease_owner, ''), turn.lease_expires_at, turn.last_heartbeat_at,
  turn.started_at, turn.ended_at, turn.error_message, true
FROM session_turns turn
WHERE turn.attempt_count > 0
ON CONFLICT (session_id, run_id, attempt_number) DO NOTHING;

ALTER TABLE session_events
  ADD COLUMN IF NOT EXISTS run_id TEXT,
  ADD COLUMN IF NOT EXISTS attempt_id TEXT;

UPDATE session_events event
SET run_id = event.turn_id
WHERE event.run_id IS NULL
  AND event.turn_id IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM session_runs run
    WHERE run.session_id = event.session_id AND run.id = event.turn_id
  );

ALTER TABLE session_events
  ADD CONSTRAINT session_events_run_fkey
  FOREIGN KEY (session_id, run_id) REFERENCES session_runs(session_id, id) ON DELETE CASCADE;

ALTER TABLE session_events
  ADD CONSTRAINT session_events_attempt_fkey
  FOREIGN KEY (session_id, run_id, attempt_id)
  REFERENCES session_run_attempts(session_id, run_id, id) ON DELETE SET NULL (attempt_id);

CREATE INDEX IF NOT EXISTS idx_session_events_session_run_seq
  ON session_events(session_id, run_id, seq)
  WHERE run_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_session_events_session_run_attempt_seq
  ON session_events(session_id, run_id, attempt_id, seq)
  WHERE attempt_id IS NOT NULL;

ALTER TABLE session_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE session_run_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_run_attempts FORCE ROW LEVEL SECURITY;

CREATE POLICY session_runs_session_isolation ON session_runs
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1 FROM sessions
      WHERE sessions.id = session_runs.session_id
        AND sessions.workspace_id = session_runs.workspace_id
        AND sessions.owner_id = session_runs.owner_id
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
      WHERE sessions.id = session_runs.session_id
        AND sessions.workspace_id = session_runs.workspace_id
        AND sessions.owner_id = session_runs.owner_id
    )
  );

CREATE POLICY session_run_attempts_session_isolation ON session_run_attempts
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1 FROM session_runs run
      WHERE run.session_id = session_run_attempts.session_id
        AND run.id = session_run_attempts.run_id
        AND run.workspace_id = session_run_attempts.workspace_id
        AND run.owner_id = session_run_attempts.owner_id
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
    AND EXISTS (
      SELECT 1 FROM session_runs run
      WHERE run.session_id = session_run_attempts.session_id
        AND run.id = session_run_attempts.run_id
        AND run.workspace_id = session_run_attempts.workspace_id
        AND run.owner_id = session_run_attempts.owner_id
    )
  );
