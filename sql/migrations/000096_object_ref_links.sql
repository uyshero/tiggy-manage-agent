CREATE TABLE IF NOT EXISTS object_ref_links (
  object_ref_id TEXT NOT NULL REFERENCES object_refs(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  owner_type TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  role TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (object_ref_id, owner_type, owner_id, role),
  CONSTRAINT object_ref_links_owner_type_check CHECK (
    owner_type IN (
      'session_artifact',
      'skill_asset',
      'skill_version',
      'skill_package_file',
      'workspace_snapshot',
      'achievement_library_item'
    )
  ),
  CONSTRAINT object_ref_links_owner_id_check CHECK (btrim(owner_id) <> ''),
  CONSTRAINT object_ref_links_role_check CHECK (btrim(role) <> '')
);

CREATE INDEX IF NOT EXISTS idx_object_ref_links_workspace_owner
  ON object_ref_links(workspace_id, owner_type, owner_id);

CREATE INDEX IF NOT EXISTS idx_object_ref_links_workspace_object
  ON object_ref_links(workspace_id, object_ref_id);

INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role, created_at)
SELECT session_artifacts.object_ref_id, session_artifacts.workspace_id, 'session_artifact',
  session_artifacts.id, session_artifacts.artifact_type, session_artifacts.created_at
FROM session_artifacts
JOIN object_refs ON object_refs.id = session_artifacts.object_ref_id
  AND object_refs.workspace_id = session_artifacts.workspace_id
ON CONFLICT DO NOTHING;

INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role, created_at)
SELECT workspace_snapshots.object_ref_id, workspace_snapshots.workspace_id, 'workspace_snapshot',
  workspace_snapshots.id, 'snapshot', workspace_snapshots.created_at
FROM workspace_snapshots
JOIN object_refs ON object_refs.id = workspace_snapshots.object_ref_id
  AND object_refs.workspace_id = workspace_snapshots.workspace_id
ON CONFLICT DO NOTHING;

INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role, created_at)
SELECT achievement_library_items.object_ref_id, achievement_library_items.workspace_id, 'achievement_library_item',
  achievement_library_items.id, 'achievement', achievement_library_items.created_at
FROM achievement_library_items
JOIN object_refs ON object_refs.id = achievement_library_items.object_ref_id
  AND object_refs.workspace_id = achievement_library_items.workspace_id
ON CONFLICT DO NOTHING;

INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role, created_at)
SELECT asset->>'object_ref_id', s.workspace_id, 'skill_asset', sv.id || ':' || COALESCE(asset->>'path', ''), 'asset', sv.created_at
FROM skill_versions sv
JOIN skills s ON s.id = sv.skill_id
JOIN LATERAL jsonb_array_elements(
  CASE
    WHEN jsonb_typeof(sv.assets_json) = 'array' THEN sv.assets_json
    WHEN jsonb_typeof(sv.assets_json->'files') = 'array' THEN sv.assets_json->'files'
    ELSE '[]'::jsonb
  END
) asset ON TRUE
JOIN object_refs ON object_refs.id = asset->>'object_ref_id'
  AND object_refs.workspace_id = s.workspace_id
WHERE sv.package_format = 'legacy_db'
  AND COALESCE(asset->>'object_ref_id', '') <> ''
  AND COALESCE(asset->>'path', '') <> ''
ON CONFLICT DO NOTHING;

INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role, created_at)
SELECT sv.package_object_ref_id, s.workspace_id, 'skill_version', sv.id, 'package_archive', sv.created_at
FROM skill_versions sv
JOIN skills s ON s.id = sv.skill_id
JOIN object_refs ON object_refs.id = sv.package_object_ref_id
  AND object_refs.workspace_id = s.workspace_id
WHERE sv.package_object_ref_id IS NOT NULL
ON CONFLICT DO NOTHING;

INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role, created_at)
SELECT sv.skill_md_object_ref_id, s.workspace_id, 'skill_version', sv.id, 'skill_md', sv.created_at
FROM skill_versions sv
JOIN skills s ON s.id = sv.skill_id
JOIN object_refs ON object_refs.id = sv.skill_md_object_ref_id
  AND object_refs.workspace_id = s.workspace_id
WHERE sv.skill_md_object_ref_id IS NOT NULL
ON CONFLICT DO NOTHING;

INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role, created_at)
SELECT spf.object_ref_id, s.workspace_id, 'skill_package_file', spf.skill_version_id || ':' || spf.path, spf.role, spf.created_at
FROM skill_version_package_files spf
JOIN skill_versions sv ON sv.id = spf.skill_version_id
JOIN skills s ON s.id = sv.skill_id
JOIN object_refs ON object_refs.id = spf.object_ref_id
  AND object_refs.workspace_id = s.workspace_id
ON CONFLICT DO NOTHING;

ALTER TABLE object_ref_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE object_ref_links FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS object_ref_links_workspace_isolation ON object_ref_links;
CREATE POLICY object_ref_links_workspace_isolation
  ON object_ref_links
  FOR ALL
  USING (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  )
  WITH CHECK (
    workspace_id = NULLIF(current_setting('tma.workspace_id', true), '')
  );
