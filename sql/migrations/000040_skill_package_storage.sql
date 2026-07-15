ALTER TABLE skill_versions
  ADD COLUMN IF NOT EXISTS package_format TEXT NOT NULL DEFAULT 'legacy_db',
  ADD COLUMN IF NOT EXISTS package_root TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS package_checksum_sha256 TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS package_object_ref_id TEXT REFERENCES object_refs(id),
  ADD COLUMN IF NOT EXISTS skill_md_object_ref_id TEXT REFERENCES object_refs(id),
  ADD COLUMN IF NOT EXISTS package_manifest_json JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE skill_versions
  DROP CONSTRAINT IF EXISTS skill_versions_package_format_check;

ALTER TABLE skill_versions
  ADD CONSTRAINT skill_versions_package_format_check
  CHECK (package_format IN ('legacy_db', 'tma.skill-package.v1'));

CREATE TABLE IF NOT EXISTS skill_version_package_files (
  skill_version_id TEXT NOT NULL REFERENCES skill_versions(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'asset',
  object_ref_id TEXT NOT NULL REFERENCES object_refs(id),
  content_type TEXT NOT NULL DEFAULT '',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  is_binary BOOLEAN NOT NULL DEFAULT false,
  executable BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (skill_version_id, path),
  CONSTRAINT skill_version_package_files_path_check CHECK (path <> '' AND path !~ '(^/|(^|/)\.\.(/|$))'),
  CONSTRAINT skill_version_package_files_role_check CHECK (role IN ('skill_md', 'asset', 'archive')),
  CONSTRAINT skill_version_package_files_size_check CHECK (size_bytes >= 0)
);

CREATE INDEX IF NOT EXISTS idx_skill_version_package_files_object_ref
  ON skill_version_package_files(object_ref_id);

CREATE INDEX IF NOT EXISTS idx_skill_versions_package_object_ref
  ON skill_versions(package_object_ref_id) WHERE package_object_ref_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_skill_versions_skill_md_object_ref
  ON skill_versions(skill_md_object_ref_id) WHERE skill_md_object_ref_id IS NOT NULL;
