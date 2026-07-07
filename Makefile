.PHONY: run test test-postgres verify-agent-runtime verify-agent-runtime-full verify-llm-provider build build-cli fmt db-up db-down db-logs migrate-up

GOCACHE_DIR ?= $(CURDIR)/.gocache
TMA_DATABASE_URL ?= postgres://tma:tma@localhost:5432/tma?sslmode=disable

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

build:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma-server ./cmd/server

build-cli:
	GOCACHE="$(GOCACHE_DIR)" go build -o bin/tma ./cmd/tma

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
