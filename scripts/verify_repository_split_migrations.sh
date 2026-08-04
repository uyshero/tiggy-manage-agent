#!/bin/sh
set -eu

REPOSITORY_ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
POSTGRES_USER="${TMA_POSTGRES_TEST_USER:-tma}"
RUN_ID="$(date +%Y%m%d%H%M%S)_$$"
UPGRADE_DATABASE="tma_repository_split_$RUN_ID"

cleanup() {
	docker compose exec -T postgres dropdb --if-exists -U "$POSTGRES_USER" "$UPGRADE_DATABASE" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

cd "$REPOSITORY_ROOT"
docker compose up -d postgres >/dev/null
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$UPGRADE_DATABASE"
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$UPGRADE_DATABASE" \
	<sql/baselines/000102_baseline.sql >/dev/null

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$UPGRADE_DATABASE" >/dev/null <<'SQL'
INSERT INTO organizations (id, name) VALUES ('org-upgrade', 'Upgrade');
INSERT INTO workspaces (id, org_id, name) VALUES ('ws-upgrade', 'org-upgrade', 'Upgrade');
INSERT INTO llm_providers (id, provider_type) VALUES ('provider-upgrade', 'fake');
INSERT INTO llm_models (provider_id, model, capability_type, capabilities_json)
VALUES ('provider-upgrade', 'legacy-text', 'text', '{"protocol":"legacy"}'::jsonb);
INSERT INTO object_refs (id, workspace_id, bucket, object_key)
VALUES ('obj-upgrade', 'ws-upgrade', 'upgrade', 'document.txt');
INSERT INTO knowledge_bases (id, workspace_id, name, created_by)
VALUES ('kb-upgrade', 'ws-upgrade', 'Upgrade collection', 'tester');
INSERT INTO knowledge_documents (
  id, workspace_id, knowledge_base_id, object_ref_id, name, created_by
) VALUES (
  'doc-upgrade', 'ws-upgrade', 'kb-upgrade', 'obj-upgrade', 'document.txt', 'tester'
);
INSERT INTO knowledge_chunks (
  document_id, workspace_id, knowledge_base_id, chunk_index, content
) VALUES (
  'doc-upgrade', 'ws-upgrade', 'kb-upgrade', 0, 'survival analysis'
);
INSERT INTO knowledge_services (
  id, workspace_id, name, scenario, knowledge_base_ids, knowledge_document_ids, created_by
) VALUES (
  'svc-upgrade', 'ws-upgrade', 'Upgrade service', 'verify migration',
  '["kb-upgrade"]'::jsonb, '["doc-upgrade"]'::jsonb, 'tester'
);
INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role)
VALUES ('obj-upgrade', 'ws-upgrade', 'knowledge_document', 'doc-upgrade', 'knowledge_source');
SQL

for migration in \
	sql/migrations/000105_tenant_administration.sql \
	sql/migrations/000106_retrieval_runtime.sql \
	sql/migrations/000107_speech_model_capabilities.sql \
	sql/migrations/000108_model_invocations.sql \
	sql/migrations/000109_service_identities.sql \
	sql/migrations/000110_model_invocation_quota.sql \
	sql/migrations/000111_application_resource_ownership.sql \
	sql/migrations/000112_event_subscriptions.sql \
	sql/migrations/000113_first_class_runs.sql \
	sql/migrations/000114_signed_artifact_exchange.sql; do
	docker compose exec -T postgres psql -v ON_ERROR_STOP=1 --single-transaction \
		-U "$POSTGRES_USER" -d "$UPGRADE_DATABASE" <"$migration" >/dev/null
done

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$UPGRADE_DATABASE" <<'SQL'
INSERT INTO llm_models (provider_id, model, capability_type, capabilities_json)
VALUES (
  'provider-upgrade', 'speech-asr', 'speech_to_text',
  '{"protocol":"platform_realtime_asr","resource_id":"speech-resource","audio_format":"pcm_s16le","sample_rate_hz":16000}'::jsonb
);

DO $$
DECLARE
  service_collections JSONB;
  service_documents JSONB;
  old_capabilities JSONB;
  link_owner TEXT;
  link_role TEXT;
  rls_enabled BOOLEAN;
  rls_forced BOOLEAN;
BEGIN
  IF to_regclass('public.knowledge_bases') IS NOT NULL
     OR to_regclass('public.knowledge_documents') IS NOT NULL
     OR to_regclass('public.knowledge_chunks') IS NOT NULL THEN
    RAISE EXCEPTION 'legacy retrieval tables still exist';
  END IF;
  IF to_regclass('public.retrieval_collections') IS NULL
     OR to_regclass('public.retrieval_documents') IS NULL
     OR to_regclass('public.retrieval_chunks') IS NULL
     OR to_regclass('public.retrieval_ingestion_jobs') IS NULL
     OR to_regclass('public.retrieval_indexes') IS NULL THEN
    RAISE EXCEPTION 'retrieval tables are incomplete';
  END IF;
	IF to_regclass('public.model_invocations') IS NULL THEN
	  RAISE EXCEPTION 'model invocation audit table is missing';
	END IF;
	IF to_regclass('public.service_identities') IS NULL
	   OR to_regclass('public.service_identity_credentials') IS NULL THEN
	  RAISE EXCEPTION 'service identity tables are incomplete';
	END IF;
	IF to_regclass('public.event_subscriptions') IS NULL
	   OR to_regclass('public.event_deliveries') IS NULL THEN
	  RAISE EXCEPTION 'event subscription tables are incomplete';
	END IF;
	IF to_regclass('public.artifact_exchanges') IS NULL THEN
	  RAISE EXCEPTION 'artifact exchange table is missing';
	END IF;

  PERFORM 1 FROM retrieval_collections WHERE id = 'kb-upgrade' AND workspace_id = 'ws-upgrade';
  IF NOT FOUND THEN RAISE EXCEPTION 'collection data was not preserved'; END IF;
  PERFORM 1 FROM retrieval_documents WHERE id = 'doc-upgrade' AND collection_id = 'kb-upgrade';
  IF NOT FOUND THEN RAISE EXCEPTION 'document data was not preserved'; END IF;
  PERFORM 1 FROM retrieval_chunks WHERE document_id = 'doc-upgrade' AND collection_id = 'kb-upgrade';
  IF NOT FOUND THEN RAISE EXCEPTION 'chunk data was not preserved'; END IF;

  SELECT retrieval_collection_ids, retrieval_document_ids
  INTO service_collections, service_documents
  FROM knowledge_services WHERE id = 'svc-upgrade';
  IF service_collections <> '["kb-upgrade"]'::jsonb
     OR service_documents <> '["doc-upgrade"]'::jsonb THEN
    RAISE EXCEPTION 'knowledge service resource IDs were not preserved';
  END IF;

  SELECT owner_type, role INTO link_owner, link_role
  FROM object_ref_links WHERE object_ref_id = 'obj-upgrade';
  IF link_owner <> 'retrieval_document' OR link_role <> 'retrieval_source' THEN
    RAISE EXCEPTION 'object reference semantics were not migrated';
  END IF;

  SELECT relrowsecurity, relforcerowsecurity INTO rls_enabled, rls_forced
  FROM pg_class WHERE oid = 'retrieval_ingestion_jobs'::regclass;
  IF NOT rls_enabled OR NOT rls_forced THEN
    RAISE EXCEPTION 'retrieval ingestion RLS is not forced';
  END IF;

  SELECT capabilities_json INTO old_capabilities
  FROM llm_models WHERE provider_id = 'provider-upgrade' AND model = 'legacy-text';
  IF old_capabilities <> '{"protocol":"legacy"}'::jsonb THEN
    RAISE EXCEPTION 'existing model capabilities were not preserved';
  END IF;

  PERFORM 1 FROM llm_models
  WHERE provider_id = 'provider-upgrade' AND model = 'speech-asr'
    AND capability_type = 'speech_to_text'
    AND capabilities_json->>'resource_id' = 'speech-resource';
  IF NOT FOUND THEN RAISE EXCEPTION 'speech capability metadata is not writable'; END IF;
END
$$;
SQL

printf '%s\n' 'verified repository split migrations with populated legacy data'
