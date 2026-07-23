# TMA 测试命令

本文档记录当前 Go 项目的手动验收命令。

当前 server 使用 `PostgresStore`，数据会持久化，ID 以数据库已有数据为准。

server 启动时会自动读取项目根目录下的 `.env`。如果 shell 里已经 `export` 了同名变量，shell 里的值优先。

环境变量总览见 [docs/configuration.md](./docs/configuration.md)。

---

## 1. 基础验证

```bash
cd "/Users/viito/Desktop/Harness 企业级定制化实现/tiggy-manage-agent"
```

```bash
make fmt
make test
make eval-agent-quality
make build
make build-cli
```

`make eval-agent-quality` 是无需 Docker、网络、数据库或真实模型的确定性 Agent 完成质量回归。它直接运行生产 Tool Loop 与 Task Plan completion gate，并对 false success、重试修正、verified evidence 合规和失败关闭执行 CI 阈值；指标定义见 [docs/architecture.md](./docs/architecture.md)。

Postgres 集成测试默认跳过。可以显式运行：

```bash
make test-postgres
```

`make test-postgres` 会启动 Compose PostgreSQL，创建一次性 `tma_test_*` 数据库，应用全部迁移后运行测试，并在结束或失败时终止残留连接、删除临时数据库。它不会使用开发 server 正在连接的 `tma` 数据库，因此后台 turn worker 不会消费集成测试数据。可通过 `TMA_POSTGRES_TEST_USER`、`TMA_POSTGRES_TEST_PASSWORD`、`TMA_POSTGRES_TEST_HOST` 和 `TMA_POSTGRES_TEST_PORT` 覆盖本地连接参数。

`make migrate-up` 使用 `ON_ERROR_STOP=1` 和单文件事务执行每个迁移。任何 SQL 错误都会立即让命令和依赖它的 verification target 失败，不允许继续执行后续迁移形成假绿；重复执行应保持幂等成功。

### Agent Core 性能基准

内存 Agent Loop 基准会报告每轮延迟、分配、事件数和持久化转换数：

```bash
make benchmark-agent-core
```

与当前 `HEAD` 使用同一份 benchmark 做基线对照：

```bash
make benchmark-agent-core-compare
```

可通过 `TMA_BENCH_BASE_REF`、`TMA_BENCHTIME` 和 `TMA_BENCH_COUNT` 调整基线、运行时间和重复次数。事务数量是硬性性能不变量：无 Tool 为 3 次；1 个或 10 个安全只读 Tool 均为 8 次；10 个非幂等写 Tool 为 17 次。安全读批次必须保持 O(1) 次持久化，写 Tool 必须逐次持久化结果。

真实 PostgreSQL 基准会创建一次性 `tma_bench_*` 数据库、应用全部迁移、运行基准并自动删除数据库，不连接开发或 staging 数据：

```bash
make benchmark-agent-core-postgres
make benchmark-agent-core-e2e
```

第一个入口测量 fast commit 和 Session event counter；第二个入口执行无 Tool、安全读、危险写和审批 pause/resume 的完整 Engine 流程。默认分别执行 50 次和 20 次，以便计算 P50/P95/P99。可通过 `TMA_POSTGRES_BENCHTIME`、`TMA_POSTGRES_E2E_BENCHTIME` 和 `TMA_POSTGRES_BENCH_COUNT` 调整。

生成安全读端到端场景的 CPU 和内存 profile：

```bash
make profile-agent-core-e2e
```

profile 输出保存在 `.codex_artifacts/profiles`。绝对延迟受宿主机、Docker 和 PostgreSQL 配置影响，不作为跨机器 CI 硬阈值；发布前应在同一机器上与目标基线比较。

真实 Keycloak OIDC/Claim mapping 验收使用独立 Compose profile。它会导入测试 realm，签发测试用户 Token，启动临时 TMA 服务，验证 Group 到 Workspace/operator 的映射和 Workspace 隔离，最后清理 Keycloak 容器及测试租户：

```bash
make verify-oidc-keycloak
```

The verification checks the effective Principal, client workspace spoofing resistance, the structured `group_mapping:finance-operators` authorization audit source, the OIDC authorization decision Prometheus counter, a delivered HMAC-protected PostgreSQL outbox row, and a real OTLP/HTTP Logs delivery to a local authenticated fixture. It creates a fully migrated isolated PostgreSQL database and removes it with the temporary fixture on exit, so development data is never read or modified. A Keycloak container started by the script is removed; a pre-existing development Keycloak instance remains running.

Keycloak Realm 安全基线可单独应用和验收。应用命令只更新安全策略，不修改用户、分组、客户端或 SMTP 凭据；验收同时检查仓库配置与当前运行实例：

```bash
make keycloak-security-apply
make verify-keycloak-security
```

该脚本使用 Resource Owner Password Grant 仅用于无交互本地测试；生产登录仍应使用 Authorization Code + PKCE。

当前覆盖：

- `user.message -> CompleteSessionTurn`：turn `completed`，Session 回到 `idle`
- `user.message -> user.interrupt -> late CompleteSessionTurn`：turn `interrupted`，不补 `agent.message`
- `user.message -> FailSessionTurn`：turn `failed`，Session 回到 `idle`，可继续发送下一条消息
- 原生 `tools` / `tool_calls` 与 `tma.tool_call.v1` fallback
- Session 级 `intervention_mode`
- CLI `event stream` 对审批事件的可读展示
- Trace / span 投影、持久 span index 写入、`/v1/spans` 索引查询和 trace index retention prune

---

## 2. 启动数据库和服务

终端 A：

```bash
cd "/Users/viito/Desktop/Harness 企业级定制化实现/tiggy-manage-agent"
make db-up
make migrate-up
make run
```

默认监听：

```text
http://localhost:8080
```

启动服务。当前服务端固定使用 durable Agent Core：

```bash
make run
```

此模式下 `agent.message` 通常返回：

```text
Agent runtime received: <你的文本>
```

AgentRuntime 设计见 [docs/architecture.md](./docs/architecture.md)。

常见误配置和修正记录见 [docs/operations.md](./docs/operations.md)。

服务启动后，可在另一个终端跑自动验收：

```bash
make verify-agent-runtime
```

也可以使用自启动完整验收，它会自动启动数据库、迁移、启动临时服务、验收、停止服务：

```bash
make verify-agent-runtime-full
```

`verify-agent-runtime-full` 会显式使用 `fake` Provider，适合检查基础 HTTP / Store / Runner / Runtime 链路，不受本地真实模型配置影响。

### Agent Core staging 生产验收

Agent Core staging 验收连接已经运行的 staging Server，默认地址是 `http://localhost:18088`。它不会启动或替换 Server，也不会创建临时数据库；普通入口会使用真实 Provider 完成模型回复、工具审批暂停/恢复和指标检查，并在当前 staging 数据库中创建验收 Agent、Environment、Session 和事件记录。

前置条件：

- `TMA_BASE_URL` 指向隔离的 staging Server。
- `.env` 或 shell 中配置了可用的 `TMA_LLM_PROVIDER`、`TMA_LLM_MODEL` 和对应 API Key。
- staging Server 与 PostgreSQL 已启动，`bin/tma` 可构建。

非中断性验收：

```bash
make verify-agent-core-staging
```

优雅重启恢复演练：

```bash
make verify-agent-core-staging-restart
```

该入口会在审批已经持久化后向 staging Server 发送 `SIGTERM`，通过 `TMA_AGENT_CORE_STAGING_START_SCRIPT` 重新启动服务，再批准工具调用并验证 continuation。默认依赖 `.tma-agent-core-staging.pid`、`screen` 和 `/tmp/tma-agent-core-staging.sh`。

进程崩溃恢复演练：

```bash
make verify-agent-core-staging-crash
```

该入口会在模型请求和未知幂等性的工具执行期间分别向 staging Server 发送 `SIGKILL`。模型 attempt 必须被标记为 abandoned 后重试；已 started 的未知幂等性工具不得重放，必须恢复为 `indeterminate`。

基础设施恢复演练：

```bash
make verify-agent-core-staging-infrastructure
```

该入口会暂时停止 `TMA_AGENT_CORE_STAGING_POSTGRES_CONTAINER`（默认 `tma-postgres`），还会对主 Server 发送 `SIGSTOP`，在 `TMA_AGENT_CORE_COMPETITOR_BASE_URL`（默认 `http://localhost:18089`）启动竞争实例以验证 lease fencing。脚本有退出清理，但执行期间数据库和主 Server 会短暂不可用。

重启、崩溃和基础设施入口只能在隔离 staging 环境执行，不能指向生产环境、共享开发数据库或承载其他验收任务的 Server。可以用 `TMA_AGENT_CORE_CRASH_MODE=model|tool|all` 和 `TMA_AGENT_CORE_INFRASTRUCTURE_MODE=database|fencing|all` 缩小演练范围。

如果要手动验证 Context Builder 历史截断，可以在启动服务前设置：

```bash
export TMA_DEFAULT_CONTEXT_WINDOW_TOKENS=128
make run
```

然后连续发送多轮消息，查看 `runtime.llm_request` 事件中的 `history_count`、`omitted_history_count`、`estimated_token_count` 和 `context_truncated`。Context Builder 当前会使用总窗口的 60%，所以上例实际预算约为 76 token。

手动写入 Session summary：

```bash
bin/tma session summary upsert \
  --session sesn_000001 \
  --text "Earlier conversation established repo layout and provider settings." \
  --until 12
```

查看 summary：

```bash
bin/tma session summary get --session sesn_000001
```

写入 summary 时会产生 `session.status_compacting` 和 `session.status_idle` 事件。

Session 级 runtime settings：

```bash
bin/tma session runtime get --session sesn_000001
bin/tma session runtime update --session sesn_000001 --intervention-mode request_approval
bin/tma session runtime update --session sesn_000001 --intervention-mode approve_for_me
bin/tma session runtime update --session sesn_000001 --intervention-mode full_access
```

Tool intervention 决策：

```bash
bin/tma session attach --session sesn_000001 --after 0

bin/tma session intervention list --session sesn_000001 --status pending
bin/tma session intervention approve --session sesn_000001 --turn turn_000003 --call call_edit --reason "looks safe"
bin/tma session intervention reject --session sesn_000001 --turn turn_000003 --call call_edit --reason "not this time"
```

Agent Core 恢复未知幂等性的 started 工具时不会自动重放，也不会直接让模型猜测结果。Turn 会进入 `waiting_human`，pending intervention 的 `request.purpose` 为 `tool_reconciliation`。先检查外部系统、日志或事务记录，再提交核对结果：

```bash
bin/tma session intervention list --session sesn_000001 --status pending

bin/tma session intervention reconcile \
  --session sesn_000001 \
  --turn turn_000003 \
  --call tool_reconciliation:call_run \
  --outcome executed \
  --summary "Transaction tx-42 exists and matches the requested payload." \
  --evidence "audit:tx-42"
```

`--outcome` 只接受：

- `executed`：人工确认副作用已经完成；journal 转为 succeeded，模型收到成功结果。
- `not_executed`：人工确认副作用没有发生；journal 转为 failed + retryable，模型可以选择重新调用。
- `compensated`：不确定副作用已经回滚或补偿；journal 转为 failed + non-retryable，模型不得把原调用当作成功。

Workbench 会把同一请求显示为结构化表单。跳过或取消核对不会把调用转换成成功，原 `indeterminate` 结果继续交给模型安全收敛。`--summary` 和 `--evidence` 会进入 durable state、审计事件和后续模型上下文，只能填写非敏感核对信息，不能包含 Token、密码或完整凭据。

## 3. LLM Provider 管理命令

Provider 管理只保存 API Key 的环境变量名，不保存真实 API Key。

查看 Provider：

```bash
bin/tma provider list
bin/tma provider get --id fake
```

创建或更新一个 OpenAI 协议 Provider：

```bash
bin/tma provider create \
  --id volcengine-agent-plan \
  --type openai \
  --base-url https://ark.cn-beijing.volces.com/api/plan/v3 \
  --api-key-env TMA_LLM_API_KEY
```

```bash
bin/tma provider update \
  --id volcengine-agent-plan \
  --base-url https://ark.cn-beijing.volces.com/api/plan/v3 \
  --api-key-env TMA_LLM_API_KEY
```

启用 / 禁用：

```bash
bin/tma provider disable --id volcengine-agent-plan
bin/tma provider enable --id volcengine-agent-plan
```

被禁用的 Provider 不能再用于创建新的 Agent；已经绑定该 Provider 的 Session 在执行 turn 时也会失败并回到 `idle`。

## 4. LLM Model 管理命令

模型配置保存总上下文窗口，不保存输入上下文预算。Context Builder 固定使用总窗口的 60%。

```bash
bin/tma model list
bin/tma model list --provider fake
```

新增或更新模型窗口：

```bash
bin/tma model upsert \
  --provider volcengine-agent-plan \
  --model doubao-seed-2.0-pro \
  --context-window 128000
```

没有显式配置的模型会按默认 `128000` 处理。

## 5. Agent 配置版本命令

查看 Agent 当前配置：

```bash
bin/tma agent get --id agt_000001
```

查看 Agent 历史配置版本：

```bash
bin/tma agent config list --agent agt_000001
```

创建新的 AgentConfigVersion：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --llm-provider volcengine-agent-plan \
  --llm-model doubao-seed-2.0-pro \
  --system "You are a concise coding agent."
```

更新后，新建 Session 会绑定新的 `agent_config_version`；已经存在的 Session 继续绑定创建时的旧版本。

如果确实要让某个已有 idle Session 后续使用 Agent 当前最新配置，需要显式升级：

```bash
bin/tma session config upgrade --session sesn_000001 --to-current
```

升级会写入 `session.config_updated` 事件；running / waiting approval 中的 Session 不允许升级。

## 6. LLM Usage 查询命令

查看某个 Session 的 token usage 总量和每轮明细：

```bash
bin/tma usage list --session sesn_000001
```

返回结构包含：

```json
{
  "session_id": "sesn_000001",
  "summary": {
    "record_count": 1,
    "input_tokens": 10,
    "output_tokens": 5,
    "total_tokens": 15,
    "cached_input_tokens": 0,
    "reasoning_tokens": 0,
    "latency_ms": 120
  },
  "records": []
}
```

`summary` 是该 Session 下所有 usage 明细的合计；`records` 是每个 turn 的 usage 明细。

查看跨 Session 的 usage 聚合：

```bash
bin/tma usage summary
```

默认按 `provider_id + model` 分组。也可以指定分组和过滤条件：

```bash
bin/tma usage summary --group-by provider
bin/tma usage summary --group-by model
bin/tma usage summary --provider volcengine-agent-plan
bin/tma usage summary --model doubao-seed-2.0-pro
bin/tma usage summary --status failed
bin/tma usage summary --from 2026-07-07T00:00:00+08:00 --to 2026-07-08T00:00:00+08:00
```

## 7. 自动验收

它会自动创建 Agent / Environment / Session，发送一条消息，并检查事件中包含：

- `session.status_running`
- `user.message`
- `runtime.started`
- `runtime.thinking`
- `runtime.llm_request`
- `runtime.llm_response`
- `runtime.completed`

如果要验证当前 `.env` 中配置的真实 LLM Provider，使用：

```bash
make verify-llm-provider
```

它会读取当前 `.env` / shell 环境中的：

```text
TMA_LLM_PROVIDER
TMA_LLM_PROVIDER_TYPE
TMA_LLM_MODEL
TMA_LLM_BASE_URL
TMA_LLM_API_KEY_ENV
TMA_LLM_API_KEY
TMA_LLM_MAX_ATTEMPTS
TMA_LLM_RETRY_BASE_DELAY_MS
```

启动服务时会把默认 Provider 写入 `llm_providers`，数据库只保存 `TMA_LLM_API_KEY_ENV` 指向的变量名，不保存真实 API Key。然后脚本创建 Agent / Environment / Session，发送一条消息，并检查真实模型链路返回了非空 `agent.message`。如果 Provider 支持流式输出，结果会显示 `runtime.llm_response` 聚合的 chunk 数量和 TTFT。

如果要验证当前 `.env` 中配置的真实 LLM Provider 的 tool approval 流程，先启动服务，再运行：

```bash
scripts/verify_intervention_flow.sh
```

默认模式会自动创建 Agent / Environment / Session，设置 `intervention_mode=request_approval`，发送 `APPROVAL_TEST_RUN_COMMAND`，等待 pending approval，然后打印 `session attach`、直接 approve、直接 reject 三组命令供手动测试。

也可以自动走 approve 或 reject 分支：

```bash
TMA_APPROVAL_TEST_DECISION=approve scripts/verify_intervention_flow.sh
TMA_APPROVAL_TEST_DECISION=reject scripts/verify_intervention_flow.sh
```

可用环境变量：

```text
TMA_BASE_URL
TMA_CLI
TMA_DOTENV_PATH
TMA_APPROVAL_TEST_WAIT_SECONDS
TMA_APPROVAL_TEST_MESSAGE
TMA_APPROVAL_TEST_DECISION=manual|approve|reject
```

### Web Search / Crawl 验收

验证 `web_search` / `web_crawl` 工具注入、执行、结果回传：

```bash
make verify-web-search-crawl
```

该目标会自动完成：

- 构建 `bin/tma-server` 和 `bin/tma`
- 启动 Postgres 并执行迁移
- 启动 Docker Compose 中的 SearXNG，并探测 `/healthz` 和 `format=json`
- 启动本地 HTML fixture，验证 Agent 调用 `web_crawl`
- 启动本地 SearXNG-compatible mock，验证 Agent 调用 `web_search`
- 检查事件中出现 `runtime.tool_call`、`runtime.tool_result`、`agent.message` 和 `session.status_idle`

通过时会输出类似：

```text
Web search/crawl verification passed
session_id=sesn_000055
crawl_turn_id=turn_000001
search_turn_id=turn_000002
```

本地服务默认端口：

```text
TMA server:           http://localhost:18083
HTML fixture:         http://127.0.0.1:18084
SearXNG mock:         http://127.0.0.1:18085
Docker SearXNG:       http://localhost:8180
```

如果端口被占用，可覆盖：

```bash
TMA_VERIFY_WEB_HTTP_ADDR=:19083 \
TMA_VERIFY_WEB_BASE_URL=http://localhost:19083 \
TMA_VERIFY_WEB_FIXTURE_ADDR=127.0.0.1:19084 \
TMA_VERIFY_WEB_MOCK_SEARXNG_ADDR=127.0.0.1:19085 \
make verify-web-search-crawl
```

- `agent.message`
- `session.status_idle`

同时校验 `agent.message` 文本为：

```text
Agent runtime received: agent runtime verify
```

并校验 `agent.message` payload 中包含当前协议版本：

```text
protocol_version = tma.agent_loop.message.v1
```

---

## 3. CLI 健康检查

终端 B：

```bash
cd "/Users/viito/Desktop/Harness 企业级定制化实现/tiggy-manage-agent"
bin/tma health
```

预期包含：

```json
{
  "service": "tiggy-manage-agent",
  "status": "ok"
}
```

---

## 4. 创建 Agent

```bash
bin/tma agent create \
  --name "Code Assistant" \
  --model gpt-4o \
  --system "You are a coding agent."
```

空数据库首次运行时，通常返回：

```text
id = agt_000001
```

---

## 5. 创建 Environment

```bash
bin/tma env create --name default-cloud
```

空数据库首次运行时，通常返回：

```text
id = env_000001
```

自定义 config 示例：

```bash
bin/tma env create \
  --name github-cloud \
  --config '{"type":"cloud","networking":{"type":"limited","allowed_hosts":["api.github.com"]}}'
```

---

## 6. 创建 Session

```bash
bin/tma session create \
  --agent agt_000001 \
  --env env_000001 \
  --title "First TMA task"
```

空数据库首次运行时，通常返回：

```text
id = sesn_000001
status = idle
```

查询 Session：

```bash
bin/tma session get --session sesn_000001
```

---

## 7. 查看历史 Events

```bash
bin/tma event list --session sesn_000001 --after 0
```

创建 Session 后至少应看到两条状态事件：

```text
seq=1 session.status_provisioning
seq=2 session.status_idle
```

只看 `seq > 1` 的事件：

```bash
bin/tma event list --session sesn_000001 --after 1
```

---

## 8. 发送消息

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "hello from cli"
```

当前异步 WorkerRunner 会先返回两类事件：

```text
session.status_running
user.message
```

两条事件的 `payload.turn_id` 应相同，例如：

```text
turn_id = turn_000001
```

如果这是创建 Session 后的第一条消息，seq 通常是：

```text
seq=3 session.status_running
seq=4 user.message
```

后台 AgentRuntime 完成后会追加：

```text
seq=5 runtime.started
seq=6 runtime.thinking
seq=7 runtime.llm_request
seq=8 runtime.llm_response
seq=9 runtime.completed
seq=10 agent.message
seq=11 session.status_idle
```

`agent.message` 和后续 `session.status_idle` 也应带同一个 `payload.turn_id`。

可选：查看 `session_turns` 中的生命周期状态：

```bash
docker compose exec -T postgres psql -U tma -d tma \
  -c "SELECT session_id, id, status, started_at, ended_at FROM session_turns ORDER BY started_at DESC LIMIT 5;"
```

完成的 turn 应显示：

```text
status = completed
```

Runner 启动或执行失败时会追加：

```text
session.status_idle
```

该事件 payload 会带 `last_turn_status=failed` 和 `reason`。同时 `session_turns.status` 标为 `failed`，`error_message` 保存失败原因，Session 回到 `idle`，可以继续发送下一条消息。

再次查看历史：

```bash
bin/tma event list --session sesn_000001 --after 2
```

---

## 9. 测试 SSE 历史续传

`event stream` 是阻塞命令，会一直监听 SSE。用 `Ctrl+C` 退出。

```bash
bin/tma event stream --session sesn_000001 --after 0
```

预期立即补发历史事件。已支持可读格式的事件会显示为短文本，例如：

```text
status: provisioning

status: idle

: stream ready
```

尚未专门格式化的事件仍会按原始 SSE 输出，便于调试新事件类型。

测试从 `seq=2` 后续传：

```bash
bin/tma event stream --session sesn_000001 --after 2
```

预期不会再补发 `seq=1` 和 `seq=2`，只返回 `seq > 2` 的历史事件，然后等待后续新事件。

---

## 10. 测试 SSE 实时推送

终端 B 先启动监听：

```bash
bin/tma event stream --session sesn_000001 --after 2
```

终端 C 发送事件：

```bash
cd "/Users/viito/Desktop/Harness 企业级定制化实现/tiggy-manage-agent"
bin/tma event send \
  --session sesn_000001 \
  --text "second message"
```

终端 B 应实时看到新事件：

```text
status: running (turn turn_000003)

user> second message
```

如果当前 Session 已设置：

```bash
bin/tma session runtime update --session sesn_000001 --intervention-mode request_approval
```

`bin/tma event stream --session sesn_000001 --after 0` 当前会把常见事件格式化为更易读的提示，而不是原始 SSE JSON。常见输出包括：

```text
status: running (turn turn_000003)

user> edit README

agent> done
```

持久化 Event Stream 不输出模型文本分片；实时打字效果由 Workbench 单独消费 `/v2/sessions/{session_id}/live/stream`。

当模型命中敏感工具时，审批事件会显示为：

```text
approval required
  seq: 12
  turn: turn_000003
  call: call_edit
  tool: default_edit_file
  mode: request_approval
  message: Tool call requires approval before execution.
  policy: optional
```

当前这一步只验证“可见性”和“拦截”：

- `request_approval`：敏感工具不执行，事件流中出现 `approval required`
- `approve_for_me`：会出现 `runtime.tool_intervention_approved`，随后继续执行
- `full_access`：直接执行，不出现审批提示

交互式会话入口：

```bash
bin/tma session attach --session sesn_000001 --after 0
```

查看交互式输入说明：

```bash
bin/tma session attach --help
```

`session attach` 会持续监听同一个 session 的 SSE，也可以直接在当前终端输入消息发送 `user.message`。
启动后会打印简短提示：

```text
attached to sesn_000001
type a message, /say MESSAGE, /interrupt, or /quit
approval: a=approve, r REASON=reject, s=skip
```

启动时会先查询当前 `status=pending` 的审批记录；即使使用了较新的 `--after` 跳过历史 `runtime.tool_intervention_required` 事件，只要 Store 里仍有 pending approval，也会恢复成本地 prompt：

```text
pending approval recovered: default_edit_file call=call_edit
approval action: a=approve, r [reason]=reject, s=skip
```

常用输入：

```text
hello agent
/say hello agent
/interrupt
/quit
```

遇到 `approval required` 时，会在当前命令中提示：

```text
approval action: a=approve, r [reason]=reject, s=skip
```

- `a` / `approve`：调用 approve API，工具执行后 event stream 继续输出后续事件
- `r not safe` / `reject not safe`：带可选拒绝原因 reject，随后 fail 当前 turn 并回到 idle
- `s` / `skip`：本次不处理，本地 prompt 消失，pending 记录继续保留
- `/say MESSAGE`：在本地已有 pending prompt 时仍强制发送一条新 user message；服务端会按 waiting approval 规则返回提醒并重新发送审批事件

发送成功后会显示：

```text
message sent
```

如果发送消息、interrupt 或审批 API 返回错误，`session attach` 会在终端打印 `... failed: ...`，但不会退出整个交互式会话；可以继续输入下一条消息或下一次审批决策。SSE 连接本身断开时，命令仍会退出并返回错误。

查看 pending 审批记录：

```bash
bin/tma session intervention list \
  --session sesn_000001 \
  --status pending
```

批准或拒绝某个 pending call：

```bash
bin/tma session intervention approve \
  --session sesn_000001 \
  --turn turn_000003 \
  --call call_edit \
  --reason "looks safe"

bin/tma session intervention reject \
  --session sesn_000001 \
  --turn turn_000003 \
  --call call_edit \
  --reason "not this time"
```

批准后应写出 `runtime.tool_intervention_approved` 事件，并消费保存的 tool call 追加 `runtime.tool_result`。拒绝后应写出 `runtime.tool_intervention_rejected` 事件，不执行工具。

在 `bin/tma event stream` 中，批准、拒绝和工具结果会分别显示为 `approval approved`、`approval rejected`、`tool result`。如果工具结果带有 artifact，`tool result` 下会显示 artifact id、名称、类型和 TMA 代理下载路径；如果 artifact 记录失败，会显示 `artifact error`。

细粒度 approve/reject 命令仍保留给脚本和调试使用：`request_approval` 会把 turn 标记为 `waiting_approval`，不会生成 pending 后的临时 `agent.message`。approve 会执行被批准的工具并回写结果事件；如果 pending 记录中带有 continuation messages，还会把 tool result 送回 LLM，并继续最多 4 轮 tool loop。续跑中再次遇到敏感工具会再次 pending / waiting approval；模型返回普通文本时生成最终 `agent.message`，并让 session 回到 `idle`。reject 会写 rejected 事件并 fail turn，不会把拒绝原因喂回模型继续推理。长期未审批时记录保持 `pending` / `waiting_approval`，不会自动 approve / reject / expire。

如果 session 正在 `waiting_approval`，用户又发送新的 `user.message`，服务端不会开启新 turn；它会返回 `202 Accepted`，写一条提醒 `agent.message`，并重新写出 pending 的 `runtime.tool_intervention_required`，方便 CLI/UI 再次展示审批信息。

approve 后的 continuation LLM 调用会进入 usage 记录，可用下面命令确认：

```bash
bin/tma usage list --session sesn_000001
```

这两条事件的 `data` 中应包含相同的 `payload.turn_id`。

稍后继续看到：

```text
event: agent.message
event: session.status_idle
```

后两条事件也应保持同一个 `payload.turn_id`。

---

## 11. Object Ref / Session Artifact

这一节覆盖对象引用、Session artifact、TMA 代理下载和安全删除。文件字节由 objectstore 后端保存，Postgres 只保存 metadata。

如果只想验证 metadata 流，可以先创建一个 object ref，表示对象存储中已经存在或即将存在的对象：

```bash
bin/tma object create \
  --bucket tma-artifacts \
  --key wksp_default/sesn_000001/output.txt \
  --content-type text/plain \
  --size 42 \
  --sha256 abc123 \
  --metadata '{"source":"manual-test"}'
```

查看 object ref：

```bash
bin/tma object get --id obj_000001
```

把 object ref 挂到 Session artifact：

```bash
bin/tma session artifact create \
  --session sesn_000001 \
  --object obj_000001 \
  --turn turn_000001 \
  --call call_write \
  --name output.txt \
  --type file \
  --metadata '{"preview":"hello"}'
```

列出 Session artifacts：

```bash
bin/tma session artifact list --session sesn_000001
bin/tma session artifact list --session sesn_000001 --json
```

预期：

- `object_ref` 中只有 bucket / key / checksum / size / metadata 等引用信息。
- `session artifact` 引用 `object_ref_id`，并记录 session / turn / call 关系。
- 默认 `session artifact list` 输出人类可读摘要；加 `--json` 输出原始 API 响应，方便脚本处理。
- Postgres 不保存文件二进制。

上传并通过 TMA 代理下载真实内容：

```bash
printf 'hello artifact\n' >/tmp/tma-artifact.txt

curl -sS -X POST \
  -F file=@/tmp/tma-artifact.txt \
  -F turn_id=turn_000001 \
  -F tool_call_id=call_manual \
  -F artifact_type=file \
  http://localhost:8080/v1/sessions/sesn_000001/artifacts/upload

bin/tma session artifact list --session sesn_000001
bin/tma session artifact download --session sesn_000001 --artifact art_000001 --output /tmp/tma-artifact.downloaded.txt
```

通过工具执行产生的输出 JSON 也会尽力记录为 Session artifact。可在 `runtime.tool_result` 的 JSON 中查看 `artifacts` 数组；下载路径仍是 `/v1/sessions/{session_id}/artifacts/{artifact_id}/download`，不暴露底层 objectstore 地址。

验证 Inspector 页面是否包含 artifact 下载和复制命令入口：

```bash
make verify-inspector-ui
```

该验收会启动真实 TMA server，读取 `/inspector` HTML，并校验页面包含 `Download`、`Copy CLI`、`data-copy`、`bin/tma session artifact download --session ...` 等关键内容。它不依赖浏览器插件，适合作为 Inspector UI 基础交互的轻量回归。

浏览器 smoke 可以在静态验收通过后手动跑：

```bash
make verify-inspector-browser-smoke
```

该验收会启动真实 TMA server 和 headless Chrome，自动造数并实际点击：

- Recent Traces `Load more`
- Session filter 后的 Span Search `Load more`
- trace card 加载 Timeline / Artifacts
- Timeline 的 tool result 截断提示
- 大文本 artifact `Preview` 的 10KB 截断提示
- 页面 console error / unhandled rejection 计数

也可以手动启动页面点检：

```bash
TMA_HTTP_ADDR=:18089 \
TMA_DATABASE_URL='postgres://tma:tma@localhost:5432/tma?sslmode=disable' \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
bin/tma-server
```

然后打开：

```text
http://localhost:18089/inspector
```

最小点检路径：

- 页面标题为 `TMA Inspector`，模块脚本来自 `/inspector/assets/app.js`
- `Recent Traces` 有数据时，点击 trace 卡片后应填充 Trace ID / Session / Turn，并渲染 Waterfall、Spans、Timeline 和 Raw JSON
- `Recent Traces` / `Span Search` 返回超过第一页时应显示 `Load more`，点击后追加下一页而不是覆盖当前列表
- 在 `Session` 输入框填入 session id 后，点 `Filter by Session` 或按 Enter，`Recent Traces` 和 `Span Search` 应只显示该 session 的结果；点 `Clear` 恢复全局 catalog
- `Span Search` 有数据时，点击 span 卡片后应定位到对应 span，URL hash 包含 `trace` 和 `span`
- 点击文本/JSON artifact 的 `Preview` 时，页面只 inline 展示前 10KB 左右内容；完整内容用 `Download` 或 `Copy CLI` 获取
- 大工具结果对应的 `runtime.tool_result` data 应包含 `context.content_truncated` / `context.state_truncated` 标记，而不是把完整长文本和大 state 直接塞进事件
- 浏览器 console 不应出现 error

Trace / span index 的 focused 回归：

```bash
go test ./internal/managedagents ./internal/observability ./internal/httpapi -count=1
```

其中 `TestGetSessionTraceProjectsTurnTimeline` 会覆盖：

- `GET /v1/sessions/{id}/trace` 仍从 `session_events` 投影完整 trace
- 生成 trace 后写入 `trace_indexes` / `trace_span_indexes`
- `/v1/traces` 和 `/v1/spans` 可从索引返回 catalog / span search，并包含 `limit`、`offset`、`next_offset`、`has_more`
- `runtime.span_started` / `runtime.span_event` / `runtime.span_ended` 会被投影为原生 lifecycle span
- `PruneTraceIndexes(before, limit)` 会删除过期 trace index，并级联删除 span index

手动验证 Postgres 索引表：

```bash
bin/tma trace show --session sesn_000001 --turn turn_000001

docker compose exec -T postgres psql -U tma -d tma \
  -c "SELECT trace_id, session_id, turn_id, span_count, updated_at FROM trace_indexes ORDER BY updated_at DESC LIMIT 5;"

docker compose exec -T postgres psql -U tma -d tma \
  -c "SELECT trace_id, span_id, kind, status, duration_ms, critical FROM trace_span_indexes ORDER BY start_time DESC LIMIT 10;"
```

注意：`session_events` 仍是事实源；索引用于 Inspector / span search / retention，索引缺失时 HTTP 查询会回退投影并回填。

---

## 12. 使用 curl 验证 SSE

```bash
curl -N "http://localhost:8080/v1/sessions/sesn_000001/events/stream?after_seq=0"
```

另一个终端发送事件：

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "hello from curl stream"
```

---

## 13. 测试 Interrupt

当前版本使用异步 WorkerRunner。发送 `user.message` 后，Session 会停留在 `running`，这时可以中断。

终端 B 发送一条消息：

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "interrupt me"
```

立刻查看 Session：

```bash
bin/tma session get --session sesn_000001
```

通常可以看到：

```text
status = running
```

马上发送中断：

```bash
bin/tma event interrupt --session sesn_000001
```

预期返回：

```text
user.interrupt
session.status_interrupting
session.status_idle
```

这三条事件的 `payload.turn_id` 应与刚才的 `user.message` 一致。

可选：再次查询 `session_turns`，中断的 turn 应显示：

```text
status = interrupted
```

如果中断太晚，Session 可能已经被后台 AgentRuntime 带回 `idle`，这时会返回：

```text
user.interrupt requires running session
```

---

## 14. Archive Session

```bash
bin/tma session archive --session sesn_000001
```

预期：

```text
status = terminated
```

再次查看历史事件：

```bash
bin/tma event list --session sesn_000001 --after 0
```

应新增一条终止状态事件：

```text
session.status_terminated
```

被 archive 的 Session 不应再接受新事件：

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "should fail"
```

预期返回冲突错误。

---

## 15. Delete Session

```bash
bin/tma session delete --session sesn_000001
```

预期：

```text
deleted session sesn_000001
```

删除后查询：

```bash
bin/tma session get --session sesn_000001
```

预期返回 `not found`。

---

## 16. 数据库验证

数据库模式不需要本机安装 `psql`。迁移命令会使用 Postgres 容器里的 `psql`。

启动 Postgres：

```bash
make db-up
```

应用初始 schema：

```bash
make migrate-up
```

验证全部租户表的强制 workspace/owner RLS，包括 MCP Registry server 和不可变版本：

```bash
TMA_RUN_POSTGRES_TESTS=1 \
TMA_DATABASE_URL='postgres://tma:tma@localhost:5432/tma?sslmode=disable' \
go test ./internal/managedagents \
  -run TestPostgresTenantTablesForceWorkspaceRLS \
  -count=1 -v
```

测试会临时创建非 superuser runtime role，验证生产启动角色检查、无 scope 查询为零、不同 workspace/owner 相互不可见、`WITH CHECK` 拒绝跨 scope 写入，并在结束后删除测试角色和数据。覆盖表为 `agents`、`agent_config_versions`、`environments`、`managed_environment_variables`、`mcp_registry_servers`、`mcp_registry_server_versions`、`object_refs`、`session_artifacts` 和 `sessions`。

启动服务：

```bash
make run
```

也可以显式指定数据库地址：

```bash
TMA_DATABASE_URL="postgres://tma:tma@localhost:5432/tma?sslmode=disable" make run
```

看到以下日志表示已进入 Postgres 模式：

```text
using postgres store
```

然后重复第 3 到第 14 节的 CLI 命令。

注意：Postgres 会保留历史数据，所以返回的 ID 不一定是 `agt_000001`。以后续命令实际返回的 ID 为准。

查看数据库日志：

```bash
make db-logs
```

停止数据库：

```bash
make db-down
```

---

## 17. Onlyboxes 验证

### 17.1 Sandbox doctor

先检查本地 cloud_sandbox 前置条件：

```bash
bin/tma sandbox doctor
```

`sandbox doctor` 会读取当前目录 `.env` 中的 `TMA_CLOUD_SANDBOX_ROOT` 和 `TMA_CLOUD_SANDBOX_IMAGE`，并检查：

- runtime 是否会落到 `cloud_sandbox`。
- workspace base 是否可创建且是目录；实际 `/workspace` 只挂载其下按 workspace、owner、session 派生的隔离子目录。
- `docker` 命令是否可找到。
- Docker daemon 是否可连接。
- sandbox 镜像是否已存在于本地；缺失时默认自动 `docker pull`。

它不会自动启动 Docker。若只是想验证 Docker 链路，可以临时指定已有镜像：

```bash
bin/tma sandbox doctor --image busybox:latest
```

如果只想做无 pull 的纯检查：

```bash
bin/tma sandbox doctor --pull=false
```

### 17.2 Provider 级验证

这一节只验证 `OnlyboxesProvider` 的真实 Docker 执行边界，不需要启动 TMA server，也不依赖 LLM。

默认使用项目内置镜像名：

```bash
make verify-onlyboxes
```

如果本地还没有 `coolfan1024/onlyboxes-runtime:default` 镜像，可以先用一个已有的 shell 镜像验证挂载和 workdir 链路：

```bash
TMA_ONLYBOXES_TEST_IMAGE=busybox:latest make verify-onlyboxes
```

预期测试会：

- 创建临时 workspace。
- 在 host 写入 `marker.txt`。
- 用 Docker 把 workspace 挂载到容器 `/workspace`。
- 如果配置了 session data root，还会把 session 数据目录挂到 `/mnt/data`。
- 在容器内读取 `marker.txt` 并写出 `out.txt`。
- 回到 host 检查 `out.txt` 内容。

通过表示 `cloud_sandbox` provider 的基本命令执行、workspace 挂载、session 数据挂载和输出回写链路可用。

### 17.3 Session 级验证

这一节验证完整链路：

```text
TMA server
  -> fake LLM 触发 default_run_command
  -> AgentRuntime tool loop
  -> SessionProviderResolver 默认选择 cloud_sandbox
  -> OnlyboxesProvider
  -> runtime.tool_call / runtime.tool_result / agent.message
```

默认使用项目内置镜像名：

```bash
make verify-onlyboxes-session
```

如果本地还没有 `coolfan1024/onlyboxes-runtime:default` 镜像，可以先用 `busybox` 验证完整 session 链路：

```bash
TMA_ONLYBOXES_TEST_IMAGE=busybox:latest make verify-onlyboxes-session
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 用 `TMA_TOOL_RUNTIME=cloud_sandbox` 启动临时 TMA server。
- 创建 agent / environment / session。
- 把 session `intervention_mode` 设置为 `approve_for_me`，避免工具审批挂起。
- 发送 `tma.verify_tool_call` 消息，让 fake LLM 发起工具调用。
- 校验事件历史中出现 `runtime.tool_call`、`runtime.tool_result`、`agent.message`，且 tool result 同时包含 `/workspace` 和 `tma-session-tool-ok`。

通过表示 session 级工具执行已经真实走到 Onlyboxes provider。

### 17.4 cloud_sandbox 外网审批验证

这一节验证 `cloud_sandbox_allow_network` 和 `intervention_mode` 的组合行为：

```bash
make verify-network-approval
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 用 `TMA_TOOL_RUNTIME=cloud_sandbox` 启动临时 TMA server。
- 让 fake LLM 通过 `tma.verify_network_download` 触发 `default_execute_code`，执行 Python `urllib.request.urlopen(...)` 下载。
- 验证 `request_approval + cloud_sandbox_allow_network=true` 会产生 pending intervention，reason 为 `network_access`，批准后继续执行。
- 验证 `approve_for_me + cloud_sandbox_allow_network=true` 会写出 auto approval 事件并执行成功。
- 验证 `full_access + cloud_sandbox_allow_network=true` 不写审批事件并直接执行成功。
- 验证 `full_access + cloud_sandbox_allow_network=false` 不写 `network_access` 审批事件，且 Python 下载不会成功。

通过表示“沙箱具备外网能力”和“访问外网进入审批策略”两层语义已经在真实 AgentRuntime 链路里闭环。

### 17.5 cloud_sandbox 上传数据验证

这一节验证用户上传文件进入 session sandbox 数据目录，并且同一 session 的 `/mnt/data` 能跨多次工具调用保留中间文件：

```bash
make verify-onlyboxes-upload-data
```

如果本地还没有默认镜像，也可以用 `busybox` 先验证链路：

```bash
TMA_ONLYBOXES_TEST_IMAGE=busybox:latest make verify-onlyboxes-upload-data
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 启动临时 TMA server。
- 创建 agent / environment / session。
- 通过 `POST /v1/sessions/{session_id}/artifacts/upload` 上传一个 `file` artifact。
- 发送 `tma.verify_uploaded_file_seed`，让 fake LLM 触发 sandbox 命令读取 `/workspace/uploads/{artifact_id}/input.txt`，并把临时状态写入 `/mnt/data/state.txt`。
- 再发送 `tma.verify_uploaded_file_read`，确认下一次工具调用仍能读取上传文件和上一轮写入的 `/mnt/data/state.txt`。

通过表示上传接口、对象存储、session artifact、`OnlyboxesProvider` 同步逻辑和 session 级 `/mnt/data` 持久目录已经形成最小闭环。

### 17.6 cloud_sandbox 输出回收验证

这一节验证 `default_run_command` / `default_execute_code` 的 `output_paths` 能把 `/mnt/data` 里生成的文件回收到 session artifact：

```bash
make verify-onlyboxes-export-artifact
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 启动临时 TMA server。
- 创建 agent / environment / session。
- 通过 `POST /v1/sessions/{session_id}/artifacts/upload` 上传一个输入文件。
- 发送 `tma.verify_uploaded_file_export`，让 fake LLM 触发 sandbox 命令在 `/workspace/outputs/export.txt` 生成最终产物，并通过 `output_paths` 请求保存。
- 从 `runtime.tool_result` 的 `artifacts` 中取出生成的 `file` artifact。
- 通过 `bin/tma session artifact download` 下载该 artifact，校验内容同时包含上传文件标记和生成文件标记。

通过表示上传文件 -> `/mnt/data` 加工 -> `output_paths` 导出 -> session artifact 下载 已经形成最小闭环。

### 17.7 Browser Extension 验证

这一节验证内置 BrowserRuntime 已移除，`browser.*` 可以由 Worker Process Plugin 提供，并验证 Browser Gateway 与 Workbench 扩展的静态契约。

运行验收：

```bash
make verify-browser-tools
```

该目标会执行：

- Go Registry 与 Process Plugin 命名空间测试。
- Browser Tool Plugin Manifest 测试。
- Browser Gateway HMAC 与隔离键测试。
- Workbench 插件 API 与完整前端测试。

完整运行时验收需要启动 Browser Gateway 和带插件的 Worker，再通过 Ingress 访问 Workbench：

```bash
deploy/docker/deploy.sh --with-browser
```

Agent 配置使用 `{"tools":["browser"],"runtime":"local_system"}`。Workbench 的“浏览器”插件页面选择同一 TMA Session 后，应能看到并操作 Agent 打开的页面。

### 17.8 S3-compatible 对象存储验证

这一节验证 `TMA_OBJECT_STORAGE_PROVIDER=s3` 的真实对象存储闭环。运行前需要先启动 RustFS / MinIO / S3-compatible 服务，并确保 bucket 已存在或服务端允许自动写入该 bucket。

默认使用：

```env
TMA_OBJECT_STORAGE_ENDPOINT=http://localhost:9000
TMA_OBJECT_STORAGE_REGION=local
TMA_OBJECT_STORAGE_BUCKET=tma-artifacts
TMA_OBJECT_STORAGE_ACCESS_KEY=tma
TMA_OBJECT_STORAGE_SECRET_KEY=tma-secret
TMA_OBJECT_STORAGE_USE_PATH_STYLE=true
```

执行：

```bash
make verify-objectstore-s3
```

也可以覆盖 endpoint / bucket / credentials：

```bash
TMA_OBJECT_STORAGE_ENDPOINT=http://localhost:9000 \
TMA_OBJECT_STORAGE_BUCKET=tma-artifacts \
TMA_OBJECT_STORAGE_ACCESS_KEY=tma \
TMA_OBJECT_STORAGE_SECRET_KEY=tma-secret \
make verify-objectstore-s3
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 用 `TMA_OBJECT_STORAGE_PROVIDER=s3` 启动临时 TMA server。
- 创建 agent / environment / session。
- 通过 `POST /v1/sessions/{session_id}/artifacts/upload` 上传一个文件 artifact。
- 通过 `bin/tma session artifact download` 从 TMA 代理下载 artifact。
- 比对上传和下载文件内容一致。
- 删除验证产生的 session artifact 和 object ref metadata。

通过表示 HTTP 上传、真实 S3-compatible PutObject、TMA 代理下载、真实 GetObject 和 metadata 管道已经形成最小闭环。当前删除步骤只清理 TMA metadata；底层对象物理回收后续需要单独设计 GC / retention 策略。

### 17.9 Worker-backed local_system 验证

这一节验证完整链路：

```text
TMA server
  -> fake LLM 触发 default_run_command
  -> AgentRuntime tool loop
  -> local_system worker capability match
  -> WorkerBackedProvider 入队 tma.work.v1 tool_execution
  -> tma-worker outbound poll / ack / result
  -> runtime.tool_result / agent.message
```

运行：

```bash
make verify-worker-backed-local-system
```

脚本会自动：

- build `bin/tma-server`、`bin/tma` 和 `bin/tma-worker`。
- 启动 Postgres 并执行迁移。
- 用 fake LLM 启动临时 TMA server，并配置 `TMA_WORKER_AUTH_TOKEN`。
- 启动一个本地 `tma-worker`，worker 只主动消费 server API，不暴露端口。
- 创建 agent / environment / session。
- 将 agent tools 配置为 `{"tools":["default"],"runtime":"local_system"}`。
- 将 session `intervention_mode` 设置为 `approve_for_me`。
- 发送 `tma.verify_tool_call`，让 fake LLM 发起 `default_run_command`。
- 校验事件历史中出现 `runtime.tool_call`、`runtime.tool_result`、`agent.message`。
- 从 worker 日志中取出 completed `work_id`，再用 `bin/tma work get --work ...` 校验 work 状态和 tool result。

通过表示 `local_system` 工具执行已经真实经过 `tma-worker`，而不是落在 server 进程内 fallback。

### 17.9.0 MCP stdio 验证

这一节验证 Agent 级 MCP stdio 接入已经形成完整闭环：

```text
Agent config mcp
  -> server 读取 MCP initialize / tools/list
  -> AgentRuntime 暴露 filesystem.read_file
  -> fake LLM 调用 filesystem.read_file
  -> runtime.tool_result / agent.message
```

运行：

```bash
make verify-mcp-stdio
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 启动临时 TMA server。
- 创建一个 verification Agent。
- 通过 `agent config update --mcp ... --tools ...` 绑定仓库内 `scripts/mcp_stdio_fixture.py`。
- 校验 `agent config list` 返回的 `mcp` 已归一化为 `servers` 数组，且 `env` 中保存的是 `env_ref` 引用。
- 创建 environment / session，并发送 `tma.verify_mcp_tool`。
- 校验事件历史中出现 `runtime.tool_call`、`runtime.tool_result`、`agent.message`、`session.status_idle`。
- 校验工具调用确实是 `filesystem.read_file`，结果包含 `tma-mcp-filesystem-ok` marker。
- fixture 会把每次进程启动的 PID 追加到临时 marker；脚本要求只有一行，证明 manifest 加载阶段的 `tools/list` 和执行阶段的 `tools/call` 命中同一个 Server 长驻 stdio 进程。

Streamable HTTP 的真实 TLS 兼容性验收运行：

```bash
make verify-mcp-http
```

该入口先运行取消、`404/410` Session 重建、SSE 重连和 `DELETE` 定向 Go 测试，再生成临时 CA/服务端证书并启动 `scripts/mcp_http_fixture.py`。临时 TMA Server 使用 HTTPS-only egress、`localhost` host allowlist、精确 loopback CIDR 和 `TMA_MCP_HTTP_CA_BUNDLE`，端到端验证 OAuth client credentials、`Mcp-Session-Id`、`Mcp-Protocol-Version`、JSON response、POST SSE、GET SSE、`Last-Event-ID`、tools/resources/prompts、logging/progress 脱敏计数、Agent 工具调用和优雅关闭 `DELETE`。Agent config 只能引用 OAuth secret，不能注入 CA 或关闭 TLS 校验。

同一批 Go 单测还覆盖 `streamable_http` transport：

```bash
go test ./internal/mcp ./internal/tools
```

覆盖点包括：

- `streamable_http` 配置要求 `url`，且只接受 `http` / `https`。
- `headers` 支持 `env_ref` / `secret_ref: "env:NAME"`，不会把真实 secret 写回 canonical config。
- HTTP MCP client 发送 `POST` JSON-RPC，支持 JSON response 和 POST response SSE；POST SSE 在目标 response 前发送 `ping` server request 时，client 会先通过 POST 返回对应结果，再继续解析工具列表。
- initialize 后同一短会话后续请求会带上 `Mcp-Protocol-Version`；initialize response 返回 `Mcp-Session-Id` 时，也会带上该 session header。
- initialize response 返回的 server `capabilities` 会被解析、保存到 MCP runtime，并在 tooling health 响应中展示。
- tooling health 在 MCP tools 加载成功后会额外探测 `resources/list` / `resources/templates/list` / `prompts/list`，并在响应中展示 `resource_count` / `resource_template_count` / `prompt_count`；探测失败只进入诊断，不会覆盖 tools 在线状态。
- `npm --prefix apps/workbench run build` 会同步验证 Workbench MCP 健康检查 badge 可展示 capabilities 和三类 catalog count，Agent 编辑器可保留 `expose.resources` / `expose.prompts` 开关，并更新内嵌静态资源。
- `npm --prefix apps/inspector test -- --run` 会覆盖 Inspector MCP 工具来源统计、诊断 badge、`tma.mcp_result.v1` / `tma.mcp_context_result.v1` 摘要，以及 `MCP Protocol` 对 call/result、重复 call ID、pending/unpaired 操作的顺序配对；测试确认 arguments、endpoint、Authorization、error message、content text 和 structured content value 不进入协议投影。
- `npm --prefix apps/inspector run build` 会验证 Inspector Timeline / Recent Events 的 MCP badge、result/context 摘要卡和独立 `MCP Protocol` 面板，并更新内嵌静态资源。浏览器验收应使用真实 MCP Session，在 1280px 与 390px viewport 检查 request/response 横向/纵向布局、`scrollWidth == clientWidth`、无控件重叠，并确认面板范围不含 marker、路径、Authorization 或 endpoint；控制台不得有 error/warn。
- `listen: true` 会启动可选 GET SSE listener；listener 会处理 server request fallback response，并在重连时发送 `Last-Event-ID`。
- `roots` 配置会归一化；stdio 与 `streamable_http` initialize 都会声明 `roots: { listChanged: false }`，收到 `roots/list` 时返回配置的 roots。
- stdio 子进程测试会在 `tools/list` 完成前反向发送 `ping`、`roots/list`、`sampling/createMessage`、`elicitation/create` 和未知方法，校验 client 不会丢弃 server request，并分别返回空结果、roots、`-32000` 安全拒绝和 `-32601` fallback。
- stdio 与 Streamable HTTP listener 都会原样回传数字或字符串 JSON-RPC request ID；HTTP listener 测试使用字符串 ID 覆盖这一兼容性。
- `sampling` 配置会保留到 runtime；listener 收到 `sampling/createMessage` 时返回 `-32000` 策略拒绝，避免远端 MCP server 绕过主流程直接触发模型采样。
- `elicitation` 配置会保留到 runtime；listener 收到 `elicitation/create` 时返回 `-32000` 策略拒绝，避免远端 MCP server 绕过主流程直接触发用户交互。
- sampling / elicitation 在真实 backend 接入前不会写入 initialize client capabilities；测试会同时校验“未声明能力”和“未协商请求仍安全拒绝”。
- stdio MCP client 会继承调用方 context；当 helper 在 `tools/call` 阶段卡住且调用超时时，测试会校验先发送 `notifications/cancelled`（包含 in-flight `requestId` 和 context reason），再返回 `context deadline exceeded`，而不是泄漏 `read mcp header` / `EOF` 等底层 pipe 细节。
- Server `StdioHost` 单测会校验同一 scope 多次调用只 initialize 一次并复用 PID、不同 Session scope 使用隔离进程、空闲 entry 被回收，以及取消请求后下一次调用自动创建新进程。
- 容量测试会校验达到 `max_sessions` 时淘汰最旧空闲 entry；全部 entry 正在使用时返回 capacity 错误，并累计 eviction/rejection 计数。
- 动态目录测试会让同一个 fixture PID 在工具调用期间发送 tools/resources/prompts 三类 `list_changed` notification，校验 host 分别累计事件，并由后续实时 `tools/list`、`resources/list`、`prompts/list` 读取更新后的目录，全程不重启子进程。
- logging 测试覆盖八级配置归一化、非法 level 拒绝、server capability 校验和 `logging/setLevel` 参数；stdio fixture 会夹带有效/非法 progress 与 logging notification，验证只累计数量/level，不保存 `data`、logger、token 或 message。progress 解析单测还会校验 `progressToken` 必须是字符串或数字，`progress` / 可选 `total` 必须是非 null JSON number。
- execution resolver 单测会连续解析同一 Session 的两个 Turn，确认只启动一个 MCP 进程；切换 Session 后必须启动第二个隔离进程。
- Streamable HTTP host 单测会校验同一 scope 只 initialize 一次并跨调用复用 `Mcp-Session-Id`、不同 Session 隔离、Turn 请求取消后继续复用、长驻 SSE listener 接收目录通知、空闲回收发送 `DELETE`，以及全部 entry 忙碌时容量拒绝。
- Streamable HTTP listener 测试会推送 progress 与包含敏感 fixture data 的 logging notification，验证 host 仅暴露脱敏计数和规范化 level；HTTP logging capability 存在时必须收到配置的 `logging/setLevel`。
- execution resolver 会连续解析同一 Session 的两个 Turn，确认只建立一个远程 MCP Session；切换 Session 后必须执行第二次隔离 initialize。
- `go test -race ./internal/mcp ./internal/execution -count=1` 覆盖 stdio/HTTP host 的请求串行、取消、回收、listener 状态和 resolver 复用路径。
- server config 测试覆盖两类 host lifecycle/capacity 默认值、dotenv 覆盖和非法范围；HTTP 测试覆盖 tooling health 的 `mcp_host` / `mcp_http_host` 快照及两组 Prometheus 指标。
- `npm --prefix apps/workbench run build` 验证 Workbench 健康检查会分别展示 Server Host 与 Remote HTTP Host 的生命周期、目录变更、progress、日志和非法通知状态；Agent 编辑器可配置 stdio/Streamable HTTP、URL、SSE listener 和八级 logging，并在单项 MCP 重测后保留最新 host 快照。浏览器验收还应在 1280px 桌面和 390px 手机 viewport 检查 MCP row 的 `scrollWidth == clientWidth`、页面无横向滚动且子控件无相交；切换到 Streamable HTTP 后应出现“服务 URL”、启用 SSE 开关并保留 logging level 选择。
- MCP client 支持 `resources/list` / `resources/read`，覆盖 stdio 与 `streamable_http` 两种 transport；resources 默认只作为底层协议能力，配置 `expose.resources` 后会注册只读 `mcp_list_resources` / `mcp_read_resource` Agent 桥接工具。
- MCP client 支持 `resources/templates/list` 与分页；配置 `expose.resources` 后额外注册只读 `mcp_list_resource_templates`，tooling health 返回 `resource_template_count`。
- MCP client 支持 `prompts/list` / `prompts/get`，覆盖 stdio 与 `streamable_http` 两种 transport；prompts 默认只作为底层协议能力，配置 `expose.prompts` 后会注册只读 `mcp_list_prompts` / `mcp_get_prompt` Agent 桥接工具，不会自动注入模型上下文。
- MCP client 与 HostedClient 支持 `completion/complete` 的 Prompt/Resource reference、context arguments，并拒绝超过 100 values 的非法响应；completion 不自动注册为模型工具。
- `tools/list`、`resources/list`、`resources/templates/list`、`prompts/list` 支持 `nextCursor` 分页合并，并覆盖 stdio 与 `streamable_http` 多页结果。
- `resources/list` / `resources/templates/list` / `prompts/list` 返回 `-32601 method not found` 时降级为空列表；`tools/list` 返回 `-32601` 时默认仍保持失败，只有显式开启 `expose.resources` / `expose.prompts` 时才允许 resource-only / prompt-only server 加载为只读 context runtime。
- `internal/tools` 单测分别覆盖 resource-only 和 prompt-only Streamable HTTP server，确保 `tools/list` 不支持时仍可通过显式 expose 执行只读桥接工具。
- 401 Unauthorized 会解析 `WWW-Authenticate`，抓取 OAuth protected resource metadata 和 authorization server metadata，并在错误中返回授权诊断信息。
- OAuth client credentials / refresh token 会覆盖配置解析、`client_secret_post` / `client_secret_basic` token 请求、Bearer 注入到 Streamable HTTP MCP 请求、带 `expires_in` token 的进程内缓存/刷新，以及 token endpoint 非 2xx 错误不回显 response body / secret。
- Server 级 `TMA_MCP_HTTP_CA_BUNDLE` 会把 PEM 根证书追加到系统 trust pool，并由 egress policy 的基础 HTTP client 统一提供给 OAuth、主 MCP、listener 和 DELETE；无效 PEM 会让 Server 启动失败，且不会降级为 insecure TLS。
- Remote MCP egress 测试覆盖默认 HTTPS-only、host wildcard allowlist、RFC1918/ULA 开关、精确 CIDR 放行、loopback/link-local/metadata 阻断、DNS 同时返回公共与 metadata 地址时整次拒绝、跨 authority redirect 拒绝和非法 host/CIDR 配置。OAuth token endpoint 与 tooling health 必须复用同一 policy 并累计 `egress_blocked_total`。
- `npm --prefix apps/workbench run build` 还会验证 Remote HTTP Host 展示“仅 HTTPS/允许 HTTP”“阻止私网/允许私网”、host/CIDR allowlist 数量和出站阻断计数；页面不展示实际内部目标。
- Server 实机验收可创建一个 URL 指向 `http://169.254.169.254/latest/meta-data` 的 Streamable HTTP MCP Agent，再调用 `POST /v1/agents/{agent_id}/tooling-health`。预期返回 `configuration_error` 和 reason-only `mcp egress policy blocked request (http_not_allowed)`，响应中不能出现目标 IP/path；同一响应的 `mcp_http_host.egress_blocked_total` 及 `/metrics` 的 `tma_mcp_streamable_http_host_egress_blocked_total` 必须立即增加。
- egress block callback 单测只允许输出固定 reason；Server 日志应出现 `mcp_http_egress_blocked reason=http_not_allowed`，不得包含被阻断 URL、host、IP、CIDR、header 或 OAuth 内容。
- `internal/tools` 单测会校验 `LoadMCPRuntime` 生成 runtime-scoped resolved client snapshot；同一个 `MCPRuntime.Execute` 不会在后续工具调用中重新解析 `env_ref`，避免同一 runtime 内 MCP 配置漂移。环境变量或 secret 轮换需要重新加载 Agent config / runtime。
- AgentRuntime 单测会校验 `runtime.tool_call` / `runtime.tool_result` 对 MCP 工具透传非敏感 `mcp_*` metadata，且不包含 endpoint URL 或 secret。

#### 第三方 MCP Server 兼容性矩阵

联网验收：

```bash
make verify-mcp-compatibility
```

该入口需要 `npx`、npm registry 网络、Docker Compose，以及可由当前仓库启动的 PostgreSQL 服务。PostgreSQL 子测试会自行执行 `docker compose up -d postgres`，创建随机一次性数据库和只读角色；不会读取或修改开发数据库业务数据。固定运行：

- `@modelcontextprotocol/server-filesystem@2026.7.10`：加载 14 个工具，通过 `read_text_file` 读取临时 marker；
- `@modelcontextprotocol/server-filesystem@2026.7.10` initial Roots：不传命令行目录，由 Server 在初始化后调用 `roots/list`，验证返回目录进入允许清单并实际读取 marker；
- `@modelcontextprotocol/server-memory@2026.7.4`：加载 9 个工具，通过 `create_entities -> read_graph` 验证同一 host 的有状态复用；
- `@modelcontextprotocol/server-sequential-thinking@2026.7.4`：加载实际名称为 `sequentialthinking` 的工具，执行一次调用并校验 structured content。
- `@modelcontextprotocol/server-everything@2026.7.4`：在本机随机端口启动 Streamable HTTP 模式，复用 Server Session，加载 tools/resources/resource templates/prompts，执行 Prompt/Resource completion 与 `echo` marker 调用。
- `@yawlabs/postgres-mcp@0.6.20`：自动创建一次性数据库和随机只读角色，通过 `secret_ref: env:...` 注入连接串，验证 21 tools、参数化读取、`POSTGRES_MAX_ROWS=2` 截断、两类只读写入拒绝、stacked query 拒绝和数据最终不变。

通过条件：五款 Server 均完成真实 `initialize`、`tools/list` 和 `tools/call`，返回非空 Server name/version 并确认协商 MCP `2025-06-18`；Filesystem 还必须在无命令行目录时完成反向 `roots/list`、允许目录有界收敛和 marker 读取；Everything 必须在同一 Streamable HTTP Session 中返回非空 tools/resources/resource templates/prompts，完成两类 completion 与 `echo`；PostgreSQL 必须证明结果截断、参数化查询、MCP 与数据库双层只读边界以及 rejected write 后数据不变。每个 `inputSchema` 都是合法 JSON，stdio 使用 MCP 标准 `json_lines` framing。文件测试数据只写入临时目录，本地 HTTP/stdio 进程、一次性数据库和角色结束后不得残留。默认 `go test ./...` 会跳过联网及数据库矩阵；完整结果与已知限制记录在 `docs/mcp.md`。

legacy framing 聚焦回归：

```bash
go test ./internal/mcp -run 'Test(ParseConfigValidatesStdioFraming|StdioClientSupportsJSONLinesFraming)' -count=3
```

Workbench 新建 stdio Server 时显式写入 `json_lines`；为保持历史版本行为，后端遇到省略字段的已有配置时按 `content_length` 解释。仓库 fixture 的 stdio/Registry/RuntimeGuard 脚本均显式使用 legacy framing，防止把 fixture 通过误当成官方 Server 兼容。

2026-07-14 实机结果：五款固定 Server 全部通过，Filesystem 的命令行 root 与 initial Roots 两种模式都通过。initial Roots 在官方实现中异步生效，测试必须等待 `list_allowed_directories` 收敛，不能仅以 `tools/list` 成功判定就绪。stdio 官方 Server 自报版本分别为 `secure-filesystem-server 0.2.0`、`memory-server 0.6.3`、`sequential-thinking-server 0.2.0`；工具数分别为 14、9、1。Everything 自报 `mcp-servers/everything 2.0.0`，返回 13 tools、7 resources、4 prompts。YawLabs PostgreSQL 自报 `0.6.20` 并返回 21 tools，读写边界及清理检查通过。TMA 声明 `roots.listChanged=false`，不把运行期 Roots 热变更计入已支持能力。

#### MCP RuntimeGuard 生产保护验证

真实故障注入端到端验收：

```bash
make verify-mcp-runtime-guard
```

该入口构建 Server/CLI，运行 RuntimeGuard race 测试，然后创建一次性 PostgreSQL 数据库、Workspace JWT 和非 owner/非 superuser runtime role。真实 Registry server 使用 `timeout_seconds=1`、`max_concurrency=1`、`failure_threshold=2`、`cooldown_seconds=3`，脚本会自动完成：

1. `timeout` 模式执行第一轮，断言一个 `mcp_timeout` 和 fixture call count = 1。
2. 下一轮 `tools/list` 成功后再次执行超时工具，断言 call count = 2、`state=open`、连续失败 2、最近分类 timeout。
3. open 期间再触发一轮，断言 fixture call count 仍为 2，证明熔断拒绝没有进入或重放 `tools/call`。
4. 冷却后断言 `half_open`，切到 success 后执行单探测和真实工具调用，断言 `closed`、连续失败归零、call count = 3。
5. 检查脱敏 `runtime.tool_result`、成功 marker、Workspace runtime-status 字段白名单和 `tma_mcp_runtime_guard_*` 指标。

fixture 控制变量为 `TMA_MCP_FAULT_MODE_FILE`、`TMA_MCP_FAULT_CALL_FILE` 和 `TMA_MCP_FAULT_DELAY_SECONDS`。模式文件支持 `success`、`timeout`、`transport`、`rpc_unavailable`、`protocol`；生产 MCP 配置不会暴露这些测试变量。

聚焦回归：

```bash
go test ./internal/mcp \
  -run 'Test(RuntimeGuard|ClassifyRuntimeError|ParseConfig.*RuntimePolicy)' \
  -count=3
go test ./internal/tools \
  -run 'Test(LoadMCPRuntimeBuildsManifestAndExecutesTool|MCPRuntimeFailureResultIsClassifiedAndRedacted)' \
  -count=3
go test ./internal/mcpregistry \
  -run 'Test(PinAndResolveRegistryBinding|NormalizeServerConfigRemovesRuntimeRegistryMetadata)' \
  -count=3
go test ./internal/observability \
  -run TestPrometheusTextIncludesMCPRuntimeGuardMetrics \
  -count=3
go test ./internal/httpapi \
  -run TestAgentToolingHealthReportsMCPAndSkillState \
  -count=3
go test -race ./internal/mcp ./internal/mcpregistry ./internal/observability -count=1
```

通过条件：

- `runtime` 四项配置正确归一化，未配置项使用 30 秒、4 并发、5 次失败和 30 秒冷却；越界值拒绝发布。
- 调用 deadline 包含并发槽等待；等待超时不进入底层 client，也不会增加已接纳调用数。
- 真实 `tools/call` 超时只进入底层 client 一次，不进行自动重放。
- catalog（list）与 operation（call/read/get）分别累计连续失败；成功 `tools/list` 不会清零持续失败的 `tools/call`。
- 连续失败达到阈值后立即返回 `mcp_circuit_open`；冷却结束只允许一个 half-open 探测，探测成功后恢复 closed。
- Registry resolved config 带有 Server ID + 固定版本运行分区，Registry 持久化 config 会删除调用方伪造的内部分区 metadata。
- failure class 仅为 `canceled`、`timeout`、`authentication`、`rate_limited`、`transport`、`protocol`、`unavailable`、`unknown`；Runtime tool result 不包含测试注入的 endpoint、Authorization 或 token。
- tooling health 返回 `mcp_runtime_guard` 聚合快照；Prometheus 返回 `tma_mcp_runtime_guard_servers`、`in_flight`、`open_circuits`、calls/results/rejections/failures 指标，label 不包含 workspace/server/URL。

2026-07-14 本轮结果：`make verify-mcp-runtime-guard` 通过。真实 Session `sesn_000001` 的 fixture 调用序列为 `timeout, timeout, success`；两次超时后 open，open 期间无新增调用，冷却后 half-open 探测和工具调用成功，最终 closed、连续失败归零。RuntimeGuard 定向测试 `-count=3`、race、HTTP runtime-status 和 82 项 Workbench 测试也通过。入口使用一次性数据库并确认结束后无残留数据库或 runtime role。

MCP 全量聚合入口：

```bash
make verify-mcp-all
```

该入口聚合 `verify-mcp-stdio`、`verify-mcp-http`、`verify-mcp-registry`、`verify-mcp-runtime-guard` 的测试与真实脚本，然后运行 MCP/Registry/execution/observability race、Workbench/Inspector 测试与构建、`git diff --check`。stdio/HTTP 使用自动创建并清理的共享临时数据库，Registry/RuntimeGuard 使用各自的受限独立数据库，不读取或修改开发数据库业务数据。各 fixture Server 显式固定 development 认证模式并关闭 browser OIDC，避免本机 `.env` 登录配置改变验收行为。

2026-07-14 实机结果：`make verify-mcp-all` 完整通过。stdio/HTTP/Registry/RuntimeGuard 四套真实脚本、MCP/Registry/execution/observability race、84 项 Workbench 测试、10 项 Inspector 测试、两套生产构建和 `git diff --check` 均成功；共享临时数据库、两个独立数据库和受限 runtime role 均已清理。

#### Workbench 单 Server 熔断状态验证

自动测试：

```bash
go test ./internal/mcp \
  -run TestRuntimeGuardRegistryStatesAreWorkspaceScopedAndVersioned \
  -count=3
go test ./internal/httpapi \
  -run TestMCPRegistryRuntimeStatusIsWorkspaceScopedAndFiltersUnknownServers \
  -count=3
npm --prefix apps/workbench test
npm --prefix apps/workbench run build
```

覆盖点：

- RuntimeGuard 按 Workspace + Registry server + 固定版本投影，版本倒序返回；embedded server 和其他 Workspace 不可见。
- `closed`、`saturated`、`open`、`half_open` 状态计算，open 状态的向上取整冷却秒数、最近失败分类和时间字段正确；零值时间不进入 JSON。
- HTTP handler 使用当前 Workspace Registry server 列表再次过滤状态，未知、归档或跨 Workspace server ID 不会返回。
- 前端按 server ID 分组多个版本，并按 `open > half_open > saturated > closed` 选择列表最严重状态；未知状态和未知失败分类不会原样显示。
- Workbench 详情逐版本展示并发、连续失败、最近失败分类与冷却秒数；刷新按钮只重新读取运行状态，不触发 MCP 调用。

浏览器验收：

1. 重启 Server 后进入 `设置 > MCP`，Registry 行和运行保护区应显示“未运行”。
2. 使用固定 Registry binding 的 Session 执行一次真实 MCP fixture，再点击刷新图标。
3. 确认详情出现 `v1`、`正常`、`并发 0/4`、`连续失败 0/5`，列表状态同步为“正常”。
4. 在 1280x900 和 390x844 viewport 检查 document/body `scrollWidth == clientWidth`，Registry list、运行保护区和版本行均无内部横向溢出。
5. 浏览器 console 不得有 error/warn；`runtime-status` 响应不得包含内部 key、Workspace ID、URL、headers、arguments、content 或零值时间。

2026-07-14 实机结果：主 Server 的 `mcps_000001` 固定 v1 fixture 调用成功；API 返回 closed、0/4、0/5，Workbench 桌面与手机均正确展示。1280px 和 390px 下 document/body 无横向溢出，相关容器 `scrollWidth <= clientWidth`，console 无 error/warn。

#### Workspace MCP 注册表验证

真实 Server、JWT 和 PostgreSQL 的一键端到端验收：

```bash
make verify-mcp-registry
```

该命令会构建 Server/CLI、运行 Registry 定向 Go 测试，然后创建一次性 `tma_verify_mcp_registry_*` 数据库并应用全部迁移。脚本还会创建两个临时 Workspace JWT 和一个非 superuser、无 `BYPASSRLS`、不拥有业务表的 runtime role；结束或失败时会终止连接、删除临时数据库和 runtime role，不修改开发数据库。

端到端覆盖：

- Alpha JWT 请求体伪造 Beta `workspace_id` 无效，资源仍写入 Alpha；Beta JWT 按 Alpha server ID 查询得到 `404`，列表也不可见。
- 创建 Registry v1，Agent 使用 `version: 0` 后落库固定为 v1；发布中央 v2 后 `usage_count=1`，Agent config version 和 binding 不变。
- 在中央当前版本为 v2 时，通过固定 v1 Agent/Session 实际执行 `filesystem.read_file`，事件包含 MCP `runtime.tool_call`、`runtime.tool_result` 和 `tma-mcp-registry-ok` marker。
- 恢复 v1 生成 v3，版本顺序为 v3/v2/v1，v3 与 v1 checksum 相同且 v2 不同；已有 Agent 继续绑定 v1。
- 有当前 binding 时归档返回 `409`；停用后 Agent tooling health 返回 `configuration_error`，重新启用后恢复的 v3 Registry 连通性测试为 `online`。
- `mcp_registry.version.restore` audit 是 Workspace 级记录，`session_id` 为空，details 精确包含 source/previous/new version。

注册表与固定版本 binding 的聚焦回归：

```bash
go test ./internal/mcpregistry ./internal/httpapi ./internal/managedagents ./internal/runner ./cmd/tma-server -count=1
go test -race ./internal/mcpregistry ./internal/httpapi -count=1
npm --prefix apps/workbench run build
```

覆盖点：

- 创建 Workspace MCP server 时生成 version 1 和 SHA-256 checksum；更新配置追加不可变版本，不覆盖历史版本。
- 列表和详情返回状态、当前版本与当前 Agent `usage_count`。
- Agent 创建、更新、config-version 和导入时将 `version: 0` 固定为当前版本；保存后的 config 不再包含浮动版本。
- runtime 与 tooling health 按 Agent 固定版本解析；中央服务发布新版本不会改变旧 Agent/Session。
- 停用服务后固定版本解析立即失败；仍被当前 Agent 使用时归档返回 conflict。
- 跨 workspace binding、缺失版本、重复 identifier 和敏感 header literal 被拒绝。
- 旧内嵌 `mcp.servers` 与中央 `bindings` 可并存，Agent 导入/导出继续只保存 secret 引用。
- Workbench 支持注册表新建、编辑发布、启停、测试、归档和 Agent binding；旧版本 binding 显示显式升级操作。
- 历史恢复把 source config/checksum 复制为新的不可变版本，不覆盖旧行；当前/未来版本和归档服务恢复被拒绝。
- 恢复响应和 `mcp_registry.version.restore` 审计包含 source、previous、new version；已有 Agent binding/config version 保持不变。
- Workbench 版本历史显示时间、checksum 和可展开 canonical config，恢复需要二次确认并在成功后刷新当前版本。
- `000052_mcp_registry_rls.sql` 对 Registry server/version 启用 `FORCE RLS`；Store 的所有 Registry SQL 都在事务内设置 workspace scope。
- 真实非 superuser runtime role 无 scope 时看不到 Registry 行，跨 workspace 创建被拒绝，按其他 workspace 的 server ID 查询返回 not found，原始 SQL 也不能绕过 `WITH CHECK`。

真实 PostgreSQL 验收已使用迁移 `000048_mcp_registry.sql` 完成：创建 `mcps_000001` v1，Agent `agt_000147` 以 `version: 0` 发布后落库固定为 v1；中央发布 v2 后 `usage_count=1`，Agent 仍保持 v1。Session `sesn_000231` 解析 v1 并成功调用 `filesystem.read_file`，结果包含 `tma-mcp-filesystem-ok`。

Workbench 浏览器验收结果（2026-07-14）：

- 1280px 桌面端 `设置 > MCP` 正确显示 server、版本、使用数、状态和管理操作；执行“测试”后状态更新为 `online`。
- `设置 > Agent` 选择绑定 Agent 后显示固定 v1，并在中央当前版本为 v2 时显示“升级到 v2”；不点击升级时配置不得变化。
- 390px 手机端 MCP 和 Agent 页面 `scrollWidth == clientWidth`，无横向滚动、控件裁切或不合理重叠。
- Registry 历史恢复专项验收显示 `Registry Smoke` 的 v2/v1、当前版本标记、时间和 checksum；v1 canonical config 可以展开查看。
- 桌面端和手机端点击“恢复此版本”都会显示“已有 Agent binding 保持不变”的二次确认；本次点击“取消”，没有执行真实恢复，fixture 继续保持 `current_version=2`。
- 1280x900 与 390x844 下 document/body `scrollWidth == clientWidth`；手机端 JSON 自动换行，恢复按钮和“取消/确认恢复”按钮均完整位于卡片内。
- 浏览器控制台没有 error/warn；`/health` 返回 `ok`，`GET /v1/mcp-servers` 仍返回 `mcps_000001` v2、`usage_count=1`。

2026-07-14 最终回归中，`make verify-mcp-http` 通过并创建 `sesn_000235`，`make verify-mcp-stdio` 通过并创建 `sesn_000237`；两者均实际执行了包括 `000048_mcp_registry.sql` 和 `000052_mcp_registry_rls.sql` 在内的完整迁移集。

历史恢复的独立真实 PostgreSQL RLS 测试：

```bash
TMA_RUN_POSTGRES_TESTS=1 \
TMA_DATABASE_URL='postgres://tma:tma@localhost:5432/tma?sslmode=disable' \
go test ./internal/managedagents \
  -run TestPostgresMCPRegistryRestoreWithRLS \
  -count=1 -v
```

该测试只给临时非 superuser 角色授权 Registry 表、usage count 所需只读表和两条 sequence，验证 v1 -> v2 -> 恢复 v1 为 v3、source checksum 保持、跨 workspace 恢复不可见以及无 scope 零行。

### 17.9.1 Worker process plugin 验证

这一节验证 worker 进程型插件可以进入完整 AgentRuntime 链路：

```text
tma-worker --plugin
  -> heartbeat capabilities.manifests
  -> AgentRuntime 暴露 robot.get_state
  -> fake LLM 调用 robot.get_state
  -> WorkerBackedProvider 下发 tma.work.v1 tool_execution
  -> 插件 execute 返回 tool result
```

运行：

```bash
make verify-worker-plugin-tools
```

脚本会自动创建一个临时 `robot` 插件，插件只实现 `manifest` 和 `execute` 两个命令。通过条件：

- `bin/tma worker list --json` 能看到在线 worker 的 `robot.get_state` API 和 `robot` manifest。
- agent tools 配置为 `{"tools":["robot"],"runtime":"local_system"}` 后，fake LLM 能触发 `robot.get_state`。
- 事件历史包含 `runtime.tool_call`、`runtime.tool_result`、`agent.message`，且结果包含 `tma-worker-plugin-ok`。
- `worker_work.payload` 是标准 `tma.work.v1`，`namespace=robot`、`api=get_state`、`capabilities=["robot.state"]`、`risk=read`。

### 17.9.2 Computer-use process plugin 验证

这一节验证 `computer` 插件可以作为 worker 后端能力挂入现有 `tool_execution` 链路。该 smoke 使用 fake CUA backend，不控制真实电脑，但会覆盖 CUA tool 映射、截图落 PNG 文件、以及带 session 的 screenshot artifact ref；真实运行时可切到 `auto` / `cua` / `ax`。详细标准见 [docs/tools.md](./docs/tools.md)。

```bash
make verify-computer-plugin-tools
```

脚本会自动：

- 启动真实 TMA server。
- 启动真实 `tma-worker --plugin examples/plugins/computer-use/computer-plugin.py`。
- 校验 worker heartbeat 发布 `computer` manifest、`computer.get_state` API 和 `computer.ax.read` capability。
- 创建启用 `{"tools":["computer"],"runtime":"local_system"}` 的 agent。
- 发送 `tma.verify_computer_plugin_tool`，让 fake LLM 调用 `computer.get_state {"capture_mode":"ax"}`。
- 发送 `tma.verify_computer_plugin_screenshot`，让 fake LLM 调用 `computer.screenshot`。
- 校验事件历史包含 `runtime.tool_call`、`runtime.tool_result`、`agent.message`，且 `get_state` 结果包含 CUA `get_accessibility_tree` 映射和 `ui_tree`。
- 校验 screenshot 结果包含 CUA `get_desktop_state` 映射、`has_screenshot=true`、无 `screenshot_png_b64`，并且有 session artifact ref。
- 校验 `worker_work.payload` 是标准 `tma.work.v1`，`namespace=computer`、`api=get_state`、`capabilities=["computer.state.read","computer.ax.read"]`、`risk=read`、`runtime=local_system`。

真实 CUA 后端手动点检：

```bash
TMA_COMPUTER_BACKEND=cua \
TMA_COMPUTER_CUA_CMD=cua-driver \
bin/tma-worker --base-url http://localhost:8080 --name computer-worker --plugin examples/plugins/computer-use/computer-plugin.py

bin/tma worker diagnose --namespace computer --api get_state --capabilities computer.state.read,computer.ax.read --runtime local_system
```

CUA CLI 调用格式如有差异，用 `TMA_COMPUTER_CUA_TEMPLATE='...'` 适配。当前不部署 OmniParser。

已验证的真实 CUA 动作包括 `computer.get_state`、`computer.list_windows`、`computer.launch_app`、`computer.bring_to_front`、`computer.type_text`、`computer.hotkey`、`computer.open_url`、`computer.search_web` 和 `computer.screenshot`。`type_text` / `hotkey` / `click` 的 `pid` 可省略，插件会尝试解析指定 app 或当前前台窗口；按键别名如 `Command` / `Return` 也会规范化。`type_text` / `hotkey` 可能返回 `verified:false`、`effect:"unverifiable"`；这表示 CUA 已发送键盘事件但无法证明目标 UI 状态变化，不表示 TMA 调用失败。`computer.screenshot` 依赖 macOS Screen Recording 权限；若返回 `screencapture failed for main display`，需要给启动 worker 的终端/CUA 授权后重启 worker。CUA screenshot 的 `screenshot_png_b64` 会在插件内落成 PNG 文件，`state.result` 只保留摘要；有 session 的 worker 路径会把 `image/*` export 上传成 artifact，避免截图 base64 进入 tool result。

### 17.10 Worker-backed local export 验证

这一节验证 `worker-backed local_system` 上的 `output_paths` 也能把 worker 机器生成的文件回收到 session artifact：

```bash
make verify-worker-backed-local-export
```

脚本会自动：

- build `bin/tma-server`、`bin/tma` 和 `bin/tma-worker`。
- 启动 Postgres 并执行迁移。
- 用 fake LLM 启动临时 TMA server，并配置 `TMA_WORKER_AUTH_TOKEN`。
- 启动一个本地 `tma-worker`。
- 创建 agent / environment / session，并将 agent tools 配置为 `{"tools":["default"],"runtime":"local_system"}`。
- 发送 `tma.verify_worker_export`，让 fake LLM 触发 `default_run_command` 在 worker workspace 里写 `worker-export.txt`，并通过 `output_paths` 请求保存。
- 从 `runtime.tool_result.artifacts` 中取出生成的 `file` artifact。
- 通过 `bin/tma session artifact download` 下载该 artifact，校验内容包含 `tma-worker-export-ok`。

注意：这一节不仅是在验证 recorder / objectstore，还在验证 `output_paths` 已经从工具入参透传到 worker work payload。若未来回归成“tool result 里只有结构化 JSON artifact、没有 file artifact”，优先检查 `capability.RunCommandRequest` / `ExecuteCodeRequest` 是否仍携带 `output_paths`。

通过表示 worker 机器生成文件 -> work result 回传 -> server objectstore 落盘 -> session artifact 下载 已经形成最小闭环。

大文件路径额外验证 worker 不再把文件内容内联进 `worker_work.result`，而是通过 server artifact upload API 上传后只回传 artifact ref：

```bash
make verify-worker-backed-large-local-export
```

该脚本会触发 fake LLM 生成一个超过 8 MiB 的 worker 本地文件，下载 artifact 并校验内容包含 `tma-worker-large-export-ok`。通过表示大文件已经走 worker -> server artifact upload，而不是 base64 塞进 work result。

### 17.11 Worker / worker work 过期收敛

第一版 worker work 不做自动重试。若 worker 掉线或长时间没有给 leased/running work 续租，后台 reaper 会把过期 work 标记为 failed，方便排障和释放卡住状态。若 worker 自身长时间没有 heartbeat，后台 worker reaper 会把过期 online worker 标记为 offline，避免 capability match / diagnose 继续把陈旧 worker 当作在线能力。

正常 `tma-worker` 执行长任务时会在 ack 后、result 前按 `TMA_WORKER_WORK_HEARTBEAT_INTERVAL` 周期性调用 work heartbeat；这里的过期回收脚本故意不续租，用来验证 reaper 兜底路径。

真实验收：

```bash
make verify-worker-work-reap-expired
```

该脚本会启动真实 TMA server，打开后台 worker reaper 与 worker work reaper，注册一个 worker，投递 work，用短 lease poll 并 ack 成 running，等待 work lease 过期后由 server 自动回收，再用 `work get` 确认该 work 已变为 `failed` 且带 `worker work lease expired` 错误信息。随后脚本还会注册一个 1 秒 lease 的 worker，不发送 heartbeat，确认 server 自动把它标记为 `offline`。

反向验证真实 `tma-worker` 的 running work heartbeat：

```bash
make verify-worker-work-heartbeat
```

该脚本会启动真实 TMA server、后台 worker work reaper 和一个真实 `tma-worker`，用 3 秒 work lease 执行一个 6 秒 `sandbox_command`。worker 每 1 秒续租 running work；最终 `work get` 必须显示 `completed`，worker 日志里也必须至少出现两条 `worker work heartbeat`。通过表示长任务不会因为执行时间超过初始 lease 而被 reaper 误标为 failed。

相关配置：

```env
TMA_WORKER_REAPER_ENABLED=true
TMA_WORKER_REAPER_INTERVAL_MS=30000
TMA_WORKER_REAPER_LIMIT=100
TMA_WORKER_WORK_REAPER_ENABLED=true
TMA_WORKER_WORK_REAPER_INTERVAL_MS=30000
TMA_WORKER_WORK_REAPER_LIMIT=100
```

脚本也会在 running 与 failed 两个阶段调用：

```bash
bin/tma work diagnose --work WORK_ID
```

`work diagnose` 面向队列中的单个 work/job，展示当前状态、assigned worker、lease、原因和建议动作；它不同于 `worker diagnose`，后者诊断的是某次 tool invocation 能否匹配到 worker。

`tma-worker` 默认串行消费队列；需要让同一个 worker 同时处理多条 work 时，可以设置：

```bash
bin/tma-worker --base-url http://localhost:8080 --name viito-mac --concurrency 2
```

或：

```env
TMA_WORKER_CONCURRENCY=2
```

并发只改变 worker 本地 poll/execute slot 数量，不改变 server 队列协议。提高并发前要确认多个 work 不会写同一文件或争用同一外部资源。

worker 收到 SIGINT / SIGTERM 后会进入 drain：停止继续 poll，heartbeat `draining`，并在 `TMA_WORKER_SHUTDOWN_TIMEOUT` 内等待已 running 的 work 完成并提交 result。单元测试 `TestRunWorkerDrainsRunningWorkOnShutdown` 覆盖该行为；如果超过 timeout，剩余 work 仍由 lease + reaper 兜底转为 failed。

真实验收：

```bash
make verify-worker-shutdown-drain
```

该脚本会启动真实 TMA server 和真实 `tma-worker`，在 work running 时向 worker 发送 SIGTERM；worker 必须 heartbeat `draining`、等待 long-running work completed、再正常退出。通过表示发布 / 重启 worker 时不会立即丢掉已经 ack 的 running work。

反向控制面取消验收：

```bash
make verify-worker-work-cancel
```

该脚本会启动真实 TMA server 和真实 `tma-worker`，投递一个长时间运行的 work，等它进入 `running` 后调用 `bin/tma work cancel --work ... --reason ...`。worker 下一次 running work heartbeat 必须观察到 `canceled`，取消本地执行，不提交 completed result；最终 `work get` 仍保持 `canceled`，错误信息为取消原因。

手动命令：

```bash
bin/tma worker reap-expired --limit 100
bin/tma work cancel --work WORK_ID --reason "user stopped it"
bin/tma work requeue --work WORK_ID --clear-worker
bin/tma work reap-expired --limit 100
```

等价 HTTP：

```bash
curl -X POST "$TMA_BASE_URL/v1/workers/reap-expired" \
  -H "Authorization: Bearer $TMA_WORKER_CONTROL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"limit":100}'

curl -X POST "$TMA_BASE_URL/v1/worker-work/reap-expired" \
  -H "Authorization: Bearer $TMA_WORKER_CONTROL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"limit":100}'

curl -X POST "$TMA_BASE_URL/v1/worker-work/WORK_ID/cancel" \
  -H "Authorization: Bearer $TMA_WORKER_CONTROL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason":"user stopped it"}'

curl -X POST "$TMA_BASE_URL/v1/worker-work/WORK_ID/requeue" \
  -H "Authorization: Bearer $TMA_WORKER_CONTROL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"clear_worker":true}'
```

通过表示 `lease_expires_at` 已过期的 `leased` / `running` work 会变为 `failed`，并带 `worker work lease expired ...` 错误信息。这里故意不自动重新入队，避免命令、文件写入、外部 API 调用被重复执行；需要重试时由控制面显式 `work requeue` 复制出新的 pending work，原 failed / canceled 记录保留用于审计。

### 17.12 Worker doctor

`tma-worker doctor` 用于在启动常驻 worker 前检查 outbound API、token 和 executor capabilities：

```bash
bin/tma-worker doctor --base-url http://localhost:8080 --name viito-mac
```

doctor 会：

- 打印当前 executor 导出的 runtimes / APIs / capabilities。
- 检查 `/health`。
- 临时注册 `<name>-doctor` worker。
- 发送 heartbeat。
- 调用 poll endpoint 验证 worker consumer API 权限。
- 调用 server-side worker diagnose，确认刚注册的 worker 能被控制面看见。
- 归档临时 worker。

### 17.13 cloud_sandbox 默认配置

新 session 默认已经走 `cloud_sandbox`，`.env` 不需要写 `TMA_TOOL_RUNTIME=cloud_sandbox`。如果要固定 workspace root 或镜像，只配置覆盖项：

```env
TMA_CLOUD_SANDBOX_ROOT=/private/tmp/tma-cloud-sandbox-workspaces
TMA_CLOUD_SANDBOX_IMAGE=coolfan1024/onlyboxes-runtime:default
TMA_CLOUD_SANDBOX_DATA_ROOT=/private/tmp/tma-cloud-sandbox-data
TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS=3600
```

如果要临时切到本机执行面，先启动同 workspace 的 `tma-worker`，再通过 `bin/tma session runtime update --session <session_id> --tool-runtime local_system` 对单个 session 热更新，不改 `.env`。没有匹配在线 worker 时，默认会隐藏 `local_system` 工具；只有受信任开发环境显式设置 `TMA_ALLOW_SERVER_LOCAL_SYSTEM=true` 才允许 server 进程本机 fallback。

---

## 18. 常见问题

### 18.1 `psql: command not found`

不要直接运行：

```bash
psql "postgres://tma:tma@localhost:5432/tma?sslmode=disable" -f sql/migrations/000001_init.sql
```

请运行：

```bash
make migrate-up
```

这个命令会进入 Docker 里的 Postgres 容器执行 `psql`。

### 18.2 连接失败

确认 server 正在运行：

```bash
bin/tma health
```

如果服务不是跑在 `localhost:8080`，指定地址：

```bash
bin/tma --base-url http://localhost:9090 health
```

或使用环境变量：

```bash
export TMA_BASE_URL=http://localhost:9090
bin/tma health
```

### 18.3 ID 不对

Postgres 会保留历史数据，ID 会继续递增。以实际命令返回的 ID 为准。

### 18.4 `event stream` 没退出

这是正常行为。SSE 是持续连接，用：

```text
Ctrl+C
```

退出监听。

### 18.5 Postgres 模式下 SSE 的边界

历史续传读取 `session_events`，所以 `--after` 可以跨重启使用。

实时推送目前使用当前 server 进程内的订阅中心。如果未来部署多个 API 进程，需要补 Postgres `LISTEN/NOTIFY` 或独立消息队列，才能让所有进程共享实时 fanout。
