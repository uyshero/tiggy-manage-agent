ALTER TABLE knowledge_services
  ADD COLUMN IF NOT EXISTS knowledge_document_ids JSONB NOT NULL DEFAULT '[]'::JSONB;

ALTER TABLE knowledge_services
  DROP CONSTRAINT IF EXISTS knowledge_services_document_ids_check;
ALTER TABLE knowledge_services
  ADD CONSTRAINT knowledge_services_document_ids_check CHECK (jsonb_typeof(knowledge_document_ids) = 'array');
