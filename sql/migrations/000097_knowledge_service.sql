CREATE TABLE IF NOT EXISTS knowledge_bases (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT knowledge_bases_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 200)
);

CREATE TABLE IF NOT EXISTS knowledge_documents (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  knowledge_base_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
  object_ref_id TEXT NOT NULL REFERENCES object_refs(id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'ready',
  error_message TEXT NOT NULL DEFAULT '',
  chunk_count INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT knowledge_documents_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 512),
  CONSTRAINT knowledge_documents_status_check CHECK (status IN ('processing', 'ready', 'failed')),
  CONSTRAINT knowledge_documents_size_check CHECK (size_bytes >= 0),
  CONSTRAINT knowledge_documents_chunk_count_check CHECK (chunk_count >= 0)
);

CREATE TABLE IF NOT EXISTS knowledge_chunks (
  document_id TEXT NOT NULL REFERENCES knowledge_documents(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  knowledge_base_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
  chunk_index INTEGER NOT NULL,
  content TEXT NOT NULL,
  embedding DOUBLE PRECISION[] NOT NULL DEFAULT '{}'::DOUBLE PRECISION[],
  embedding_model TEXT NOT NULL DEFAULT 'local-hash-v1',
  search_vector TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (document_id, chunk_index),
  CONSTRAINT knowledge_chunks_content_check CHECK (length(btrim(content)) > 0),
  CONSTRAINT knowledge_chunks_index_check CHECK (chunk_index >= 0)
);

CREATE TABLE IF NOT EXISTS knowledge_services (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  scenario TEXT NOT NULL,
  system_prompt TEXT NOT NULL DEFAULT '',
  knowledge_base_ids JSONB NOT NULL DEFAULT '[]'::JSONB,
  allow_web_search BOOLEAN NOT NULL DEFAULT false,
  sensitive_terms JSONB NOT NULL DEFAULT '[]'::JSONB,
  status TEXT NOT NULL DEFAULT 'active',
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT knowledge_services_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 200),
  CONSTRAINT knowledge_services_scenario_check CHECK (length(btrim(scenario)) BETWEEN 1 AND 4000),
  CONSTRAINT knowledge_services_kb_ids_check CHECK (jsonb_typeof(knowledge_base_ids) = 'array'),
  CONSTRAINT knowledge_services_sensitive_terms_check CHECK (jsonb_typeof(sensitive_terms) = 'array'),
  CONSTRAINT knowledge_services_status_check CHECK (status IN ('active', 'disabled'))
);

CREATE TABLE IF NOT EXISTS knowledge_service_shares (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  service_id TEXT NOT NULL REFERENCES knowledge_services(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ,
  CONSTRAINT knowledge_service_shares_hash_check CHECK (token_hash ~ '^[0-9a-f]{64}$')
);

CREATE TABLE IF NOT EXISTS knowledge_service_questions (
  id BIGSERIAL PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  service_id TEXT NOT NULL REFERENCES knowledge_services(id) ON DELETE CASCADE,
  share_id TEXT REFERENCES knowledge_service_shares(id) ON DELETE SET NULL,
  question TEXT NOT NULL,
  answer TEXT NOT NULL,
  refused BOOLEAN NOT NULL DEFAULT false,
  refusal_reason TEXT NOT NULL DEFAULT '',
  source_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE SEQUENCE IF NOT EXISTS tma_knowledge_base_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_knowledge_document_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_knowledge_service_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_knowledge_share_id_seq;

CREATE INDEX IF NOT EXISTS knowledge_bases_workspace_updated_idx
  ON knowledge_bases (workspace_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS knowledge_documents_base_updated_idx
  ON knowledge_documents (workspace_id, knowledge_base_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS knowledge_documents_object_ref_idx
  ON knowledge_documents (object_ref_id);
CREATE INDEX IF NOT EXISTS knowledge_chunks_base_idx
  ON knowledge_chunks (workspace_id, knowledge_base_id, document_id, chunk_index);
CREATE INDEX IF NOT EXISTS knowledge_chunks_search_idx
  ON knowledge_chunks USING GIN (search_vector);
CREATE INDEX IF NOT EXISTS knowledge_services_workspace_updated_idx
  ON knowledge_services (workspace_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS knowledge_service_shares_service_idx
  ON knowledge_service_shares (workspace_id, service_id, created_at DESC);
CREATE INDEX IF NOT EXISTS knowledge_service_questions_service_idx
  ON knowledge_service_questions (workspace_id, service_id, created_at DESC);

ALTER TABLE object_ref_links DROP CONSTRAINT IF EXISTS object_ref_links_owner_type_check;
ALTER TABLE object_ref_links ADD CONSTRAINT object_ref_links_owner_type_check CHECK (
  owner_type IN (
    'session_artifact', 'skill_asset', 'skill_version', 'skill_package_file',
    'workspace_snapshot', 'achievement_library_item', 'knowledge_document'
  )
);

ALTER TABLE knowledge_bases ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_bases FORCE ROW LEVEL SECURITY;
ALTER TABLE knowledge_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_documents FORCE ROW LEVEL SECURITY;
ALTER TABLE knowledge_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_chunks FORCE ROW LEVEL SECURITY;
ALTER TABLE knowledge_services ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_services FORCE ROW LEVEL SECURITY;
ALTER TABLE knowledge_service_shares ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_service_shares FORCE ROW LEVEL SECURITY;
ALTER TABLE knowledge_service_questions ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_service_questions FORCE ROW LEVEL SECURITY;

CREATE POLICY knowledge_bases_isolation ON knowledge_bases FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
CREATE POLICY knowledge_documents_isolation ON knowledge_documents FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
CREATE POLICY knowledge_chunks_isolation ON knowledge_chunks FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
CREATE POLICY knowledge_services_isolation ON knowledge_services FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
CREATE POLICY knowledge_service_shares_isolation ON knowledge_service_shares FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
CREATE POLICY knowledge_service_questions_isolation ON knowledge_service_questions FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

-- Anonymous callers never query tenant tables directly. This narrowly-scoped
-- function resolves one high-entropy token hash and returns only routing IDs.
CREATE OR REPLACE FUNCTION resolve_knowledge_service_share(p_token_hash TEXT)
RETURNS TABLE (share_id TEXT, workspace_id TEXT, service_id TEXT, expires_at TIMESTAMPTZ)
LANGUAGE SQL
SECURITY DEFINER
STABLE
SET search_path = public, pg_temp
SET row_security = off
AS $$
  SELECT s.id, s.workspace_id, s.service_id, s.expires_at
  FROM knowledge_service_shares s
  JOIN knowledge_services svc ON svc.id = s.service_id AND svc.workspace_id = s.workspace_id
  WHERE s.token_hash = p_token_hash
    AND s.revoked_at IS NULL
    AND (s.expires_at IS NULL OR s.expires_at > now())
    AND svc.status = 'active'
  LIMIT 1
$$;
