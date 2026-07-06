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

它会自动创建 Agent / Environment / Session，发送一条消息，并检查事件中包含：

- `session.status_running`
- `user.message`
- `runtime.started`
- `runtime.thinking`
- `runtime.llm_request`
- `runtime.llm_response`
- `runtime.completed`
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
