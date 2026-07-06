# TMA 研发记录

本文档记录 Tiggy Manage Agent (TMA) 的阶段性研发决策、实现内容、验证结果和后续待办，方便之后回溯为什么这样设计。

最后更新：2026-07-06

---

## 当前结论

TMA 当前定位为一个 Postgres 持久化的 Agent Session / Event 管理服务。

核心闭环已经具备：

- Agent / Environment / Session 基础资源
- Session Event 历史查询
- SSE 历史续传和实时推送
- CLI 验证入口
- Postgres 持久化
- 可替换 Runner 层和异步 WorkerRunner
- 当前服务端固定使用 `AgentRuntimeTurnExecutor + agentruntime.DemoRuntime`
- `user.interrupt` 中断路径
- `turn_id` 标识一次用户消息对应的一次执行
- `session_turns` 持久化每次执行的生命周期状态
- JSON 结构化日志记录 session / turn / event 关键字段
- Capability Provider 能力接口骨架

当前刻意不保留生产代码里的 MemoryStore，避免同时维护两套状态机。单元测试使用 `_test.go` 内部 testStore，不进入正式构建。

---

## 关键设计决策

### 1. Event 表是事实源

`session_events` 是 Session 运行过程的事实源。

SSE 只是投递通道：

- 断线后用 `after_seq` 从 `session_events` 续传
- 实时推送从当前进程内订阅中心发送
- Postgres 模式下，历史事件可跨重启恢复

当前边界：

- 多 API 进程共享实时 SSE fanout 尚未实现
- 后续可用 Postgres `LISTEN/NOTIFY` 或消息队列解决

### 2. Store 只保留 PostgresStore

早期有 MemoryStore，用于快速开发和测试。

后来删除生产 MemoryStore，原因：

- 状态机已经包含异步执行、中断、turn_id，双 Store 容易分叉
- 目标产品需要持久化、审计、回放和续传
- Postgres 是正式路径，越早收敛越简单

现在：

- `cmd/server` 缺少 `TMA_DATABASE_URL` 会直接失败
- `make run` 默认使用本地 Postgres URL
- 单元测试用 `internal/httpapi/test_store_test.go`

### 3. 异步执行代替同步 mock

早期 `user.message` 会同步写入：

```text
session.status_running
user.message
agent.message
session.status_idle
```

这导致 `user.interrupt` 几乎没有成功窗口。

现在改为：

```text
POST user.message
  -> session.status_running
  -> user.message
  -> HTTP 立即返回

background MockRunner
  -> agent.message
  -> session.status_idle
```

中断路径：

```text
user.interrupt
session.status_interrupting
session.status_idle
```

### 4. turn_id 和 session_turns 串联一次执行

`turn_id` 标识一次用户消息触发的一次执行。

事件 payload 中仍保留 `turn_id`，方便 SSE 和事件历史直接回放：

```json
{
  "turn_id": "turn_000001"
}
```

同时，`session_turns` 表持久化一次执行的生命周期：

```text
running -> completed
running -> interrupted
running -> failed
```

同一次执行的事件都带同一个 `payload.turn_id`：

```text
session.status_running
user.message
agent.message
session.status_idle
```

中断时：

```text
user.interrupt
session.status_interrupting
session.status_idle
```

失败时：

```text
session.status_idle
```

失败原因会写入 idle 事件 payload 的 `reason`，并带上 `last_turn_status=failed`，同时保存到 `session_turns.error_message`。Session 自身回到 `idle`，避免普通 turn 失败阻塞后续对话。

保护逻辑：

- 后台 mock completion 必须匹配当前 running turn
- 如果 turn 已被 interrupt 带回 idle，后台 completion 不再补 `agent.message`
- `user.message` 创建 `session_turns` 记录
- mock completion 将 turn 标记为 `completed`
- `user.interrupt` 将 turn 标记为 `interrupted`
- Runner 启动或执行失败会通过 `FailSessionTurn` 将 turn 标记为 `failed`，并让 Session 回到 `idle`

---

## 已实现内容

### HTTP API

```text
GET  /health
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

### CLI

```text
bin/tma health
bin/tma agent create
bin/tma env create
bin/tma session create
bin/tma session get
bin/tma session archive
bin/tma session delete
bin/tma event send
bin/tma event interrupt
bin/tma event list
bin/tma event stream
```

### 数据库

迁移文件：

```text
sql/migrations/000001_init.sql
sql/migrations/000002_session_turns.sql
sql/migrations/000003_id_sequences.sql
```

当前表：

- `organizations`
- `workspaces`
- `agents`
- `agent_versions`
- `environments`
- `sessions`
- `session_events`
- `session_turns`

默认数据：

- `org_default`
- `wksp_default`

---

## 验证记录

常规验证：

```bash
make fmt
make test
make build
make build-cli
make verify-agent-runtime
make verify-agent-runtime-full
```

2026-07-06 配置层抽取后重新验证：

```text
make fmt                         pass
make test                        pass
make build                       pass
make build-cli                   pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000013
turn_id=turn_000001
```

2026-07-06 CommandTurnExecutor 协议版本化后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
protocol_version=tma.command.v1
session_id=sesn_000015
turn_id=turn_000001
```

2026-07-06 Capability Provider 能力层调整后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000016
turn_id=turn_000001
```

2026-07-06 Runner / TurnExecutor 概念重命名后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000017
turn_id=turn_000001
```

2026-07-06 Sandbox 从 turn-level executor 调整为 Provider 能力层后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000018
turn_id=turn_000001
```

2026-07-06 CommandTurnExecutor 底层统一到 LocalSystemProvider 后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000019
turn_id=turn_000001
```

2026-07-06 移除 `process` 执行器入口，统一为 `command` 后重新验证：

```text
make fmt                           pass
make test                          pass
make build                         pass
make build-cli                     pass
make verify-agent-runtime-full  pass
```

完整验收结果：

```text
session_id=sesn_000020
turn_id=turn_000001
```

2026-07-06 配置与 Provider 分层收口后重新验证：

```text
make fmt                           pass
make test                          pass
make build                         pass
make build-cli                     pass
make verify-agent-runtime-full  pass
```

本次收口：

```text
TMA_TURN_QUEUE_SIZE=16
TMA_TURN_TIMEOUT_MS=3600000
internal/capability.Provider
capability.LocalSystemProvider
```

完整验收结果：

```text
session_id=sesn_000021
turn_id=turn_000001
```

数据库验证：

```bash
make db-up
make migrate-up
make test-postgres
make run
```

手动验证过的关键路径：

- 创建 Agent / Environment / Session
- 创建 Session 后自动写入 provisioning / idle 事件
- `event list --after` 历史续传
- `event stream --after` SSE 续传
- `event send` 立即返回 running / user.message
- 后台 mock 自动补 agent.message / idle
- `event interrupt` 生成 user.interrupt / interrupting / idle
- 中断后的后台 mock 不会再补 agent.message
- 同一个执行链路中的事件保持相同 `payload.turn_id`
- 全局 Agent / Environment / Session / Event ID 使用 Postgres sequence 递增

实际 Postgres 验证样例：

```text
turn_000001
  seq=3  session.status_running
  seq=4  user.message
  seq=5  agent.message
  seq=6  session.status_idle

turn_000002
  seq=7   session.status_running
  seq=8   user.message
  seq=9   user.interrupt
  seq=10  session.status_interrupting
  seq=11  session.status_idle
```

ID sequence 迁移验证样例：

```text
迁移前最大 ID:
  agt_000003
  env_000003
  sesn_000003
  evt_000044

迁移后新建资源:
  agt_000004
  env_000004
  sesn_000004
  evt_000045+
```

---

## 当前边界和风险

### ID 生成方式已改为数据库 sequence

早期 `PostgresStore` 用 `count(*) + 1` 生成全局 ID，这不适合并发环境。

现在已改为 Postgres sequence：

```text
tma_agent_id_seq
tma_environment_id_seq
tma_session_id_seq
tma_event_id_seq
```

新增 migration：

```text
sql/migrations/000003_id_sequences.sql
```

该 migration 会根据已有数据把 sequence 对齐到当前最大 ID，避免存量数据库迁移后生成重复 ID。

`turn_id` 仍然是 Session 内编号，例如 `turn_000001`。它依赖同一个 Session 行的 `FOR UPDATE` 锁串行化生成，避免同一个 Session 并发创建重复 turn。

### Session 状态仍是单 running turn

当前一个 Session 同时只能 running 一个 turn。

这是合理的 P1 约束，但需要明确：

- 并发 `user.message` 会被拒绝
- 并发 interrupt / completion 依赖事务和当前状态判断
- 后续真实 Runner 要继续强化并发控制

### 实时 SSE 只支持单进程 fanout

历史续传没问题，因为读 Postgres。

实时推送目前只发给当前 server 进程内的订阅者。

多进程部署前需要：

- Postgres `LISTEN/NOTIFY`
- Redis Pub/Sub
- NATS / Kafka
- 或其他消息总线

### Runner 层已从 HTTP 抽出

HTTP 层现在只负责：

- 接收 `user.message` / `user.interrupt`
- 让 Store 先完成事件和状态落库
- 根据已落库事件调用 `runner.Runner`

Runner 接口位于：

```text
internal/runner
```

HTTP Server 通过 `NewServerWithStoreAndRunner` 接收可替换实现。当前不再提供默认 mock 构造函数，调用方必须显式传入 Runner。

早期默认实现是 `MockRunner`，只做：

- 延迟一小段时间
- 生成 mock agent.message payload
- 调用 Store 完成 turn
- 写入 idle
- turn 不匹配时放弃 completion
- 维护当前进程内 active turn registry
- 收到 interrupt 时取消对应后台 turn，避免继续尝试 completion
- 重复启动同一个 session/turn 会返回 `ErrTurnAlreadyRunning`
- Runner 启动失败时，HTTP 会调用 Store 的 `FailSessionTurn`，记录 `session.status_idle` 和失败原因

同时新增了 `WorkerRunner` 骨架：

- 内部队列接收 turn
- worker goroutine 调用 `TurnExecutor.RunTurn`
- `TurnExecutor` 成功时用返回的 payload 调 `CompleteSessionTurn`
- `TurnExecutor` 失败时调 `FailSessionTurn`
- `InterruptTurn` 会 cancel 正在执行的 TurnExecutor context
- `Close` 会停止接收并 cancel active turns

服务端运行时不再暴露 `mock|echo|command` 选择。真实启动固定走：

```text
cmd/server
  -> WorkerRunner
  -> CommandTurnExecutor
  -> capability.LocalSystemProvider.RunCommand
```

当前保留的运行时配置：

```text
TMA_TURN_QUEUE_SIZE=16
TMA_TURN_TIMEOUT_MS=3600000
```

`MockRunner` / `EchoExecutor` 只作为测试辅助和早期验证代码存在，不再作为服务端启动模式。

`CommandTurnExecutor` 接外部命令：

- stdin 输入 `session_id`、`turn_id`、`user_payload`
- stdout 输出 `agent.message` payload JSON
- 非 0 退出、超时、空 stdout、非 JSON stdout 都会进入 `FailSessionTurn`
- 内部通过 `capability.LocalSystemProvider.RunCommand` 执行命令，不再直接散落 `os/exec` 调用
- 示例脚本位于 `scripts/command_turn_echo.sh`
- 协议文档位于 `docs/command-turn-protocol.md`
- 排障和修正记录位于 `docs/troubleshooting.md`
- 配置总览位于 `docs/configuration.md`
- 服务端配置解析集中在 `internal/serverconfig`，并有单元测试覆盖默认值、`.env`、shell 优先级和非法配置

CommandTurnExecutor 协议已版本化：

- 当前版本固定为 `tma.command.v1`
- TMA 发送给外部命令的 stdin 会包含 `protocol_version`
- stdout payload 必须输出同一个 `protocol_version`
- stdout 缺少 `protocol_version` 会被拒绝
- stdout 带了非 `tma.command.v1` 的版本会被拒绝

能力方向已从 turn-level executor 调整为 Provider 能力层：

- 代码位于 `internal/capability/provider.go`
- 本地实现位于 `internal/capability/local.go`
- 设计文档位于 `docs/capability-provider.md`
- 当前协议版本为 `tma.capability.v1`
- `capability.Provider` 定义底层能力：`RunCommand`、`ExecuteCode`、`ReadFile`、`WriteFile`
- `capability.LocalSystemProvider` 已实现本地命令执行、代码执行、文件读写
- `RequestMeta` 负责携带 `session_id`、`turn_id`、`deadline`
- 当前不引入 `ToolManifest` / `ToolRegistry` / `ToolExecutor`
- 当前不把 local system / cloud sandbox 暴露成 turn mode
- 未来 AgentRuntime / Tool Calling 成形后，再把具体 Provider 包装成 builtin tools

Runner / TurnExecutor 概念已收敛：

- `Runner` 管 turn 生命周期：启动、排队、中断、取消、成功/失败状态回写
- `TurnExecutor` 管 turn 的具体执行：输入 `TurnRequest`，输出 `agent.message` payload 或错误
- `WorkerRunner` 是 `Runner`
- `AgentRuntimeTurnExecutor` 是当前服务端默认运行时 `TurnExecutor`
- `CommandTurnExecutor` 保留为外部进程协议适配器
- `EchoExecutor` 仅保留为测试/验证用 `TurnExecutor`
- 不再保留 `TMA_TURN_MODE`，也不再保留 `process` 模式

2026-07-06 命名再次收口：

- 不把 `LocalSystemProvider` 当作 turn executor 名称；它只表示本机能力 Provider
- 原有命令执行类型改为 `CommandTurnExecutor`，明确它是一次 turn 的适配器
- 用户侧配置曾短暂改为 `TMA_TURN_COMMAND` / `TMA_TURN_COMMAND_ARGS` / `TMA_TURN_COMMAND_TIMEOUT_MS`
- 验证脚本改为 `scripts/command_turn_echo.sh`、`scripts/verify_agent_runtime.sh`
- 验收目标改为 `make verify-agent-runtime` / `make verify-agent-runtime-full`
- 协议文档改为 `docs/command-turn-protocol.md`
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000022`，`turn_id=turn_000001`

2026-07-06 运行模式再次收口：

- 删除运行时 `TMA_TURN_MODE`
- 当时 `cmd/server` 固定组装 `WorkerRunner + CommandTurnExecutor`
- HTTP server 构造函数不再默认注入 `MockRunner`，必须显式传入 Runner
- `.env.example` 改为可运行的 command turn demo 配置
- `MockRunner` / `EchoExecutor` 不再出现在真实启动文档中，只保留给测试和历史验证
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000023`，`turn_id=turn_000001`

2026-07-06 command 配置再次收口：

- 删除 `TMA_TURN_COMMAND` / `TMA_TURN_COMMAND_ARGS`
- 删除 `TMA_TURN_COMMAND_TIMEOUT_MS`，改为通用 `TMA_TURN_TIMEOUT_MS`
- `cmd/server` 内部暂时固定 demo command turn：`sh scripts/command_turn_echo.sh`
- 用户侧不再需要理解 demo 脚本的启动细节
- 未来接真实 AgentRuntime 时，再以更明确的一等配置替换 demo command turn
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000024`，`turn_id=turn_000001`

2026-07-06 turn 超时默认值调整：

- `TMA_TURN_TIMEOUT_MS` 默认值先从 `30000` 调整为 `1800000`，随后调整为 `3600000`
- 该超时表示整次 turn 的兜底保护，不是单条轻量命令超时
- 真实智能体可能执行依赖安装、构建、测试、仓库检索等长任务，短超时容易误杀
- 用户主动停止应使用 interrupt，而不是依赖短超时
- 超时后当前 turn 会进入 `failed`，Session 回到 `idle`，可以继续下一条消息
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000025`，`turn_id=turn_000001`

2026-07-06 AgentRuntime 雏形接入：

- 新增 `internal/agentruntime.Runtime`
- 新增 `agentruntime.DemoRuntime`，用于替代内置 command demo 脚本作为服务端默认执行路径
- 新增 `runner.AgentRuntimeTurnExecutor`
- `cmd/server` 改为组装 `WorkerRunner + AgentRuntimeTurnExecutor + agentruntime.DemoRuntime`
- `CommandTurnExecutor` 不再是默认 server path，仅保留为外部进程协议适配器
- 新增设计文档 `docs/agent-runtime.md`
- 验收脚本改为 `scripts/verify_agent_runtime.sh` / `scripts/verify_agent_runtime_full.sh`
- Make target 改为 `make verify-agent-runtime` / `make verify-agent-runtime-full`
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000026`，`turn_id=turn_000001`

2026-07-06 Runtime step 事件接入：

- 新增 runtime 事件类型：`runtime.started`、`runtime.thinking`、`runtime.tool_call`、`runtime.tool_result`、`runtime.completed`、`runtime.failed`
- 新增 `Store.AppendRuntimeEvent`
- PostgresStore 写 runtime 事件时会校验 Session 仍是 `running` 且 turn_id 是当前 turn
- 中断或完成后的旧 runtime step 不会再补写
- `DemoRuntime` 当前会写入 `runtime.started`、`runtime.thinking`、`runtime.completed`
- `AgentRuntimeTurnExecutor` 在 Runtime 报错时会尽量写入 `runtime.failed`
- `make verify-agent-runtime-full` 已校验事件链路包含 runtime step
- 完整验收通过：`session_id=sesn_000027`，`turn_id=turn_000001`

2026-07-06 LLM Client 边界接入：

- 新增 `internal/llm.Client`
- 新增 `llm.Request`、`llm.Response`、`llm.Message`、`llm.ContentPart`
- 新增 `llm.FakeClient`，不调用外部模型，只返回确定性 assistant message
- `agentruntime.DemoRuntime` 改为通过 `llm.Client.Generate` 生成回复
- 新增 runtime 事件：`runtime.llm_request`、`runtime.llm_response`
- 当前仍不引入 API key、模型厂商 SDK 或真实网络调用
- `make verify-agent-runtime-full` 已校验事件链路包含 `runtime.llm_request` / `runtime.llm_response`
- 完整验收通过：`session_id=sesn_000028`，`turn_id=turn_000001`

配置层已从 `cmd/server/main.go` 抽到 `internal/serverconfig`：

- `cmd/server` 只负责组装 logger、Store、Runner 和 HTTP server
- `serverconfig.Load(".env")` 统一处理 `.env` 和 shell 环境变量
- `.env` 只补缺省值，不覆盖 shell 中已有配置
- `command` 相关配置在启动前校验，避免 server 运行后才暴露明显配置错误

Store 边界也同步收窄：`CompleteSessionTurn` 不再生成 mock 回复，只负责把 Runner 产出的 `agent.message` payload 落库，并补齐 `turn_id`。

后续真实 Runner 需要接：

- Sandbox
- 模型调用
- 工具调用
- 日志流
- 中断信号传播
- 超时和失败状态

---

## 日志约定

服务端使用 Go 标准库 `slog` 输出 JSON 日志。

关键字段：

```text
session_id
turn_id
event_id
event_seq
event_type
after_seq
history_events
```

目前覆盖：

- server 启动和停止
- PostgresStore 初始化
- HTTP append events 成功后逐条 event 记录
- mock turn scheduled / completed / skipped / failed
- mock completion 写出的 agent / idle 事件
- runner start failure 写出的 failed 事件
- SSE stream opened / closed

---

## 下一步建议

优先级建议：

1. 实现真实 Runner
   - `StartTurn(ctx, TurnRequest)`
   - `InterruptTurn(ctx, InterruptRequest)`
   - 给 `WorkerRunner` 接 Sandbox / Agent Runtime TurnExecutor
   - 真实执行失败时调用 `FailSessionTurn`

2. 增加 Postgres 集成测试
   - 用环境变量控制是否运行
   - 不影响普通 `make test`

---

## 常用命令索引

```bash
make fmt
make test
make build
make build-cli
```

```bash
make db-up
make migrate-up
make run
make db-down
```

```bash
bin/tma health
bin/tma event list --session sesn_000001 --after 0
bin/tma event stream --session sesn_000001 --after 0
```

更完整的手动验收命令见：

```text
TESTING.md
```
