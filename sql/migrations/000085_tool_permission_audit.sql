CREATE TABLE tool_permission_audit_records (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  turn_id TEXT NOT NULL,
  call_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  path TEXT NOT NULL DEFAULT '',
  decision TEXT NOT NULL,
  allowed BOOLEAN NOT NULL,
  required BOOLEAN NOT NULL,
  intervention_mode TEXT NOT NULL,
  approval_policy TEXT NOT NULL,
  approval_status TEXT NOT NULL,
  execution_status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  risk TEXT NOT NULL DEFAULT '',
  matched_rule_id TEXT NOT NULL DEFAULT '',
  rule_source TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (session_id, turn_id, call_id),
  CONSTRAINT tool_permission_audit_decision_check
    CHECK (decision IN ('allow', 'ask', 'deny')),
  CONSTRAINT tool_permission_audit_intervention_mode_check
    CHECK (intervention_mode IN ('request_approval', 'approve_for_me', 'full_access')),
  CONSTRAINT tool_permission_audit_approval_policy_check
    CHECK (approval_policy IN ('never', 'conditional', 'always')),
  CONSTRAINT tool_permission_audit_approval_status_check
    CHECK (approval_status IN ('not_required', 'pending', 'auto_approved', 'approved', 'rejected')),
  CONSTRAINT tool_permission_audit_execution_status_check
    CHECK (execution_status IN ('planned', 'denied', 'started', 'succeeded', 'failed', 'indeterminate')),
  CONSTRAINT tool_permission_audit_rule_source_check
    CHECK (rule_source IN ('', 'workspace', 'agent', 'session'))
);

CREATE INDEX tool_permission_audit_session_order_idx
  ON tool_permission_audit_records (session_id, created_at DESC, turn_id DESC, call_id DESC);

CREATE INDEX tool_permission_audit_session_decision_order_idx
  ON tool_permission_audit_records (session_id, decision, created_at DESC, turn_id DESC, call_id DESC);

CREATE INDEX tool_permission_audit_session_tool_order_idx
  ON tool_permission_audit_records (session_id, tool, created_at DESC, turn_id DESC, call_id DESC);

ALTER TABLE tool_permission_audit_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE tool_permission_audit_records FORCE ROW LEVEL SECURITY;

CREATE POLICY tool_permission_audit_records_isolation
  ON tool_permission_audit_records
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
