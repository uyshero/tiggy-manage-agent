ALTER TABLE knowledge_service_shares
  ADD COLUMN IF NOT EXISTS token TEXT NOT NULL DEFAULT '';

ALTER TABLE knowledge_service_shares
  DROP CONSTRAINT IF EXISTS knowledge_service_shares_token_check;
ALTER TABLE knowledge_service_shares
  ADD CONSTRAINT knowledge_service_shares_token_check CHECK (token = '' OR token ~ '^[0-9a-f]{48}$');
