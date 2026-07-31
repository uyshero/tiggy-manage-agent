CREATE SEQUENCE IF NOT EXISTS tma_service_identity_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_service_credential_id_seq;

CREATE TABLE IF NOT EXISTS service_identities (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  kind TEXT NOT NULL DEFAULT 'application',
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'member',
  scopes TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
  status TEXT NOT NULL DEFAULT 'active',
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT service_identities_kind_check CHECK (kind IN ('application', 'service')),
  CONSTRAINT service_identities_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 120),
  CONSTRAINT service_identities_description_check CHECK (length(description) <= 1000),
  CONSTRAINT service_identities_role_check CHECK (role IN ('viewer', 'member', 'operator')),
  CONSTRAINT service_identities_scopes_check CHECK (cardinality(scopes) BETWEEN 1 AND 32 AND array_position(scopes, '') IS NULL),
  CONSTRAINT service_identities_status_check CHECK (status IN ('active', 'disabled')),
  CONSTRAINT service_identities_created_by_check CHECK (btrim(created_by) <> ''),
  UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS service_identity_credentials (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  service_identity_id TEXT NOT NULL REFERENCES service_identities(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  locator TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL,
  secret_hash BYTEA NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  expires_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ,
  CONSTRAINT service_identity_credentials_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 120),
  CONSTRAINT service_identity_credentials_locator_check CHECK (length(locator) BETWEEN 16 AND 64),
  CONSTRAINT service_identity_credentials_prefix_check CHECK (btrim(token_prefix) <> ''),
  CONSTRAINT service_identity_credentials_hash_check CHECK (octet_length(secret_hash) = 32),
  CONSTRAINT service_identity_credentials_status_check CHECK (status IN ('active', 'revoked')),
  CONSTRAINT service_identity_credentials_expiry_check CHECK (expires_at IS NULL OR expires_at > created_at),
  CONSTRAINT service_identity_credentials_revocation_check CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
  CONSTRAINT service_identity_credentials_created_by_check CHECK (btrim(created_by) <> '')
);

CREATE INDEX IF NOT EXISTS service_identities_workspace_idx
  ON service_identities(workspace_id, status, name, id);
CREATE INDEX IF NOT EXISTS service_identity_credentials_identity_idx
  ON service_identity_credentials(workspace_id, service_identity_id, created_at DESC);

ALTER TABLE service_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_identities FORCE ROW LEVEL SECURITY;
ALTER TABLE service_identity_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_identity_credentials FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS service_identities_workspace_isolation ON service_identities;
CREATE POLICY service_identities_workspace_isolation
  ON service_identities
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

DROP POLICY IF EXISTS service_identity_credentials_workspace_isolation ON service_identity_credentials;
CREATE POLICY service_identity_credentials_workspace_isolation
  ON service_identity_credentials
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '') AND
    EXISTS (
      SELECT 1 FROM service_identities identity
      WHERE identity.id = service_identity_id AND identity.workspace_id = service_identity_credentials.workspace_id
    )
  );

CREATE OR REPLACE FUNCTION tma_authenticate_service_credential(requested_locator TEXT, requested_secret_hash BYTEA)
RETURNS TABLE(
  identity_id TEXT, workspace_id TEXT, kind TEXT, name TEXT, description TEXT,
  role TEXT, scopes TEXT[], identity_status TEXT, created_by TEXT,
  identity_created_at TIMESTAMPTZ, identity_updated_at TIMESTAMPTZ, credential_id TEXT
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
  UPDATE public.service_identity_credentials credential
  SET last_used_at = now()
  FROM public.service_identities identity
  WHERE credential.locator = btrim(requested_locator)
    AND credential.secret_hash = requested_secret_hash
    AND credential.service_identity_id = identity.id
    AND credential.workspace_id = identity.workspace_id
    AND credential.status = 'active'
    AND identity.status = 'active'
    AND (credential.expires_at IS NULL OR credential.expires_at > now())
  RETURNING identity.id, identity.workspace_id, identity.kind, identity.name, identity.description,
    identity.role, identity.scopes, identity.status, identity.created_by,
    identity.created_at, identity.updated_at, credential.id;
$$;

ALTER TABLE model_invocations
  ADD COLUMN IF NOT EXISTS service_identity_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS model_invocations_workspace_service_identity_idx
  ON model_invocations(workspace_id, service_identity_id, started_at DESC)
  WHERE service_identity_id <> '';
