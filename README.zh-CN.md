# Tiggy Manage Agent (TMA)

[English](./README.md) | [简体中文](./README.zh-CN.md)

Tiggy Manage Agent 是 Agent Cloud Runtime 项目的 Go 实现。它不只是一个聊天界面，而是运行在云端的 Harness Runtime，用于让企业 Agent 成为稳定、可审计的业务流程节点。

项目的长期方向是利用运行证据推动 Agent 演进。事件、Trace、工具结果、摘要、制品和用户反馈将用于对 `system`、记忆、工具、Skills、多 Agent 路由和运行策略进行可监控、可测试、可版本化的升级。

当前能力包括：

- 带版本的 Agent、Environment、Session、Run、Event、Artifact、Skill、MCP、可观测与编排 API
- 基于 Postgres 的持久化状态、Turn Lease、故障恢复、Workspace 隔离和数据库迁移
- Go 与 TypeScript `/v2` Core SDK，以及运维 CLI `tma`
- React Workbench（`/app`）和 Trace/运维 Inspector（`/inspector`）
- 本地/云端沙箱能力、Worker 承载的 `local_system` 工具和进程工具插件
- Agent 绑定的 stdio、Streamable HTTP MCP Server、Web 搜索/抓取和审批流程
- OIDC/JWKS、JWT 或可信网关认证，Workspace 级 RBAC 和审计导出
- Subagent、持久化 Task Group、fan-out/fan-in Reducer 和有边界的多 Agent 讨论

## 快速开始

前置条件：

- Go 1.25+
- Docker 与 Docker Compose
- 仅在重新构建 Web 应用或 TypeScript SDK 时需要 Node.js 20+ 和 npm

首次使用时创建本地配置：

```bash
cp .env.example .env
```

启动数据库、应用迁移、运行测试并启动服务：

```bash
make db-up
make migrate-up
make test
make test-postgres
make run
```

服务默认监听 `:8080`。Makefile 将 Go 构建缓存写入项目内的 `.gocache/`，可用于文件系统权限受限的工作区。

启动后可访问：

- Workbench：<http://localhost:8080/app>
- Inspector：<http://localhost:8080/inspector>
- 健康检查：<http://localhost:8080/health>

仓库已包含预构建并嵌入 Server 的 Web 资源。修改 React 应用后，运行 `make build-web-ui` 重新构建。

健康检查的预期响应：

```json
{"status":"ok","service":"tiggy-manage-agent"}
```

用户和控制面 API 支持 OIDC/JWKS、兼容的 HS256 JWT 或可信网关认证，并提供 Workspace 级 RBAC。受保护的请求会输出结构化授权决策审计日志和低基数 Prometheus 指标，也可以异步导出 OTLP/HTTP Logs 到企业 SIEM。本地开发默认使用 `TMA_AUTH_MODE=disabled`；`TMA_ENV=production` 下，如果身份配置或 Worker Service Token 不完整，服务将拒绝启动。详见[配置说明](./docs/configuration.md#unified-identity-and-rbac)和[安全运维](./docs/security-operations.md)。

## 项目结构

```text
cmd/tma-server/          HTTP Server 入口
cmd/tma-worker/          长期运行的 Worker 入口
cmd/tma/                 CLI 入口
apps/workbench/          面向用户的 React Workbench
apps/inspector/          运维与 Trace Inspector
sdk/tma/                 Go Core SDK
sdk/typescript/          TypeScript Core SDK
api/v2/                  OpenAPI 源文件与契约测试
internal/httpapi/        HTTP 路由、Handler 与嵌入的 Web 资源
internal/managedagents/  资源模型、Store 接口与 PostgresStore
internal/runner/         Turn 调度与执行
internal/agentruntime/   Agent Runtime 实现
internal/capability/     命令、代码和文件 Capability Provider
internal/serverconfig/   Server 配置解析
sql/migrations/         Postgres Schema 迁移
```

## 扩展开发

新增 Provider、Worker 插件、Tool Namespace、MCP 集成、生命周期能力或设置面板前，请遵循以下规范：

- [扩展与 Provider 治理规范](./docs/extension-governance-standard.md)：分类、Descriptor、能力发现、兼容性、Worker 离线行为、冲突和用户确认的 Provider 切换
- [扩展设置规范](./docs/extension-settings-standard.md)：Settings Contribution、配置作用域、Schema 驱动渲染、Secret、诊断、Revision 和离线 UI
- [Tool Plugin SDK](./docs/tool-plugin-sdk.md)：当前进程插件与 Worker 执行协议
- [API v2 响应与数据规范](./docs/api-v2-response-and-data-standards.md)：HTTP 状态、稳定错误、可重试性、JSON 数字、时间、枚举和 Cursor 分页
- [Workbench 插件规范](./docs/workbench-plugin-standard.md)：企业页面、导航、Widget、Command、详情面板、SDK 访问和租户启用模型
- [Server、Core SDK 与 App 扩展边界](./docs/core-sdk-extension-architecture.md)：所有权、依赖方向、认证和多 Server 规则
- [Go Core SDK](./docs/go-core-sdk.md)与[TypeScript Core SDK](./docs/typescript-core-sdk.md)：`/v2` 客户端、认证、错误、SSE 和兼容策略
- [MCP Server 兼容矩阵](./docs/mcp-server-compatibility.md)：已固定的第三方版本、传输验证和已知缺口

新扩展完成治理规范和设置规范中的检查清单后，才能视为可集成。

## Core SDK

Go Core SDK 位于 `sdk/tma`，覆盖用户和控制面 API。Worker 的 poll/ack/heartbeat/result 仍是独立机器协议。新的 Go 集成应复用 SDK 中的 HTTP、认证、SSE、审批和 Artifact 能力。

```go
client, err := tma.NewClient("http://localhost:8080", tma.WithBearerToken(token))
session, err := client.Sessions.Create(ctx, tma.CreateSessionRequest{AgentID: "agt_general"})
run, err := client.Runs.Start(ctx, session.ID, tma.StartRunRequest{Input: tma.TextInput("分析这个仓库")})
result, err := run.Wait(ctx)
```

重新生成完整 `/v2` OpenAPI 接口和 Go 低层客户端：

```bash
make generate-go-sdk
```

TypeScript/Node Alpha SDK 位于 `sdk/typescript`。生成的低层客户端覆盖完整 `/v2` 契约，类型化高层 Service 覆盖当前用户和控制面领域。Worker Consumer 机器路由、兼容的 `/v1/task-templates`、Tool Catalog、工具创建与直接执行不在该包范围内。

```bash
make generate-typescript-sdk
make test-typescript-sdk
make test-typescript-sdk-e2e
```

Workbench 和 Inspector 均使用本地 TypeScript SDK 处理类型化查询、管理写入、SSE、Artifact 下载和仅支持不透明 Cursor 的 Trace/Span 分页。Workbench 生产请求中仅 `/v1/task-templates` 仍保留 v1 接口。

运维 CLI 同样复用类型化 Service，例如：

```bash
bin/tma agent export --id agt_000001 --output agent.json
bin/tma agent import --file agent.json --name imported-agent
bin/tma session compare --left sesn_000001 --right sesn_000002
bin/tma trace list --limit 20
bin/tma trace show --trace trace_000001 --json
```

## 持久化与运行时

Server 依赖 Postgres。启动数据库并应用 Schema：

```bash
make db-up
make migrate-up
make run
```

`make migrate-up` 使用 Postgres 容器内的 `psql`，不要求本机安装 `psql`。Server 启动时会读取项目根目录的 `.env`，Shell 中已经导出的同名变量优先。

配置参考见[配置说明](./docs/configuration.md)，生产 Docker Compose 与 Kubernetes 流程见[部署说明](./docs/deployment.md)。受保护的快速部署入口为：

```bash
scripts/deploy.sh docker
scripts/deploy.sh k8s
```

`make run` 的本地默认连接为：

```text
postgres://tma:tma@localhost:5432/tma?sslmode=disable
```

直接执行 `go run ./cmd/tma-server` 时，仍需从 `.env` 或 Shell 提供 `TMA_DATABASE_URL`。需要覆盖连接串时使用：

```bash
TMA_DATABASE_URL="postgres://user:pass@localhost:5432/db?sslmode=disable" make run
```

本地 `tma` 数据库用户同时是 Migration Owner，只能用于开发。生产 Server 必须使用独立、非 Owner、无 `SUPERUSER` 和 `BYPASSRLS` 权限的运行角色；启动过程会校验托管环境变量的强制 Workspace RLS 策略。详见[数据库配置](./docs/configuration.md#tma_database_url)。

Server 通过 `WorkerRunner + AgentRuntimeTurnExecutor + agentruntime.DemoRuntime` 运行 Turn。Runtime 解析 Session 绑定的 AgentConfigVersion，再通过其中的 `llm_provider` / `llm_model` 调用 `llm.Manager`。默认 `fake` Provider 完全在本地运行；`openai-compatible` 可连接兼容 OpenAI Chat Completions 的端点。详见[Agent Runtime](./docs/agent-runtime.md)。

验证完整的 CLI / HTTP / Runner 链路：

```bash
make verify-agent-runtime
```

也可以运行包含数据库启动、迁移、临时 Server 和清理的完整验证：

```bash
make verify-agent-runtime-full
```

`PostgresStore` 的 SSE 实时投递目前仍是进程内的；历史回放和断线续传读取 `session_events`，因此 Server 重启后 `--after` 仍可继续使用。Server 通过 Go `slog` 输出 JSON 结构化日志，Event 和 Turn 日志包含 `session_id`、`turn_id`、`event_seq`、`event_type` 和 `event_id` 等字段。

## API 兼容性

新集成应通过 Go 或 TypeScript Core SDK 使用类型化 `/v2` 契约。以下 `/v1` 路由仍用于兼容和低层运维流程：

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

完整 HTTP 契约见 [API Reference](./docs/api-reference.md)。

## CLI 与 Worker

构建 CLI 并查看命令：

```bash
make build-cli
bin/tma help
```

Server 使用 OIDC 时，通过 IdP Device Authorization Flow 登录。CLI 会打开验证页面并将凭据写入操作系统 Keychain，不会请求用户密码。

```bash
bin/tma auth login
bin/tma auth status
bin/tma agent list
bin/tma auth logout
```

自动化场景中，`--auth-token` 或 `TMA_AUTH_TOKEN` 的优先级高于 Keychain。`auth logout` 不会移除进程环境中显式设置的 Token。

构建并管理 Worker：

```bash
make build-worker
make worker-start
make worker-status
make worker-restart
make worker-stop
```

后台 Worker 将 PID 写入 `.tma-worker.pid`，日志写入 `.tma-worker.log`。通过 `TMA_BASE_URL` 和 `TMA_WORKER_*` 环境变量配置，或直接向脚本传参：

```bash
scripts/tma_worker.sh start --name viito-mac --concurrency 2
```

Worker 会注册、发送心跳、轮询并确认工作、续租运行中的任务，最后提交执行结果。`tool_execution` 使用 `tma.work.v1` 调用格式；`default.*` 工具在 Worker 所在机器通过 `tools.DefaultRuntime + LocalSystemProvider` 执行。默认并发为 1，可通过 `--concurrency N` 或 `TMA_WORKER_CONCURRENCY=N` 调整。收到 SIGINT/SIGTERM 后，Worker 会进入 `draining`、停止轮询，并在 Shutdown Timeout 内等待正在运行的工作结束。

直接运行或只检查连接与声明的能力：

```bash
bin/tma-worker --base-url http://localhost:8080 --name viito-mac
bin/tma-worker doctor --base-url http://localhost:8080 --name viito-mac
```

当 Agent 配置启用 `local_system` 时，只有匹配的 Worker 在线，AgentRuntime 才会暴露相应工具；可信本地开发可以显式启用 Server 本地回退。

## 工具插件与 MCP

Worker 可以加载进程工具插件。插件通过现有 `tool_execution` 协议贡献新的 `namespace.api` 工具，不需要增加 `work_type`：

```bash
bin/tma-worker --base-url http://localhost:8080 --name lab-worker \
  --plugin /opt/tma/plugins/robot
```

仓库包含最小 Robot 示例：

```bash
bin/tma-worker --base-url http://localhost:8080 --name robot-worker \
  --plugin examples/plugins/robot-shell/robot-plugin.py
bin/tma worker list --workspace wksp_default --status online
bin/tma worker diagnose --namespace robot --api get_state \
  --capabilities robot.state --runtime local_system
```

也包含可替换后端的 `computer` 桌面控制示例。它以 CUA 为主后端，以本地 AX/UI Tree 为检查回退，不依赖 OmniParser：

```bash
TMA_COMPUTER_BACKEND=auto \
bin/tma-worker --base-url http://localhost:8080 --name computer-worker \
  --plugin examples/plugins/computer-use/computer-plugin.py
```

协议说明见 [Tool Plugin SDK](./docs/tool-plugin-sdk.md)，`computer.*` 接口见[计算机控制插件](./docs/computer-use-plugin.md)。

Agent 可通过 `config_version.mcp` 绑定 stdio 或 Streamable HTTP MCP Server，并将 MCP 工具作为普通模型工具接入现有 TMA 工具/结果链路。TMA Server 按 Session 和 Agent 配置维护 stdio 进程与远程 HTTP Session，同时隔离作用域并回收空闲实例。详见 [MCP 集成](./docs/mcp-integration.md)。

```bash
make verify-mcp-stdio
make verify-worker-backed-local-system
```

## Web 搜索与抓取

TMA 将 `web.search` 和 `web.crawl` 作为 Server 内置工具提供，不依赖 `cloud_sandbox` 或 `local_system` Provider。

本地搜索默认使用 `8180` 端口的 SearXNG：

```bash
docker compose up -d searxng
```

如果配置了 `TMA_WEB_TAVILY_API_KEY`、`TMA_WEB_BRAVE_API_KEY`、`TMA_WEB_EXA_API_KEY` 或 `TMA_WEB_BAIDU_API_KEY`，并且没有设置 `TMA_WEB_SEARCH_PROVIDERS`，系统会先尝试带 Key 的 Provider，再回退到本地 SearXNG。

```bash
make verify-web-search-crawl
make verify-network-approval
bin/tma web doctor --searxng-url http://localhost:8180 --query 测试 --timeout 20
bin/tma web search --query "中文 AI 智能体 新闻" --limit 5 --timeout 30
bin/tma web crawl --url https://example.com --timeout 45
```

为 Agent 配置启用 Web 工具：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --tools '{"tools":["web"],"runtime":"auto"}'
```

配置细节见 [Web Search & Crawl](./docs/configuration.md#web-search--crawl)。

## 基本使用流程

```bash
bin/tma health

bin/tma agent create \
  --name "Code Assistant" \
  --llm-provider volcengine-agent-plan \
  --llm-model gpt-4o-mini \
  --system "You are a coding agent."

bin/tma env create --name default-cloud

bin/tma session create \
  --agent agt_000001 \
  --env env_000001 \
  --title "First TMA task"

bin/tma session attach --session sesn_000001 --after 0
```

`session attach` 是推荐的人机交互 CLI 入口。它会流式展示 Session Event，允许直接输入用户消息，并在同一终端用 `a` / `r reason` / `s` 处理工具审批；同时支持 `/interrupt` 和 `/quit`。

AgentRuntime 异步运行 Turn。发送消息会记录 `session.status_running` 和 `user.message`；后台 Worker 完成后写入 `agent.message` 和 `session.status_idle`。同一次执行的 Event 使用相同的 `payload.turn_id`。Turn 生命周期与执行 Lease 持久化在 `session_turns` 中，Server 实例通过数据库行锁认领任务、执行期间续租，并在重启后恢复过期或未认领的任务。

```bash
bin/tma event interrupt --session sesn_000001
bin/tma event list --session sesn_000001 --after 0
bin/tma event stream --session sesn_000001 --after 0
bin/tma session archive --session sesn_000001
bin/tma session delete --session sesn_000001
```

更多手动验收命令见 [TESTING.md](./TESTING.md)。

Skills 与 Marketplace 控制面命令使用 Go Core SDK：

```bash
bin/tma skill list --workspace wksp_default
bin/tma marketplace discover --session sesn_000001 --repository owner/repository
bin/tma marketplace preview --session sesn_000001 \
  --source '{"provider":"github","repository":"owner/repository","ref":"main","path":"SKILL.md"}'
```

Marketplace 写操作由 Server 控制，不会创建或直接执行工具。完整的 Skill Version、Package、Retention/GC、Marketplace 安装、绑定和策略命令可通过 `bin/tma help` 查看。

## 文档索引

- [故障排查](./docs/troubleshooting.md)
- [生产部署](./docs/deployment.md)
- [Inspector 使用说明](./docs/inspector.md)
- [API Reference](./docs/api-reference.md)
- [Onlyboxes 沙箱部署与 LobeHub 集成](./docs/产品设计架构图梳理.md)
- [产品缺口与建议开发顺序](./docs/product-gap-roadmap.md)
- [多 Agent 能力边界与上线清单](./docs/agent-orchestration-status.md)
- [架构决策与开发历史](./DEVELOPMENT_LOG.md)

## 后续方向

1. 完成 `docs/agent-orchestration-status.md` 中的生产收尾清单。
2. 将多主体 RBAC 扩展到剩余控制操作和审计查询。
3. 增加 Token、费用、Wall-clock 和 fan-out Budget，以及 Stuck Detection 和运维 Runbook。
4. 建立 Task Group 容量基线、回放样例、真实模型样本和离线评测。
5. 在租户定制确有需要时，增加带版本、支持 Workspace Override 的 Task Group Template。
6. 在产品出现多阶段长期编排需求前，继续推迟 Durable Workflow / DAG。
