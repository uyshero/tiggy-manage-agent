.PHONY: run server-start server-stop server-restart server-status test test-postgres verify-agent-runtime verify-agent-runtime-full verify-llm-provider verify-web-search-crawl verify-browser-tools verify-browser-takeover-local verify-searxng-cn verify-objectstore-s3 verify-inspector-ui verify-inspector-browser-smoke verify-worker-work-reap-expired verify-worker-work-heartbeat verify-worker-shutdown-drain verify-worker-work-cancel verify-worker-plugin-tools verify-computer-plugin-tools verify-onlyboxes verify-onlyboxes-session verify-network-approval verify-onlyboxes-upload-data verify-onlyboxes-export-artifact verify-worker-backed-local-system verify-worker-backed-local-export verify-worker-backed-large-local-export build build-web-ui build-app-ui build-inspector-ui build-cli build-worker build-browser-sandbox fmt db-up db-down db-logs migrate-up

GOCACHE_DIR ?= $(CURDIR)/.gocache
TMA_DATABASE_URL ?= postgres://tma:tma@localhost:5432/tma?sslmode=disable
TMA_ONLYBOXES_TEST_IMAGE ?= coolfan1024/onlyboxes-runtime:default
TMA_BROWSER_SANDBOX_IMAGE ?= tma-browser-sandbox:playwright

run:
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" GOCACHE="$(GOCACHE_DIR)" go run ./cmd/server

server-start: build build-cli
	scripts/tma_server.sh start

server-stop:
	scripts/tma_server.sh stop

server-restart: build build-cli
	scripts/tma_server.sh restart

server-status:
	scripts/tma_server.sh status

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

verify-browser-tools: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" TMA_BROWSER_SANDBOX_IMAGE="$(TMA_BROWSER_SANDBOX_IMAGE)" scripts/verify_browser_tools.sh

verify-browser-takeover-local: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_browser_takeover_local.sh

verify-searxng-cn:
	scripts/verify_searxng_cn.sh

verify-objectstore-s3: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_objectstore_s3.sh

verify-inspector-ui: build-inspector-ui build db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_inspector_ui.sh

verify-inspector-browser-smoke: build-inspector-ui build db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_inspector_browser_smoke.sh

verify-worker-work-reap-expired: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_worker_work_reap_expired.sh

verify-worker-work-heartbeat: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_worker_work_heartbeat.sh

verify-worker-shutdown-drain: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_worker_shutdown_drain.sh

verify-worker-work-cancel: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_worker_work_cancel.sh

verify-worker-plugin-tools: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_worker_plugin_tools.sh

verify-computer-plugin-tools: build build-cli build-worker db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_computer_plugin_tools.sh

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

build-web-ui: build-inspector-ui build-app-ui

build-app-ui:
	npm --prefix web-app run build

build-inspector-ui:
	npm --prefix web-inspector run build

build-cli:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma ./cmd/tma

build-worker:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma-worker ./cmd/worker

build-browser-sandbox:
	docker build -f docker/browser-sandbox.Dockerfile -t "$(TMA_BROWSER_SANDBOX_IMAGE)" .

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
