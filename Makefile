.PHONY: run server-start server-stop server-restart server-status worker-start worker-stop worker-restart worker-status test benchmark-agent-core benchmark-agent-core-compare benchmark-agent-core-postgres benchmark-agent-core-e2e profile-agent-core-e2e eval-agent-quality eval-filesystem-tools test-sdk-e2e test-typescript-sdk test-typescript-sdk-e2e test-postgres keycloak-security-apply verify-keycloak-security keycloak-cli-client-apply verify-keycloak-cli-client verify-oidc-keycloak verify-agent-runtime verify-agent-runtime-full verify-agent-core-staging verify-agent-core-staging-restart verify-agent-core-staging-crash verify-agent-core-staging-infrastructure verify-llm-provider verify-mcp-stdio verify-mcp-http verify-mcp-registry verify-mcp-runtime-guard verify-mcp-compatibility verify-mcp-all verify-web-search-crawl verify-browser-tools verify-searxng-cn verify-objectstore-s3 verify-inspector-ui verify-inspector-browser-smoke verify-worker-work-reap-expired verify-worker-work-heartbeat verify-worker-shutdown-drain verify-worker-work-cancel verify-worker-plugin-tools verify-computer-plugin-tools verify-onlyboxes verify-onlyboxes-session verify-network-approval verify-onlyboxes-upload-data verify-onlyboxes-export-artifact verify-worker-backed-local-system verify-worker-backed-local-export verify-worker-backed-large-local-export generate-openapi-v2 generate-go-sdk generate-typescript-sdk generate-sql-baseline verify-sql-baseline build build-web-ui build-workbench-ui build-inspector-ui build-space-ui build-cli build-worker build-browser-gateway fmt db-up db-down db-logs migrate-up

GOCACHE_DIR ?= $(CURDIR)/.gocache
TMA_DATABASE_URL ?= postgres://tma:tma@localhost:5432/tma?sslmode=disable
TMA_ONLYBOXES_TEST_IMAGE ?= coolfan1024/onlyboxes-runtime:default
TMA_BROWSER_GATEWAY_IMAGE ?= tma-browser-gateway:local
TMA_BENCHTIME ?= 1s
TMA_BENCH_COUNT ?= 5
TMA_POSTGRES_BENCHTIME ?= 50x
TMA_POSTGRES_BENCH_COUNT ?= 1
TMA_POSTGRES_E2E_BENCHTIME ?= 20x
TMA_POSTGRES_PROFILE_BENCHTIME ?= 50x

run:
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" GOCACHE="$(GOCACHE_DIR)" go run ./cmd/tma-server

server-start: build build-cli
	scripts/tma_server.sh start

server-stop:
	scripts/tma_server.sh stop

server-restart: build build-cli
	scripts/tma_server.sh restart

server-status:
	scripts/tma_server.sh status

worker-start: build-worker
	scripts/tma_worker.sh start

worker-stop:
	scripts/tma_worker.sh stop

worker-restart: build-worker
	scripts/tma_worker.sh restart

worker-status:
	scripts/tma_worker.sh status

test:
	GOCACHE="$(GOCACHE_DIR)" go test ./...

benchmark-agent-core:
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/agentcore -run '^$$' -bench '^BenchmarkAgentLoop$$' -benchmem -benchtime="$(TMA_BENCHTIME)" -count="$(TMA_BENCH_COUNT)"

benchmark-agent-core-compare:
	TMA_BENCHTIME="$(TMA_BENCHTIME)" TMA_BENCH_COUNT="$(TMA_BENCH_COUNT)" GOCACHE="$(GOCACHE_DIR)" scripts/benchmark_agent_core_compare.sh

benchmark-agent-core-postgres:
	TMA_POSTGRES_BENCHTIME="$(TMA_POSTGRES_BENCHTIME)" TMA_POSTGRES_BENCH_COUNT="$(TMA_POSTGRES_BENCH_COUNT)" GOCACHE="$(GOCACHE_DIR)" scripts/benchmark_agent_core_postgres.sh

benchmark-agent-core-e2e:
	TMA_POSTGRES_BENCHMARK='^BenchmarkPostgresAgentLoopEndToEnd$$' TMA_POSTGRES_BENCHTIME="$(TMA_POSTGRES_E2E_BENCHTIME)" TMA_POSTGRES_BENCH_COUNT="$(TMA_POSTGRES_BENCH_COUNT)" GOCACHE="$(GOCACHE_DIR)" scripts/benchmark_agent_core_postgres.sh

profile-agent-core-e2e:
	mkdir -p .codex_artifacts/profiles
	TMA_POSTGRES_BENCHMARK='^BenchmarkPostgresAgentLoopEndToEnd/safe_reads_10$$' TMA_POSTGRES_BENCHTIME="$(TMA_POSTGRES_PROFILE_BENCHTIME)" TMA_POSTGRES_BENCH_COUNT=1 TMA_POSTGRES_CPU_PROFILE="$(CURDIR)/.codex_artifacts/profiles/agent-core-e2e-cpu.pprof" TMA_POSTGRES_MEM_PROFILE="$(CURDIR)/.codex_artifacts/profiles/agent-core-e2e-mem.pprof" TMA_POSTGRES_PROFILE_OUTPUT_DIR="$(CURDIR)/.codex_artifacts/profiles" GOCACHE="$(GOCACHE_DIR)" scripts/benchmark_agent_core_postgres.sh
	go tool pprof -top -nodecount=30 .codex_artifacts/profiles/agent-core-e2e-cpu.pprof
	go tool pprof -top -alloc_space -nodecount=30 .codex_artifacts/profiles/agent-core-e2e-mem.pprof

eval-agent-quality:
	GOCACHE="$(GOCACHE_DIR)" go run ./cmd/tma-agent-quality-eval -fixtures testdata/agent-quality/completion-gate.json
	GOCACHE="$(GOCACHE_DIR)" go run ./cmd/tma-agent-quality-eval -fixtures testdata/agent-quality/filesystem-tools.json

eval-filesystem-tools:
	GOCACHE="$(GOCACHE_DIR)" go run ./cmd/tma-agent-quality-eval -fixtures testdata/agent-quality/filesystem-tools.json

test-sdk-e2e:
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/httpapi -run '^Test(GoCoreSDKRealServerE2E|TypedAdministrationSDKRealServerE2E|TypedSkillsSDKRealServerE2E|TypedMarketplaceSDKRealServerE2E)$$' -count=1

test-typescript-sdk:
	npm --prefix sdk/typescript run verify

test-typescript-sdk-e2e: test-typescript-sdk
	TMA_RUN_TYPESCRIPT_SDK_E2E=1 GOCACHE="$(GOCACHE_DIR)" go test ./internal/httpapi -run '^TestTypeScriptCoreSDKRealServerE2E$$' -count=1

test-postgres:
	GOCACHE="$(GOCACHE_DIR)" scripts/test_postgres_isolated.sh

generate-openapi-v2:
	GOCACHE="$(GOCACHE_DIR)" go run scripts/generate_openapi_v2.go

generate-go-sdk: generate-openapi-v2
	@tmp="$$(mktemp sdk/tma/internal/generated/client.gen.go.tmp.XXXXXX)"; \
	if GOCACHE="$(GOCACHE_DIR)" go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1 -generate types,client -package generated api/v2/openapi.yaml > "$$tmp"; then \
		mv "$$tmp" sdk/tma/internal/generated/client.gen.go; \
	else \
		rm -f "$$tmp"; \
		exit 1; \
	fi

generate-typescript-sdk: generate-openapi-v2
	npm --prefix sdk/typescript run generate

keycloak-security-apply:
	scripts/keycloak_realm_security.sh apply

verify-keycloak-security:
	scripts/keycloak_realm_security.sh verify

keycloak-cli-client-apply:
	scripts/keycloak_cli_client.sh apply

verify-keycloak-cli-client:
	scripts/keycloak_cli_client.sh verify

verify-oidc-keycloak: build db-up
	scripts/verify_oidc_keycloak.sh

verify-agent-runtime: build-cli
	scripts/verify_agent_runtime.sh

verify-agent-runtime-full: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_agent_runtime_full.sh

verify-agent-core-staging: build-cli
	scripts/verify_agent_core_staging.sh

verify-agent-core-staging-restart: build-cli
	TMA_AGENT_CORE_STAGING_RESTART_DRILL=true scripts/verify_agent_core_staging.sh

verify-agent-core-staging-crash: build-cli
	TMA_AGENT_CORE_STAGING_CRASH_DRILL=true scripts/verify_agent_core_staging.sh

verify-agent-core-staging-infrastructure: build-cli
	TMA_AGENT_CORE_STAGING_INFRASTRUCTURE_DRILL=true scripts/verify_agent_core_staging.sh

verify-llm-provider: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_llm_provider_full.sh

verify-mcp-stdio: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_mcp_stdio.sh

verify-mcp-http: build build-cli db-up migrate-up
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/mcp -run 'TestStreamableHTTP(ClientReadsSSEResponse|ListenerRepliesUnsupportedServerRequestAndReconnects|HostKeepsSessionAfterRequestCancellation|HostReinitializesAfterRemoteSessionExpires|HostReusesSessionAndDeletesOnClose)$$' -count=1
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_mcp_http.sh

verify-mcp-registry: build build-cli db-up
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/mcpregistry -count=1
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/httpapi -run MCPRegistry -count=1
	scripts/verify_mcp_registry.sh

verify-mcp-runtime-guard: build build-cli db-up
	GOCACHE="$(GOCACHE_DIR)" go test -race ./internal/mcp -run 'TestRuntimeGuard' -count=1
	scripts/verify_mcp_runtime_guard.sh

verify-mcp-compatibility:
	TMA_RUN_MCP_COMPATIBILITY=1 GOCACHE="$(GOCACHE_DIR)" go test ./internal/mcp -run TestExternalMCPCompatibility -v -count=1

verify-mcp-all: build build-cli db-up
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/mcp -run 'TestStreamableHTTP(ClientReadsSSEResponse|ListenerRepliesUnsupportedServerRequestAndReconnects|HostKeepsSessionAfterRequestCancellation|HostReinitializesAfterRemoteSessionExpires|HostReusesSessionAndDeletesOnClose)$$' -count=1
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/mcpregistry -count=1
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/httpapi -run MCPRegistry -count=1
	GOCACHE="$(GOCACHE_DIR)" go test -race ./internal/mcp ./internal/mcpregistry ./internal/execution ./internal/observability -count=1
	scripts/verify_mcp_all.sh
	npm --prefix apps/workbench test
	npm --prefix apps/workbench run build
	npm --prefix apps/inspector test -- --run
	npm --prefix apps/inspector run build
	git diff --check

verify-web-search-crawl: build build-cli db-up migrate-up
	TMA_DATABASE_URL="$(TMA_DATABASE_URL)" scripts/verify_web_search_crawl.sh

verify-browser-tools:
	GOCACHE="$(GOCACHE_DIR)" go test ./internal/tools -run 'Test(DefaultRegistryExcludesBuiltInBrowser|BrowserNamespaceIsAvailableToProcessPlugins)' -count=1
	python3 -m unittest extensions/browser-tool-plugin/test_browser_plugin.py
	npm --prefix extensions/browser-gateway test
	npm --prefix apps/workbench test

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
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma-server ./cmd/tma-server

build-web-ui: build-inspector-ui build-workbench-ui build-space-ui

build-workbench-ui: test-typescript-sdk
	npm --prefix apps/workbench run build

build-inspector-ui: test-typescript-sdk
	npm --prefix apps/inspector run build

build-space-ui: test-typescript-sdk
	npm --prefix apps/space test
	npm --prefix apps/space run build

build-cli:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma ./cmd/tma

build-worker:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma-worker ./cmd/tma-worker

build-browser-gateway:
	docker build -t "$(TMA_BROWSER_GATEWAY_IMAGE)" extensions/browser-gateway

fmt:
	GOCACHE="$(GOCACHE_DIR)" go fmt ./...

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

db-logs:
	docker compose logs -f postgres

migrate-up:
	docker compose exec -T postgres sh -c 'set -eu; for file in /migrations/*.sql; do psql -v ON_ERROR_STOP=1 --single-transaction -U tma -d tma -f "$$file"; done'

generate-sql-baseline:
	sh scripts/generate_sql_baseline.sh 000092

verify-sql-baseline: generate-sql-baseline
	sh scripts/verify_sql_baseline.sh sql/baselines/000092_baseline.sql
