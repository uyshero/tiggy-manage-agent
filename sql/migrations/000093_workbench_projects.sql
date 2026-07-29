CREATE SEQUENCE IF NOT EXISTS tma_workbench_project_id_seq;

CREATE TABLE IF NOT EXISTS workbench_projects (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  owner_id TEXT NOT NULL,
  plugin_id TEXT NOT NULL,
  name TEXT NOT NULL,
  objective TEXT NOT NULL DEFAULT '',
  repository_provider TEXT NOT NULL DEFAULT 'gitlab',
  repository_path TEXT NOT NULL,
  repository_id TEXT NOT NULL DEFAULT '',
  repository_url TEXT NOT NULL DEFAULT '',
  default_branch TEXT NOT NULL DEFAULT 'main',
  sync_status TEXT NOT NULL DEFAULT 'local',
  sync_error TEXT NOT NULL DEFAULT '',
  notebook_url TEXT NOT NULL DEFAULT '',
  active_file TEXT NOT NULL DEFAULT '',
  notebook_code TEXT NOT NULL DEFAULT '',
  files_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT workbench_projects_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 120),
  CONSTRAINT workbench_projects_plugin_check CHECK (length(btrim(plugin_id)) BETWEEN 1 AND 240),
  CONSTRAINT workbench_projects_repository_path_check CHECK (length(btrim(repository_path)) BETWEEN 1 AND 240),
  CONSTRAINT workbench_projects_sync_status_check CHECK (sync_status IN ('local', 'syncing', 'synced', 'error')),
  CONSTRAINT workbench_projects_files_check CHECK (jsonb_typeof(files_json) = 'array'),
  UNIQUE (workspace_id, owner_id, plugin_id, repository_path)
);

CREATE INDEX IF NOT EXISTS workbench_projects_workspace_owner_updated_idx
  ON workbench_projects (workspace_id, owner_id, updated_at DESC, id DESC);

ALTER TABLE workbench_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE workbench_projects FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS workbench_projects_isolation ON workbench_projects;
CREATE POLICY workbench_projects_isolation
  ON workbench_projects
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
    AND (
      NULLIF(current_setting('tma.owner_id', true), '') IS NULL
      OR owner_id = NULLIF(current_setting('tma.owner_id', true), '')
    )
  );
