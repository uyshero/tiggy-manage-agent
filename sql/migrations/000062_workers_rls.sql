ALTER TABLE workers ENABLE ROW LEVEL SECURITY;
ALTER TABLE workers FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS workers_workspace_isolation ON workers;

CREATE POLICY workers_workspace_isolation
  ON workers
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  );

ALTER TABLE worker_work ENABLE ROW LEVEL SECURITY;
ALTER TABLE worker_work FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS worker_work_workspace_isolation ON worker_work;

CREATE POLICY worker_work_workspace_isolation
  ON worker_work
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      worker_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM workers
        WHERE workers.id = worker_work.worker_id
          AND workers.workspace_id = worker_work.workspace_id
      )
    )
    AND (
      environment_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM environments
        WHERE environments.id = worker_work.environment_id
          AND environments.workspace_id = worker_work.workspace_id
      )
    )
    AND (
      session_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM sessions
        WHERE sessions.id = worker_work.session_id
          AND sessions.workspace_id = worker_work.workspace_id
      )
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      worker_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM workers
        WHERE workers.id = worker_work.worker_id
          AND workers.workspace_id = worker_work.workspace_id
      )
    )
    AND (
      environment_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM environments
        WHERE environments.id = worker_work.environment_id
          AND environments.workspace_id = worker_work.workspace_id
      )
    )
    AND (
      session_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM sessions
        WHERE sessions.id = worker_work.session_id
          AND sessions.workspace_id = worker_work.workspace_id
      )
    )
  );
