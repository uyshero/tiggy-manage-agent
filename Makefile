.PHONY: run test test-postgres verify-agent-runtime verify-agent-runtime-full verify-llm-provider verify-web-search-crawl verify-searxng-cn verify-objectstore-s3 verify-inspector-ui verify-worker-work-reap-expired verify-onlyboxes verify-onlyboxes-session verify-network-approval verify-onlyboxes-upload-data verify-onlyboxes-export-artifact verify-worker-backed-local-system verify-worker-backed-local-export verify-worker-backed-large-local-export build build-cli build-worker fmt db-up db-down db-logs migrate-up

GOCACHE_DIR ?= $(CURDIR)/.gocache
TMA_DATABASE_URL ?= postgres://tma:tma@localhost:5432/tma?sslmode=disable
TMA_ONLYBOXES_TEST_IMAGE ?= coolfan1024/onlyboxes-runtime:default

run:
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" GOCACHE="$(GOCACHE_DIR)" go run ./cmd/server

test:
	GOCACHE="$(GOCACHE_DIR)" go test ./...

test-postgres:
	TMA_RUN_POSTGRES_TESTS=1 TMA_DATABASE_URL="$(TMA_DATABASE_URL)" GOCACHE="$(GOCACHE_DIR)" go test ./internal/managedagents -run Postgres -count=1

verify-agent-runtime: build-cli
	scripts/verify_agent_runtime.sh

verify-agent-runtime-full: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_agent_runtime_full.sh

verify-llm-provider: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_llm_provider_full.sh

verify-web-search-crawl: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_web_search_crawl.sh

verify-searxng-cn:
	scripts/verify_searxng_cn.sh

verify-objectstore-s3: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_objectstore_s3.sh

verify-inspector-ui: build db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_inspector_ui.sh

verify-worker-work-reap-expired: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_worker_work_reap_expired.sh

verify-onlyboxes:
	TMA_RUN_ONLYBOXES_TESTS=1 TMA_ONLYBOXES_TEST_IMAGE="$(TMA_ONLYBOXES_TEST_IMAGE)" GOCACHE="$(GOCACHE_DIR)" go test ./internal/capability -run TestOnlyboxesProviderRealDocker -count=1 -v

verify-onlyboxes-session: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" TMA_CLOUD_SANDBOX_IMAGE="$(TMA_ONLYBOXES_TEST_IMAGE)" scripts/verify_cloud_sandbox_session.sh

verify-network-approval: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" TMA_CLOUD_SANDBOX_IMAGE="$(TMA_ONLYBOXES_TEST_IMAGE)" scripts/verify_network_approval_flow.sh

verify-onlyboxes-upload-data: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" TMA_CLOUD_SANDBOX_IMAGE="$(TMA_ONLYBOXES_TEST_IMAGE)" scripts/verify_cloud_sandbox_upload_data.sh

verify-onlyboxes-export-artifact: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" TMA_CLOUD_SANDBOX_IMAGE="$(TMA_ONLYBOXES_TEST_IMAGE)" scripts/verify_cloud_sandbox_export_artifact.sh

verify-worker-backed-local-system: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_worker_backed_local_system.sh

verify-worker-backed-local-export: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_worker_backed_local_export.sh

verify-worker-backed-large-local-export: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" TMA_VERIFY_BASE_URL="http://localhost:18088" TMA_VERIFY_HTTP_ADDR=":18088" TMA_VERIFY_SERVER_LOG=".verify-worker-large-export-server.log" TMA_VERIFY_WORKER_LOG=".verify-worker-large-export-worker.log" TMA_VERIFY_WORKER_EXPORT_TEXT="tma.verify_worker_large_export" TMA_VERIFY_WORKER_EXPORT_MARKER="tma-worker-large-export-ok" scripts/verify_worker_backed_local_export.sh

build:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma-server ./cmd/server

build-cli:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma ./cmd/tma

build-worker:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma-worker ./cmd/worker

fmt:
	GOCACHE="$(GOCACHE_DIR)" go fmt ./...

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

db-logs:
	docker compose logs -f postgres

migrate-up:
	docker compose exec -T postgres sh -c 'for file in /migrations/*.sql; do psql -U tma -d tma -f "$$file"; done'
