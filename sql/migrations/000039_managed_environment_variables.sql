CREATE TABLE IF NOT EXISTS managed_environment_variables (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  ciphertext BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, name),
  CONSTRAINT managed_environment_variables_name_check CHECK (name ~ '^[A-Za-z_][A-Za-z0-9_]*$'),
  CONSTRAINT managed_environment_variables_ciphertext_check CHECK (octet_length(ciphertext) > 0)
);

CREATE INDEX IF NOT EXISTS idx_managed_environment_variables_workspace
  ON managed_environment_variables(workspace_id, name);
