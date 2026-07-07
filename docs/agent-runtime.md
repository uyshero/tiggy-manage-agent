# TMA Agent Runtime Design

本文档记录当前 AgentRuntime 雏形。它的目标是把“一轮用户消息如何变成 agent.message”从 Runner 和 HTTP 层里拆出来。

## 当前分层

```text
HTTP API
  -> Store
  -> WorkerRunner
  -> AgentRuntimeTurnExecutor
  -> agentruntime.Runtime
  -> agentruntime.ContextBuilder
  -> llm.Manager
  -> llm.Client
```

职责边界：

- `WorkerRunner`：排队、取消、中断、成功/失败状态回写。
- `AgentRuntimeTurnExecutor`：把 Runner 的 `TurnRequest` 适配到 AgentRuntime，并按 Session 解析 `AgentConfigVersion`，再把 Runtime usage 组装成可落库的 LLM usage 记录。
- `agentruntime.Runtime`：执行一轮智能体逻辑，返回 `agent.message` payload 和归一化后的 LLM usage。
- `agentruntime.ContextBuilder`：把 system、历史消息和当前 user payload 组装成 LLM messages。
- `llm.Manager`：模型 Provider 注册和默认 Provider / Model 管理入口；每次请求也可以指定 Agent 配置里的 Provider / Model。
- `llm.Client`：模型调用边界；当前默认使用 FakeClient，不访问外部 API。
- `internal/tools`：内置工具 manifest、registry 和 executor；当前内置 `tma.local_system`。
- `capability.Provider`：提供底层命令、代码、文件等能力，由 tools executor 调用。

## 当前实现

代码位置：

```text
internal/agentruntime
```

当前接口：

```go
type Runtime interface {
	RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error)
}

type TurnResult struct {
	AgentPayload json.RawMessage
	Usage        llm.Usage
	Provider     string
	ProviderType string
	Model        string
}
```

当前默认实现是 `DemoRuntime`。它通过 `llm.Client` 生成一个可验证的 `agent.message` payload。服务端默认注入 `llm.Manager`，Manager 当前内置 `fake` 和 `openai-compatible` Provider。当前 `agent.message` payload 仍使用 demo 协议：

```json
{
  "protocol_version": "tma.agent_runtime.demo.v1",
  "content": [
    {
      "type": "text",
      "text": "Agent runtime received: hello"
    }
  ]
}
```

同时它会写入三类 runtime step 事件：

```text
runtime.started
runtime.thinking
runtime.llm_request
runtime.llm_delta
runtime.llm_response
runtime.tool_call
runtime.tool_result
runtime.completed
```

`runtime.llm_delta` 只在 LLM client 支持流式输出时出现。默认 `fake` Provider 不会写 delta；`openai-compatible` 会解析 Chat Completions SSE，并把每段文本写成 delta 事件。

当前 `DemoRuntime` 已有最小 tool loop：Runtime 会把 `internal/tools.Registry.ModelTools()` 生成的函数 schema 放进 `llm.Request.Tools`。`openai-compatible` 会把它转换成 Chat Completions `tools`，并把响应里的原生 `tool_calls` 解析回 `llm.Message.ToolCalls`。Runtime 随后调用 `internal/tools.Executor`，记录 `runtime.tool_call` / `runtime.tool_result`，再把工具结果作为带 `tool_call_id` 的 `tool` role message 送回模型继续本轮回复。

作为兼容路径，如果模型返回一个文本 JSON envelope，且其中包含 `tool_calls`，Runtime 也会按同一套 executor 执行。这个 envelope 是 TMA 内部稳定协议，用来把模型输出、内置工具 registry 和底层 capability provider 解耦，并兼容尚未支持厂商原生 function calling 的 Provider。

当前工具调用 envelope 版本是 `tma.tool_call.v1`：

```json
{
  "protocol_version": "tma.tool_call.v1",
  "tool_calls": [
    {
      "id": "call_1",
      "type": "function",
      "function": {
        "name": "tma.local_system.run_command",
        "arguments": {
          "command": "sh",
          "args": ["-c", "pwd"]
        }
      }
    }
  ]
}
```

`function.name` 使用 `<tool_identifier>.<api_name>` 格式，例如 `tma.local_system.run_command`。Runtime 当前仍兼容旧的 `tma.agent_runtime.demo.v1` 工具调用 envelope，便于已有测试和历史会话逐步迁移。

## 多轮上下文

一次 turn 的 Runtime 输入包含：

```text
session_id
turn_id
history
user_payload
AgentConfigVersion
```

`user_payload` 来自触发 turn 的当前 `user.message`。`history` 来自同一个 Session 中当前 `user.message` 之前的对话消息，只包含：

```text
user.message  -> user
agent.message -> assistant
```

Runtime 构造 LLM messages 的顺序是：

```text
system
summary
tools / skills
历史 user / assistant
当前 user
```

这样第二轮及后续对话能看到前面已完成的 user / agent 消息；当前正在处理的 `user.message` 不会从历史里重复注入。

这部分逻辑已从 `DemoRuntime` 抽到 `DefaultContextBuilder`：

```go
type ContextBuilder interface {
	Build(request ContextBuildRequest) (ContextBuildResult, error)
}
```

当前基础规则：

- system 非空时放在第一条。
- session summary 非空时放在 system 后面。
- `tools.Registry.ModelContext()` 生成的工具 manifest 非空时作为 system 级上下文注入；当前默认包含 `tma.local_system`。
- AgentConfigVersion 的 `skills` 非空时作为 system 级上下文注入。
- 历史只接收 `user` / `assistant` 角色。
- 历史空文本会跳过。
- 当前 user message 总是追加到最后。
- 每个模型用 `context_window_tokens` 表示总上下文窗口；未配置时默认 128000。
- Context Builder 固定最多使用总窗口的 60% 作为输入上下文预算。

截断规则：

- system、summary 和当前 user message 是保底消息，不会被删除。
- history 从最新到最旧尝试纳入预算，最终仍按原始时间顺序排列。
- token 数当前是近似估算：每条 message 有固定开销，文本按约 4 个字符 1 token 估算。
- `runtime.llm_request` step 会记录 `history_count`、`omitted_history_count`、`estimated_token_count`、`context_truncated` 和 `summary_included`。

Summary 当前有两种入口。

自动 summary 使用 just-in-time compaction：下一轮 `user.message` 触发 session 从 `idle` 进入 `running` 后，`ContextBuilder` 如果发现历史超过输入预算并发生截断，Runtime 会先在本轮 `running` 内生成 summary，写入 `session_summaries`，过滤已被 summary 覆盖的历史，再重建上下文并继续原本的 LLM 回复流程。自动 summary 不切换 session 状态，只写 runtime 事件：

```text
runtime.context_compacting
runtime.context_compacted
```

手动写入入口：

```text
PUT /v1/sessions/{session_id}/summary
GET /v1/sessions/{session_id}/summary
```

CLI：

```bash
bin/tma session summary upsert --session sesn_000001 --text "..." --until 12
bin/tma session summary get --session sesn_000001
```

写入 summary 时，Session 必须处于 `idle`，Store 会写出：

```text
session.status_compacting
session.status_idle
```

后续真实 tokenizer、多模态消息和更细粒度的 compaction 策略都应该继续扩展 Context Builder / Runtime 边界，而不是塞回 Runner 或 HTTP 层。

如果 Runtime 返回错误，`AgentRuntimeTurnExecutor` 会尽量先写入：

```text
runtime.failed
```

这些事件都会带同一个 `payload.turn_id`。如果用户已经 interrupt，Store 会拒绝旧 turn 继续补 runtime 事件。

## Usage 审计

当前 usage 链路是：

```text
llm.Client
  -> agentruntime.TurnResult.Usage
  -> AgentRuntimeTurnExecutor
  -> runner.TurnResult.Usage
  -> WorkerRunner
  -> Store.RecordLLMUsage
  -> llm_usage_records
```

分工：

- Provider / Client 负责把厂商返回的 token usage 归一化成 `llm.Usage`。
- Runtime 不直接写数据库，只把本轮模型调用结果带回。
- `AgentRuntimeTurnExecutor` 用 Session 固定的 workspace / agent / AgentConfigVersion / provider / model 补齐审计维度。
- `WorkerRunner` 在 turn 成功完成后写 `status=completed` usage；如果执行器返回错误但同时带回 usage，则写 `status=failed` usage。

失败 usage 的边界：

- 模型调用没有发生，或无法拿到 usage：不写 usage。
- 模型调用已经发生，后续 runtime step / tool loop / payload 处理失败：写 `status=failed`，并记录 `error_message`。
- 用户 interrupt / context cancel：当前不补 failed usage，避免把主动取消误算成失败。

`openai-compatible` 已支持非流式响应里的 `usage`，也会在流式请求中发送 `stream_options.include_usage=true`，并解析最后 chunk 的 usage。Fake Provider 会产生 0 token usage，但仍能验证审计链路。

Session 维度查询入口：

```text
GET /v1/sessions/{session_id}/usage
```

返回内容包含：

- `summary`：当前 Session 的总 input / output / total / cached / reasoning tokens，以及总 latency。
- `records`：每个 turn 的 usage 明细，包含 provider / model / status。

CLI：

```bash
bin/tma usage list --session sesn_000001
```

跨 Session 聚合入口：

```text
GET /v1/llm-usage
```

支持 query：

```text
group_by=provider|model|provider_model
workspace_id=wksp_default
provider_id=fake
model=fake-demo
status=completed
from=2026-07-07T00:00:00+08:00
to=2026-07-08T00:00:00+08:00
```

CLI：

```bash
bin/tma usage summary --group-by provider
bin/tma usage summary --provider volcengine-agent-plan --group-by model
```

## 为什么先做 Runtime

之前的 `CommandTurnExecutor` 能验证外部进程协议，但它不是智能体运行时本身。继续把 demo 脚本作为服务端默认路径，会让用户误以为真实运行方式就是配置 shell 命令。

现在改为：

```text
cmd/server
  -> WorkerRunner
  -> AgentRuntimeTurnExecutor
  -> agentruntime.DemoRuntime
      -> llm.Manager
      -> llm.FakeClient
```

这样后续接真实模型时，可以先新增 Provider 并注册到 `llm.Manager`；如果要加入 tool loop，再扩展 `agentruntime.Runtime`。

## AgentConfigVersion

`AgentConfigVersion` 表达的是“Agent 配置版本”，不是运行状态。它保存：

```text
llm_provider
llm_model
system
tools
skills
```

`Session` 创建时绑定 `agent_id + agent_config_version`。执行 turn 时，`AgentRuntimeTurnExecutor` 会通过 `session_id` 解析这份配置，然后传给 `DemoRuntime`。这样 Agent 后续升级配置时，旧 Session 仍然可以按创建时的配置快照运行和回溯。

配置更新不会覆盖旧版本，而是创建新的 AgentConfigVersion，并把 Agent 的 `current_config_version` 指向新版本：

```bash
bin/tma agent get --id agt_000001
bin/tma agent config list --agent agt_000001
bin/tma agent config update --agent agt_000001 --llm-model doubao-seed-2.0-pro --system "You are concise."
```

新建 Session 会绑定更新后的当前版本；已存在的 Session 继续绑定创建时的版本。

## LLM Provider 配置

当前配置项：

```env
TMA_LLM_PROVIDER=fake
TMA_LLM_MODEL=fake-demo
# TMA_LLM_PROVIDER=volcengine-agent-plan
# TMA_LLM_PROVIDER_TYPE=openai
# TMA_LLM_MODEL=gpt-4o-mini
# TMA_LLM_BASE_URL=https://api.openai.com/v1
# TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY
# TMA_LLM_API_KEY=sk-...
```

`TMA_LLM_PROVIDER` / `TMA_LLM_MODEL` 是服务默认值。服务启动时会把默认 Provider 写入 `llm_providers`，创建 Agent 时，如果请求没有显式传 `llm_provider` / `llm_model`，HTTP 层会用这组默认值补齐。

`TMA_LLM_PROVIDER` 是 Provider ID，可以是 `volcengine-agent-plan` 这类业务自定义 ID。自定义 ID 通过 `TMA_LLM_PROVIDER_TYPE=openai` 指定底层协议类型。真实 API Key 不存入数据库；`llm_providers.api_key_env` 只保存环境变量名，例如 `TMA_LLM_API_KEY`。

`llm.Manager` 本身实现了 `llm.Client`。Runtime 每次调用模型时，会把 `AgentConfigVersion` 里的 Provider / Model，以及从 `llm_providers` 解析出的 Provider Type / Base URL / API Key 放进 `llm.Request`，Manager 再选择或创建对应 client。这样未来即使增加运行时切换 API，也不需要重新创建 HTTP Server、WorkerRunner 或 AgentRuntimeTurnExecutor。

`openai-compatible` 当前支持 Chat Completions 文本响应、SSE 文本流式输出、非流式原生 tool calling 适配和 usage 归集。普通文本流式输出会先写入多条 `runtime.llm_delta`，最后仍然合并成一个完整 `agent.message`；带工具 schema 的请求第一版先走非流式，避免漏掉流式 tool call delta。Key Vault 和完整 model-bank 仍待做。此处先固定边界：Provider 负责创建模型 client，Manager 负责根据请求选择 Provider / Model，Runtime 只依赖 `llm.Client`。

Provider 长期路线、模型能力和 token usage 审计设计见 [llm-provider-roadmap.md](./llm-provider-roadmap.md)。

## 与 CommandTurnExecutor 的关系

`CommandTurnExecutor` 暂时保留，用于：

- 验证 JSON stdin/stdout 协议
- 未来接远端 worker 或独立进程 runtime
- 保持 `LocalSystemProvider.RunCommand` 的端到端测试入口

但它不再是 `cmd/server` 的默认执行路径。

## 后续路线

建议顺序：

1. 给 `Runtime` 增加更完整的 step payload。
2. 增加真实模型 client 实现。
3. 加 Tool 调用循环。
4. 将 Tool API 映射到 `capability.Provider`。
5. 再决定是否把 `CommandTurnExecutor` 作为可选 remote/runtime adapter。
