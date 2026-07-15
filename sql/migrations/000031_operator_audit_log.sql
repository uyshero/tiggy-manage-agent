CREATE TABLE IF NOT EXISTS operator_audit_log (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  principal_id TEXT NOT NULL,
  operator_label TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'admin',
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL,
  error_message TEXT NOT NULL DEFAULT '',
  details_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT operator_audit_log_outcome_check CHECK (outcome IN ('succeeded', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_operator_audit_log_session
  ON operator_audit_log(session_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_operator_audit_log_principal
  ON operator_audit_log(principal_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_operator_audit_log_action
  ON operator_audit_log(action, created_at DESC);

CREATE SEQUENCE IF NOT EXISTS tma_operator_audit_id_seq;
