ALTER TABLE security_audit_outbox
  ADD COLUMN IF NOT EXISTS workspace_id TEXT;

UPDATE security_audit_outbox
SET workspace_id = NULLIF(BTRIM(payload_json::jsonb ->> 'workspace_id'), '')
WHERE workspace_id IS NULL
  AND EXISTS (
    SELECT 1
    FROM workspaces
    WHERE workspaces.id = NULLIF(BTRIM(security_audit_outbox.payload_json::jsonb ->> 'workspace_id'), '')
  );

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'security_audit_outbox_workspace_fk'
      AND conrelid = 'security_audit_outbox'::regclass
  ) THEN
    ALTER TABLE security_audit_outbox
      ADD CONSTRAINT security_audit_outbox_workspace_fk
      FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE SET NULL
      NOT VALID;
  END IF;
END
$$;

ALTER TABLE security_audit_outbox
  VALIDATE CONSTRAINT security_audit_outbox_workspace_fk;

CREATE INDEX IF NOT EXISTS idx_security_audit_outbox_workspace_status_created
  ON security_audit_outbox(workspace_id, status, created_at);

CREATE INDEX IF NOT EXISTS idx_security_audit_outbox_workspace_claim
  ON security_audit_outbox(workspace_id, next_attempt_at, created_at)
  WHERE status = 'pending';

ALTER TABLE security_audit_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_audit_outbox FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS security_audit_outbox_workspace_isolation
  ON security_audit_outbox;

CREATE POLICY security_audit_outbox_workspace_isolation
  ON security_audit_outbox
  FOR ALL
  USING (
    CASE
      WHEN current_setting('tma.security_audit_global', true) = 'on'
        THEN workspace_id IS NULL
      ELSE workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    END
  )
  WITH CHECK (
    CASE
      WHEN current_setting('tma.security_audit_global', true) = 'on'
        THEN workspace_id IS NULL
      ELSE workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    END
  );
