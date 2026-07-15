ALTER TABLE security_audit_outbox
  ADD COLUMN IF NOT EXISTS integrity_key_id TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'security_audit_outbox_integrity_key_id_check'
      AND conrelid = 'security_audit_outbox'::regclass
  ) THEN
    ALTER TABLE security_audit_outbox
      ADD CONSTRAINT security_audit_outbox_integrity_key_id_check
      CHECK (
        integrity_key_id = ''
        OR integrity_key_id ~ '^[A-Za-z0-9._-]{1,128}$'
      );
  END IF;
END
$$;
