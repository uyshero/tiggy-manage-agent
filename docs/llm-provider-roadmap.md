# TMA LLM Provider Roadmap

本文档记录 LLM Provider 相关的长期设计约定，避免后续接多个模型厂商时把差异散落到 Runner、AgentRuntime 或 HTTP 层。

## 当前边界

当前链路：

```text
cmd/server
  -> WorkerRunner
  -> AgentRuntimeTurnExecutor
  -> agentruntime.DemoRuntime
  -> llm.Manager
  -> llm.Provider / llm.Client
```

当前已经具备：

- `llm_providers` 表保存 Provider ID、协议类型、Base URL、API Key 环境变量名和启用状态。
- `AgentConfigVersion` 保存 `llm_provider` / `llm_model` / `system` / `tools` / `skills`。
- `Session` 创建时绑定 `agent_config_version`，旧 Session 不受 Agent 后续配置升级影响。
- 自定义 Provider ID 可以通过 `provider_type=openai` 走 OpenAI Chat Completions 兼容协议。
- 数据库不保存真实 API Key，只保存 `api_key_env`。

## 长期原则

1. `AgentRuntime` 不写厂商判断。
2. Provider 差异下沉到 `internal/llm`。
3. 上层只认统一请求和统一流出协议。
4. OpenAI-compatible Provider 尽量复用同一套 client / factory。
5. 专用协议只在 Provider 层扩展，例如 Anthropic、Google、Bedrock。
6. 密钥不落库，后续可从环境变量升级到 Key Vault。
7. Provider 和模型能力要数据化，而不是写死在 Runtime。
8. 每次模型调用必须可审计：按 provider / model / session / turn 统计 token 和费用。

## Provider 分层

建议分三层实现：

```text
Layer A: OpenAI-compatible
  大多数企业网关、国产模型网关、自建 OpenAI 兼容接口

Layer B: 专用协议 Provider
  Anthropic / Google / 其他非 OpenAI payload 或 SSE 协议

Layer C: 原生复杂 Provider
  多模态生成、异步任务、Webhook、Router / failover
```

新增 Provider 时优先走 Layer A。只有协议明显不兼容时才增加 Layer B / C。

## Provider DB 设计方向

当前已有：

```text
llm_providers
  id
  provider_type
  base_url
  api_key_env
  enabled
  created_at
```

后续建议增加：

```text
llm_models
  provider_id
  model
  display_name
  abilities_json
  default_params_json
  enabled
  created_at
```

`abilities_json` 可先保持轻量：

```json
{
  "stream": true,
  "vision": false,
  "function_call": false,
  "reasoning": false,
  "search": false,
  "structured_output": false
}
```

这份能力信息未来会同时影响 Runtime、Tool Calling、参数校验和前端 UI。

## Token Usage 审计

未来需要精确审计每个模型调用的消耗。不要只依赖日志，应该落库。

建议新增表：

```text
llm_usage_records
  id
  workspace_id
  agent_id
  agent_config_version
  session_id
  turn_id
  provider_id
  provider_type
  model
  request_id
  input_tokens
  output_tokens
  total_tokens
  cached_input_tokens
  reasoning_tokens
  tool_call_count
  unit_price_input
  unit_price_output
  cost_amount
  currency
  latency_ms
  status
  error_message
  created_at
```

最小第一版可以只做：

```text
session_id
turn_id
provider_id
model
input_tokens
output_tokens
total_tokens
latency_ms
status
created_at
```

审计维度必须支持：

- 按 Provider 汇总
- 按 Model 汇总
- 按 Agent 汇总
- 按 Session / Turn 追踪
- 按时间范围统计
- 区分成功、失败、取消、超时

## Usage 来源

不同 Provider 返回 usage 的方式不同：

- 非流式响应：通常在最终 JSON 里返回 `usage`。
- OpenAI-compatible 流式响应：有的在最后 chunk 返回 `usage`，有的完全不返回。
- 部分 Provider 会返回 reasoning token、cached token。
- 有些网关只返回计费字段，不返回标准 token。

因此 `llm.Response` / 流式最终结果需要逐步扩展：

```go
type Usage struct {
    InputTokens       int64
    OutputTokens      int64
    TotalTokens       int64
    CachedInputTokens int64
    ReasoningTokens   int64
}
```

Provider 层负责把各家 usage 归一化成 TMA 的 `Usage`。

## 统一流协议

当前已有：

```text
runtime.llm_delta
```

它只表达文本增量。未来需要升级成统一 chunk：

```text
runtime.llm_chunk
  type: text | reasoning | tool_calls | grounding | usage | stop | error
  data: ...
```

迁移策略：

1. 保留 `runtime.llm_delta` 一段时间，兼容现有验收脚本。
2. 新增 `runtime.llm_chunk`。
3. Tool Calling / Reasoning / Usage 开始接入后，再逐步弱化 `runtime.llm_delta`。

## Router / Failover

Provider Router 不是当前优先级，但需要保留设计空间。

未来可以支持：

```text
provider_id = enterprise-openai-router
channels:
  1. internal gateway
  2. backup gateway
  3. public compatible endpoint
```

Router 应该仍然暴露为一个 Provider ID，上层不感知内部 channel 切换。

## 推荐实施顺序

1. Context Builder：把 Session 历史消息组装进 LLM request。已完成基础版本。
2. Usage 基础结构：`llm.Usage`、`llm_usage_records`、每 turn 成功完成后落库。已完成基础版本。
3. OpenAI-compatible usage 解析：非流式和流式最终 usage。已完成基础版本。
4. Model abilities：`llm_models` 基础表已完成，当前先保存 `context_window_tokens`；能力 JSON 后续补。
5. 统一 `runtime.llm_chunk`。
6. Tool Calling：最小内部 tool loop 已通过 `internal/tools` 接到 `capability.Provider`；`openai-compatible` 已支持第一版原生 function calling schema / response 适配，并保留稳定的 `tma.tool_call.v1` 内部 envelope 兼容路径；Inspector / Intervention UI 待做。
7. Provider Router / failover。

当前进展：Context Builder 已从 `DemoRuntime` 中抽离，负责把 summary、`tools.Registry.ModelContext()` 生成的 manifest、skills、当前 `user.message` 之前的 `user.message` / `agent.message` 注入 LLM messages；模型总窗口由 `llm_models.context_window_tokens` 提供，Context Builder 固定使用 60% 作为输入预算；`session_summaries` 已提供手动 summary 和 just-in-time 自动 summary 落库；`internal/tools` 已提供内置工具 manifest / registry / executor 的最小闭环，并内置 `tma.local_system`；Runtime 已支持 `llm.Request.Tools` / `llm.Message.ToolCalls` 原生工具调用路径，`openai-compatible` 已把它适配到 Chat Completions `tools` / `tool_calls`，同时兼容 `tma.tool_call.v1` 和旧的 `tma.agent_runtime.demo.v1` 文本工具调用 envelope；LLM usage 会从 `llm.Client` 返回，经 `AgentRuntimeTurnExecutor` 补齐 workspace / agent / session / turn / provider / model 维度后，由 `WorkerRunner` 写入 `llm_usage_records`。后续真实 tokenizer、多模态上下文、流式 tool call delta 和更多 Provider 的原生 tool calling 适配应继续在 Runtime 边界内演进。

Usage 查询当前已支持两类基础入口：

- `GET /v1/sessions/{session_id}/usage`：查看单个 Session 的总量和 turn 明细。
- `GET /v1/llm-usage`：跨 Session 按 provider / model / provider_model 聚合，并支持时间范围过滤。

## 不做的事

当前阶段不做：

- 不把真实 API Key 存入数据库。
- 不在 AgentRuntime 里写 Provider-specific 分支。
- 不一次性实现所有厂商 SDK。
- 不为了未来 Router 过早重构当前 `llm.Manager`。
- 不在日志里替代 usage 审计表。
