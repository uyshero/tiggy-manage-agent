CREATE TABLE IF NOT EXISTS security_audit_outbox (
  id TEXT PRIMARY KEY,
  payload_json TEXT NOT NULL,
  integrity_algorithm TEXT NOT NULL,
  integrity_digest TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  delivered_at TIMESTAMPTZ,
  CONSTRAINT security_audit_outbox_payload_check CHECK (jsonb_typeof(payload_json::jsonb) = 'object'),
  CONSTRAINT security_audit_outbox_status_check CHECK (status IN ('pending', 'delivering', 'delivered', 'dead_letter')),
  CONSTRAINT security_audit_outbox_attempt_check CHECK (attempt_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_security_audit_outbox_claim
  ON security_audit_outbox(next_attempt_at, created_at)
  WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_security_audit_outbox_expired_lease
  ON security_audit_outbox(lease_expires_at, created_at)
  WHERE status = 'delivering';

CREATE INDEX IF NOT EXISTS idx_security_audit_outbox_status_created
  ON security_audit_outbox(status, created_at);

CREATE INDEX IF NOT EXISTS idx_security_audit_outbox_delivered
  ON security_audit_outbox(delivered_at)
  WHERE status = 'delivered';
