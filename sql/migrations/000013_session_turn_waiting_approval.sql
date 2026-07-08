ALTER TABLE session_turns
  DROP CONSTRAINT IF EXISTS session_turns_status_check;

ALTER TABLE session_turns
  ADD CONSTRAINT session_turns_status_check CHECK (
    status IN (
      'running',
      'waiting_approval',
      'interrupted',
      'completed',
      'failed'
    )
  );
