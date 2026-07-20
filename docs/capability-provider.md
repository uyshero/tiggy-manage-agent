# TMA Capability Provider Design

上位扩展治理、Provider 发现、兼容性、下线和人工切换规则见 [TMA Extension 与 Provider 治理标准](./extension-governance-standard.md)。设置页贡献与配置作用域见 [Extension 设置页与配置贡献标准](./extension-settings-standard.md)。

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

`ReadFileRequest` 支持互斥的 raw byte `offset_bytes/max_bytes` 和 1-based `start_line/max_lines`。`FileResult` 返回 size、实际 offset、returned bytes、next offset、EOF、revision 和行范围；大文件始终在 Provider 层有界读取，而不是到模型上下文阶段才裁剪。内置 Provider 还实现可选的 `FileSearchProvider`，供 `default.search_file` 流式返回命中行号和 raw byte offset。完整协议见 [大文件分页读取设计](./large-file-reading.md)。

当前协议版本：

```text
tma.capability.v1
```

`RunCommandRequest` 使用结构化 `command + args`，不会自动拼接 Shell 字符串。`timeout_ms` 默认 120000，允许 100～600000；`max_output_bytes` 分别约束 stdout 和 stderr，默认每路 65536、最大 1048576。Provider 会继续读取并计数超限输出，但只保留前缀，`CommandResult` 返回 total/captured bytes、truncated、duration、status 和 timed_out。Unix 本机执行使用独立进程组，超时后终止整组；Session 云沙箱在 timeout/cancel 后强制删除容器，清理失败则失败关闭，并另外施加 CPU、内存和 PID 容器限制。本机 Worker 不提供等价资源隔离。

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

- `/workspace`：挂载 workspace base 下按 `workspace_id + owner_id + session_id` 派生的隔离目录，用于上传文件、项目源码和最终交付物。
- `/mnt/data`：挂载按相同完整作用域派生的 host 临时目录，只用于缓存和加工中间产物。

两个目录名都包含完整作用域的 SHA-256 摘要，避免 ID 清洗碰撞；同作用域复用，任一字段不同都隔离。`/mnt/data` 超过配置 TTL 后由后续 sandbox 调用顺手清理。通过上传接口进入 session 的文件，会在执行前同步到 `/workspace/uploads/{artifact_id}/{filename}`，最终交付物也必须放在 `/workspace`。文件工具还会解析现有路径前缀的软链接并拒绝越出挂载根目录。

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
