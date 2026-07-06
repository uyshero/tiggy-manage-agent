# Tiggy Manage Agent (TMA)

Tiggy Manage Agent is a Go-based runtime skeleton for the TMA project.

Current scope:

- HTTP server entrypoint
- `/health` endpoint
- Agent / Environment / Session / Event APIs
- Postgres-backed Store with `TMA_DATABASE_URL`
- Postgres docker-compose and initial schema migration
- internal API package
- unit test baseline

## Quick Start

```bash
make db-up
make migrate-up
make test
make test-postgres
make run
```

The server listens on `:8080` by default.
The Makefile stores Go build cache in the project-local `.gocache/` directory so it works in restricted workspaces.

```bash
curl http://localhost:8080/health
```

Expected response:

```json
{"status":"ok","service":"tiggy-manage-agent"}
```

## Layout

```text
cmd/server/        HTTP server entrypoint
internal/httpapi/  HTTP routes and handlers
internal/managedagents/  TMA resource model, Store interface, and PostgresStore
internal/runner/  Replaceable turn Runner interface, WorkerRunner, AgentRuntimeTurnExecutor, and test helpers
internal/agentruntime/  Agent runtime interface and current demo runtime
internal/capability/  Capability provider interfaces for command, code, and file operations
internal/serverconfig/  Server environment and .env configuration parser
sql/migrations/  Postgres schema migrations
```

## Persistence Setup

The server requires Postgres. Start the database and apply the schema:

```bash
make db-up
```

```bash
make migrate-up
```

`make migrate-up` uses the `psql` client inside the Postgres container, so a local `psql` installation is not required.

Then start the server:

```bash
make run
```

The server also loads `.env` from the project root on startup. Values already exported in your shell take precedence over `.env`.
Configuration reference is in [docs/configuration.md](./docs/configuration.md).

`make run` uses this local default unless you override it:

```bash
postgres://tma:tma@localhost:5432/tma?sslmode=disable
```

Direct `go run ./cmd/server` still requires `TMA_DATABASE_URL` from `.env` or your shell.

Override the connection string when needed:

```bash
TMA_DATABASE_URL="postgres://user:pass@localhost:5432/db?sslmode=disable" make run
```

The server runs turns through `WorkerRunner + AgentRuntimeTurnExecutor + agentruntime.DemoRuntime`. The current runtime does not call an LLM yet; it returns a deterministic `agent.message` so the HTTP / Store / Runner lifecycle can be verified. Runtime design notes are in [docs/agent-runtime.md](./docs/agent-runtime.md).

Command turns are still documented as a lower-level external process adapter in [docs/command-turn-protocol.md](./docs/command-turn-protocol.md), but they are not the default server path.

The next Capability Provider boundary is documented in [docs/capability-provider.md](./docs/capability-provider.md). The current code defines a low-level provider surface, not a turn-level environment executor or a full Tool module.

After the server is running, verify the full CLI / HTTP / Runner path with:

```bash
make verify-agent-runtime
```

Or run the full self-starting verification, including database startup, migrations, temporary server startup, and cleanup:

```bash
make verify-agent-runtime-full
```

SSE live delivery in `PostgresStore` is currently process-local. Historical replay and reconnect resume read from `session_events`, so `--after` works across restarts after the server is back online.

Server logs are JSON structured logs via Go `slog`. Event and turn logs include fields such as `session_id`, `turn_id`, `event_seq`, `event_type`, and `event_id`.

## P1 Skeleton APIs

```text
POST /v1/agents
POST /v1/environments
POST /v1/sessions
GET  /v1/sessions/{id}
POST /v1/sessions/{id}/archive
DELETE /v1/sessions/{id}
POST /v1/sessions/{id}/events
GET  /v1/sessions/{id}/events
GET  /v1/sessions/{id}/events/stream
```

Minimal flow:

```bash
curl -sS http://localhost:8080/v1/agents \
  -H "Content-Type: application/json" \
  -d '{"name":"Code Assistant","model":"gpt-4o","system":"You are a coding agent."}'
```

```bash
curl -sS http://localhost:8080/v1/environments \
  -H "Content-Type: application/json" \
  -d '{"name":"default-cloud","config":{"type":"cloud","networking":{"type":"limited","allowed_hosts":["api.github.com"]}}}'
```

## CLI

Build the CLI:

```bash
make build-cli
```

Example flow:

```bash
bin/tma health
```

```bash
bin/tma agent create \
  --name "Code Assistant" \
  --model gpt-4o \
  --system "You are a coding agent."
```

```bash
bin/tma env create --name default-cloud
```

```bash
bin/tma session create \
  --agent agt_000001 \
  --env env_000001 \
  --title "First TMA task"
```

```bash
bin/tma session archive --session sesn_000001
```

```bash
bin/tma session delete --session sesn_000001
```

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "hello"
```

AgentRuntime turns run asynchronously. `event send` immediately records `session.status_running` and `user.message`; the background worker then records `agent.message` and `session.status_idle` when the runtime finishes.
The demo runtime also records `runtime.started`, `runtime.thinking`, `runtime.llm_request`, `runtime.llm_response`, and `runtime.completed` events so the execution process is visible in `event list` and SSE streams.

HTTP depends on the `internal/runner.Runner` interface. `cmd/server` injects a `WorkerRunner` backed by `AgentRuntimeTurnExecutor`, so HTTP handlers do not know runtime execution details.
If a Runner cannot start or complete a turn, the Store marks that turn as `failed`, records the failure reason, and returns the Session to `idle`.
`CompleteSessionTurn` stores the `agent.message` payload produced by Runner; response text lives in AgentRuntime output, not in Store.

Events for the same execution carry the same `payload.turn_id`, for example `turn_000001`.
Turn lifecycle is also persisted in `session_turns`, so the service can track whether a turn is `running`, `completed`, `interrupted`, or `failed`.

```bash
bin/tma event interrupt --session sesn_000001
```

`event interrupt` is valid while a Session is `running`. Run it right after `event send` to verify the interrupt path.

```bash
bin/tma event list --session sesn_000001 --after 0
```

```bash
bin/tma event stream --session sesn_000001 --after 0
```

More manual verification commands are in [TESTING.md](./TESTING.md).

Troubleshooting notes are in [docs/troubleshooting.md](./docs/troubleshooting.md).

Architecture decisions and development history are recorded in [DEVELOPMENT_LOG.md](./DEVELOPMENT_LOG.md).

## Next Steps

1. Add structured logging.
2. Add config loading.
3. Add sandbox provisioning.
4. Add a real WorkerRunner TurnExecutor backed by Sandbox / Agent Runtime.
5. Add Postgres LISTEN/NOTIFY if multiple API processes need shared live SSE fanout.
