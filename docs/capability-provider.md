# TMA Capability Provider Design

本文档记录 TMA 当前对能力 Provider 的设计判断：**现在不引入 Tool 模块，也不把 local system / cloud sandbox 固化成 turn-level executor**。

## 为什么调整

从 LLM 的视角，本机系统或云沙箱通常不是“一整块环境”，而是一组可调用能力：

```text
runCommand
executeCode
readFile
writeFile
```

LLM 后续通过 function calling 调用的是这些能力，而不是直接说“进入某个环境工作”。

因此如果现在把本机系统或云沙箱设计成 turn-level executor：

```text
WorkerRunner -> LocalSystemTurnExecutor / CloudSandboxTurnExecutor
```

会把环境能力过早固定成“整次 turn 的执行器”。等以后引入 Agent Runtime / Tool Calling 时，还要拆掉这层抽象。

当前改为：

```text
WorkerRunner -> TurnExecutor

internal/tools
  -> manifest / registry / executor

internal/capability.Provider
  -> runCommand
  -> executeCode
  -> readFile
  -> writeFile
```

`Provider` 是底层能力面。`LocalSystemProvider`、未来的 `CloudSandboxProvider`、`RemoteProvider` 都应该是并列实现。未来 Tool 模块出现后，可以把这些能力包装成 LLM 可见的 builtin tools。

## 当前不做什么

当前阶段不新增完整 UI Tool 模块：

```text
assistant.tool_call
tool.call_result
本机系统 turn mode
云沙箱 turn mode
```

原因：

- 内部 `internal/tools` manifest / registry / executor 已存在，`openai-compatible` 已有第一版原生 function calling 适配。
- 还没有 Inspector / Intervention UI。
- 直接上 Tool 模块会提前引入大量未验证抽象。

当前 `DemoRuntime` 有一个最小 tool loop：优先使用 `llm.Request.Tools` / `llm.Message.ToolCalls` 承载厂商原生工具调用适配；同时继续兼容 `tma.tool_call.v1` 文本 JSON envelope。两条路径最终都通过 `internal/tools` 映射到 `capability.Provider`。它用于验证 Runtime -> Tools -> Capability Provider 的执行闭环，仍不是完整 UI Tool 模块。

## 当前代码边界

代码位置：

```text
internal/capability/provider.go
```

核心接口：

```go
type Provider interface {
	RunCommand(ctx context.Context, request RunCommandRequest) (CommandResult, error)
	ExecuteCode(ctx context.Context, request ExecuteCodeRequest) (CommandResult, error)
	ReadFile(ctx context.Context, request ReadFileRequest) (FileResult, error)
	WriteFile(ctx context.Context, request WriteFileRequest) (FileResult, error)
}
```

当前协议版本：

```text
tma.capability.v1
```

公共上下文：

```go
type RequestMeta struct {
	ProtocolVersion string
	SessionID        string
	TurnID           string
	Deadline         *time.Time
}
```

`RequestMeta` 用于让底层 Provider 知道一次能力调用属于哪个 session / turn，以及什么时候必须停止。

## 与 Runner / TurnExecutor 的关系

当前稳定分层：

```text
Runner
  管 turn 生命周期：启动、排队、中断、取消、状态回写

TurnExecutor
  管一次 turn 的执行：输入 TurnRequest，输出 agent.message payload

Capability Provider
  管底层能力：命令、代码、文件读写
```

现在的 `CommandTurnExecutor` 是一个 `TurnExecutor`，用于把整次 turn 交给外部命令。

它通过 `capability.LocalSystemProvider.RunCommand` 执行外部命令。这样当前可运行的 command 验收路径和未来 tool-level 本地能力使用同一个底层 provider。

未来更完整的形态可能是：

```text
WorkerRunner
  -> AgentRuntimeTurnExecutor
      -> LLM loop
          -> internal/tools
              -> Capability Provider
```

这样 Capability Provider 不需要知道 LLM，也不需要知道 UI。它只负责安全、隔离和执行。

## 与未来 Tool Calling 的关系

未来如果加入 Tool 模块，可以自然包装：

```text
Builtin Tool: tma.local_system
  API: runCommand    -> LocalSystemProvider.RunCommand
  API: executeCode   -> LocalSystemProvider.ExecuteCode
  API: readFile      -> LocalSystemProvider.ReadFile
  API: writeFile     -> LocalSystemProvider.WriteFile

Builtin Tool: tma.cloud_sandbox
  API: runCommand    -> CloudSandboxProvider.RunCommand
  API: executeCode   -> CloudSandboxProvider.ExecuteCode
  API: readFile      -> CloudSandboxProvider.ReadFile
  API: writeFile     -> CloudSandboxProvider.WriteFile
```

也就是说：

- `internal/tools` 是当前内置工具 manifest / registry / executor 层。
- `internal/capability.Provider` 是系统底层能力面。
- `LocalSystemProvider` 和 `CloudSandboxProvider` 是并列 Provider，不是从属关系。
- 当前 `tools.Manifest` 是面向 LLM 的内置工具暴露面，并能生成厂商原生 function calling schema；未来可继续扩展到 UI 注册。
- 二者不是同一层，但 API 名称可以保持一致。

## 后续路线

建议顺序：

1. 保持现有 `Runner / TurnExecutor` 稳定。
2. 持续完善 `LocalSystemProvider`。
3. 再引入 `AgentRuntimeTurnExecutor`。
4. 继续完善 `internal/tools` 的 manifest / registry / executor，并补齐更多 Provider 的原生 function calling 适配。
5. 最后把具体 Provider 包装成 `tma.local_system`、`tma.cloud_sandbox` 等 builtin tools。
