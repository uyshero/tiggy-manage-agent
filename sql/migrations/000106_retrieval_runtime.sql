ALTER TABLE knowledge_bases RENAME TO retrieval_collections;
ALTER TABLE knowledge_documents RENAME TO retrieval_documents;
ALTER TABLE knowledge_chunks RENAME TO retrieval_chunks;

ALTER TABLE retrieval_documents RENAME COLUMN knowledge_base_id TO collection_id;
ALTER TABLE retrieval_chunks RENAME COLUMN knowledge_base_id TO collection_id;

ALTER SEQUENCE tma_knowledge_base_id_seq RENAME TO tma_retrieval_collection_id_seq;
ALTER SEQUENCE tma_knowledge_document_id_seq RENAME TO tma_retrieval_document_id_seq;

ALTER TABLE knowledge_services RENAME COLUMN knowledge_base_ids TO retrieval_collection_ids;
ALTER TABLE knowledge_services RENAME COLUMN knowledge_document_ids TO retrieval_document_ids;

ALTER INDEX knowledge_bases_workspace_updated_idx RENAME TO retrieval_collections_workspace_updated_idx;
ALTER INDEX knowledge_documents_base_updated_idx RENAME TO retrieval_documents_collection_updated_idx;
ALTER INDEX knowledge_documents_object_ref_idx RENAME TO retrieval_documents_object_ref_idx;
ALTER INDEX knowledge_chunks_base_idx RENAME TO retrieval_chunks_collection_idx;
ALTER INDEX knowledge_chunks_search_idx RENAME TO retrieval_chunks_search_idx;

ALTER POLICY knowledge_bases_isolation ON retrieval_collections RENAME TO retrieval_collections_isolation;
ALTER POLICY knowledge_documents_isolation ON retrieval_documents RENAME TO retrieval_documents_isolation;
ALTER POLICY knowledge_chunks_isolation ON retrieval_chunks RENAME TO retrieval_chunks_isolation;

ALTER TABLE object_ref_links DROP CONSTRAINT IF EXISTS object_ref_links_owner_type_check;

UPDATE object_ref_links
SET owner_type = 'retrieval_document'
WHERE owner_type = 'knowledge_document';

UPDATE object_ref_links
SET role = 'retrieval_source'
WHERE owner_type = 'retrieval_document' AND role = 'knowledge_source';

ALTER TABLE object_ref_links ADD CONSTRAINT object_ref_links_owner_type_check CHECK (
  owner_type IN (
    'session_artifact', 'skill_asset', 'skill_version', 'skill_package_file',
    'workspace_snapshot', 'achievement_library_item', 'retrieval_document'
  )
);

CREATE TABLE retrieval_ingestion_jobs (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  collection_id TEXT NOT NULL REFERENCES retrieval_collections(id) ON DELETE CASCADE,
  document_id TEXT REFERENCES retrieval_documents(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  error_message TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT 'system',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  CONSTRAINT retrieval_ingestion_jobs_status_check
    CHECK (status IN ('queued', 'processing', 'ready', 'failed'))
);

CREATE TABLE retrieval_indexes (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  collection_id TEXT NOT NULL REFERENCES retrieval_collections(id) ON DELETE CASCADE,
  index_type TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT 'postgres',
  model TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'ready',
  revision BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT retrieval_indexes_type_check CHECK (index_type IN ('keyword', 'vector')),
  CONSTRAINT retrieval_indexes_status_check CHECK (status IN ('building', 'ready', 'failed')),
  CONSTRAINT retrieval_indexes_revision_check CHECK (revision > 0),
  CONSTRAINT retrieval_indexes_collection_type_unique UNIQUE (workspace_id, collection_id, index_type)
);

CREATE SEQUENCE tma_retrieval_ingestion_job_id_seq;
CREATE SEQUENCE tma_retrieval_index_id_seq;

CREATE INDEX retrieval_ingestion_jobs_collection_created_idx
  ON retrieval_ingestion_jobs (workspace_id, collection_id, created_at DESC, id DESC);
CREATE INDEX retrieval_indexes_collection_idx
  ON retrieval_indexes (workspace_id, collection_id, index_type);

ALTER TABLE retrieval_ingestion_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE retrieval_ingestion_jobs FORCE ROW LEVEL SECURITY;
ALTER TABLE retrieval_indexes ENABLE ROW LEVEL SECURITY;
ALTER TABLE retrieval_indexes FORCE ROW LEVEL SECURITY;

CREATE POLICY retrieval_ingestion_jobs_isolation ON retrieval_ingestion_jobs FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
CREATE POLICY retrieval_indexes_isolation ON retrieval_indexes FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
