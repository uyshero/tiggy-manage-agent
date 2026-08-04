CREATE SEQUENCE IF NOT EXISTS tma_event_subscription_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_event_delivery_id_seq;

CREATE TABLE IF NOT EXISTS event_subscriptions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  app_id TEXT NOT NULL,
  name TEXT NOT NULL,
  endpoint_url TEXT NOT NULL,
  event_types TEXT[] NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  secret_version INTEGER NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT event_subscriptions_app_fkey
    FOREIGN KEY (workspace_id, app_id) REFERENCES service_identities(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT event_subscriptions_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 128),
  CONSTRAINT event_subscriptions_endpoint_check CHECK (length(btrim(endpoint_url)) BETWEEN 1 AND 2048),
  CONSTRAINT event_subscriptions_types_check CHECK (cardinality(event_types) BETWEEN 1 AND 16),
  CONSTRAINT event_subscriptions_status_check CHECK (status IN ('active', 'disabled')),
  CONSTRAINT event_subscriptions_secret_version_check CHECK (secret_version > 0),
  UNIQUE (workspace_id, app_id, name)
);

CREATE INDEX IF NOT EXISTS idx_event_subscriptions_workspace_app
  ON event_subscriptions(workspace_id, app_id, created_at);

CREATE INDEX IF NOT EXISTS idx_event_subscriptions_active_types
  ON event_subscriptions USING GIN(event_types)
  WHERE status = 'active';

CREATE TABLE IF NOT EXISTS event_deliveries (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  subscription_id TEXT NOT NULL REFERENCES event_subscriptions(id) ON DELETE CASCADE,
  app_id TEXT NOT NULL,
  source_event_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload_json JSONB NOT NULL,
  endpoint_url TEXT NOT NULL,
  secret_version INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMPTZ,
  last_http_status INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  delivered_at TIMESTAMPTZ,
  CONSTRAINT event_deliveries_app_fkey
    FOREIGN KEY (workspace_id, app_id) REFERENCES service_identities(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT event_deliveries_payload_check CHECK (jsonb_typeof(payload_json) = 'object'),
  CONSTRAINT event_deliveries_status_check CHECK (status IN ('pending', 'delivering', 'delivered', 'dead_letter')),
  CONSTRAINT event_deliveries_attempt_check CHECK (attempt_count >= 0),
  CONSTRAINT event_deliveries_http_status_check CHECK (last_http_status BETWEEN 0 AND 599),
  UNIQUE (subscription_id, source_event_id)
);

CREATE INDEX IF NOT EXISTS idx_event_deliveries_claim
  ON event_deliveries(next_attempt_at, created_at, id)
  WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_event_deliveries_expired_lease
  ON event_deliveries(lease_expires_at, created_at, id)
  WHERE status = 'delivering';

CREATE INDEX IF NOT EXISTS idx_event_deliveries_subscription_status
  ON event_deliveries(workspace_id, subscription_id, status, created_at DESC);

ALTER TABLE event_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE event_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_deliveries FORCE ROW LEVEL SECURITY;

CREATE POLICY event_subscriptions_workspace_isolation ON event_subscriptions
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

CREATE POLICY event_deliveries_workspace_isolation ON event_deliveries
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
