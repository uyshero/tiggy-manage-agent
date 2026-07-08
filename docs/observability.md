# TMA Observability Design

本文档记录 TMA 可观测性规划。设计参考 Hermes-Agent（Plugin Hook + Gateway 展示流）、Claude Code（OTel 三信号 + Perfetto 本地 trace），并贴合 TMA 现有 **`session_events` 事件溯源** 架构。

核心判断：**可观测性数据有两类观众——模型和人。** 它们共享同一事实源，但投影格式、时机和约束不同。给人看的 Perfetto / Langfuse / OTel 是可选插件；给模型用的结构化观测（Observation）是 Agent 完成任务的基础设施。

## 设计目标

1. **模型可感知**：错误、环境、跨 turn 轨迹能以结构化形式进入 LLM 上下文，支持自愈与任务完成。
2. **人类可调试**：开发、产品、运维能从同一事实源查看 trace、成本与延迟。
3. **单一事实源**：`session_events` 为权威记录；导出器可关、可采样，挂掉不影响 turn 执行。
4. **渐进落地**：先 `ProjectForModel`，再 `/trace` API，再按需接 Perfetto / Langfuse / OTel。

## 两类观测，不要混成一个

```text
session_events（事实源，seq 单调）
    │
    ├── ProjectForModel()   → tool message / environment_snapshot / summary
    │                         （同步，受 token 预算约束，驱动 Agent 推理）
    │
    └── ProjectForHumans()  → /trace API / Perfetto / Langfuse / OTel
                              （异步，可采样，给人分析与告警）
```

| 维度 | 给模型（Agent 上下文） | 给人（运维可观测） |
|------|------------------------|-------------------|
| 目的 | 继续推理、纠错、完成任务 | 调试、监控、成本分析、告警 |
| 格式 | `tma.tool_result.v1` JSON、`system` 摘要 | span 树、图表、Chrome Trace JSON |
| 粒度 | 受 `ContextBuilder` token 预算限制，需压缩 | 可全量存 DB，导出时可脱敏 |
| 时机 | **同步**，下一轮 LLM 请求前必须注入 | 异步 fan-out，可 batch |
| 隐私 | 不能把 API key、全文件内容喂给模型 | 工程师可见，导出默认脱敏 |

### 谁消费什么

| 数据 | 模型 | 开发 | 产品 | 运维 |
|------|:----:|:----:|:----:|:----:|
| tool 错误 + `hint` / `retryable` | 主消费者 | ✓ | ✓ | — |
| `environment_snapshot` | ✓ | ✓ | — | — |
| turn 轨迹 summary | ✓ | ✓ | ✓ | — |
| 完整 `session_events` seq | —（太长） | ✓ | ✓ | ✓ |
| span 树 / 时间轴 | — | Perfetto | Langfuse | OTel |
| P99 延迟 / 告警 | — | — | — | OTel |

## 与 Agent 回环的关系

Observability 不是只给人看的仪表盘。给模型用的观测是 **Agent 回环** 的一部分：

```text
用户输入
  → environment_snapshot 注入（模型知道 work_dir / 约束）
  → LLM 规划 tool_calls
  → 执行 → ProjectForModel → tool role message（outcome / error.code / hint）
  → LLM 根据观测纠错（业务错误不 fail turn）
  → turn 结束 → 轨迹摘要写入 summary（跨 turn 记忆）
```

与人用观测的关系（同一 `edit_file` 失败事件）：

```text
edit_file 失败
  ├─→ 模型：tool message { outcome: error, hint: "read_file first" }
  ├─→ Langfuse：tool span 标红 + arguments 预览
  └─→ OTel：tma_tool_duration_seconds{outcome=error} +1
```

详见错误回环与 `tma.tool_result.v1` 约定；本文档侧重观测架构与导出路径。

## 事实源：session_events

TMA 已有有序事件日志，是可观测性的天然底座：

```text
user.message / agent.message
session.status_*
runtime.started / thinking / llm_request / llm_response / llm_delta
runtime.tool_call / tool_result
runtime.tool_intervention_required / approved / rejected
runtime.context_compacting / compacted / failed
runtime.completed / failed
```

能力：

- `GET /v1/sessions/{id}/events?after_seq=` 历史续传
- SSE `events/stream` 实时推送
- `session_interventions` 审批审计
- `RecordLLMUsage` 用量落库

**原则（借鉴 Hermes Gateway）**：SSE / CLI 可读展示是**展示层投影**，不改变 DB 中事实 payload 契约。

## 给模型用的观测（ProjectForModel）

### 1. Tool 观测：`tma.tool_result.v1`

每次 tool 执行后，通过 `tools.ResultMessage()` 送回 LLM 的 JSON（`tool` role message）：

```json
{
  "protocol_version": "tma.tool_result.v1",
  "call_id": "call_edit",
  "identifier": "tma.local_system",
  "api_name": "edit_file",
  "outcome": "error",
  "error": {
    "type": "edit_failed",
    "code": "OLD_STRING_NOT_FOUND",
    "message": "The specified old_string was not found in the file",
    "retryable": true,
    "hint": "Call read_file first to get current content"
  },
  "observation": {
    "content": "diff preview or stderr excerpt",
    "state": { "exit_code": 1, "path": "src/main.go" }
  }
}
```

硬规则：

- **业务失败不 fail turn**；基础设施不可恢复（DB、Provider 未配置）才 `runtime.failed`。
- `outcome`：`success` | `error` | `pending_intervention`。
- `error.code` 枚举化，便于测试与 `hint` 配置。
- 命令 `exit_code != 0` 必须 `outcome: error`，不能仅靠 stderr 文本。

### 2. 环境快照：`tma.environment_snapshot.v1`

每 turn 开始时注入 `system`（或首条上下文前缀）：

```json
{
  "protocol_version": "tma.environment_snapshot.v1",
  "session_id": "sesn_xxx",
  "environment": {
    "id": "env_xxx",
    "work_dir": "/workspace/project",
    "os": "darwin",
    "sandbox": "local"
  },
  "constraints": {
    "max_tool_rounds": 8,
    "intervention_mode": "request_approval"
  }
}
```

来源：`Environment.config` + Session 绑定 + Provider 探测。动态状态（git branch 等）仍由工具探测，不硬编码进快照。

### 3. 跨 turn 轨迹摘要

`ContextBuilder` 当前只保留 `user` / `assistant` 历史，tool 轨迹不进入跨 turn 上下文。需在 turn 结束时从本 turn 的 `runtime.*` 事件生成摘要，写入 `session_summaries`：

```text
Previous turns:
- read_file src/main.go (ok, 120 lines)
- edit_file failed: OLD_STRING_NOT_FOUND
- User asked to fix the import error
```

### 4. 审批 / 拒绝反馈

`reject_and_continue`（规划中）将拒绝原因作为观测送回模型，而非仅 `FailSessionTurn`：

```text
User rejected tool call edit_file: "don't modify package.json"
```

对应事件：`runtime.tool_intervention_rejected` + synthetic tool/user message。

## 给人用的观测（ProjectForHumans）

### 外部工具简介

| 工具 | 是什么 | 主要用途 | TMA 角色 |
|------|--------|----------|----------|
| **Perfetto** | Google trace 可视化（ui.perfetto.dev） | 本地时间轴，看哪段慢 | 开发机可选 exporter |
| **Langfuse** | LLM 专用观测平台 | prompt、tool 链、token、成本 UI | 按需 exporter，可自托管 |
| **OTel** | OpenTelemetry 行业标准 | metrics / logs / traces → Grafana/Jaeger | 生产监控 exporter |

三者不是「都要全开」，而是 **observability 总线上的可插拔 exporter**：

```text
阶段 0：session_events + ProjectForModel + GET /trace（零外部依赖）
阶段 1：+ Perfetto（开发，TMA_PERFETTO=1）
阶段 2：+ Langfuse（有 prompt 调优需求时）
阶段 3：+ OTel（多实例生产、接公司 Collector 时）
```

### Span 树（与 Claude Code sessionTracing 对齐）

```text
tma.interaction                    # 一次 user.message → agent.message（turn）
├── tma.llm_request                # runtime.llm_request
│   └── tma.llm_response           # runtime.llm_response / llm_delta*
├── tma.tool                       # runtime.tool_call
│   ├── tma.tool.blocked_on_user   # runtime.tool_intervention_required
│   │   └── approved / rejected
│   └── tma.tool.execution         # runtime.tool_result
├── tma.context.compact            # context_compacting / compacted
└── tma.interaction.complete       # runtime.completed / agent.message
```

`trace_id` 建议：`trc_{session_id}_{turn_id}`。

### 事件 → Observation 映射

| `session_events.type` | Span Kind | 说明 |
|------------------------|-----------|------|
| `user.message` | interaction 开始 | 新 turn root |
| `runtime.llm_request` | llm_request 开始 | 含 `tool_round` |
| `runtime.llm_response` | llm_request 结束 | 写 usage |
| `runtime.llm_delta` | llm_request 子切片 | 主要给 Perfetto |
| `runtime.tool_call` | tool 开始 | |
| `runtime.tool_result` | tool 结束 | `duration_ms`, `outcome` |
| `runtime.tool_intervention_*` | intervention | 审批子 span |
| `runtime.context_compacting` | compact | |
| `agent.message` | interaction 结束 | |
| `runtime.failed` | interaction error | |

## 目标架构

```text
Runtime 埋点一次（emitStep / AppendRuntimeEvent）
    │
    ├── Postgres session_events     # 事实源，始终写入
    │
    └── internal/observability
            ObservabilityBus        # fan-out，fail-open
                ├── ProjectForModel  → ContextBuilder / ResultMessage / summary
                ├── PerfettoExporter   (if TMA_PERFETTO=1)
                ├── LangfuseExporter   (if TMA_LANGFUSE_ENABLED=1)
                └── OTelExporter       (if TMA_OTEL_ENABLED=1)
```

设计原则（Hermes + Claude Code）：

1. **展示 ≠ 持久化**：SSE 可读格式不改变 DB payload。
2. **观测 ≠ 控制**：exporter 只读，不改 tool result。
3. **fail-open**：单个 exporter panic/错误只 `slog.Warn`，不阻断 turn。
4. **懒加载**：未启用的 exporter 不加载 SDK。
5. **Cardinality 控制**：metrics 用 `api_name`，不用文件路径。

## 建议代码结构

```text
internal/observability/
├── bus.go              # ObservabilityBus，注册/分发
├── context.go          # TraceContext（trace_id, span_id, turn_id）
├── types.go            # Observation, SpanKind, RedactPolicy
├── mapper.go           # runtime.* → Observation
├── model.go            # ProjectForModel（tool result / summary / env）
├── human.go            # ProjectForHumans（trace 树重建）
├── redact.go           # 脱敏
├── perfetto/
│   └── exporter.go     # Chrome Trace JSON
├── langfuse/
│   └── exporter.go     # Langfuse trace/generation/span
└── otel/
    ├── bootstrap.go    # TracerProvider + MeterProvider
    └── metrics.go      # counters / histograms
```

埋点入口（单点）：`AgentRuntimeTurnExecutor.emitStep`，在 `AppendRuntimeEvent` 之后调用 `bus.Emit`（错误不向上返回）。

## 配置

### 统一配置结构（示意）

```yaml
observability:
  redact:
    secrets: true
    log_prompts: false
    log_tool_content: false

  perfetto:
    enabled: false
    dir: ~/.tma/traces

  langfuse:
    enabled: false
    sample_rate: 1.0

  otel:
    enabled: false
    metrics_listen: ":9090"
```

### 环境变量

**Perfetto（本地调试）**

```bash
TMA_PERFETTO=1
# 输出：~/.tma/traces/{session_id}/{turn_id}.json
# 打开：https://ui.perfetto.dev
```

**Langfuse（LLM 分析）**

```bash
TMA_LANGFUSE_ENABLED=1
LANGFUSE_PUBLIC_KEY=pk-lf-...
LANGFUSE_SECRET_KEY=sk-lf-...
LANGFUSE_BASE_URL=https://cloud.langfuse.com
TMA_LANGFUSE_LOG_PROMPTS=0
TMA_LANGFUSE_LOG_TOOL_CONTENT=0
```

**OTel（生产监控）**

```bash
TMA_OTEL_ENABLED=1
OTEL_SERVICE_NAME=tiggy-manage-agent
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.corp:4318
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
```

### 按场景启用

| 场景 | Perfetto | Langfuse | OTel |
|------|:--------:|:--------:|:----:|
| 本地开发 | ✓ | 可选 | console |
| 测试 / prompt 调优 | — | ✓ | console |
| 生产 | — | 采样 10% | ✓ |
| 内网不出网 | ✓ | 自托管 | 自建 Collector |
| CI golden trace | export | — | — |

## 拟新增 API / CLI

```text
GET  /v1/sessions/{id}/trace?turn_id=...     # JSON span 树（给人）
GET  /metrics                                 # Prometheus（OTel 开启时）

bin/tma trace show --session ... --turn ...   # 终端树形输出
bin/tma trace export --session ... --format perfetto --open
bin/tma observability status                  # exporter 健康检查
bin/tma debug bundle --session ...            # 脱敏 events + logs（规划）
```

## 事件 payload 补齐字段

在现有 `runtime.tool_call` / `runtime.tool_result` 等事件的 `data` 中统一增加：

```json
{
  "duration_ms": 42,
  "outcome": "error",
  "error": {
    "type": "edit_failed",
    "code": "OLD_STRING_NOT_FOUND",
    "retryable": true,
    "hint": "Call read_file first"
  },
  "trace": {
    "trace_id": "trc_sesn_001_turn_003",
    "span_id": "spn_..."
  }
}
```

拟新增事件类型：

- `runtime.environment_snapshot` — turn 开始环境快照

## OTel Metrics（规划）

| 指标 | 类型 | 标签 |
|------|------|------|
| `tma_turn_total` | Counter | `status`, `workspace_id` |
| `tma_tool_duration_seconds` | Histogram | `identifier`, `api_name`, `outcome` |
| `tma_llm_tokens_total` | Counter | `provider`, `model`, `token_type` |
| `tma_intervention_total` | Counter | `action` |
| `tma_tool_rounds` | Histogram | — |
| `tma_event_export_errors_total` | Counter | `exporter` |

## 隐私与采样

| 数据 | session_events（事实） | 日志 | Langfuse / OTel 导出 |
|------|------------------------|------|----------------------|
| tool arguments | hash + 截断 preview | 脱敏 | 默认 REDACTED |
| 文件内容 | path + state | 不记录全文 | 不记录 |
| LLM messages | 不存全文 | debug 可选 | `TMA_LANGFUSE_LOG_PROMPTS=1` |
| API key | 永不落库 | 脱敏 | 永不 |

采样建议：

```yaml
langfuse:
  sample_rate: 0.1          # 生产 10% session
otel:
  trace_sample_rate: 0.05
  always_sample_errors: true  # 失败 turn 100% 保留
```

导出队列满时丢弃并计数 `tma_event_export_dropped_total`，不阻塞 Runtime。

## 分阶段落地

### Phase 1 — 模型可感知（优先，无外部依赖）

- [ ] `tma.tool_result.v1`：`outcome` / `error.code` / `hint` / `retryable`
- [ ] 业务错误不 fail turn；`executeToolCalls` 始终写 tool message
- [ ] `exit_code != 0` 标记为 `outcome: error`
- [ ] `runtime.environment_snapshot` + `ContextBuilder` 注入
- [ ] turn 结束写轨迹摘要到 `session_summaries`
- [ ] 事件 payload 补 `duration_ms` / `trace` 字段
- [ ] Golden test：固定 event seq → 断言模型侧 observation 结构

### Phase 2 — 人类 trace 视图

- [ ] `internal/observability` 内核（`bus`, `mapper`, `human.go`）
- [ ] `emitStep` 接 `bus.Emit`
- [ ] `GET /v1/sessions/{id}/trace`
- [ ] `bin/tma trace show`

### Phase 3 — Perfetto（开发可选）

- [ ] `perfetto/exporter.go`
- [ ] `bin/tma trace export --format perfetto --open`
- [ ] 开发默认 `TMA_PERFETTO=1` 文档说明

### Phase 4 — Langfuse（按需）

- [ ] `langfuse/exporter.go`（参考 Hermes `plugins/observability/langfuse`）
- [ ] 脱敏与 `TMA_LANGFUSE_LOG_*` 门控
- [ ] mock API 集成测试

### Phase 5 — OTel（生产按需）

- [ ] `otel/bootstrap.go` 懒加载
- [ ] Traces + Metrics + `GET /metrics`
- [ ] Grafana dashboard（可选）

### Phase 6 — 运维

- [ ] `bin/tma observability status`
- [ ] `bin/tma debug bundle`（脱敏打包）
- [ ] `reject_and_continue` 审批观测回环

## 与现有模块映射

| 现有组件 | 观测职责 |
|----------|----------|
| `session_events` | 事实源；trace 重建；模型摘要原料 |
| `EmitStep` / `AppendRuntimeEvent` | 埋点入口 |
| `tools.ResultMessage` | ProjectForModel 主输出 |
| `ContextBuilder` | env snapshot + summary 注入 |
| `session_interventions` | 审批审计；intervention span |
| `RecordLLMUsage` | OTel metrics 校验 / 双写 |
| SSE hub | 实时展示投影（不入 Langfuse） |
| `bin/tma event stream` | CLI timeline |

## 参考

- Hermes-Agent：`gateway/stream_events.py`（展示与历史分离）、`hermes_cli/plugins.py`（hook 总线）、`plugins/observability/langfuse`
- Claude Code：`src/utils/telemetry/sessionTracing.ts`、`instrumentation.ts`、`perfettoTracing.ts`
- TMA：`docs/agent-runtime.md`、`docs/capability-provider.md`

## 总结

> **以 `session_events` 为唯一事实源；`ProjectForModel` 驱动 Agent 回环（tool observation、环境快照、摘要）；`ProjectForHumans` 按需投影到 /trace、Perfetto、Langfuse、OTel。先让模型「看得见、纠得了」，再给人「查得着、告警得了」。**
