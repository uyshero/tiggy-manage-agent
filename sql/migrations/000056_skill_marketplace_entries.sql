CREATE TABLE IF NOT EXISTS skill_marketplace_entries (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  skill_id TEXT NOT NULL REFERENCES skills(id),
  skill_version INTEGER NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT '',
  tags_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  status TEXT NOT NULL DEFAULT 'draft',
  submitted_by TEXT NOT NULL DEFAULT '',
  submitted_at TIMESTAMPTZ,
  published_by TEXT NOT NULL DEFAULT '',
  published_at TIMESTAMPTZ,
  withdrawn_by TEXT NOT NULL DEFAULT '',
  withdrawn_at TIMESTAMPTZ,
  review_note TEXT NOT NULL DEFAULT '',
  withdrawal_reason TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, skill_id, skill_version),
  FOREIGN KEY (skill_id, skill_version) REFERENCES skill_versions(skill_id, version),
  CONSTRAINT skill_marketplace_entries_version_check CHECK (skill_version > 0),
  CONSTRAINT skill_marketplace_entries_status_check CHECK (
    status IN ('draft', 'pending_review', 'published', 'withdrawn')
  ),
  CONSTRAINT skill_marketplace_entries_tags_check CHECK (jsonb_typeof(tags_json) = 'array')
);

CREATE INDEX IF NOT EXISTS idx_skill_marketplace_entries_workspace_status
  ON skill_marketplace_entries(workspace_id, status, updated_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_marketplace_entries_published_skill
  ON skill_marketplace_entries(workspace_id, skill_id)
  WHERE status = 'published';

CREATE SEQUENCE IF NOT EXISTS tma_skill_marketplace_entry_id_seq;
