CREATE TABLE IF NOT EXISTS observability_exporter_runs (
  id TEXT PRIMARY KEY,
  exporter TEXT NOT NULL,
  status TEXT NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  turn_id TEXT NOT NULL,
  trace_id TEXT,
  destination TEXT,
  message TEXT,
  attempt_count INTEGER NOT NULL DEFAULT 1,
  next_retry_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ NOT NULL,
  finished_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE observability_exporter_runs
  ADD COLUMN IF NOT EXISTS attempt_count INTEGER NOT NULL DEFAULT 1;

ALTER TABLE observability_exporter_runs
  ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ;

CREATE SEQUENCE IF NOT EXISTS tma_observability_exporter_run_id_seq;

CREATE INDEX IF NOT EXISTS idx_observability_exporter_runs_finished
  ON observability_exporter_runs(finished_at DESC);

CREATE INDEX IF NOT EXISTS idx_observability_exporter_runs_exporter_status
  ON observability_exporter_runs(exporter, status, finished_at DESC);

CREATE INDEX IF NOT EXISTS idx_observability_exporter_runs_session_turn
  ON observability_exporter_runs(session_id, turn_id, finished_at DESC);

CREATE INDEX IF NOT EXISTS idx_observability_exporter_runs_retry
  ON observability_exporter_runs(status, next_retry_at, attempt_count)
  WHERE status = 'failed' AND next_retry_at IS NOT NULL;
