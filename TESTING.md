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
make build
make build-cli
```

Postgres 集成测试默认跳过。启动数据库并迁移后，可以显式运行：

```bash
make db-up
make migrate-up
make test-postgres
```

当前覆盖：

- `user.message -> CompleteSessionTurn`：turn `completed`，Session 回到 `idle`
- `user.message -> user.interrupt -> late CompleteSessionTurn`：turn `interrupted`，不补 `agent.message`
- `user.message -> FailSessionTurn`：turn `failed`，Session 回到 `idle`，可继续发送下一条消息
- 原生 `tools` / `tool_calls` 与 `tma.tool_call.v1` fallback
- Session 级 `intervention_mode`
- CLI `event stream` 对审批事件的可读展示

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

启动服务。当前服务端使用内置 `agentruntime.DemoRuntime`：

```bash
make run
```

此模式下 `agent.message` 通常返回：

```text
Agent runtime received: <你的文本>
```

AgentRuntime 设计见 [docs/agent-runtime.md](./docs/agent-runtime.md)。

常见误配置和修正记录见 [docs/troubleshooting.md](./docs/troubleshooting.md)。

服务启动后，可在另一个终端跑自动验收：

```bash
make verify-agent-runtime
```

也可以使用自启动完整验收，它会自动启动数据库、迁移、启动临时服务、验收、停止服务：

```bash
make verify-agent-runtime-full
```

`verify-agent-runtime-full` 会显式使用 `fake` Provider，适合检查基础 HTTP / Store / Runner / Runtime 链路，不受本地真实模型配置影响。

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
bin/tma session intervention list --session sesn_000001 --status pending
bin/tma session intervention approve --session sesn_000001 --turn turn_000003 --call call_edit --reason "looks safe"
bin/tma session intervention reject --session sesn_000001 --turn turn_000003 --call call_edit --reason "not this time"
```

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
```

启动服务时会把默认 Provider 写入 `llm_providers`，数据库只保存 `TMA_LLM_API_KEY_ENV` 指向的变量名，不保存真实 API Key。然后脚本创建 Agent / Environment / Session，发送一条消息，并检查真实模型链路返回了非空 `agent.message`。如果 Provider 支持流式输出，结果会显示 `runtime.llm_delta` 数量。
- `agent.message`
- `session.status_idle`

同时校验 `agent.message` 文本为：

```text
Agent runtime received: agent runtime verify
```

并校验 `agent.message` payload 中包含当前协议版本：

```text
protocol_version = tma.agent_runtime.demo.v1
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

预期立即补发历史事件，例如：

```text
id: evt_000001
event: session.status_provisioning
data: {..."seq":1...}

id: evt_000002
event: session.status_idle
data: {..."seq":2...}

: stream ready
```

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
event: session.status_running
event: user.message
```

如果当前 Session 已设置：

```bash
bin/tma session runtime update --session sesn_000001 --intervention-mode request_approval
```

当模型命中敏感工具时，`bin/tma event stream --session sesn_000001 --after 0` 当前会把审批事件和工具结果事件格式化为更易读的提示，而不是原始 SSE JSON。例如：

```text
approval required
  seq: 12
  turn: turn_000003
  call: call_edit
  tool: tma.local_system.edit_file
  mode: request_approval
  message: Tool call requires approval before execution.
  policy: optional
```

当前这一步只验证“可见性”和“拦截”：

- `request_approval`：敏感工具不执行，事件流中出现 `approval required`
- `approve_for_me`：会出现 `runtime.tool_intervention_approved`，随后继续执行
- `full_access`：直接执行，不出现审批提示

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

在 `bin/tma event stream` 中，批准、拒绝和工具结果会分别显示为 `approval approved`、`approval rejected`、`tool result`。

当前 approve/reject 是非交互式决策入口：`request_approval` 会把 turn 标记为 `waiting_approval`，不会生成 pending 后的临时 `agent.message`。approve 会执行被批准的工具并回写结果事件；如果 pending 记录中带有 continuation messages，还会把 tool result 送回 LLM，并继续最多 4 轮 tool loop。续跑中再次遇到敏感工具会再次 pending / waiting approval；模型返回普通文本时生成最终 `agent.message`，并让 session 回到 `idle`。reject 会写 rejected 事件并 fail turn，不会把拒绝原因喂回模型继续推理。长期未审批时记录保持 `pending` / `waiting_approval`，不会自动 approve / reject / expire。

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

## 11. 使用 curl 验证 SSE

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

## 12. 测试 Interrupt

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

## 13. Archive Session

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

## 14. Delete Session

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

## 15. 数据库验证

数据库模式不需要本机安装 `psql`。迁移命令会使用 Postgres 容器里的 `psql`。

启动 Postgres：

```bash
make db-up
```

应用初始 schema：

```bash
make migrate-up
```

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

## 16. 常见问题

### 16.1 `psql: command not found`

不要直接运行：

```bash
psql "postgres://tma:tma@localhost:5432/tma?sslmode=disable" -f sql/migrations/000001_init.sql
```

请运行：

```bash
make migrate-up
```

这个命令会进入 Docker 里的 Postgres 容器执行 `psql`。

### 16.2 连接失败

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

### 16.3 ID 不对

Postgres 会保留历史数据，ID 会继续递增。以实际命令返回的 ID 为准。

### 16.4 `event stream` 没退出

这是正常行为。SSE 是持续连接，用：

```text
Ctrl+C
```

退出监听。

### 16.5 Postgres 模式下 SSE 的边界

历史续传读取 `session_events`，所以 `--after` 可以跨重启使用。

实时推送目前使用当前 server 进程内的订阅中心。如果未来部署多个 API 进程，需要补 Postgres `LISTEN/NOTIFY` 或独立消息队列，才能让所有进程共享实时 fanout。
