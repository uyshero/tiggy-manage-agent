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
