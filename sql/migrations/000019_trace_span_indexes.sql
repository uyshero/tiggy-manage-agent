CREATE TABLE IF NOT EXISTS trace_indexes (
  trace_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  turn_id TEXT NOT NULL,
  session_title TEXT NOT NULL DEFAULT '',
  session_status TEXT NOT NULL DEFAULT '',
  turn_status TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMPTZ NOT NULL,
  ended_at TIMESTAMPTZ NOT NULL,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  step_count INTEGER NOT NULL DEFAULT 0,
  span_count INTEGER NOT NULL DEFAULT 0,
  tool_calls INTEGER NOT NULL DEFAULT 0,
  errors INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, turn_id)
);

CREATE TABLE IF NOT EXISTS trace_span_indexes (
  trace_id TEXT NOT NULL REFERENCES trace_indexes(trace_id) ON DELETE CASCADE,
  span_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  session_title TEXT NOT NULL DEFAULT '',
  parent_span_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  depth INTEGER NOT NULL DEFAULT 0,
  start_time TIMESTAMPTZ NOT NULL,
  start_offset_ms BIGINT NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  self_duration_ms BIGINT NOT NULL DEFAULT 0,
  critical BOOLEAN NOT NULL DEFAULT FALSE,
  event_count INTEGER NOT NULL DEFAULT 0,
  attributes_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (trace_id, span_id)
);

CREATE INDEX IF NOT EXISTS idx_trace_indexes_workspace_started
  ON trace_indexes(workspace_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_trace_indexes_session_turn
  ON trace_indexes(session_id, turn_id);

CREATE INDEX IF NOT EXISTS idx_trace_span_indexes_search
  ON trace_span_indexes(workspace_id, kind, status, start_time DESC);

CREATE INDEX IF NOT EXISTS idx_trace_span_indexes_trace
  ON trace_span_indexes(trace_id, critical, duration_ms DESC);

CREATE INDEX IF NOT EXISTS idx_trace_span_indexes_session_turn
  ON trace_span_indexes(session_id, turn_id, start_time DESC);
