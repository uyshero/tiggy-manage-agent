# TMA Agent Runtime Design

本文档记录当前 AgentRuntime 雏形。它的目标是把“一轮用户消息如何变成 agent.message”从 Runner 和 HTTP 层里拆出来。

## 当前分层

```text
HTTP API
  -> Store
  -> WorkerRunner
  -> AgentRuntimeTurnExecutor
  -> agentruntime.Runtime
  -> llm.Manager
  -> llm.Client
```

职责边界：

- `WorkerRunner`：排队、取消、中断、成功/失败状态回写。
- `AgentRuntimeTurnExecutor`：把 Runner 的 `TurnRequest` 适配到 AgentRuntime，并按 Session 解析 `AgentConfigVersion`。
- `agentruntime.Runtime`：执行一轮智能体逻辑，返回 `agent.message` payload。
- `llm.Manager`：模型 Provider 注册和默认 Provider / Model 管理入口；每次请求也可以指定 Agent 配置里的 Provider / Model。
- `llm.Client`：模型调用边界；当前默认使用 FakeClient，不访问外部 API。
- `capability.Provider`：提供底层命令、代码、文件等能力，后续由 Tool 层调用。

## 当前实现

代码位置：

```text
internal/agentruntime
```

当前接口：

```go
type Runtime interface {
	RunTurn(ctx context.Context, request TurnRequest) (json.RawMessage, error)
}
```

当前默认实现是 `DemoRuntime`。它通过 `llm.Client` 生成一个可验证的 `agent.message` payload。服务端默认注入 `llm.Manager`，Manager 当前内置 `fake` 和 `openai-compatible` Provider：

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
runtime.completed
```

`runtime.llm_delta` 只在 LLM client 支持流式输出时出现。默认 `fake` Provider 不会写 delta；`openai-compatible` 会解析 Chat Completions SSE，并把每段文本写成 delta 事件。

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
历史 user / assistant
当前 user
```

这样第二轮及后续对话能看到前面已完成的 user / agent 消息；当前正在处理的 `user.message` 不会从历史里重复注入。

如果 Runtime 返回错误，`AgentRuntimeTurnExecutor` 会尽量先写入：

```text
runtime.failed
```

这些事件都会带同一个 `payload.turn_id`。如果用户已经 interrupt，Store 会拒绝旧 turn 继续补 runtime 事件。

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

`openai-compatible` 当前支持 Chat Completions 文本响应和 SSE 文本流式输出。流式输出会先写入多条 `runtime.llm_delta`，最后仍然合并成一个完整 `agent.message`。还没有 tool calling、usage 归集、Key Vault 或 model-bank。此处先固定边界：Provider 负责创建模型 client，Manager 负责根据请求选择 Provider / Model，Runtime 只依赖 `llm.Client`。

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
