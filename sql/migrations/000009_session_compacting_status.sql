ALTER TABLE sessions
  DROP CONSTRAINT IF EXISTS sessions_status_check;

ALTER TABLE sessions
  ADD CONSTRAINT sessions_status_check CHECK (
    status IN (
      'provisioning',
      'idle',
      'running',
      'interrupting',
      'compacting',
      'failed',
      'terminated'
    )
  );
