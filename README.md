# Tiggy Manage Agent (TMA)

[English](./README.md) | [简体中文](./README.zh-CN.md)

**Live demo:** [https://app.tiggy.cloud/](https://app.tiggy.cloud/)

Tiggy Manage Agent is the Go implementation of the Agent Cloud Runtime project. It is not a chat UI; it is the cloud-side Harness runtime that lets enterprise Agents run as stable, auditable business-process nodes.

The long-term product direction is to make runtime evidence useful for Agent evolution. Events, traces, tool results, summaries, artifacts, and user feedback should eventually drive monitored, tested, versioned upgrades to `system`, memory, tools, skills, multi-agent routing, and runtime policies.

Current capabilities:

- Versioned Agent, Environment, Session, Run, Event, Artifact, Skill, MCP, observability, and orchestration APIs
- Postgres-backed durable state, turn leases, recovery, workspace isolation, and schema migrations
- Go and TypeScript `/v2` Core SDKs plus the `tma` operational CLI
- React Workbench (`/app`) and trace/operations Inspector (`/inspector`)
- Local/cloud sandbox capabilities, worker-backed `local_system` tools, and process tool plugins
- Agent-bound stdio and Streamable HTTP MCP servers, web search/crawl, and approval flows
- OIDC/JWKS, JWT, or trusted-gateway authentication with workspace-scoped RBAC and audit export
- Subagents, persistent task groups, fan-out/fan-in reducers, and bounded multi-agent deliberation

## Quick Start

Prerequisites:

- Go 1.25+
- Docker with Docker Compose
- Node.js 20+ and npm only when rebuilding the Web apps or TypeScript SDK

Create a local configuration file on first use:

```bash
cp .env.example .env
```

```bash
make db-up
make migrate-up
make test
make test-postgres
make run
```

The server listens on `:8080` by default.
The Makefile stores Go build cache in the project-local `.gocache/` directory so it works in restricted workspaces.

Open the Workbench at [http://localhost:8080/app](http://localhost:8080/app) or the Inspector at [http://localhost:8080/inspector](http://localhost:8080/inspector). The repository includes prebuilt embedded assets; run `make build-web-ui` after changing either React app.

User and control-plane APIs support OIDC/JWKS, legacy HS256 JWT, or trusted-gateway authentication with workspace-scoped RBAC. Protected requests emit structured authorization decision audit logs and low-cardinality Prometheus counters, with optional asynchronous OTLP/HTTP Logs export to an enterprise SIEM. Local development defaults to `TMA_AUTH_MODE=disabled`; `TMA_ENV=production` refuses to start without a complete identity configuration and worker service token. See [configuration](./docs/configuration.md#server-与认证) and [operations](./docs/operations.md).

```bash
curl http://localhost:8080/health
```

Expected response:

```json
{"status":"ok","service":"tiggy-manage-agent"}
```

## Layout

```text
cmd/tma-server/          HTTP server entrypoint
cmd/tma-worker/          Long-running worker entrypoint
cmd/tma/                 CLI entrypoint
apps/workbench/          User-facing React workbench
apps/inspector/          Operations and trace inspector
sdk/tma/                 Go Core SDK
sdk/typescript/          TypeScript Core SDK
api/v2/                  OpenAPI source and contract tests
internal/httpapi/        HTTP routes, handlers, and embedded web assets
internal/managedagents/  Resource model, Store interface, and PostgresStore
internal/runner/         Turn scheduling and execution
internal/agentruntime/   Agent runtime implementation
internal/capability/     Command, code, and file capability providers
internal/serverconfig/   Server configuration parser
sql/migrations/         Postgres schema migrations
```

## Extension Development

Before adding a Provider, Worker plugin, Tool namespace, MCP integration, lifecycle capability, or settings panel, read [Extensions](./docs/extensions.md), [Tools](./docs/tools.md), [MCP](./docs/mcp.md), and [Workbench](./docs/workbench.md). New extensions must pass the governance, compatibility, permission, isolation, and upgrade checks defined there.

## Go Core SDK

The Go Core SDK lives in `sdk/tma`. It covers the user and control-plane API, while worker poll/ack/heartbeat/result remains a separate machine protocol. New Go integrations should use the SDK instead of copying HTTP, authentication, SSE, approval, or artifact logic.

```go
client, err := tma.NewClient("http://localhost:8080", tma.WithBearerToken(token))
session, err := client.Sessions.Create(ctx, tma.CreateSessionRequest{AgentID: "agt_general"})
run, err := client.Runs.Start(ctx, session.ID, tma.StartRunRequest{Input: tma.TextInput("analyze this repository")})
result, err := run.Wait(ctx)
```

Regenerate the complete v2 OpenAPI surface and low-level Go client with `make generate-go-sdk`.

The TypeScript/Node alpha SDK lives in `sdk/typescript`. Use `make generate-typescript-sdk`, `make test-typescript-sdk`, and `make test-typescript-sdk-e2e`; its generated low-level client covers the complete `/v2` contract and typed high-level services cover every current user and control-plane domain. Worker consumer machine routes, legacy `/v1/task-templates`, Tool Catalog, tool creation, and direct tool execution remain outside the package.

The Web App SDK pilot now uses the local package for typed queries and management writes across the complete user/control-plane surface while retaining its existing React-facing response shapes. Normal messages create Runs, queued messages use typed v2 Session event append, interrupts cancel the active Run, and Session SSE uses the SDK reconnecting AsyncGenerator. Trace/Span catalogs expose only opaque cursor pagination. Native ObjectRef and Skill package links target `/v2`; Workbench plugins consume host-provided Task and Artifact facades backed by the SDK. Legacy `/v1/task-templates` is the only production Web App v1 request retained.

Session create/archive/restore/rerun/delete, metadata, and runtime settings writes now also use the typed SDK. Session message/interrupt events and config upgrade remain separate because they have run and config-version semantics beyond ordinary resource lifecycle operations.

The Web Inspector uses the same SDK for Session, Events, Usage, Summary, Artifacts, Interventions, Observability, Session Trace, and Trace/Span catalog/detail queries. Artifact previews use SDK downloads, browser links target the same `/v2` resource, and approval decisions use the typed Interventions service. Catalog pagination is cursor-only; the UI consumes `next_cursor` and no longer exposes or computes numeric offsets. Metrics, event sending, and other writes remain on their existing interfaces.

Operational CLI commands use the same typed services for portable Agent transfer, Session comparison, and cursor-based trace inspection:

```bash
bin/tma agent export --id agt_000001 --output agent.json
bin/tma agent import --file agent.json --name imported-agent
bin/tma session compare --left sesn_000001 --right sesn_000002
bin/tma trace list --limit 20
bin/tma trace show --trace trace_000001 --json
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
Production Docker Compose and Kubernetes procedures are in [docs/deployment.md](./docs/deployment.md).
The guarded quick-deploy entry point is `scripts/deploy.sh docker|k8s`.

`make run` uses this local default unless you override it:

```bash
postgres://tma:tma@localhost:5432/tma?sslmode=disable
```

Direct `go run ./cmd/tma-server` still requires `TMA_DATABASE_URL` from `.env` or your shell.

The local `tma` database user is also the migration owner and is development-only. Production `tma-server` must use a separate non-owner PostgreSQL runtime role without `SUPERUSER` or `BYPASSRLS`; startup validates the forced workspace RLS policy for managed environment variables. See [deployment](./docs/deployment.md#数据库迁移).

Override the connection string when needed:

```bash
TMA_DATABASE_URL="postgres://user:pass@localhost:5432/db?sslmode=disable" make run
```

The server runs turns through `WorkerRunner + AgentRuntimeTurnExecutor + Agent Core`. Agent Core persists every turn transition and resolves the Session-bound AgentConfigVersion before calling `llm.Manager`. The default `fake` provider stays local; `openai-compatible` can call an OpenAI Chat Completions compatible endpoint. Runtime design notes are in [docs/architecture.md](./docs/architecture.md).

Command turns are still documented as a lower-level external process adapter in [docs/architecture.md](./docs/architecture.md), but they are not the default server path.

The next Capability Provider boundary is documented in [docs/architecture.md](./docs/architecture.md). The current code defines a low-level provider surface, not a turn-level environment executor or a full Tool module.

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

## API Compatibility

New integrations should use the typed `/v2` contract through the Go or TypeScript Core SDK. The following `/v1` routes remain available for compatibility and low-level operational flows:

```text
POST /v1/agents
GET  /v1/agents/default
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
curl -sS http://localhost:8080/v1/agents/default
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

When the Server uses OIDC, sign in through the IdP Device Authorization Flow. The CLI opens the verification page and stores the resulting credential in the operating system Keychain; it never asks for the user's password.

```bash
bin/tma auth login
bin/tma auth status
bin/tma agent list
bin/tma auth logout
```

For automation, `--auth-token` or `TMA_AUTH_TOKEN` takes precedence over the Keychain. An explicit token remains active after `auth logout` and must be removed from the process environment separately.

Build the minimal worker:

```bash
make build-worker
```

Run the worker in the background with the same process-management commands as
the server:

```bash
make worker-start
make worker-status
make worker-restart
make worker-stop
```

The managed worker writes its PID to `.tma-worker.pid` and logs to
`.tma-worker.log`. Configure it through `TMA_BASE_URL` and the existing
`TMA_WORKER_*` environment variables. For command-line-only options, invoke the
script directly, for example
`scripts/tma_worker.sh start --name viito-mac --concurrency 2`.

The worker registers itself, sends worker heartbeat, polls `/v1/workers/{id}/work/poll`, acknowledges work, heartbeats running work leases, and completes work. `tool_execution` work uses the `tma.work.v1` invocation format; `default.*` tools run through `tools.DefaultRuntime + LocalSystemProvider` on the machine running `tma-worker`. Set `--workspace-root PATH` or `TMA_WORKER_WORKSPACE_ROOT=PATH` to constrain local filesystem tools to one existing directory; production workers should configure this boundary. By default a worker executes one work item at a time; use `--concurrency N` or `TMA_WORKER_CONCURRENCY=N` to lease and execute multiple queue jobs concurrently. Long-running work is renewed with `--work-heartbeat-interval` / `TMA_WORKER_WORK_HEARTBEAT_INTERVAL` while it is executing. On SIGINT/SIGTERM, the worker marks itself `draining`, stops polling, and waits up to `--shutdown-timeout` / `TMA_WORKER_SHUTDOWN_TIMEOUT` for running work to finish. When an agent config enables `local_system`, AgentRuntime only exposes those tools if a matching online worker exists, unless trusted local development explicitly enables server-local fallback.

```bash
bin/tma-worker --base-url http://localhost:8080 --name viito-mac --workspace-root "$PWD"
```

Check worker connectivity and declared capabilities without starting the long-running loop:

```bash
bin/tma-worker doctor --base-url http://localhost:8080 --name viito-mac
```

Workers can also load process tool plugins. A plugin contributes new `namespace.api`
tools to the existing `tool_execution` work protocol without adding new
`work_type` values:

```bash
bin/tma-worker --base-url http://localhost:8080 --name lab-worker --plugin /opt/tma/plugins/robot
```

If an agent enables the plugin namespace, for example `{"tools":["robot"],"runtime":"local_system"}`, online worker plugin manifests are exposed to AgentRuntime as model tools and executed through worker-backed `tool_execution` work.

The repository includes a minimal robot example:

```bash
bin/tma-worker --base-url http://localhost:8080 --name robot-worker --plugin examples/plugins/robot-shell/robot-plugin.py
bin/tma worker list --workspace wksp_default --status online
bin/tma worker diagnose --namespace robot --api get_state --capabilities robot.state --runtime local_system
```

It also includes a `computer` example for desktop computer-use integration. This plugin is intentionally backend-pluggable: use CUA as the primary backend, local AX/UI tree as an inspect fallback, and no OmniParser dependency.

```bash
TMA_COMPUTER_BACKEND=auto \
bin/tma-worker --base-url http://localhost:8080 --name computer-worker --plugin examples/plugins/computer-use/computer-plugin.py

bin/tma worker diagnose --namespace computer --api get_state --capabilities computer.state.read,computer.ax.read --runtime local_system
```

The process plugin protocol and SDK contract are documented in [docs/tools.md](./docs/tools.md). The `computer.*` API and backend contract are documented in [docs/tools.md](./docs/tools.md). Future language SDK packages should wrap this protocol rather than changing the core `tool_execution` shape.

Agent-level MCP integration is documented in [docs/mcp.md](./docs/mcp.md). This path lets an Agent bind stdio or Streamable HTTP MCP servers through `config_version.mcp`, expose their tools as standard model tools, and execute them through the existing TMA tool/result pipeline. The TMA Server keeps stdio processes and remote HTTP Sessions alive per Session and Agent config, while isolating scopes and reclaiming idle entries.

Run the end-to-end MCP stdio smoke test with:

```bash
make verify-mcp-stdio
```

Verify the local worker-backed path with a temporary server and worker:

```bash
make verify-worker-backed-local-system
```

## Web Search And Crawl

TMA exposes `web.search` and `web.crawl` as server builtin tools. They do not require the `cloud_sandbox` or `local_system` provider to be available.

Local search defaults to SearXNG on port `8180`:

```bash
docker compose up -d searxng
```

If `TMA_WEB_TAVILY_API_KEY`, `TMA_WEB_BRAVE_API_KEY`, `TMA_WEB_EXA_API_KEY`, or `TMA_WEB_BAIDU_API_KEY` is configured and `TMA_WEB_SEARCH_PROVIDERS` is not set, keyed providers are tried before the local SearXNG fallback.

Run the full web tool verification:

```bash
make verify-web-search-crawl
```

That target checks the local SearXNG JSON API, then runs AgentRuntime tool-call loops for both `web.crawl` and `web.search`.

Verify cloud sandbox outbound network approval behavior with:

```bash
make verify-network-approval
```

That target covers `request_approval`, `approve_for_me`, `full_access`, and `cloud_sandbox_allow_network=false` using a fake LLM-triggered Python download.

Inspect the current local web configuration with:

```bash
bin/tma web doctor --searxng-url http://localhost:8180 --query 测试 --timeout 20
```

Directly exercise search/crawl without an LLM session:

```bash
bin/tma web search --query "中文 AI 智能体 新闻" --limit 5 --timeout 30
bin/tma web crawl --url https://example.com --timeout 45
bin/tma web crawl --url https://example.com --impl browserless --attempts-only
```

Enable web tools for an agent config with:

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --tools '{"tools":["web"],"runtime":"auto"}'
```

Configuration details are in [docs/configuration.md](./docs/configuration.md#web-与-browser).

Example flow:

```bash
bin/tma health
```

```bash
bin/tma agent create \
  --name "Code Assistant" \
  --llm-provider volcengine-agent-plan \
  --llm-model gpt-4o-mini \
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
bin/tma session attach --session sesn_000001 --after 0
```

`session attach` is the recommended human CLI entrypoint. It streams session
events, lets you type user messages directly, and handles pending tool approval
with `a` / `r reason` / `s` in the same terminal. It also supports `/interrupt`
and `/quit`.

```bash
bin/tma session archive --session sesn_000001
```

```bash
bin/tma session delete --session sesn_000001
```

AgentRuntime turns run asynchronously. Sending a message through `session attach`
records `session.status_running` and `user.message`; the background worker then
records `agent.message` and `session.status_idle` when the runtime finishes.
Agent Core records durable `runtime.*`, `model.*`, `tool.*`, `completion.*`, and intervention events so execution and recovery remain visible in `event list` and SSE streams.

HTTP depends on the `internal/runner.Runner` interface. `cmd/tma-server` injects a `WorkerRunner` backed by `AgentRuntimeTurnExecutor`, so HTTP handlers do not know runtime execution details.
If a Runner cannot start or complete a turn, the Store marks that turn as `failed`, records the failure reason, and returns the Session to `idle`.
`CompleteSessionTurn` stores the `agent.message` payload produced by Runner; response text lives in AgentRuntime output, not in Store.

Events for the same execution carry the same `payload.turn_id`, for example `turn_000001`.
Turn lifecycle and execution leases are persisted in `session_turns`. Server instances claim runnable turns with database row locking, renew leases while executing, recover expired/unclaimed `running` turns after restart, and observe interrupts written by other instances. Completed-turn summary refresh and trace export run outside the Turn worker pool.

```bash
bin/tma event interrupt --session sesn_000001
```

`event send`, `event interrupt`, and `event stream` remain useful scripting and
debugging entrypoints. For normal manual use, prefer `session attach`.

`event interrupt` is valid while a Session is `running`. Run it right after sending a message to verify the interrupt path.

```bash
bin/tma event list --session sesn_000001 --after 0
```

```bash
bin/tma event stream --session sesn_000001 --after 0
```

More manual verification commands are in [TESTING.md](./TESTING.md).

Skills and Marketplace control-plane commands use the typed Go Core SDK:

```bash
bin/tma skill list --workspace wksp_default
bin/tma marketplace discover --session sesn_000001 --repository owner/repository
bin/tma marketplace preview --session sesn_000001 --source '{"provider":"github","repository":"owner/repository","ref":"main","path":"SKILL.md"}'
```

Run `bin/tma help` for Skill version/package/retention/GC and Marketplace install, binding, entry, and policy commands. Marketplace writes remain Server-controlled and do not create or directly execute tools.

The consolidated documentation index is [docs/README.md](./docs/README.md). Architecture decisions and development history remain in [DEVELOPMENT_LOG.md](./DEVELOPMENT_LOG.md).

## Next Steps

1. Complete the production closeout checklist in `docs/roadmap.md`.
2. Extend multi-principal RBAC to the remaining control actions and audit queries.
3. Add token, cost, wall-clock, and fan-out budgets plus stuck detection and operational runbooks.
4. Establish task-group capacity baselines, replay fixtures, real-model samples, and offline evals.
5. Add versioned, workspace-overridable task-group templates when tenant customization requires them.
6. Keep durable workflow / DAG deferred until product requirements require multi-stage long-running orchestration.
