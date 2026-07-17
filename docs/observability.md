# TMA Observability Design

本文档记录 TMA 可观测性规划。设计参考 Hermes-Agent（Plugin Hook + Gateway 展示流）、Claude Code（OTel 三信号 + Perfetto 本地 trace），并贴合 TMA 现有 **`session_events` 事件溯源** 架构。

核心判断：**可观测性数据有两类观众——模型和人。** 它们共享同一事实源，但投影格式、时机和约束不同。给人看的 Perfetto / Langfuse / OTel 是可选插件；给模型用的结构化观测（Observation）是 Agent 完成任务的基础设施。

## 设计目标

1. **模型可感知**：错误、环境、跨 turn 轨迹能以结构化形式进入 LLM 上下文，支持自愈与任务完成。
2. **人类可调试**：开发、产品、运维能从同一事实源查看 trace、成本与延迟。
3. **单一事实源**：`session_events` 为权威记录；导出器可关、可采样，挂掉不影响 turn 执行。
4. **渐进落地**：先 `ProjectForModel`，再 `/trace` API，再按需接 Perfetto / Langfuse / OTel。

## 当前实现状态（2026-07-09）

已落地：

- `session_events` 仍是唯一事实源。
- `GET /v1/sessions/{id}/trace?turn_id=...` 已可把单个 turn 投影成 trace JSON，包含 `stats`、`turns`、trace graph（roots / edges / critical path）、timeline steps、span tree、span depth / waterfall offsets / self duration、child span 索引与 span 内 source events。
- `GET /v1/traces`、`GET /v1/traces/{trace_id}`、`GET /v1/spans` 与 `GET /v1/traces/{trace_id}/spans/{span_id}` 已提供近期 trace catalog、直接 trace 查找、span 搜索聚合（kind/status/critical counts）、span 详情深链；`/v1/spans` 支持 `trace_id` / `session_id` / `turn_id` / `kind` / `status` / `critical` / `min_duration_ms` / `max_duration_ms` / `min_self_duration_ms` / `q` 过滤。
- 主 turn 与审批 continuation 现在统一由 AgentRuntime 写出关键 runtime 事件，并携带原生 `trace_id` / `span_id` / `parent_span_id` / `span_name` / `span_kind` / `span_status`；LLM/tool 结果事件会尽量写入真实 `duration_ms`，旧事件仍可由投影层回退配对。
- `bin/tma trace show --session ... [--turn ...]` 已可终端查看 turn stats、trace graph、critical path、span tree / self duration、timeline 与 tool/artifact 线索。
- `GET /v1/sessions/{id}/trace?turn_id=...&format=perfetto|otel` 已可导出 Perfetto / OTel 风格 JSON；Perfetto args 与 OTel attributes 会携带 span depth / start offset / self duration / critical 标记，导出 metadata 会携带 trace graph，OTel span events 会携带关联的 session event seq/type/message。
- `bin/tma trace export --format perfetto|otel|json --output FILE` 已可把导出落盘；`--otlp-endpoint URL` 已可把 OTel JSON 通过 OTLP/HTTP 推到 collector。
- turn 完成后已可按环境变量自动 fan-out exporter：`TMA_PERFETTO=1` 写 Perfetto 文件，`TMA_OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_ENDPOINT` 推 OTLP/HTTP traces；`TMA_OBSERVABILITY_SAMPLE_RATE` 可对自动导出做确定性采样。
- `GET /metrics` 已可输出 Prometheus 文本指标，覆盖授权决策（按 auth type/outcome/reason）、LLM usage、worker 数量、Skills 外部 binary scanner 次数/耗时、exporter 启用、自动导出采样率、最近成功/最近失败/最近尝试/最近持久化运行记录，以及指定 `session_id` / `turn_id` 的 event、trace、span、critical path、span depth、tool、approval 和 completion validation（pass/retry/fail、bounded validator）指标。逐身份授权调查使用结构化 `authorization_decision` 日志，Prometheus 标签不包含 subject、Workspace、Group 或路径。
- `deploy/prometheus/tma-security-alerts.yml` 同时包含 completion validation fail 和高 retry rate 告警；告警使用默认 `/metrics` 暴露的进程级 `tma_completion_validation_events_total` counter。Inspector 的 Completion Quality 面板使用带 Session/Turn 参数的 `tma_completion_validation_total` 诊断 gauge，显示当前 Turn 的 pass/retry/fail、重试率和 validator。
- 授权决策可通过 `TMA_SECURITY_AUDIT_OTLP_ENDPOINT` 发送到 OTLP/HTTP Logs `/v1/logs`；默认先写 PostgreSQL durable outbox，再由租约 worker 批量投递、指数退避并在最大次数后进入 dead letter。生产使用 HMAC-SHA256 完整性校验，下游按稳定 `event.id` 去重。发送、持久化失败、outbox 状态、最老积压和 key rotation blockers 进入 Prometheus；`deploy/prometheus/tma-security-alerts.yml` 提供认证失败、跨 scope、Operator 拒绝、persistence/export failure、dead letter、backlog、未知 key 引用和长期轮换阻塞规则。
- `GET /v1/observability/status` 与 `bin/tma observability status` 已可查看 exporter 启用状态、目的地、采样策略、最近成功/失败/尝试、持久化 recent runs，以及授权审计 HMAC key rotation readiness；token 和完整性密钥只暴露是否配置或 key ID，不返回密钥值。
- `GET /v1/observability/security-audit/integrity-keys` 与 `bin/tma observability integrity-keys` 提供逐 key ID 的 pending/delivering/delivered/dead-letter 计数和服务端 `safe_to_remove` 判定。
- exporter 失败 run 已写入 `attempt_count` / `next_retry_at`，支持指数退避；server 默认会在后台周期性重试到期项，也可通过 `POST /v1/observability/retry`、Inspector Exporters 面板或 `bin/tma observability retry` 手动触发。
- `GET /inspector` 已提供 embedded static Inspector UI（`internal/httpapi/inspector/index.html`、`styles.css`、`app.js`），可查看 turn 列表、trace stats、span waterfall / critical path、可过滤 span table、span 详情、span events/child counts、usage、summary、context coverage/diff、context budget 分账、pending approvals、artifacts、artifact inline preview、events、metrics、exporter / sampling 状态与 raw export，并支持 `#session=...&turn=...&trace=...&span=...` 深链定位。
- turn 完成后会把本轮 tool / approval 轨迹摘要追加到 `session_summaries`，供后续 `ContextBuilder` 注入。
- `reject` 决策在存在 continuation messages 时，会生成 rejected tool observation 并继续同一条 LLM continuation loop。
- `/trace` 返回里已包含第一版 `spans`，不再只是纯 timeline steps。

仍待做：

- 专门的 `internal/observability` bus / exporter 分层。
- 更完整的原生 span 生命周期覆盖（所有事件写入时即有精确 start/end，而不只是在关键 runtime 事件上写 trace metadata）。
- 生产级 trace/span 存储索引与更高级查询（当前仍以 `session_events` 投影和近期 catalog/search 为主）。
- Inspector 进一步补浏览器端视觉回归验证，以及更完整的前端组件化 / 构建链路。
- exporter runs 已有持久化运行记录与手动 retry/backoff；后续需补后台自动 retry worker 与失败告警。
- 后台异步 observability bus / exporter 分层、生产级采样增强（例如错误 100% 保留、按 workspace 配额）、Langfuse exporter。

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
runtime.subagent_spawn_rejected / runtime.subagent_start_rejected
runtime.subagent_start_queued / runtime.subagent_start_dequeued
runtime.subagent_start_canceled / runtime.subagent_start_expired
runtime.context_compacting / compacted / failed
runtime.completed / failed
```

能力：

- `GET /v1/sessions/{id}/events?after_seq=` 历史续传
- SSE `events/stream` 实时推送
- `session_interventions` 审批审计
- `RecordLLMUsage` 用量落库

**原则（借鉴 Hermes Gateway）**：SSE / CLI 可读展示是**展示层投影**，不改变 DB 中事实 payload 契约。

### Span closure 契约

Server Core 不为每个内置动作重复写 `runtime.span_started/ended`。内置 Span 直接由语义事件闭合：

- `runtime.started → runtime.completed|runtime.failed|session.status_idle`：`tma.interaction`；
- `runtime.llm_request → runtime.llm_response`：`tma.llm`；
- `runtime.tool_call → runtime.tool_result`：`tma.tool.*`；
- `runtime.tool_intervention_required → approved|rejected`：`tma.tool.blocked_on_user`；
- `runtime.context_compacting → compacted|compaction_failed`：`tma.context.compact`。

同一 Turn 的 Runner 复用一个 trace state，成对事件使用相同 `span_id`，结束事件写 `span_status` 和 `duration_ms`。Store 直接写出的审批、interrupt synthetic tool result 和 idle 事件由 Trace 投影按确定性 span ID 补全。Turn 已进入 failed/interrupted/completed 终态但子操作缺少结束事件时，投影器会在 Turn 终点闭合为 `error` / `canceled`，避免 Inspector 永久显示 open。

`runtime.span_started` / `runtime.span_event` / `runtime.span_ended` 保留给 Worker、插件或外部长任务表达没有对应内置语义事件的自定义 Span，不与上述核心事件强制双写。

## 给模型用的观测（ProjectForModel）

### 1. Tool 观测：`tma.tool_result.v1`

每次 tool 执行后，通过 `tools.ResultMessage()` 送回 LLM 的 JSON（`tool` role message）：

```json
{
  "protocol_version": "tma.tool_result.v1",
  "call_id": "call_edit",
  "identifier": "default",
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
tma.interaction                    # runtime.started → completed/failed/idle
├── tma.llm                        # runtime.llm_request → llm_response
├── tma.tool                       # runtime.tool_call
│   ├── tma.tool.blocked_on_user   # runtime.tool_intervention_required
│   │   └── approved / rejected
│   └── tma.tool.execution         # runtime.tool_result
├── tma.context.compact            # context_compacting / compacted
└── interaction closure            # runtime.completed / failed / session idle
```

`trace_id` 建议：`trc_{session_id}_{turn_id}`。

### 事件 → Observation 映射

| `session_events.type` | Span Kind | 说明 |
|------------------------|-----------|------|
| `user.message` | interaction 开始 | 新 turn root |
| `runtime.llm_request` | llm_request 开始 | 含 `tool_round` |
| `runtime.llm_response` | llm_request 结束 | 写 usage |
| `runtime.llm_chunk` | llm_request 子切片 | `data.type=text|reasoning|tool_call|usage|stop|error` |
| `runtime.llm_delta` | llm_request 子切片 | 主要给 Perfetto |
| `runtime.tool_call` | tool 开始 | |
| `runtime.tool_result` | tool 结束 | `duration_ms`, `outcome` |
| `runtime.tool_intervention_*` | intervention | 审批子 span |
| `runtime.subagent_spawn_rejected` | governance event | subagent quota / depth 拒绝 |
| `runtime.subagent_start_rejected` | governance event | workspace / user queued quota 拒绝 |
| `runtime.subagent_start_queued` | governance event | 启动请求持久化排队 |
| `runtime.subagent_start_dequeued` | governance event | 排队请求获得 active 槽位 |
| `runtime.subagent_start_canceled` | governance event | 排队请求被显式或级联取消 |
| `runtime.subagent_start_expired` | governance event | 排队请求超时 |
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
TMA_PERFETTO_DIR=~/.tma/traces
# 输出：~/.tma/traces/{session_id}/{turn_id}.perfetto.json
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
OTEL_SERVICE_NAME=tiggy-manage-agent
TMA_OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.corp:4318
TMA_OTEL_EXPORTER_OTLP_TOKEN=...
# 或使用 OpenTelemetry 约定的环境变量：
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.corp:4318
```

**自动导出采样**

```bash
# 默认 1.0，即自动导出每个完成 turn；0.1 表示按 session_id + turn_id 确定性采样 10%
TMA_OBSERVABILITY_SAMPLE_RATE=0.1
```

**Exporter retry**

```bash
# 默认启用；失败 exporter run 会写入 next_retry_at，后台 worker 与手动触发都只重试到期项
TMA_OBSERVABILITY_EXPORTER_RETRY=1
TMA_OBSERVABILITY_EXPORTER_RETRY_MAX_ATTEMPTS=3
TMA_OBSERVABILITY_EXPORTER_RETRY_INITIAL_DELAY_MS=30000
TMA_OBSERVABILITY_EXPORTER_RETRY_MAX_DELAY_MS=600000
TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_ENABLED=1
TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_INTERVAL_MS=30000
TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_LIMIT=20
```

**Trace/span index retention**

```bash
# 默认关闭；开启后只清理 trace_indexes / trace_span_indexes，不删除 session_events 事实源
TMA_TRACE_INDEX_RETENTION_ENABLED=1
TMA_TRACE_INDEX_RETENTION_DAYS=30
TMA_TRACE_INDEX_RETENTION_INTERVAL_MS=3600000
TMA_TRACE_INDEX_RETENTION_LIMIT=1000
```

### 按场景启用

| 场景 | Perfetto | Langfuse | OTel |
|------|:--------:|:--------:|:----:|
| 本地开发 | ✓ | 可选 | console |
| 测试 / prompt 调优 | — | ✓ | console |
| 生产 | — | 采样 10% | ✓ |
| 内网不出网 | ✓ | 自托管 | 自建 Collector |
| CI golden trace | export | — | — |

## 当前 API / CLI

```text
GET  /v1/sessions/{id}/trace?turn_id=...     # JSON span 树（给人）
GET  /v1/traces?limit=...                     # 近期 trace catalog
GET  /v1/traces/{trace_id}                    # 按 trace_id 直接查完整 trace
GET  /v1/spans?q=...&kind=...&status=...      # 近期 span 搜索与聚合
GET  /v1/spans?trace_id=...&critical=true     # 按 trace / critical path / duration 等维度筛 span
GET  /v1/traces/{trace_id}/spans/{span_id}    # span 详情深链
GET  /metrics                                 # Prometheus
GET  /metrics?session_id=...&turn_id=...      # Session / turn 维度指标
GET  /v1/observability/status                 # Exporter 配置、采样与健康状态
GET  /v1/observability/security-audit/integrity-keys # HMAC key rotation readiness
POST /v1/observability/retry                  # 手动重试到期 exporter failures

bin/tma trace show --session ... --turn ...   # 终端树形输出
bin/tma trace export --session ... --format perfetto --output trace.json
bin/tma trace export --session ... --format otel --otlp-endpoint http://collector:4318
bin/tma observability status                  # Exporter 状态
bin/tma observability integrity-keys          # HMAC 旧 key 移除检查
bin/tma observability retry                   # 手动重试到期 exporter failures
TMA_PERFETTO=1 TMA_PERFETTO_DIR=./traces bin/tma-server
TMA_OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318 TMA_OBSERVABILITY_SAMPLE_RATE=0.1 bin/tma-server
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
| `tma_skill_binary_scans_total` | Counter | `provider`, `outcome` |
| `tma_skill_binary_scan_duration_milliseconds_total` | Counter | `provider`, `outcome` |
| `tma_skill_asset_gc_runs_total` | Counter | `outcome`, `dry_run` |
| `tma_skill_asset_gc_objects_total` | Counter | `outcome` |
| `tma_skill_asset_gc_bytes_total` | Counter | `outcome` |
| `tma_skill_asset_gc_candidates` | Gauge | - |
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
