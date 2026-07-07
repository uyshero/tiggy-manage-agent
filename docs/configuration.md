# TMA Configuration

本文档集中说明 TMA 当前使用的环境变量和典型配置场景。

服务端启动时会读取项目根目录下的 `.env`。如果同名环境变量已经在 shell 中设置，shell 中的值优先，`.env` 不会覆盖。

注意：`.env` 不是 shell 脚本，不支持反斜杠续行。不要写：

```env
TMA_TURN_TIMEOUT_MS=3600000 \
```

应写成单行：

```env
TMA_TURN_TIMEOUT_MS=3600000
```

## Server

### `TMA_HTTP_ADDR`

HTTP 服务监听地址。

默认：

```env
TMA_HTTP_ADDR=:8080
```

### `TMA_DATABASE_URL`

Postgres 连接字符串。服务端必需。

默认本地开发值：

```env
TMA_DATABASE_URL=postgres://tma:tma@localhost:5432/tma?sslmode=disable
```

## CLI

### `TMA_BASE_URL`

CLI 请求的 API 地址。

默认：

```env
TMA_BASE_URL=http://localhost:8080
```

示例：

```bash
TMA_BASE_URL=http://localhost:18080 bin/tma health
```

## Turn

### `TMA_TURN_QUEUE_SIZE`

异步 turn 队列大小。

默认：

```env
TMA_TURN_QUEUE_SIZE=16
```

服务端固定使用 `WorkerRunner + AgentRuntimeTurnExecutor`，该队列用于控制待执行 turn 的缓冲大小。

### `TMA_TURN_TIMEOUT_MS`

单次 turn 的超时时间。它保护的是“一轮用户消息到最终 agent.message”的整体执行时间，不是单个轻量命令的超时。

默认：

```env
TMA_TURN_TIMEOUT_MS=3600000
```

默认值是 1 小时，给安装依赖、构建、运行测试、仓库检索等长任务留出空间。需要提前停止时应优先使用 interrupt。

超时后，当前 turn 会被取消并标记为 `failed`，Session 会回到 `idle`，后续仍可继续发送新的 `user.message`。

当前服务端内置 `agentruntime.DemoRuntime`。AgentRuntime 设计见 [agent-runtime.md](./agent-runtime.md)。

## LLM

### `TMA_LLM_PROVIDER`

当前默认模型 Provider。

默认：

```env
TMA_LLM_PROVIDER=fake
```

当前内置：

```text
fake
openai-compatible
```

`fake` 不访问外部模型 API，只用于把 HTTP / Store / Runner / AgentRuntime / LLM Client 这条链路跑通。

`openai-compatible` 调用 OpenAI Chat Completions 兼容接口，适用于 OpenAI 或企业内部兼容网关。

当前 `openai-compatible` 使用 `stream: true` 读取 SSE 文本增量，服务端会把增量写成 `runtime.llm_delta` 事件；最终仍会写一条完整 `agent.message`。

这个配置项先作为默认 Provider 选择入口保留。服务启动时会把它 upsert 到 `llm_providers` 表；创建 Agent 时，如果请求没有传 `llm_provider`，HTTP 层会用它补齐 AgentConfigVersion。

`TMA_LLM_PROVIDER` 可以是内置 Provider ID，也可以是业务自定义 Provider ID。例如：

```env
TMA_LLM_PROVIDER=volcengine-agent-plan
TMA_LLM_PROVIDER_TYPE=openai
```

这里 `volcengine-agent-plan` 是 TMA 内部保存和展示的 Provider ID，`openai` 是底层协议类型。自定义 Provider ID 如果没有显式设置 `TMA_LLM_PROVIDER_TYPE`，当前会默认按 `openai` 注册。

后续增加 Anthropic、本地模型或企业内部网关时，可以继续扩展 Provider Type。执行 turn 时，Runtime 会按 Session 绑定的 AgentConfigVersion 找到 Provider ID，再从 `llm_providers` 解析底层协议、Base URL 和 API Key 环境变量名。

Provider 也可以通过 HTTP / CLI 管理：

```bash
bin/tma provider list
bin/tma provider create --id volcengine-agent-plan --type openai --base-url https://ark.cn-beijing.volces.com/api/plan/v3 --api-key-env TMA_LLM_API_KEY
bin/tma provider disable --id volcengine-agent-plan
bin/tma provider enable --id volcengine-agent-plan
```

`.env` 里的 Provider 配置只用于服务启动时保证默认 Provider 存在；正式运行中，AgentConfigVersion 绑定的是 `llm_providers.id`。

Provider 长期设计、模型能力和 token usage 审计路线见 [llm-provider-roadmap.md](./llm-provider-roadmap.md)。

### `TMA_LLM_PROVIDER_TYPE`

Provider 底层协议类型。

当前支持：

```text
openai
openai-compatible
```

推荐使用 `openai`。`openai-compatible` 作为历史别名保留兼容。

内置 Provider `fake` 不需要配置 provider type。自定义 Provider ID 建议显式配置：

```env
TMA_LLM_PROVIDER=volcengine-agent-plan
TMA_LLM_PROVIDER_TYPE=openai
```

### `TMA_LLM_MODEL`

当前默认模型名。

默认：

```env
TMA_LLM_MODEL=fake-demo
```

`TMA_LLM_MODEL` 由当前 Provider 解释。对于 `fake` Provider，它只是运行时事件和调试日志里的模型标识。创建 Agent 时，如果请求没有传 `llm_model` 或兼容字段 `model`，HTTP 层会用它补齐 AgentConfigVersion。

热切换设计说明：当前服务端已经通过 `llm.Manager` 间接调用模型。未来即使增加运行时切换 API，也不需要重组 `WorkerRunner` 或 `AgentRuntimeTurnExecutor`；后续 turn 会读取 Session 绑定的 AgentConfigVersion，或读取切换后的默认配置创建新 Agent。

### `TMA_LLM_BASE_URL`

OpenAI-compatible 接口地址。

默认：

```env
TMA_LLM_BASE_URL=https://api.openai.com/v1
```

使用 OpenAI 协议 Provider 时会使用它。请求路径会拼成：

```text
{TMA_LLM_BASE_URL}/chat/completions
```

### `TMA_LLM_API_KEY_ENV`

OpenAI-compatible API Key 的环境变量名。

默认：

```env
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY
```

TMA 会把这个变量名写入 `llm_providers.api_key_env`，执行 turn 时再从进程环境变量读取真实 API Key。这样数据库只保存密钥引用，不保存真实密钥。

例如可以给不同 Provider 准备不同变量：

```env
TMA_LLM_PROVIDER=volcengine-agent-plan
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY_VOLCENGINE
TMA_LLM_API_KEY_VOLCENGINE=...
```

### `TMA_LLM_API_KEY`

OpenAI-compatible API Key。

示例：

```env
TMA_LLM_API_KEY=sk-...
```

如果使用 OpenAI 协议 Provider，该配置必填，除非你把 `TMA_LLM_API_KEY_ENV` 指向了其他环境变量。当前不会把 API Key 存入数据库，也不会返回给客户端。

### OpenAI-compatible 示例

```env
TMA_LLM_PROVIDER=volcengine-agent-plan
TMA_LLM_PROVIDER_TYPE=openai
TMA_LLM_MODEL=gpt-4o-mini
TMA_LLM_BASE_URL=https://api.openai.com/v1
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY
TMA_LLM_API_KEY=sk-...
```

创建 Agent 时也可以显式指定：

```bash
bin/tma agent create \
  --name "Code Assistant" \
  --llm-provider volcengine-agent-plan \
  --llm-model gpt-4o-mini \
  --system "You are a coding agent."
```

## Tests

### `TMA_RUN_POSTGRES_TESTS`

是否运行 Postgres 集成测试。

默认不运行。显式启用：

```bash
TMA_RUN_POSTGRES_TESTS=1 TMA_DATABASE_URL=postgres://tma:tma@localhost:5432/tma?sslmode=disable go test ./internal/managedagents -run Postgres
```

推荐使用：

```bash
make test-postgres
```

## Verification Scripts

### `make verify-llm-provider`

验证当前 `.env` 或 shell 环境中配置的真实 LLM Provider。

```bash
make verify-llm-provider
```

它会启动临时服务，创建 Agent / Environment / Session，发送一条测试消息，并检查：

```text
runtime.llm_request
runtime.llm_response
agent.message
```

如果 Provider 返回流式增量，会同时统计 `runtime.llm_delta` 数量。该命令不会打印 API Key。

### `TMA_CLI`

验收脚本使用的 CLI 路径。

默认：

```env
TMA_CLI=bin/tma
```

### `TMA_VERIFY_MESSAGE`

`scripts/verify_agent_runtime.sh` 发送的测试消息。

默认：

```env
TMA_VERIFY_MESSAGE=agent runtime verify
```

### `TMA_VERIFY_EXPECTED_TEXT`

`scripts/verify_agent_runtime.sh` 期望的 `agent.message` 文本。

默认：

```env
TMA_VERIFY_EXPECTED_TEXT=Agent runtime received: agent runtime verify
```

### `TMA_VERIFY_EXPECTED_PROTOCOL`

`scripts/verify_agent_runtime.sh` 期望的 `agent.message.payload.protocol_version`。

默认：

```env
TMA_VERIFY_EXPECTED_PROTOCOL=tma.agent_runtime.demo.v1
```

如果临时调试时需要跳过协议版本断言，可以设为空字符串：

```bash
TMA_VERIFY_EXPECTED_PROTOCOL= make verify-agent-runtime
```

### `TMA_VERIFY_WAIT_SECONDS`

等待后台 `agent.message` 的秒数。

默认：

```env
TMA_VERIFY_WAIT_SECONDS=10
```

### `TMA_VERIFY_BASE_URL`

`scripts/verify_agent_runtime_full.sh` 启动临时服务后用于验收的 API 地址。

默认：

```env
TMA_VERIFY_BASE_URL=http://localhost:18080
```

### `TMA_VERIFY_HTTP_ADDR`

`scripts/verify_agent_runtime_full.sh` 启动临时服务时使用的监听地址。

默认：

```env
TMA_VERIFY_HTTP_ADDR=:18080
```

### `TMA_SERVER_BIN`

自启动验收脚本使用的 server 二进制。

默认：

```env
TMA_SERVER_BIN=bin/tma-server
```

### `TMA_VERIFY_SERVER_LOG`

自启动验收脚本写入的 server 日志文件。

默认：

```env
TMA_VERIFY_SERVER_LOG=.verify-agent-runtime-server.log
```

### `TMA_VERIFY_SERVER_WAIT_SECONDS`

自启动验收脚本等待临时 server `/health` 成功的秒数。

默认：

```env
TMA_VERIFY_SERVER_WAIT_SECONDS=20
```

## Common Scenarios

### 本地 AgentRuntime 开发

```env
TMA_HTTP_ADDR=:8080
TMA_DATABASE_URL=postgres://tma:tma@localhost:5432/tma?sslmode=disable
TMA_TURN_QUEUE_SIZE=16
TMA_TURN_TIMEOUT_MS=3600000
```

启动：

```bash
make run
```

### 临时覆盖 turn 超时

```bash
TMA_TURN_TIMEOUT_MS=3600000 \
make run
```

### Postgres 集成测试

```bash
make db-up
make migrate-up
make test-postgres
```

### AgentRuntime 完整自启动验收

```bash
make verify-agent-runtime-full
```
