# TMA Capability Provider Design

本文档记录 TMA 当前对能力 Provider 的设计判断：**不把 local system / cloud sandbox 固化成 turn-level executor**。工具 namespace 表达能力域，runtime 表达执行位置偏好，provider 是最终执行实现。第一版 runtime 只有 `auto`、`cloud_sandbox`、`local_system`。

## 为什么调整

从 LLM 的视角，本机系统或云沙箱通常不是“一整块环境”，而是一组可调用能力：

```text
runCommand
executeCode
readFile
writeFile
editFile
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
  -> editFile
```

`Provider` 是底层能力面。当前 `cloud_sandbox` 落到 `OnlyboxesProvider`，`auto` 在 `default.*` tools 上先等价选择 `cloud_sandbox`。`local_system` 表示需要本机执行能力，但真实部署里只有匹配在线 `tma-worker` 时才存在；worker 内部用 `LocalSystemProvider` 执行。server 进程只有在显式开启 `TMA_ALLOW_SERVER_LOCAL_SYSTEM=true` 的受信任开发环境中才允许直接使用 `LocalSystemProvider`。本地路径限制只是 `WorkspacePathGuardProvider` 这类内部 guard，不作为 runtime，也不对外称为 sandbox。

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
	EditFile(ctx context.Context, request EditFileRequest) (EditFileResult, error)
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
Builtin Tool Namespace: default
  API: run_command
  API: execute_code
  API: read_file
  API: write_file
  API: edit_file

Runtime resolution:
  cloud_sandbox -> OnlyboxesProvider
  local_system  -> matching tma-worker -> LocalSystemProvider on that worker
  auto          -> cloud_sandbox for default.* in v1
```

`cloud_sandbox` 采用 Session 级容器复用模型：第一次工具调用由 `OnlyboxesProvider` 执行 `docker run -d`，后续同一 Session 和 scope 使用 `docker exec`。容器按空闲 TTL、最大寿命和 Server 关闭事件回收；TMA 仍不负责自动启动 Docker daemon 或 Onlyboxes Console。

容器内目前有两类挂载：

- `/workspace`：挂载 workspace root，用于项目源码和普通文件工具。
- `/mnt/data`：挂载 session 级 host 数据目录，用于用户文件加工和跨多次工具调用保存中间产物。

`/mnt/data` 目录按清洗后的 session id 隔离，同 session 复用，超过配置 TTL 后由后续 sandbox 调用顺手清理。通过上传接口进入 session 的文件，会在执行前同步到 `/mnt/data/uploads/{artifact_id}/{filename}`。当前 provider 不声明 browser 能力，`browser.*` 应由后续专门实现承接。

也就是说：

- `internal/tools` 是当前内置工具 manifest / registry / executor 层。
- `internal/capability.Provider` 是系统底层能力面。
- `OnlyboxesProvider` 是第一版默认沙箱 Provider；`LocalSystemProvider` 是本机执行实现，通常由 `tma-worker` 持有，不是 tool namespace。
- 当前 `tools.Manifest` 是面向 LLM 的内置工具暴露面，并能生成厂商原生 function calling schema；未来可继续扩展到 UI 注册。
- 二者不是同一层，但 API 名称可以保持一致。

## 后续路线

建议顺序：

1. 保持现有 `Runner / TurnExecutor` 稳定。
2. 持续完善 `LocalSystemProvider`。
3. 再引入 `AgentRuntimeTurnExecutor`。
4. 继续完善 `internal/tools` 的 manifest / registry / executor，并补齐更多 Provider 的原生 function calling 适配。
5. 最后把具体 Provider 包装成 `default`、`tma.sandboxed_provider` 等 builtin tools。
6. 在 builtin tools 稳定后，再设计 TMA 自身的 Plugin 能力包机制：Plugin 可以包含 skills、tools、hooks、assets、UI panels 和 marketplace metadata；Plugin tools 应复用同一套 `tools.Registry`、`capability.Provider` 和 human intervention policy。
