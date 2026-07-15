ALTER TABLE observability_exporter_runs
  ADD COLUMN IF NOT EXISTS workspace_id TEXT REFERENCES workspaces(id);

UPDATE observability_exporter_runs runs
SET workspace_id = sessions.workspace_id
FROM sessions
WHERE sessions.id = runs.session_id
  AND runs.workspace_id IS NULL;

ALTER TABLE observability_exporter_runs
  ALTER COLUMN workspace_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_observability_exporter_runs_workspace_finished
  ON observability_exporter_runs(workspace_id, finished_at DESC);

ALTER TABLE observability_exporter_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE observability_exporter_runs FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS observability_exporter_runs_session_isolation
  ON observability_exporter_runs;

CREATE POLICY observability_exporter_runs_session_isolation
  ON observability_exporter_runs
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = observability_exporter_runs.session_id
        AND sessions.workspace_id = observability_exporter_runs.workspace_id
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = observability_exporter_runs.session_id
        AND sessions.workspace_id = observability_exporter_runs.workspace_id
    )
  );

ALTER TABLE operator_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_audit_log FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS operator_audit_log_workspace_isolation
  ON operator_audit_log;

CREATE POLICY operator_audit_log_workspace_isolation
  ON operator_audit_log
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      session_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM sessions
        WHERE sessions.id = operator_audit_log.session_id
          AND sessions.workspace_id = operator_audit_log.workspace_id
      )
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      session_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM sessions
        WHERE sessions.id = operator_audit_log.session_id
          AND sessions.workspace_id = operator_audit_log.workspace_id
      )
    )
  );
