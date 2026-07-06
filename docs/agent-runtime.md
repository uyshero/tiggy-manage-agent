# TMA Agent Runtime Design

本文档记录当前 AgentRuntime 雏形。它的目标是把“一轮用户消息如何变成 agent.message”从 Runner 和 HTTP 层里拆出来。

## 当前分层

```text
HTTP API
  -> Store
  -> WorkerRunner
  -> AgentRuntimeTurnExecutor
  -> agentruntime.Runtime
  -> llm.Client
```

职责边界：

- `WorkerRunner`：排队、取消、中断、成功/失败状态回写。
- `AgentRuntimeTurnExecutor`：把 Runner 的 `TurnRequest` 适配到 AgentRuntime。
- `agentruntime.Runtime`：执行一轮智能体逻辑，返回 `agent.message` payload。
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

当前默认实现是 `DemoRuntime`。它通过 `llm.Client` 生成一个可验证的 `agent.message` payload。默认 client 是 `llm.FakeClient`，不调用外部模型：

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
runtime.llm_response
runtime.completed
```

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
      -> llm.FakeClient
```

这样后续接真实模型时，可以先替换 `llm.Client` 实现；如果要加入 tool loop，再扩展 `agentruntime.Runtime`。

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
