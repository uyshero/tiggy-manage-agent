DO $$ BEGIN
  ALTER TABLE service_identities
    ADD CONSTRAINT service_identities_workspace_id_id_unique UNIQUE (workspace_id, id);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE agents
  ADD COLUMN IF NOT EXISTS app_id TEXT,
  ADD COLUMN IF NOT EXISTS external_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS labels_json JSONB NOT NULL DEFAULT '{}'::jsonb;

DO $$ BEGIN
  ALTER TABLE agents ADD CONSTRAINT agents_app_identity_fkey
    FOREIGN KEY (workspace_id, app_id) REFERENCES service_identities(workspace_id, id);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE agents ADD CONSTRAINT agents_external_ref_app_check CHECK (external_ref = '' OR app_id IS NOT NULL);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE agents ADD CONSTRAINT agents_labels_object_check CHECK (jsonb_typeof(labels_json) = 'object');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS agents_app_external_ref_unique
  ON agents(workspace_id, app_id, external_ref)
  WHERE app_id IS NOT NULL AND external_ref <> '';

ALTER TABLE environments
  ADD COLUMN IF NOT EXISTS app_id TEXT,
  ADD COLUMN IF NOT EXISTS external_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS labels_json JSONB NOT NULL DEFAULT '{}'::jsonb;

DO $$ BEGIN
  ALTER TABLE environments ADD CONSTRAINT environments_app_identity_fkey
    FOREIGN KEY (workspace_id, app_id) REFERENCES service_identities(workspace_id, id);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE environments ADD CONSTRAINT environments_external_ref_app_check CHECK (external_ref = '' OR app_id IS NOT NULL);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE environments ADD CONSTRAINT environments_labels_object_check CHECK (jsonb_typeof(labels_json) = 'object');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS environments_app_external_ref_unique
  ON environments(workspace_id, app_id, external_ref)
  WHERE app_id IS NOT NULL AND external_ref <> '';

ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS app_id TEXT,
  ADD COLUMN IF NOT EXISTS external_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS labels_json JSONB NOT NULL DEFAULT '{}'::jsonb;

DO $$ BEGIN
  ALTER TABLE sessions ADD CONSTRAINT sessions_app_identity_fkey
    FOREIGN KEY (workspace_id, app_id) REFERENCES service_identities(workspace_id, id);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE sessions ADD CONSTRAINT sessions_external_ref_app_check CHECK (external_ref = '' OR app_id IS NOT NULL);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE sessions ADD CONSTRAINT sessions_labels_object_check CHECK (jsonb_typeof(labels_json) = 'object');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS sessions_app_external_ref_unique
  ON sessions(workspace_id, app_id, external_ref)
  WHERE app_id IS NOT NULL AND external_ref <> '';

ALTER TABLE skills
  ADD COLUMN IF NOT EXISTS app_id TEXT,
  ADD COLUMN IF NOT EXISTS external_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS labels_json JSONB NOT NULL DEFAULT '{}'::jsonb;

DO $$ BEGIN
  ALTER TABLE skills ADD CONSTRAINT skills_app_identity_fkey
    FOREIGN KEY (workspace_id, app_id) REFERENCES service_identities(workspace_id, id);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE skills ADD CONSTRAINT skills_external_ref_app_check CHECK (external_ref = '' OR app_id IS NOT NULL);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE skills ADD CONSTRAINT skills_labels_object_check CHECK (jsonb_typeof(labels_json) = 'object');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS skills_app_external_ref_unique
  ON skills(workspace_id, app_id, external_ref)
  WHERE app_id IS NOT NULL AND external_ref <> '';

ALTER TABLE mcp_registry_servers
  ADD COLUMN IF NOT EXISTS app_id TEXT,
  ADD COLUMN IF NOT EXISTS external_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS labels_json JSONB NOT NULL DEFAULT '{}'::jsonb;

DO $$ BEGIN
  ALTER TABLE mcp_registry_servers ADD CONSTRAINT mcp_registry_servers_app_identity_fkey
    FOREIGN KEY (workspace_id, app_id) REFERENCES service_identities(workspace_id, id);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE mcp_registry_servers ADD CONSTRAINT mcp_registry_servers_external_ref_app_check CHECK (external_ref = '' OR app_id IS NOT NULL);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  ALTER TABLE mcp_registry_servers ADD CONSTRAINT mcp_registry_servers_labels_object_check CHECK (jsonb_typeof(labels_json) = 'object');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS mcp_registry_servers_app_external_ref_unique
  ON mcp_registry_servers(workspace_id, app_id, external_ref)
  WHERE app_id IS NOT NULL AND external_ref <> '';
