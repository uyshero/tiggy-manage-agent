ALTER TABLE session_task_items
  ADD COLUMN IF NOT EXISTS evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'session_task_items_evidence_refs_array_check'
      AND conrelid = 'session_task_items'::regclass
  ) THEN
    ALTER TABLE session_task_items
      ADD CONSTRAINT session_task_items_evidence_refs_array_check
      CHECK (jsonb_typeof(evidence_refs) = 'array');
  END IF;
END
$$;
