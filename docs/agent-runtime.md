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
- `internal/tools`：内置工具 manifest、registry 和 executor；当前内置 `default`。
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

### 完成质量门禁

Runtime 在模型准备返回最终文本时先写入 `runtime.turn_completing`，并执行完成门禁；只有校验通过后才发布 `agent.message`。校验返回可修正失败时，Runtime 会把反馈追加到同一 Tool Loop，要求模型继续执行，默认最多重试 3 次，可通过 Session `runtime_settings.completion_gate.max_retries` 在 1–10 之间调整。校验器异常、非法结果或重试耗尽会失败关闭，不能把候选文本当作成功结果。

服务端默认启用持久化 Task Plan 完成校验：

- 没有活动计划时正常放行。
- 活动计划存在 pending、in_progress、blocked 或缺少 evidence 的 completed item 时阻止完成，并向模型返回精确的 item ID、状态和修正动作。
- completed item 除 evidence 文本外，还必须通过 `evidence_refs` 引用当前 turn 中真实成功的非 `task.*` 工具结果；Store 会从持久化 `runtime.tool_result` 校验并回填工具名和 Artifact ID，模型不能自行声明引用有效。
- 不存在的调用、失败调用、计划创建前的调用以及 `task.update_items` 等 Task 工具自证都会被拒绝。
- 所有 item 都 completed 且包含已核验 evidence refs、但计划仍为 active 时，要求模型先成功调用 `task.complete_plan`。
- Task Plan Store 读取失败时不放行，避免运行时在无法确认执行状态时错误报告成功。

门禁事件只保存 validator、reason、有限的结构化 evidence 和反馈字符数，不保存候选回复或完整反馈文本。

同时它会写入三类 runtime step 事件：

```text
runtime.started
runtime.thinking
runtime.llm_request
runtime.llm_response
runtime.tool_call
runtime.tool_result
runtime.completed
```

模型流片段不属于持久化 runtime step。`openai-compatible` 仍会解析 `text|reasoning|tool_call|usage|stop|error` 六类 Provider delta，但只有最终用户可见的 text 通过 `/v2/sessions/{session_id}/live/stream` 以 `llm.text` 临时广播；reasoning 和工具参数分片不会对外广播或写入 `session_events`。Runtime 将 chunk 分类数量、输出/推理字符数、TTFT 和 finish reason 聚合到单条 `runtime.llm_response.data.stream`。

当前 `DemoRuntime` 已有最小 tool loop：Runtime 会把 `internal/tools.Registry.ModelTools()` 生成的函数 schema 放进 `llm.Request.Tools`。`openai-compatible` 会把它转换成 Chat Completions `tools`，并把响应里的原生 `tool_calls` 解析回 `llm.Message.ToolCalls`。Runtime 会先记录 `runtime.tool_call` 表示模型请求了工具调用，再按注册 API 的 Draft 2020-12 JSON Schema 校验参数；非法参数在审批和执行前以可恢复的 `invalid_tool_arguments` 返回模型，连续两轮非法会终止 Turn，schema 本身无效则失败关闭。只有参数合法且通过审批或直通策略后，才调用 `internal/tools.Executor`；`RegistryExecutor` 会重复执行同一 schema 校验作为最终边界。Runtime 用 `runtime.tool_result` 记录真实执行或拒绝结果，再把工具结果作为带 `tool_call_id` 的 `tool` role message 送回模型继续本轮回复。校验反馈只包含 instance/constraint path，不回显实际参数值。

Session 级工具开启等级通过 `runtime_settings.intervention_mode` 热更新，当前支持：

- `request_approval`：敏感工具不执行，写出 `runtime.tool_intervention_required`，保存 pending intervention，并暂停当前 turn。
- `approve_for_me`：敏感工具先写出 `runtime.tool_intervention_approved`，再继续执行。
- `full_access`：直接执行。

`request_approval` 同时会把 pending call 保存到 `session_interventions`，保存恢复 LLM tool loop 所需的 continuation messages，并把 turn 标记为 `waiting_approval`。可以通过 `bin/tma session attach --session ...` 在同一个交互式会话命令中发送 user message、监听事件并处理审批，也可以通过 `GET /v1/sessions/{session_id}/interventions?status=pending` 或 `bin/tma session intervention list --session ... --status pending` 查看，再用细粒度 `approve` / `reject` 写入用户决策事件。审批 HTTP Handler 只负责持久化决定并唤醒后台 `WorkerRunner`，不在请求 goroutine 内执行工具或调用 LLM，因此客户端断开不会取消续跑。Postgres 决策事务会把 turn 恢复为 `running`、写入 `resume_intervention_call_id` 并清理旧 lease；`WorkerRunner` 通过持久化 lease/claim 获取带 intervention continuation 的 `TurnRequest`，所以服务重启后仍能恢复。`AgentRuntimeTurnExecutor` 负责解码持久化 continuation，并统一交给 `agentruntime.Runtime`：approve 执行保存的 tool call、写出 `runtime.tool_result`，再把结果作为 `tool` role message 继续最多 4 轮 Tool Loop；reject 生成包含 `decision_reason` 的 rejected observation 并走同一条 Runtime Tool Loop，没有 continuation 的旧记录仍回退为 fail turn。续跑中再次遇到敏感工具会再次 pending / waiting approval；模型返回普通文本时生成最终 `agent.message`，并让 session 回到 `idle`。相同决定支持幂等重试，已完成 turn 不会重复执行。长期无人审批时记录保持 `pending` / `waiting_approval`，不会自动超时；如果用户在等待期间发送新消息，服务端会返回提醒并重新发送 pending 审批事件，而不是开启新 turn。

用户 interrupt 等待审批的 turn 时，Store 会为每个 pending call 依次写入 `runtime.tool_intervention_rejected` 和 synthetic `runtime.tool_result`，后者固定携带同一 call ID、`status=canceled`、`reason=user_interrupted`、`success=false`、`retryable=false`。随后 turn 进入 `interrupted`、session 回到 `idle`；该路径不会生成 `agent.message` 或继续调用 LLM。

当前 `default.read_file` 默认直通；`run_command`、`write_file`、`edit_file` 会进入这套 policy。`execute_code` 仍保留完整执行和审批能力，但默认对模型隐藏，只有 Agent 显式配置 `default.execute_code` 时才会进入同一套 policy。

分段文件生成的首次骨架审批会把目标路径、占位符和 continuation 状态一起持久化；后续只对该路径已登记 placeholder 的 `edit_file` 复用计划批准。Runtime 不信任模型自行声明完成：它在最终回复前读取目标文件、确认占位符为零，并要求最后一段写入之后存在成功且引用目标路径的 check/test/compile/lint/build 调用。中间 Write/Edit 不发布 Artifact，最终校验成功后只发布一次正式文件 Artifact。

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
        "name": "default.run_command",
        "arguments": {
          "command": "sh",
          "args": ["-c", "pwd"]
        }
      }
    }
  ]
}
```

`function.name` 使用 `<tool_identifier>.<api_name>` 格式，例如 `default.run_command`。新项目不兼容旧的 `tma.agent_runtime.demo.v1` 工具调用 envelope；该协议名只保留给当前 demo `agent.message` payload。

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
- Runtime 每轮读取服务端本地日期，并以独立 system message 注入 `Today's date is YYYY-MM-DD.`；该消息属于不可压缩前缀，避免模型自行猜测“今天”。
- Session `runtime_settings.pinned_context` / `protected_context` 非空时会作为 `Pinned context` system message 注入，属于不可压缩上下文。
- session summary 非空时放在 system 后面。
- `AgentConfigVersion.tools` 会先被解析成工具策略：数组或 `enabled_tools` 表示本版本启用哪些工具 / tool API，`runtime` 和 `tool_runtime` 会参与 provider 选择；未显式配置时保持默认内置工具集合。
- `tools.Registry.ModelContext()` 生成的工具 manifest 非空时作为 system 级上下文注入；当前默认包含 `default`。
- AgentConfigVersion 的 `skills` 非空时作为 system 级上下文注入，不参与 provider 选择。缺省 `summary` 模式只注入 metadata、manifest 摘要和按需读取提示；模型需要完整 SKILL.md 时调用冻结版本的 `skills.inspect`，并按 `next_offset` 分页读取，每页最多 8000 字符。显式 `full` 仍会注入正文。
- 历史只接收 `user` / `assistant` 角色。
- 历史空文本会跳过。
- 当前 user message 总是追加到最后。
- 每个模型用 `context_window_tokens` 表示总上下文窗口；未配置时默认 128000。
- Context Builder 默认最多使用总窗口的 60% 作为输入上下文预算；Session `runtime_settings.context_input_budget_ratio_percent` 可覆盖，范围会夹在 10–95。
- Session `runtime_settings.context_output_reserve_tokens` 可显式预留输出空间；设置后输入预算会取「比例预算」和「总窗口减输出预留」里的较小值。

截断规则：

- system、当前日期上下文、pinned context、summary 和当前 user message 是保底消息，不会被删除。
- 读取跨 turn history 后，会先排除 `seq <= summary.source_until_seq` 的消息，避免 summary 与其已覆盖历史重复进入模型上下文。
- history 从最新到最旧尝试纳入预算，最终仍按原始时间顺序排列；带同一 `turn_id` 的 user/assistant 历史会整组保留或整组省略，避免半段 turn 进入上下文。
- 原生 function calling 的 tool schema 会参与输入预算；工具 schema 变大时会优先挤掉较旧 history，而不是让真实请求悄悄越过输入预算。
- token 数仍是 Provider 无关的近似估算：每条 message 有固定开销，连续 ASCII 按约 4 字符/token，CJK 与其他非 ASCII 字符按约 1 字符/token，tool schema 按 JSON 序列化后的文本估算。第一轮收到 Provider 的真实 `input_tokens` 后，同一 Tool Loop 后续轮次只向上校准估算倍率，不会因一次低用量而放宽预算。
- `runtime.llm_request` step 会记录 `history_count`、`omitted_history_count`、`estimated_token_count`、`budgeted_token_count`、`token_estimate_multiplier`、`estimated_message_tokens`、`estimated_tool_schema_tokens`、`tool_schema_count`、`available_output_tokens`、`context_truncated`、`current_date_context_included`、`pinned_context_included` 和 `summary_included`，并在 `context_budget` 中给出 `context_window_tokens`、`input_budget_ratio_percent`、`max_input_tokens`、`reserved_output_tokens`、`message_tokens`、`current_date_context_tokens`、`pinned_context_tokens` 和 `tool_schema_tokens` 等分账。
- 同一 Turn 的工具循环会对 tool result 执行两级预算：先按 `tool_result_context_max_chars` 裁剪单条结果，再按 `tool_result_context_total_max_chars` micro-compact 较旧结果。assistant tool call 与对应 tool message 不删除，最新结果保持完整；`runtime.llm_request` 会记录累计字符、预算、压缩数量和节省字符。
- `default.read_file` 在 Provider 层先执行有界分页，普通大文本不会先完整载入内存。当前页文本只放在 `ExecutionResult.Content`，分页 metadata 放在 `State`。`State.truncated/eof` 表示文件读取范围；`State.model_context_truncated` 和 tool result 的 `context.content_truncated` 表示当前页又被模型上下文预算裁剪。`default.search_file` 提供无需 exec 审批的流式单文件定位。详见 [大文件分页读取设计](./large-file-reading.md)。
- 每轮请求会按 `context_window_tokens - budgeted_token_count` 动态收缩 `max_output_tokens`。如果输入估算本身已经耗尽窗口，Runtime 会在调用 Provider 前返回非重试 `context_length`，避免无效网络请求和重复计费。

Summary 当前有两种入口。

自动 summary 使用 just-in-time compaction：下一轮 `user.message` 触发 session 从 `idle` 进入 `running` 后，`ContextBuilder` 如果发现历史超过输入预算并发生截断，Runtime 会先在本轮 `running` 内生成 summary，写入 `session_summaries`，过滤已被 summary 覆盖的历史，再重建上下文并继续原本的 LLM 回复流程。自动 summary 不切换 session 状态，只写 runtime 事件：

```text
runtime.context_compacting
runtime.context_compacted
```

自动 summary 的压缩 prompt 默认最多 60000 字符，生成后的 summary 默认最多 12000 字符。Session `runtime_settings.compaction_prompt_max_chars` 和 `runtime_settings.compaction_summary_max_chars` 可覆盖这两个上限；事件中会记录 `prompt_max_chars`、`summary_max_chars` 和是否截断。

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
cmd/tma-server
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

`tools` 第一版表示工具策略，不只是上下文文本：

- `["default.read_file"]` 这类数组，表示启用的工具或 tool API。
- `{"enabled_tools":[...],"runtime":"cloud_sandbox"}` 这类结构化对象，表示显式启用列表和工具 runtime 偏好。
- 未传 `tools` 时保持默认内置工具集合。

`Session` 创建时绑定 `agent_id + agent_config_version`。执行 turn 时，`AgentRuntimeTurnExecutor` 会通过 `session_id` 解析这份配置，然后先筛出启用工具，再把工具策略和 session runtime settings 一起交给 provider resolver。这样 Agent 后续升级配置时，旧 Session 仍然可以按创建时的配置快照运行和回溯。

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

`openai-compatible` 当前支持 Chat Completions 文本响应、六类 SSE delta、原生 tool calling 适配和 usage 归集。tool-call fragment 只用于运行期组装和文件变更大小保护，Runtime 等 Provider 组装完最终 `Response.Message.ToolCalls` 后才执行工具；流内 error 进入统一失败路径。最终文本合并成一个完整、持久化的 `agent.message`。Key Vault 和完整 model-bank 仍待做。此处先固定边界：Provider 负责创建模型 client，Manager 负责根据请求选择 Provider / Model，Runtime 只依赖 `llm.Client`。

Provider 长期路线、模型能力和 token usage 审计设计见 [llm-provider-roadmap.md](./llm-provider-roadmap.md)。

## 与 CommandTurnExecutor 的关系

`CommandTurnExecutor` 暂时保留，用于：

- 验证 JSON stdin/stdout 协议
- 未来接远端 worker 或独立进程 runtime
- 保持 `LocalSystemProvider.RunCommand` 的端到端测试入口

但它不再是 `cmd/tma-server` 的默认执行路径。

## 后续路线

建议顺序：

1. 给 `Runtime` 增加更完整的 step payload。
2. 增加真实模型 client 实现。
3. 加 Tool 调用循环。
4. 将 Tool API 映射到 `capability.Provider`。
5. 再决定是否把 `CommandTurnExecutor` 作为可选 remote/runtime adapter。

## 相关文档

- [Observability Design](./observability.md) — 可观测性规划（模型观测 vs 人类观测、Perfetto / Langfuse / OTel、分阶段落地）
