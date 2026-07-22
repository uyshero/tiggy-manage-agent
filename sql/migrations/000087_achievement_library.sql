CREATE TABLE achievement_library_items (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  object_ref_id TEXT NOT NULL REFERENCES object_refs(id) ON DELETE RESTRICT,
  source_session_id TEXT NOT NULL DEFAULT '',
  source_artifact_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  directory TEXT NOT NULL DEFAULT '',
  tags_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT achievement_library_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 512),
  CONSTRAINT achievement_library_directory_check CHECK (length(directory) <= 512),
  CONSTRAINT achievement_library_tags_check CHECK (jsonb_typeof(tags_json) = 'array')
);

CREATE INDEX achievement_library_workspace_updated_idx
  ON achievement_library_items (workspace_id, updated_at DESC, id DESC);

CREATE INDEX achievement_library_workspace_directory_idx
  ON achievement_library_items (workspace_id, directory, updated_at DESC);

CREATE INDEX achievement_library_object_ref_idx
  ON achievement_library_items (object_ref_id);

CREATE SEQUENCE tma_achievement_library_item_id_seq;

ALTER TABLE achievement_library_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE achievement_library_items FORCE ROW LEVEL SECURITY;

CREATE POLICY achievement_library_items_isolation
  ON achievement_library_items
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
