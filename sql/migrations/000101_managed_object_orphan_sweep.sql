CREATE INDEX IF NOT EXISTS idx_object_refs_managed_orphan_sweep
  ON object_refs(workspace_id, created_at, id)
  WHERE metadata_json #>> '{object_lifecycle,class}' = 'managed';
