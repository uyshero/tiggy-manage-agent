# TMA API Reference

本文档记录当前实现中的 HTTP API 契约。它不是未来 SDK 设计稿，也不是完整 OpenAPI；目标是让 CLI、测试脚本、UI 和后续 SDK 都能按同一份真实接口对齐。

Go Core SDK 使用 `/v2`。除本文单独列出的 Run API 和结构化错误外，`/v2` 用户与控制面路径与 `/v1` 同名并复用相同成功响应；Worker register/heartbeat/poll/ack/result 机器协议只保留在 `/v1`。完整可生成契约见 `api/v2/openapi.yaml`。

当前默认服务地址：

```text
http://localhost:8080
```

## Authentication

When `TMA_AUTH_MODE` is `oidc`, `jwt`, or `gateway`, every `/v1/*` endpoint requires a unified Principal. `/health` and static Workbench/Inspector assets remain public. `/metrics` additionally requires `operator` or `admin`.

OIDC and JWT clients send `Authorization: Bearer <token>`. Browser deployments may use the `tma_access_token` Cookie so SSE reconnects remain authenticated; Cookie-authenticated `POST`, `PUT`, `PATCH`, and `DELETE` requests must carry a trusted `Origin`. Trusted gateways inject `X-TMA-Subject`, `X-TMA-Organization-ID`, `X-TMA-Workspace-ID`, `X-TMA-Owner-ID`, and `X-TMA-Roles` together with the configured `X-TMA-Gateway-Token` secret.

Roles are hierarchical: `viewer`, `member`, `operator`, `admin`. The server derives Organization, Workspace, Owner, and actor fields from the Principal; body/query fields cannot broaden the authenticated scope.

```text
GET /v1/auth/me
```

Returns the effective authenticated Principal.

Protected requests emit structured authorization decision logs. Successful OIDC resolution records the configured Claim, role mapping, and matched Group mapping sources used to derive the Principal; tokens, Cookies, query strings, and raw unmatched Claims are never logged. Internal authorization sources are not part of the Principal response. Operators can monitor aggregate results through `tma_authorization_decisions_total{auth_type,outcome,reason}` and the `tma_security_audit_exporter_*` / `tma_security_audit_export_events_total` metrics on `/metrics`.

`GET /v1/observability/status` includes `security_audit_outbox` counts and the oldest pending age when the Store supports the durable outbox. Operators can manually move dead-letter events back to pending with:

```text
POST /v1/observability/security-audit/replay?limit=100
```

The endpoint requires `operator` or `admin`; `limit` must be between 1 and 1000. Replayed events reset their attempt counter and retain the original stable event ID.

Operators can inspect HMAC key rotation readiness with:

```text
GET /v1/observability/security-audit/integrity-keys
```

The response contains the active key ID and per-key `pending`, `delivering`, `delivered`, `dead_letter`, `blocking`, `configured`, `active`, and `safe_to_remove` fields. It never returns key material. `safe_to_remove` is true only for a configured non-active key with no pending, delivering, or dead-letter rows, and only when no migration-era HMAC rows with an empty key ID remain blocked. The same summary appears as `security_audit_integrity_keys` in `GET /v1/observability/status`. Both endpoints require `operator` or `admin`.

## 基础约定

### JSON

除 SSE 外，请求和响应均使用 JSON：

```http
Content-Type: application/json
```

服务端 JSON decoder 当前启用 `DisallowUnknownFields`，请求体中出现未声明字段会返回 `400`。

时间字段使用 RFC3339 字符串，例如：

```json
"2026-07-08T14:13:00Z"
```

### 错误响应

错误响应目前统一为：

```json
{
  "error": "invalid input: ..."
}
```

常见状态码：

| 状态码 | 含义 |
|---:|---|
| `400` | 请求体、查询参数或状态机输入非法 |
| `401` | 缺少或提供了无效身份凭据 |
| `403` | 角色不足、Owner 不匹配、跨 Workspace/Organization 访问，或 Cookie 写请求 Origin 不可信 |
| `404` | 资源不存在 |
| `409` | Session 已终止等冲突状态 |
| `500` | 服务端内部错误 |

### ID 和默认值

当前本地开发默认 workspace 为 `wksp_default`。认证开启后，服务端始终使用 Principal 的 workspace；客户端提交的 `workspace_id` 不能改变资源归属。只有显式关闭认证的开发模式才使用 Store 默认值。

常见 ID 前缀：

| 资源 | 示例 |
|---|---|
| Agent | `agt_000001` |
| Environment | `env_000001` |
| Session | `sesn_000001` |
| Event | `evnt_000001` |
| Turn | `turn_000001` |
| LLM usage | `llmu_000001` |

## Health

### `GET /health`

返回服务健康状态。

响应 `200`：

```json
{
  "status": "ok",
  "service": "tiggy-manage-agent"
}
```

## LLM Provider

### `POST /v1/llm-providers`

创建一个新的 LLM Provider。Provider ID 已存在时返回 `409 Conflict`，不会覆盖现有配置。

请求：

```json
{
  "id": "volcengine-agent-plan",
  "provider_type": "openai",
  "base_url": "https://ark.cn-beijing.volces.com/api/v3",
  "api_key_env": "TMA_LLM_API_KEY_VOLCENGINE",
  "enabled": true
}
```

字段：

| 字段 | 必填 | 说明 |
|---|:---:|---|
| `id` | 是 | Provider ID |
| `provider_type` | 否 | 当前常用 `fake` 或 `openai`；`openai-compatible` 作为历史别名保留 |
| `base_url` | 否 | Provider API base URL |
| `api_key_env` | 否 | API Key 的环境变量名；不保存真实密钥 |
| `enabled` | 否 | 默认 `true` |

响应 `201`：`LLMProvider`。响应包含 `revision: 1`，并返回 `ETag: "1"`。

### `GET /v1/llm-providers`

响应 `200`：

```json
{
  "providers": [
    {
      "id": "fake",
      "provider_type": "fake",
      "enabled": true,
	  "revision": 1,
	  "created_at": "2026-07-08T06:00:00Z",
	  "updated_at": "2026-07-08T06:00:00Z"
    }
  ]
}
```

### `GET /v1/llm-providers/{provider_id}`

响应 `200`：`LLMProvider`，同时返回当前 revision 对应的 `ETag`。

### `PATCH /v1/llm-providers/{provider_id}`

部分更新 Provider。省略字段保持原值。请求必须使用上次读取到的 revision 发送强条件头，例如 `If-Match: "3"`。

请求：

```json
{
  "base_url": "https://ark.cn-beijing.volces.com/api/v3",
  "enabled": false
}
```

响应 `200`：更新后的 `LLMProvider`，revision 加一并返回新的 `ETag`。

缺少或格式非法的 `If-Match` 返回 `400 Bad Request`；revision 已过期返回 `412 Precondition Failed`，客户端应重新读取 Provider 后再提交。

### `POST /v1/llm-providers/{provider_id}/enable`

启用 Provider。请求体可为空对象 `{}`，必须携带当前 `If-Match`。

响应 `200`：更新后的 `LLMProvider` 和新 `ETag`。

### `POST /v1/llm-providers/{provider_id}/disable`

禁用 Provider。请求体可为空对象 `{}`，必须携带当前 `If-Match`。

响应 `200`：更新后的 `LLMProvider` 和新 `ETag`。

### `POST /v1/llm-providers/{provider_id}/test`

使用已保存的 Base URL 和 `api_key_env` 指向的进程环境变量执行连接诊断。OpenAI 兼容 Provider 会调用 `GET {base_url}/models`，验证地址、网络与认证；`fake` Provider 在本地返回成功。该操作使用 control auth，并记录 `llm.provider.test` 审计事件。

诊断本身执行完成时响应 `200`，上游连接失败通过 `status: "failed"` 表示。响应只包含固定错误类型、固定文案、耗时与认证状态，不返回 API Key、URL、URL query、请求正文或上游响应正文。

```json
{
  "status": "succeeded",
  "latency_ms": 83,
  "authenticated": true,
  "message": "Provider connection succeeded.",
  "retryable": false,
  "checked_at": "2026-07-15T07:30:00Z"
}
```

### `DELETE /v1/llm-providers/{provider_id}`

删除未被引用的 Provider，必须携带当前 `If-Match`。成功返回 `204`；revision 过期返回 `412 Precondition Failed`，Provider 仍被引用时返回 `409 Conflict`。

## LLM Model

### `POST /v1/llm-models`

创建或更新 Provider 下的模型元数据。创建时必须携带 `If-None-Match: *`；更新时必须携带上次读取到的强 ETag，例如 `If-Match: "3"`。

请求：

```json
{
  "provider_id": "volcengine-agent-plan",
  "model": "doubao-seed-2.0-pro",
  "context_window_tokens": 256000,
  "capability_type": "text_image",
  "capabilities": {},
  "is_default_vision": true
}
```

创建成功响应 `201`，更新成功响应 `200`。两者都会返回 `ETag: "<revision>"`：

```json
{
  "provider_id": "volcengine-agent-plan",
  "model": "doubao-seed-2.0-pro",
  "context_window_tokens": 256000,
  "capability_type": "text_image",
  "capabilities": {"normalized": false},
  "is_default_vision": true,
  "is_default_embedding": false,
  "is_default_reranker": false,
  "revision": 3,
  "created_at": "2026-07-08T06:00:00Z",
  "updated_at": "2026-07-08T06:00:00Z"
}
```

`capability_type` 是显式模型能力类型：

| 值 | 说明 |
|---|---|
| `text` | 文本理解/生成 |
| `text_image` | 文本 + 图片理解，可接收 OpenAI 兼容 `image_url` 内容 |
| `image_generation` | 图片生成；当前仅登记能力，未接入生成工作流 |
| `video_generation` | 视频生成；当前仅登记能力，未接入生成工作流 |
| `embedding` | 文本向量化模型；需要配置维度、距离度量和调用协议 |
| `reranker` | 查询与候选文档重排模型；需要配置调用协议和候选上限 |

`is_default_vision=true` 只允许用于 `text_image` 模型，且全局最多一个。把新模型设为默认视觉模型时，旧默认值会自动清除。当前 Session 模型不支持图片时，Runtime 先用该统一视觉模型提取图片内容，再把分析文本交给当前模型继续处理。

Embedding 模型示例：

```json
{
  "provider_id": "enterprise-ai",
  "model": "BAAI/bge-m3",
  "context_window_tokens": 8192,
  "capability_type": "embedding",
  "capabilities": {
    "dimensions": 1024,
    "distance_metric": "cosine",
    "normalized": true,
    "max_batch_size": 32,
    "protocol": "openai_embeddings"
  },
  "is_default_embedding": true
}
```

Reranker 模型示例：

```json
{
  "provider_id": "enterprise-ai",
  "model": "BAAI/bge-reranker-v2-m3",
  "context_window_tokens": 8192,
  "capability_type": "reranker",
  "capabilities": {
    "max_candidates": 50,
    "protocol": "jina_rerank"
  },
  "is_default_reranker": true
}
```

`is_default_embedding` 和 `is_default_reranker` 分别只允许用于对应能力类型，且各自全局最多一个。Embedding/Reranker 不能作为 AgentConfigVersion 的对话模型；当前接口完成模型目录、版本、默认选择和审计，实际 Embedding/Rerank 推理客户端由知识库检索模块接入。

`revision` 是服务端递增的并发版本。任何模型更新都会递增目标模型的 revision；默认视觉、Embedding 或 Reranker 模型切换还会递增被取消默认状态的旧模型 revision。缺少或格式非法的条件头返回 `400 Bad Request`，重复创建返回 `409 Conflict`，revision 已过期返回 `412 Precondition Failed`。冲突后客户端应重新读取模型列表再提交。

### `GET /v1/llm-models`

查询模型列表。

查询参数：

| 参数 | 说明 |
|---|---|
| `provider_id` | 可选；只返回该 Provider 下的模型 |

响应 `200`：

```json
{
  "models": []
}
```

### `POST /v1/llm-models/{provider_id}/{model}/test`

按模型能力执行最小真实请求，并记录 `llm.model.test` 审计事件：

- `text` / `text_image`：最小 Chat Completion。
- `embedding`：按 `openai_embeddings`、`tei_embeddings` 或 `ollama_embed` 协议生成真实向量，并校验实际维度与模型目录配置一致。
- `reranker`：按 `jina_rerank`、`cohere_rerank` 或 `vllm_score` 协议对两个候选文本重排，并校验返回结果数。

成功响应示例：

```json
{
  "status": "succeeded",
  "capability_type": "embedding",
  "protocol": "openai_embeddings",
  "latency_ms": 124,
  "dimensions": 1024,
  "authenticated": true,
  "message": "Model diagnostic succeeded.",
  "retryable": false,
  "checked_at": "2026-07-15T07:30:00Z"
}
```

失败时 `error_type` 是稳定枚举：`configuration`、`authentication`、`rate_limit`、`timeout`、`network`、`invalid_request`、`invalid_response`、`dimension_mismatch`、`unsupported` 或 `upstream`。与 Provider 诊断相同，响应和审计都不保留上游敏感正文。

### `DELETE /v1/llm-models/{provider_id}/{model}`

删除未被 Agent 配置或 Session 引用的模型。请求必须携带当前 `If-Match`；成功返回 `204`，revision 过期返回 `412 Precondition Failed`，模型仍被引用返回 `409 Conflict`，缺少条件头返回 `400 Bad Request`。

## Workers

Worker 是执行面进程的 server-side registry 记录。`tma-worker` 通过 HTTP API 注册和心跳，不直连数据库。

### `POST /v1/workers`

注册一个 worker。

请求：

```json
{
  "workspace_id": "wksp_default",
  "name": "viito-mac",
  "worker_type": "local",
  "capabilities": {
    "tools": ["default"]
  },
  "metadata": {
    "os": "darwin"
  },
  "registered_by": "viito",
  "lease_seconds": 60
}
```

响应 `201`：`Worker`。

### `GET /v1/workers`

查询 worker 列表。

鉴权：配置 control token 后必须使用 control token。该接口会暴露 worker 名称、能力、last_seen 和 lease 信息。

查询参数：

| 参数 | 说明 |
|---|---|
| `workspace_id` | 可选，默认 `wksp_default` |
| `status` | 可选，`online` / `offline` / `draining` / `archived` |

CLI 默认会把 worker 的 `runtimes`、`apis` 和 `capabilities` 摘要展开展示；需要原始响应时使用 `bin/tma worker list --json`。

响应 `200`：

```json
{
  "workers": []
}
```

### `GET /v1/workers/{worker_id}`

鉴权：配置 control token 后必须使用 control token。该接口会暴露 worker 名称、能力、last_seen 和 lease 信息。

响应 `200`：`Worker`。

### `POST /v1/workers/diagnose`

按一次标准 tool invocation 解释当前 workspace 的在线 worker 是否能执行。CLI 和后续 UI/SDK 都应调用这个 server 侧接口，不在客户端复制 worker selector 逻辑。

鉴权：配置 token 后必须使用 worker token 或 control token。worker token 用于 `tma-worker doctor` 自检，control token 用于 CLI / 运维诊断。

请求：

```json
{
  "workspace_id": "wksp_default",
  "namespace": "default",
  "api": "run_command",
  "runtime": "local_system",
  "capabilities": ["exec"],
  "input": {}
}
```

响应 `200`：

```json
{
  "invocation": {
    "protocol_version": "tma.work.v1",
    "namespace": "default",
    "api": "run_command",
    "runtime": "local_system",
    "capabilities": ["exec"],
    "input": {}
  },
  "matches": 1,
  "diagnostics": [
    {
      "worker_id": "wrk_000001",
      "workspace_id": "wksp_default",
      "name": "viito-mac",
      "worker_type": "local",
      "status": "online",
      "match": true,
      "runtimes": ["local_system"],
      "apis": ["default.run_command"],
      "capabilities": ["exec"]
    }
  ]
}
```

### `POST /v1/workers/{worker_id}/heartbeat`

更新 worker 在线状态和租约。

请求：

```json
{
  "status": "online",
  "lease_seconds": 60
}
```

响应 `200`：更新后的 `Worker`。

### `POST /v1/workers/{worker_id}/archive`

归档 worker。

鉴权：配置 token 后必须使用 worker token 或 control token。worker token 用于 worker/doctor 自清理，control token 用于运维归档。

响应 `200`：更新后的 `Worker`。

### `POST /v1/worker-work`

向 server 控制面写入一条待执行 work。当前用于调试和最小 worker 验证；后续调度器也会走同一类 server-side enqueue 边界。

当 server 配置了 `TMA_WORKER_CONTROL_AUTH_TOKEN` 时，请求必须带：

```http
Authorization: Bearer <control-token>
```

请求：

```json
{
  "workspace_id": "wksp_default",
  "worker_id": "wrk_000001",
  "environment_id": "env_000001",
  "session_id": "sess_000001",
  "turn_id": "turn_000001",
  "work_type": "tool_execution",
  "payload": {
    "protocol_version": "tma.work.v1",
    "namespace": "default",
    "api": "run_command",
    "capabilities": ["exec"],
    "risk": "exec",
    "runtime": "local_system",
    "input": {
      "command": "sh",
      "args": ["-c", "printf hello"]
    }
  }
}
```

字段说明：

| 字段 | 说明 |
|---|---|
| `workspace_id` | 可选，默认 `wksp_default` |
| `worker_id` | 可选；指定后只会被该 worker poll 到；`tool_execution` 未指定时，server 会按在线 worker 的 namespace / api / runtime / capabilities 自动选择 |
| `environment_id` | 可选；为后续按环境选择 worker 留出的关联 |
| `session_id` | 可选；关联发起 work 的 session |
| `turn_id` | 可选；关联发起 work 的 turn |
| `work_type` | 可选，默认 `tool_execution`；当前支持 `tool_execution` / `sandbox_command` / `artifact_sync` |
| `payload` | 可选 JSON object；`tool_execution` 必须符合 `tma.work.v1` work invocation |

响应 `201`：新建的 `WorkerWork`。

如果 `tool_execution` 未指定 `worker_id` 且没有匹配的在线 worker，返回 `409`，响应体会带同一套 worker diagnostics：

```json
{
  "error": "conflict: no online worker matches tool invocation default.run_command runtime local_system",
  "invocation": {
    "protocol_version": "tma.work.v1",
    "namespace": "default",
    "api": "run_command",
    "runtime": "local_system",
    "capabilities": ["exec"],
    "input": {}
  },
  "matches": 0,
  "diagnostics": [
    {
      "worker_id": "wrk_reader",
      "workspace_id": "wksp_default",
      "name": "reader",
      "worker_type": "local",
      "status": "online",
      "match": false,
      "reasons": ["missing capability exec"],
      "runtimes": ["local_system"],
      "apis": ["default.run_command"],
      "capabilities": ["filesystem.read"]
    }
  ]
}
```

### `GET /v1/worker-work/{work_id}`

按 work id 查询 server 控制面保存的 worker work 状态，用于调试 AgentRuntime worker-backed execution、worker poll/ack/result 生命周期。

当 server 配置了 `TMA_WORKER_CONTROL_AUTH_TOKEN` 时，请求必须带 `Authorization: Bearer <control-token>`。

响应 `200`：`WorkerWork`。

### `GET /v1/worker-work/{work_id}/diagnose`

诊断队列中单个 worker work 当前状态。它解释 work 是否还在 pending、是否已被某个 worker lease、lease 是否过期、assigned worker 是否还在线，并返回建议动作。

当 server 配置了 `TMA_WORKER_CONTROL_AUTH_TOKEN` 时，请求必须带 `Authorization: Bearer <control-token>`。

响应 `200`：

```json
{
  "work": {
    "id": "work_000001",
    "worker_id": "wrk_000001",
    "work_type": "tool_execution",
    "status": "leased",
    "lease_expires_at": "2026-07-09T12:00:00Z"
  },
  "worker": {
    "id": "wrk_000001",
    "name": "viito-mac",
    "worker_type": "local",
    "status": "online",
    "lease_expires_at": "2026-07-09T12:00:00Z"
  },
  "reasons": [
    "work is leased but not acknowledged",
    "work lease expired at 2026-07-09T12:00:00Z"
  ],
  "actions": [
    "run: bin/tma work reap-expired"
  ]
}
```

### `POST /v1/worker-work/{work_id}/cancel`

手动取消 pending / leased / running 的 worker work。取消后 work 状态变为 `canceled`，worker 后续 heartbeat / result 会看到 canceled 状态，已完成或已失败的 work 不会被重新改写。

当 server 配置了 `TMA_WORKER_CONTROL_AUTH_TOKEN` 时，请求必须带 `Authorization: Bearer <control-token>`。

请求体：

```json
{
  "reason": "user stopped it"
}
```

响应 `200`：更新后的 `WorkerWork`。

### `POST /v1/worker-work/{work_id}/requeue`

从控制面把一条 `failed` / `canceled` worker work 复制成新的 `pending` work。原 work 不会被修改，新的 work 会保留 workspace / environment / session / turn / work_type / payload，清空 result / error / lease / started_at / completed_at。

默认会复制原来的 `worker_id`；如果希望新 work 由同 workspace 的任意 worker 消费，可以传 `clear_worker: true`；如果要指定新 worker，可以传 `worker_id`。`clear_worker` 和 `worker_id` 不能同时使用。`completed`、`pending`、`leased`、`running` work 不允许 requeue，避免重复执行仍在进行或已成功完成的任务。

当 server 配置了 `TMA_WORKER_CONTROL_AUTH_TOKEN` 时，请求必须带 `Authorization: Bearer <control-token>`。

请求体：

```json
{
  "clear_worker": true
}
```

响应 `201`：新创建的 `WorkerWork`。

### `POST /v1/worker-work/reap-expired`

控制面手动收敛过期 worker work。Server 会扫描 `status in (leased, running)` 且 `lease_expires_at` 已经过期的 work，将其标记为 `failed`，并写入 `error_message`。第一版不自动重新入队，避免重复执行有副作用的工具调用。

当 server 配置了 `TMA_WORKER_CONTROL_AUTH_TOKEN` 时，请求必须带 `Authorization: Bearer <control-token>`。

请求体：

```json
{
  "limit": 100
}
```

| 字段 | 说明 |
|---|---|
| `limit` | 可选，默认 100，最大 1000 |

响应 `200`：

```json
{
  "count": 1,
  "expired": [
    {
      "id": "work_000001",
      "status": "failed",
      "error_message": "worker work lease expired at 2026-07-09 12:00:00+00"
    }
  ]
}
```

### `GET /v1/workers/{worker_id}/work/poll`

Worker 拉取一条待执行 work。

查询参数：

| 参数 | 说明 |
|---|---|
| `lease_seconds` | 可选，默认 60 |

响应 `200`：

```json
{
  "work": null
}
```

如果有 work，会返回一个 `WorkerWork`。

当前 work type：

| work_type | 当前 worker 行为 |
|---|---|
| `sandbox_command` | `tma-worker` 将 `payload` 解析为 `capability.RunCommandRequest`，并通过 `LocalSystemProvider.RunCommand` 在运行 worker 的机器上执行 |
| `tool_execution` | `payload` 必须是 `tma.work.v1`；`tma-worker` 通过本地 tool registry 执行 `default.*` 或 Process Plugin 声明的 `namespace.api`；`browser.*` 也由插件提供 |
| `artifact_sync` | 当前返回 echo result |

`tool_execution` payload 示例：

```json
{
  "protocol_version": "tma.work.v1",
  "namespace": "default",
  "api": "run_command",
  "capabilities": ["exec"],
  "risk": "exec",
  "runtime": "local_system",
  "input": {
    "command": "sh",
    "args": ["-c", "printf hello"],
    "timeout_ms": 120000,
    "max_output_bytes": 65536
  }
}
```

`sandbox_command` payload 示例：

```json
{
  "command": "sh",
  "args": ["-c", "printf hello"],
  "work_dir": ".",
  "env": {},
  "timeout_ms": 120000,
  "max_output_bytes": 65536
}
```

`sandbox_command` result 示例：

```json
{
  "status": "executed",
  "work_id": "work_000001",
  "work_type": "sandbox_command",
  "worker_name": "viito-mac",
  "command_result": {
    "status": "completed",
    "exit_code": 0,
    "stdout": "hello",
    "stdout_bytes": 5,
    "stdout_captured_bytes": 5,
    "duration_ms": 3
  }
}
```

`timeout_ms` 默认 120000，范围 100～600000。`max_output_bytes` 分别作用于 stdout 和 stderr，默认每路 65536，范围 1024～1048576；超限部分不保留，但结果继续返回流的总字节数和 `*_truncated`。Unix 本机执行超时会终止整个进程组。

### `POST /v1/workers/{worker_id}/work/{work_id}/ack`

确认 worker 已开始处理该 work。

响应 `200`：更新后的 `WorkerWork`。

### `POST /v1/workers/{worker_id}/work/{work_id}/heartbeat`

刷新 work 的租约。

请求：

```json
{
  "lease_seconds": 60
}
```

响应 `200`：更新后的 `WorkerWork`。

### `POST /v1/workers/{worker_id}/work/{work_id}/result`

提交 work 结果。

请求：

```json
{
  "success": true,
  "result": {},
  "error_message": ""
}
```

响应 `200`：更新后的 `WorkerWork`。

## Agent

服务启动时会幂等初始化内置通用智能体 `agt_general`。它绑定启动配置中的默认 LLM Provider 和 Model，未显式配置 `tools`，因此可使用当前默认内置工具集合。

### `GET /v1/agents/default`

返回内置通用智能体。该别名当前解析到 `agt_general`，响应 `200`：`Agent`。

### `POST /v1/agents`

创建 Agent，并创建第一版 config version。

请求：

```json
{
  "workspace_id": "wksp_default",
 "name": "Code Assistant",
 "llm_provider": "fake",
 "llm_model": "fake-demo",
 "system": "You are a coding agent.",
  "tools": ["default.read_file"],
  "skills": {}
}
```

兼容字段：

| 字段 | 说明 |
|---|---|
| `model` | 旧字段；当 `llm_model` 为空时会作为模型名使用 |

如果 `llm_provider` 为空，服务端使用启动时的默认 Provider。如果 `llm_model` 和 `model` 都为空，服务端使用启动时的默认 Model。

`tools` 现在表示 AgentConfigVersion 的工具策略，而不是纯文本上下文。第一版兼容两种写法：

- `["default.read_file", "default.edit_file"]`
- 结构化对象：

```json
{
  "enabled_tools": ["default.read_file"],
  "runtime": "cloud_sandbox"
}
```

含义：

- `enabled_tools` / 数组元素：允许本版本暴露给模型并执行的工具或 tool API。
- `runtime`：该配置偏好的工具 runtime，当前支持 `auto`、`cloud_sandbox`、`local_system`。
- `tool_runtime`：显式指定时直接偏向该模式，仍会和 session runtime settings 合并。

如果未传 `tools`，当前会保持默认内置工具集合；`skills` 仍然只作为上下文注入，不参与 provider 选择。

`mcp` 表示 AgentConfigVersion 绑定的 MCP servers。当前支持 `stdio` 和 `streamable_http` transport，兼容两种写法：

- `{"servers":[...]}`
- `{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp/project"],"stdio_framing":"json_lines"}}}`

`stdio.env` 和 `streamable_http.headers` 值可以是 literal 字符串，也可以是引用对象：

```json
{
  "mcpServers": {
    "fetch": {
      "command": "uvx",
      "args": ["mcp-server-fetch"],
      "stdio_framing": "json_lines",
      "env": {
        "FETCH_USER_AGENT": "tiggy-manage-agent",
        "API_TOKEN": {
          "env_ref": "TMA_MCP_FETCH_TOKEN"
        }
      }
    },
    "remote_search": {
      "transport": "streamable_http",
      "url": "https://mcp.example.test/mcp",
      "listen": true,
      "roots": [
        {
          "path": "/workspace/project",
          "name": "Project"
        }
      ],
      "sampling": {
        "enabled": false
      },
      "elicitation": {
        "enabled": false
      },
      "logging": {
        "level": "warning"
      },
      "expose": {
        "resources": true,
        "prompts": true
      },
      "headers": {
        "Authorization": {
          "env_ref": "TMA_MCP_REMOTE_AUTH"
        }
      }
    },
    "secure_remote": {
      "transport": "streamable_http",
      "url": "https://secure-mcp.example.test/mcp",
      "oauth": {
        "grant_type": "client_credentials",
        "token_url": "https://auth.example.test/oauth/token",
        "client_id": {
          "env_ref": "TMA_MCP_CLIENT_ID"
        },
        "client_secret": {
          "secret_ref": "env:TMA_MCP_CLIENT_SECRET"
        },
        "scopes": ["mcp.read"],
        "resource": "https://secure-mcp.example.test/mcp",
        "token_endpoint_auth_method": "client_secret_post"
      }
    }
  }
}
```

OAuth `client_secret` 必须使用 `env_ref` 或 `secret_ref: "env:NAME"`，不能用 literal 字符串；`oauth` 与 `headers.Authorization` 互斥。

服务端会把 `mcp` 归一化为 `servers` 数组，并在运行时执行 `initialize` + `tools/list`，把返回的 MCP tools 暴露为标准 `namespace.api` model tools。MCP 标准 stdio Server 使用 newline-delimited JSON-RPC，即 `stdio_framing=json_lines`；Workbench 新建 stdio 配置会显式写入该值。为保持已有 Agent/Registry 版本行为不变，省略字段时继续按 legacy `content_length` 解释。`initialize` 返回的 server `capabilities` 会被解析并在 tooling health 中展示，包括 `tools`、`resources`、`prompts`、`completions` 和 `logging`。`tooling-health` 在工具加载成功后还会额外探测 `resources/list` / `resources/templates/list` / `prompts/list`，返回 `resource_count` / `resource_template_count` / `prompt_count` 作为可观测信息，探测失败只写入诊断，不会把已加载 tools 的 MCP server 判为离线；Workbench MCP / Skills 健康检查页会把这些值展示为诊断 badge。`tools/list`、`resources/list`、`resources/templates/list`、`prompts/list` 会跟随 `nextCursor` 合并分页结果。底层 MCP client 支持 `resources/list` / `resources/templates/list` / `resources/read`、`prompts/list` / `prompts/get` 和 `completion/complete`；resources / prompts 默认不会自动注册成模型可调用工具，只有配置 `expose.resources` / `expose.prompts` 后才会以只读 `mcp_list_resources`、`mcp_list_resource_templates`、`mcp_read_resource`、`mcp_list_prompts`、`mcp_get_prompt` 桥接工具暴露，结果仍走标准工具调用链路。completion 只作为 Client / HostedClient 参数补全能力提供，不自动暴露为模型工具。server 不支持 `resources/list`、`resources/templates/list` 或 `prompts/list` 时会降级为空列表；`tools/list` 对默认 MCP tools runtime 仍保持失败，但显式开启 `expose.resources` / `expose.prompts` 后，resource-only / prompt-only server 即使不支持 `tools/list` 也可以加载为只读上下文工具源。`env_ref` / `secret_ref: "env:NAME"` 会在启动 MCP 子进程、发送 Streamable HTTP 请求或执行 OAuth client credentials token 请求前从服务端进程环境解析，数据库和 API 响应只保存引用，不保存真实 secret。Streamable HTTP 当前支持 POST JSON-RPC、JSON response、POST response SSE、`Mcp-Protocol-Version` 和 `Mcp-Session-Id` 短会话 header；配置 `oauth.grant_type=client_credentials` 时会向 `token_url` 换取 Bearer token，并注入 MCP 请求的 `Authorization` header，支持 `client_secret_post` 和 `client_secret_basic`，且与 `headers.Authorization` 互斥；带 `expires_in` 的 token 会在当前进程内缓存并在接近过期时刷新，不会持久化或通过 API 返回；`listen: true` 时会启动可选 GET SSE listener，支持 `Last-Event-ID` 重连，收到 `roots/list` 会返回配置里的 `roots`，收到 `sampling/createMessage` 或 `elicitation/create` 会返回 `-32000` 策略错误，其他 server request 返回 `-32601` fallback。私有 CA 只能由 Server 运维侧通过 `TMA_MCP_HTTP_CA_BUNDLE` 追加到系统信任池，Agent 不能关闭 TLS 校验或覆盖 CA；该信任配置统一用于 OAuth 与全部 MCP HTTP 请求。401 响应会触发 OAuth protected resource / authorization server metadata discovery，并把授权入口写入错误诊断。企业服务账号使用现有 `client_credentials` / 显式 `refresh_token`；浏览器授权、动态客户端注册、授权码 token exchange 和用户级 token 托管仅在出现个人账号连接需求时再开发。真实 sampling/elicitation、resource subscription 等 client capability 仍未实现。更详细的字段约定见 [mcp-integration.md](./mcp-integration.md)，真实第三方版本见 [mcp-server-compatibility.md](./mcp-server-compatibility.md)。

响应 `201`：

```json
{
  "id": "agt_000001",
  "workspace_id": "wksp_default",
  "name": "Code Assistant",
  "current_config_version": 1,
  "config_version": {
    "version": 1,
    "llm_provider": "fake",
    "llm_model": "fake-demo",
    "system": "You are a coding agent.",
    "created_at": "2026-07-08T06:00:00Z"
  },
  "created_at": "2026-07-08T06:00:00Z"
}
```

### `GET /v1/agents/{agent_id}`

响应 `200`：`Agent`。

### `PATCH /v1/agents/{agent_id}`

更新 Agent 名称并创建新的当前配置版本。请求可包含 `name`、`llm_provider`、`llm_model`、`system`、`tools`、`mcp` 和 `skills`；省略的配置字段继承当前版本。已存在 Session 仍固定到原 `agent_config_version`。

响应 `200`：更新后的 `Agent`。

## Workspace MCP Registry

Workspace MCP Registry 集中维护可复用 MCP server。写操作和连通性测试使用 control auth；读取操作仍按请求 principal 校验 workspace。支持端点：

PostgreSQL 还通过 `000052_mcp_registry_rls.sql` 对 Registry server 和不可变版本执行强制 workspace RLS。请求 Principal 的 workspace 会进入事务局部 `tma.workspace_id`；跨 workspace 资源查询返回 not found，不能通过 ID 探测资源归属。生产 runtime role 必须是非 superuser、无 `BYPASSRLS`、不是表 owner，并具备两张表的 DML 与两条 Registry sequence 的使用权限。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/v1/mcp-servers?workspace_id=...` | 列出 workspace MCP servers，包含当前版本、状态和 `usage_count` |
| `GET` | `/v1/mcp-servers/runtime-status?workspace_id=...` | 返回当前 Server 进程内、按 Registry server/version 分组的脱敏 RuntimeGuard 状态 |
| `POST` | `/v1/mcp-servers` | 创建 server 和不可变 version 1 |
| `GET` | `/v1/mcp-servers/{server_id}` | 获取 server 当前配置 |
| `PATCH` | `/v1/mcp-servers/{server_id}` | 更新名称/说明；提供 `config` 时发布新的不可变版本 |
| `DELETE` | `/v1/mcp-servers/{server_id}` | 归档 server；仍被当前 Agent 使用时返回 `409` |
| `POST` | `/v1/mcp-servers/{server_id}/enable` | 启用 server |
| `POST` | `/v1/mcp-servers/{server_id}/disable` | 停用 server，固定版本 binding 会立即停止解析 |
| `POST` | `/v1/mcp-servers/{server_id}/test` | 对当前版本执行真实 initialize/catalog 连通性检查 |
| `GET` | `/v1/mcp-servers/{server_id}/versions` | 按版本倒序列出不可变配置和 SHA-256 checksum |
| `POST` | `/v1/mcp-servers/{server_id}/versions/{version}/restore` | 把历史版本复制为新的不可变当前版本；使用 control auth |

`runtime-status` 响应示例：

```json
{
  "checked_at": "2026-07-14T08:12:41Z",
  "states": [
    {
      "server_id": "mcps_000001",
      "version": 1,
      "state": "closed",
      "in_flight": 0,
      "max_concurrency": 4,
      "consecutive_failures": 0,
      "failure_threshold": 5
    }
  ]
}
```

`state` 只会是 `closed`、`saturated`、`open`、`half_open`。打开熔断时还会返回有限的 `last_failure_class`、`last_failure_at`、`open_until` 和 `cooldown_remaining_seconds`。handler 先按 principal 确定 Workspace，再用该 Workspace 当前 Registry server 列表过滤进程状态；不会返回内部 guard key、其他 Workspace、已归档/未知 server、URL、header、arguments 或结果正文。状态不持久化，Server 重启后列表为空，首次实际调用后重新建立。

创建请求：

```json
{
  "workspace_id": "wksp_default",
  "identifier": "filesystem",
  "name": "Workspace Filesystem",
  "description": "团队只读文件服务",
  "config": {
    "transport": "stdio",
    "command": "python3",
    "args": ["scripts/mcp_stdio_fixture.py", "v1"]
  }
}
```

响应中的 `id` 形如 `mcps_000001`，`current_version` 从 1 开始，`status` 为 `active`、`disabled` 或 `archived`。`config` 是单个 canonical MCP server 对象；服务端会强制其 `identifier` 与注册表一致。Authorization、Cookie、token、secret、password、API key 等敏感 header 不接受 literal，必须使用 `env_ref` / `secret_ref`。

Agent `mcp` 可引用中央版本：

```json
{
  "bindings": [
    {
      "server_id": "mcps_000001",
      "version": 0,
      "identifier": "filesystem"
    }
  ],
  "servers": []
}
```

Agent 创建、更新、config-version 发布或导入时，`version: 0` 会在落库前固定为注册表当前版本。Agent config 只保存 binding；runtime 和 tooling health 使用固定版本解析出 server 配置。中央发布新版本不会改变已有 Agent/Session；停用则作为即时 kill switch 阻止所有版本解析。旧内嵌 `servers` 与 bindings 可并存，且继续完整兼容。

版本响应示例：

```json
{
  "versions": [
    {
      "server_id": "mcps_000001",
      "version": 2,
      "checksum_sha256": "<sha256>",
      "config": {
        "identifier": "filesystem",
        "transport": "stdio",
        "command": "python3",
        "args": ["scripts/mcp_stdio_fixture.py", "v2"]
      }
    }
  ]
}
```

历史恢复要求 source version 存在且小于当前版本。服务端在一个事务中锁定 Registry server，复制 source config/checksum、追加 `current_version + 1` 并更新当前指针；不会覆盖或删除任何历史版本。响应示例：

```json
{
  "source_version": 1,
  "previous_version": 2,
  "new_version": 3,
  "server": {
    "id": "mcps_000001",
    "current_version": 3,
    "status": "active"
  }
}
```

恢复会写入 Workspace 级 `mcp_registry.version.restore` operator audit，`session_id` 为空，details 包含 source/previous/new version。已绑定 v1/v2 的 Agent 和已有 Session 不会变化；只有显式升级 Agent binding 或新发布 Agent config 才会使用恢复后生成的新版本。归档服务不能恢复，尝试恢复当前或未来版本返回 `400`。

### `POST /v1/agents/{agent_id}/tooling-health`

对 Agent 当前配置执行 MCP / Skills 健康检查。MCP 会实际执行 `initialize` 与 `tools/list`，Skills 会解析固定版本并验证内容渲染。该端点受 control auth 保护。

可选请求：

```json
{"kind":"mcp","identifier":"filesystem"}
```

不传筛选条件时检查全部绑定。状态包括 `online`、`offline`、`configuration_error` 和 `permission_required`，响应同时提供耗时、MCP 工具数、MCP initialize 声明的 `capabilities`、MCP `resource_count` / `resource_template_count` / `prompt_count`、Skill 版本和预估 token 数。Server 还会分别返回 stdio 进程与 Streamable HTTP 远程 Session 快照：

```json
{
  "mcp_host": {
    "sessions": 2,
    "in_use_sessions": 1,
    "max_sessions": 64,
    "idle_timeout_seconds": 600,
    "sweep_interval_seconds": 60,
    "starts_total": 4,
    "stops_total": 2,
    "discards_total": 0,
    "reaped_total": 2,
    "evictions_total": 0,
    "rejections_total": 0,
    "tools_list_changed_total": 3,
    "resources_list_changed_total": 1,
    "prompts_list_changed_total": 1,
    "progress_notifications_total": 8,
    "log_messages_total": 3,
    "invalid_notifications_total": 1,
    "log_messages_by_level": {"info": 2, "warning": 1}
  },
  "mcp_http_host": {
    "sessions": 1,
    "in_use_sessions": 0,
    "max_sessions": 64,
    "idle_timeout_seconds": 600,
    "sweep_interval_seconds": 60,
    "starts_total": 2,
    "stops_total": 1,
    "discards_total": 0,
    "reaped_total": 1,
    "evictions_total": 0,
    "rejections_total": 0,
    "delete_errors_total": 0,
    "tools_list_changed_total": 1,
    "resources_list_changed_total": 0,
    "prompts_list_changed_total": 0,
    "progress_notifications_total": 4,
    "log_messages_total": 2,
    "invalid_notifications_total": 0,
    "log_messages_by_level": {"error": 2},
    "egress_policy_enabled": true,
    "egress_allow_http": false,
    "egress_allow_private_networks": false,
    "egress_allowed_host_count": 2,
    "egress_allowed_cidr_count": 1,
    "egress_blocked_total": 3
  }
}
```

健康检查自身使用独立短会话，不会进入或改变业务 Session 对应的长驻 MCP 进程。

三类 `*_list_changed_total` 记录业务长驻 host 接收到的 MCP 目录变更通知。Server 不缓存目录结果；通知后的下一次 list 请求会复用原进程或远程 Session 并读取最新目录。`mcp_http_host.delete_errors_total` 记录回收远程 Session 时非预期的 DELETE 失败。

`progress_notifications_total`、`log_messages_total`、`invalid_notifications_total` 和 `log_messages_by_level` 是脱敏统计。MCP notification 的原始 `data`、logger、progress token、message 和数值不会出现在响应或指标中。

`mcp_http_host` 的 `egress_*` 字段是 Server 全局 Remote MCP 出站策略的脱敏状态。只返回开关、allowlist 数量和累计阻断数，不返回内部 host、CIDR、DNS 结果或被阻断 URL。

### `GET /v1/agents/{agent_id}/config-versions`

响应 `200`：

```json
{
  "config_versions": [
    {
      "version": 1,
      "llm_provider": "fake",
      "llm_model": "fake-demo",
      "system": "You are a coding agent.",
      "tools": ["default.read_file"],
      "created_at": "2026-07-08T06:00:00Z"
    }
  ]
}
```

### `POST /v1/agents/{agent_id}/config-versions`

创建新的 Agent config version。省略字段会继承当前版本。

请求：

```json
{
  "llm_model": "fake-v2",
  "system": "You are concise.",
  "mcp": {
    "mcpServers": {
      "filesystem": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/project"],
        "stdio_framing": "json_lines"
      }
    }
  }
}
```

兼容字段：

| 字段 | 说明 |
|---|---|
| `model` | 当 `llm_model` 为空时会作为模型名使用 |

响应 `201`：更新后的 `Agent`。

已存在 Session 会继续固定到创建时的 `agent_config_version`；新 Session 使用 Agent 当前版本。

### `POST /v1/agents/{agent_id}/config-versions/{version}/rollback`

把指定历史配置复制为一个新的当前版本。该操作不会修改或删除已有版本，也不会把版本号倒退；例如 Agent 当前为版本 4，回滚到版本 2 会创建内容与版本 2 一致的新版本 5。

`version` 必须存在且小于当前版本。来源版本绑定的 LLM Provider 必须仍处于启用状态，Skills 和 MCP 配置也会按当前校验规则重新验证。已有 Session 继续固定在原 `agent_config_version`，新 Session 使用回滚后生成的新版本。

响应 `201`：

```json
{
  "agent": {
    "id": "agt_000001",
    "current_config_version": 5,
    "config_version": {
      "version": 5,
      "llm_provider": "fake",
      "llm_model": "fake-demo",
      "system": "You are a coding agent."
    }
  },
  "previous_version": 4,
  "source_version": 2,
  "new_version": 5
}
```

成功和失败操作都会写入 `agent.config.rollback` 操作审计记录，详情包含来源版本、回滚前版本，以及成功时生成的新版本。

### `GET /v1/agents/{agent_id}/export`

把 Agent 当前配置导出为可移植的 `tma.agent` JSON 文件。响应设置 `Content-Disposition: attachment`，并包含来源 Agent、来源配置版本和导出时间。

```json
{
  "format": "tma.agent",
  "schema_version": 1,
  "exported_at": "2026-07-13T06:20:00Z",
  "source_agent_id": "agt_000001",
  "source_config_version": 5,
  "workspace_id": "wksp_default",
  "agent": {
    "name": "Code Assistant",
    "llm_provider": "fake",
    "llm_model": "fake-demo",
    "system": "You are a coding agent.",
    "tools": {"enabled_tools": ["filesystem"]},
    "mcp": {"servers": []},
    "skills": {"enabled": []}
  }
}
```

导出只保存 MCP 的 `env_ref` / `secret_ref` 等引用，不解析或返回服务端环境变量和真实 secret。操作会写入 `agent.export` 审计记录。

### `POST /v1/agents/import`

从 `tma.agent` schema v1 文档创建一个新 Agent 和配置版本 1。请求顶层可传 `name` 覆盖导出名称，也可传 `workspace_id` 指定目标工作区；否则使用文档内名称和请求上下文的工作区。

导入会重新验证 LLM Provider 是否启用、Skills 是否存在且可绑定，以及 MCP 配置是否合法。格式或 schema 版本不支持时返回 `400`。

响应 `201`：新建的 `Agent`。成功和失败操作都会写入 `agent.import` 审计记录。

## Environment

### `POST /v1/environments`

创建环境。

请求：

```json
{
  "workspace_id": "wksp_default",
  "name": "default-cloud",
  "config": {
    "type": "cloud",
    "networking": {
      "type": "limited",
      "allowed_hosts": ["api.github.com"]
    }
  }
}
```

响应 `201`：`Environment`。

## Session

### `POST /v1/sessions`

创建 Session。创建后会写入初始状态事件：

```text
session.status_provisioning
session.status_idle
```

请求：

```json
{
  "workspace_id": "wksp_default",
  "agent_id": "agt_000001",
  "environment_id": "env_000001",
  "title": "First TMA task",
  "created_by": "cli"
}
```

兼容字段：

| 字段 | 说明 |
|---|---|
| `agent` | 旧字段；当 `agent_id` 为空时作为 Agent ID |

如果 `agent_id` 和兼容字段 `agent` 都为空，服务端自动使用内置通用智能体 `agt_general`。

响应 `201`：

```json
{
  "id": "sesn_000001",
  "workspace_id": "wksp_default",
  "agent_id": "agt_000001",
  "agent_config_version": 1,
  "environment_id": "env_000001",
  "status": "idle",
  "title": "First TMA task",
  "created_by": "cli",
  "created_at": "2026-07-08T06:00:00Z"
}
```

### `GET /v1/sessions/{session_id}`

响应 `200`：`Session`。

### `GET /v1/sessions/{session_id}/task-group-tree`

返回 Inspector 使用的跨 Session agent 执行树。根节点是指定 Session，每个节点包含该 Session 创建的 task groups、直接 child sessions，以及整棵树的 `sessions`、`groups`、`items`、`queued`、`running`、`rejected`、`waiting` 和 `max_wait_seconds` 汇总。

树会包含已归档的后代 Session，便于检查 orphan 回收或服务重启后的历史执行；每个 task group state 包含 item 状态、queue wait、结果校验、aggregate 和 template metadata。

### Multi-Agent Deliberation

编排目录接口在 v2 归入 Orchestration：

```text
GET /v2/agent/task-group-templates
GET /v2/agent/discussion-strategies
```

对应的 `/v1/agent/...` 路径继续兼容现有消费者。

讨论策略与 `team_plan` JSON Schema：

```text
GET /v1/agent/discussion-strategies
```

当前提供 `brainstorm_then_critique`、`structured_debate`、`red_team_review` 和 `expert_panel`。团队包含 2–8 个动态角色，固定执行两轮；角色是对既有 Agent / Environment 的临时 profile，不创建永久 Agent 记录。

按父 Session 查询讨论及完整状态：

```text
GET /v1/sessions/{session_id}/deliberations
GET /v1/sessions/{session_id}/deliberations/{deliberation_id}
```

响应包含 discussion 的 status / phase / strategy / budget、participants、rounds、contributions、主持人 agreements / disagreements / questions，以及完成后的 structured final result。读取会执行幂等 reconciliation，因此服务重启后可从持久化 round 和 task-group 状态继续推进。

以下控制端点需要 control bearer token，并写入 `operator_audit_log`：

```text
POST /v1/sessions/{session_id}/deliberations/{deliberation_id}/cancel
POST /v1/sessions/{session_id}/deliberations/{deliberation_id}/participants/{participant_index}/retry
```

取消请求为 `{"reason":"operator canceled"}`；参与者重试请求为 `{"round_number":1}`，只允许重试当前轮中处于可恢复失败状态的参与者。

### Task group 控制端点

以下端点需要 control bearer token（配置了 `TMA_CONTROL_AUTH_TOKEN` 时）：

```text
POST /v1/sessions/{session_id}/task-groups/{group_id}/cancel
POST /v1/sessions/{session_id}/task-groups/{group_id}/retry
POST /v1/sessions/{session_id}/task-groups/{group_id}/items/{item_index}/retry
POST /v1/subagents/reap-orphans
```

`cancel` 请求可传 `{"reason":"operator canceled"}`；orphan reap 请求可传 `{"limit":100}`。Inspector 的 control token 只保存在当前浏览器 `sessionStorage`，执行树可独立每 2 秒刷新。

每次控制动作都会写入持久化 `operator_audit_log`，同时记录成功和失败结果。配置 control token 时，`principal_id` 是 token 的 SHA-256 短指纹，不保存或返回原始 token；客户端可通过 `X-TMA-Operator` 提供便于展示的 operator 标签。

```text
GET /v1/operator-audit?session_id=...&principal_id=...&action=...&limit=50
GET /v1/sessions/{session_id}/operator-audit
```

第二个接口会合并根 Session 与所有后代 Session 的最近审计记录，供 Inspector 执行树展示。两个查询接口同样需要 control bearer token。

### `GET /v1/sessions`

返回 Session，用于工作台恢复最近聊天。支持查询参数 `workspace_id`、`status`、`include_archived` 和 `limit`（最大 100）。置顶任务按 `pinned_at` 倒序排在普通任务之前，其余任务按创建时间倒序排列。

Session 对象除基础字段外还会返回：

- `pinned_at`：置顶时间；未置顶时省略。
- `tags`：任务标签数组。
- `summary_text`：优先取持久化 Session 摘要；没有摘要时回退到最近一条 `agent.message`，便于任务列表直接展示结论。

响应 `200`：

```json
{
  "sessions": [
    {
      "id": "sesn_000001",
      "title": "Review payment retry logic",
      "pinned_at": "2026-07-13T03:40:00Z",
      "tags": ["代码", "调研"],
      "summary_text": "发现重试计数存在一次边界偏差，并已补充回归测试。"
    }
  ]
}
```

### `PATCH /v1/sessions/{session_id}`

部分更新任务的置顶状态和标签。至少需要提供一个字段；未提供的字段保持不变。

请求：

```json
{
  "pinned": true,
  "tags": ["代码", "调研"]
}
```

标签会去除首尾空白和空字符串，并按不区分大小写的方式去重。单个 Session 最多 8 个标签，每个标签最多 32 个字符；传入空数组会清空标签。

响应 `200`：更新后的 `Session`。每次更新都会写入 `session.metadata.update` 操作审计记录，详情包含本次提交的置顶状态和标签。

### `POST /v1/sessions/{session_id}/config/upgrade`

显式把一个 idle Session 升级到 Agent 当前或指定的 config version。已有 Session 默认继续固定创建时的 `agent_config_version`；这个接口用于用户明确选择“在当前会话后续 turns 使用新配置”的场景。

请求：

```json
{
  "to_current": true,
  "updated_by": "cli"
}
```

精确版本请求：

```json
{
  "to_version": 2,
  "updated_by": "workbench"
}
```

约束：

- `to_current=true` 与正整数 `to_version` 必须且只能选择一个；空请求兼容为 `to_current=true`。
- `to_version` 必须属于当前 Session 的同一 Agent，且不能低于 Session 已固定的版本。
- 精确版本允许低于 Agent 的 `current_config_version`，用于把 `skills.enable` 刚发布的版本安全应用到当前 Session，避免并发发布后误用其他配置。
- Session 必须是 `idle`，否则返回 `409`。
- 如果 Session 已经是目标版本，返回 `changed=false`，不写事件。
- 如果发生升级，会写入 `session.config_updated` 审计事件。

响应 `200`：

```json
{
  "changed": true,
  "old_agent_config_version": 1,
  "new_agent_config_version": 2,
  "latest_agent_config_version": 2,
  "session": {
    "id": "sesn_000001",
    "agent_config_version": 2
  },
  "event": {
    "type": "session.config_updated"
  }
}
```

`latest_agent_config_version` 表示 Agent 当前最新版本；使用 `to_version` 时，它可以高于 `new_agent_config_version`。Workbench 的 Skill Enable 结果卡使用 `new_config_version` 作为精确目标，升级成功后刷新 Session Runtime config，并把卡片更新为“Skill 已生效”。

### `PATCH /v1/sessions/{session_id}/runtime-settings`

热更新 Session 运行时设置。

请求：

```json
{
  "intervention_mode": "request_approval",
  "tool_runtime": "cloud_sandbox",
  "cloud_sandbox_allow_network": true
}
```

`intervention_mode` 支持：

| 值 | 行为 |
|---|---|
| `request_approval` | 工具调用需要用户审批，turn 挂起等待 |
| `approve_for_me` | 需要审批的工具由系统自动批准执行，并记录 auto approval 事件 |
| `full_access` | 不请求审批，直接执行 |

响应 `200`：更新后的 `Session`。

同时支持更新 `tool_runtime`、`cloud_sandbox_root`、`cloud_sandbox_image`、`cloud_sandbox_allow_network`。`cloud_sandbox_allow_network=true` 表示沙箱容器使用 Docker 默认网络并具备外网访问能力；设为 `false` 时容器会用 `--network none` 断网。具备外网能力的 `default.run_command` 会进入 `network_access` 审批层；显式配置后使用的兼容 API `default.execute_code` 也遵循该策略。系统按 `intervention_mode` 决定等待用户、自动批准或直接执行。如果请求体为空对象，会把 `runtime_settings` 写为 `{}`。

### `POST /v1/sessions/{session_id}/archive`

归档 Session，状态变为 `terminated` 并写入 `archived_at`。

响应 `200`：更新后的 `Session`。

### `POST /v1/sessions/{session_id}/restore`

恢复已归档 Session。状态回到 `idle`、清除 `archived_at` 并追加 `session.status_idle` 事件，恢复后可以继续发送消息。

响应 `200`：更新后的 `Session`。

### `DELETE /v1/sessions/{session_id}`

删除 Session。

响应 `204`，无响应体。

## Run API v2

Run 是现有 Session Turn 的公开投影，不是第二套运行状态机。

```text
POST /v2/sessions/{session_id}/runs
GET  /v2/sessions/{session_id}/runs
GET  /v2/sessions/{session_id}/runs/{run_id}
POST /v2/sessions/{session_id}/runs/{run_id}/cancel
GET  /v2/sessions/{session_id}/runs/{run_id}/events
GET  /v2/sessions/{session_id}/runs/{run_id}/events/stream
```

创建请求：

```json
{
  "input": {
    "content": [{"type": "text", "text": "hello"}]
  },
  "idempotency_key": "business-task-001"
}
```

首次创建返回 `201` 和 `created:true`；同一 Session 使用相同 key 和相同规范化输入返回原 Run 和 `200`，相同 key 对应不同输入返回 `409 idempotency_conflict`。Run 状态为 `running`、`waiting_approval`、`completed`、`failed` 或 `interrupted`。

Run Event 接口只返回目标 `turn_id` 的事件，仍使用 Session 级单调 `seq` 和 `after_seq`。Cancel 对终态 Run 幂等返回当前状态；取消本地 SDK Context 不会调用远端 Cancel。

## Event

### Event 对象

```json
{
  "id": "evnt_000004",
  "session_id": "sesn_000001",
  "seq": 4,
  "type": "user.message",
  "payload": {
    "content": [{"type": "text", "text": "hello"}],
    "turn_id": "turn_000001"
  },
  "created_at": "2026-07-08T06:00:00Z"
}
```

`seq` 在单个 Session 内单调递增。客户端断线重连时应使用最后看到的 `seq` 作为 `after_seq`。

### `POST /v1/sessions/{session_id}/events`

追加事件。CLI/UI 可以写入 `user.message`、`user.steer`、`user.follow_up` 和 `user.interrupt`。

发送用户消息：

```json
{
  "events": [
    {
      "type": "user.message",
      "payload": {
        "content": [{"type": "text", "text": "hello"}]
      }
    }
  ]
}
```

响应 `201`：

```json
{
  "events": [
    {
      "type": "session.status_running",
      "payload": {"turn_id": "turn_000001"}
    },
    {
      "type": "user.message",
      "payload": {
        "content": [{"type": "text", "text": "hello"}],
        "turn_id": "turn_000001"
      }
    }
  ]
}
```

服务端会生成 `turn_id`，并写回同一 turn 的状态事件和用户消息事件。后台 Runner 随后异步继续写入 runtime / agent / idle 事件。

运行中的 turn 可以接收控制消息：

```json
{
  "events": [
    {
      "type": "user.steer",
      "payload": {
        "content": [{"type": "text", "text": "优先保证正确性，先补测试"}]
      }
    },
    {
      "type": "user.follow_up",
      "payload": {
        "text": "最终回复中列出验证结果"
      }
    }
  ]
}
```

`user.steer` 会在下一次安全控制点进入模型上下文，用于修正当前执行方向；`user.follow_up` 会在准备完成时追加要求，并强制继续一个模型轮次。两者只在 Session 为 `running` 时接受，服务端会绑定当前 `turn_id`，按 Session event `seq` 去重并保持顺序。

发送中断：

```json
{
  "events": [
    {
      "type": "user.interrupt"
    }
  ]
}
```

中断只在 Session `running` 时有效。Store 会把当前 turn 标记为 interrupted，并写入：

```text
user.interrupt
session.status_interrupting
session.status_idle
```

如果 Session 正在等待工具审批，再发送 `user.message` 不会启动新 turn。服务端会返回 `202`，追加一条提醒型 `agent.message`，并重新投递当前 pending 的 `runtime.tool_intervention_required` 事件。

### `GET /v1/sessions/{session_id}/events`

查询历史事件。

查询参数：

| 参数 | 说明 |
|---|---|
| `after_seq` | 可选；只返回 `seq > after_seq` 的事件，默认 `0` |

响应 `200`：

```json
{
  "events": []
}
```

### `GET /v1/sessions/{session_id}/events/stream`

SSE 事件流。服务端会先按 `after_seq` 补发历史事件，再订阅后续实时事件。

查询参数：

| 参数 | 说明 |
|---|---|
| `after_seq` | 可选；只返回 `seq > after_seq` 的事件，默认 `0` |

响应头：

```http
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

SSE frame：

```text
id: evnt_000004
event: user.message
data: {"id":"evnt_000004","session_id":"sesn_000001","seq":4,"type":"user.message","payload":{"content":[{"type":"text","text":"hello"}],"turn_id":"turn_000001"},"created_at":"2026-07-08T06:00:00Z"}

```

历史补发结束后，服务端会发送注释行：

```text
: stream ready

```

实时 fanout 当前是进程内订阅；历史补发来自 `session_events`，所以服务重启后仍可通过 `after_seq` 恢复历史。

### 当前事件类型

Session / chat：

```text
session.status_provisioning
session.status_idle
session.status_running
session.status_interrupting
session.status_compacting
session.status_failed
session.status_terminated
user.message
user.steer
user.follow_up
user.interrupt
agent.message
```

Runtime：

```text
runtime.started
runtime.thinking
runtime.llm_request
runtime.llm_response
runtime.tool_call
runtime.tool_intervention_required
runtime.tool_intervention_approved
runtime.tool_intervention_rejected
runtime.tool_result
runtime.subagent_spawn_rejected
runtime.subagent_start_rejected
runtime.subagent_start_queued
runtime.subagent_start_dequeued
runtime.subagent_start_canceled
runtime.subagent_start_expired
runtime.span_started
runtime.span_event
runtime.span_ended
runtime.context_compacting
runtime.context_compacted
runtime.context_compaction_failed
runtime.completed
runtime.failed
```

`runtime.llm_response.payload.data.stream` 保存聚合后的流指标：`streamed`、`chunk_count`、各类型 chunk 数、`output_chars`、`reasoning_chars`、`ttft_ms` 和 `finish_reason`。原始文本、reasoning 和工具参数分片不写入 Session Event。

实时文本使用 `GET /v2/sessions/{session_id}/live/stream`。该 SSE 的 `llm.text` 数据包含独立 `stream_seq`、Turn、追加文本和格式；不提供历史补发或持久化游标，断线后以最终 `agent.message` 为准。

内置 interaction、LLM、tool、approval、context Span 由对应语义事件成对闭合，并复用相同 `span_id`；`runtime.span_started/event/ended` 仅供没有内置语义事件的扩展 Span 使用。

普通 turn 失败不会把 Session 置为 `failed`，而是把对应 turn 标记为 failed，并写一条 `session.status_idle`，payload 中包含 `last_turn_status=failed` 和失败原因。

`runtime.tool_result` 事件保留结构化可观测结果；继续送入模型上下文的 tool message 使用 `tools.ContextResultMessage()` 序列化，先按 `runtime_settings.tool_result_context_max_chars` 裁剪单条大内容，再按 `tool_result_context_total_max_chars` 对同一 Turn 的较旧结果执行 micro-compaction。压缩不会删除 assistant/tool 配对，且保留调用身份、成功/错误和 artifact 引用。工具成功执行后，Runtime 会尽力把完整工具输出 JSON 写成 Session artifact；成功时 result 中带 `artifacts`，每个 artifact 提供 `artifact_id`、`object_ref_id`、名称、类型和 TMA 代理下载路径。artifact 记录失败只写入 `artifact_error`，不改变工具调用的 success/error 语义。

`runtime.subagent_spawn_rejected` 表示父 agent 的 `agent.spawn` 被 subagent 治理策略拒绝，payload 包含命中的 quota policy、limit、当前计数和父 session / turn 标识。

`runtime.subagent_start_rejected` 表示 active 槽位不足且 workspace / user queued quota 也已满，payload 包含命中的 queue policy、当前排队数和 `subagent_session_id`。子 session 保持 idle。

`runtime.subagent_start_queued` 写在父 turn，表示启动消息已持久化排队；`runtime.subagent_start_dequeued` 写在子 turn，表示 WorkerRunner 已获得 active 槽位并把请求晋升为普通可 claim turn。

`runtime.subagent_start_canceled` 表示 pending 请求被 `agent.cancel_start` 或 session archive 取消；`runtime.subagent_start_expired` 表示请求超过 `TMA_SUBAGENT_QUEUE_TIMEOUT_SECONDS` 后被超时扫描终止。

## Tool Intervention

工具审批以 Session 级 `runtime_settings.intervention_mode` 控制。

在 `request_approval` 下，Runtime 遇到需要审批的工具调用时会：

1. 写入 `runtime.tool_call`
2. 保存一条 pending `session_interventions`
3. 将 turn 标记为 `waiting_approval`
4. 写入 `runtime.tool_intervention_required`
5. 保持 Session `running`，等待 approve 或 reject

pending intervention 当前不过期，没有 `expires_at`。

### `GET /v1/sessions/{session_id}/interventions`

查询工具审批记录。

查询参数：

| 参数 | 说明 |
|---|---|
| `status` | 可选；`pending`、`approved`、`rejected` |

响应 `200`：

```json
{
  "interventions": [
    {
      "session_id": "sesn_000001",
      "turn_id": "turn_000001",
      "call_id": "call_read",
      "tool_identifier": "default",
      "api_name": "read_file",
      "arguments": {"path": "README.md"},
      "intervention_mode": "request_approval",
      "reason": "requires user approval",
      "status": "pending",
      "requested_at": "2026-07-08T06:00:00Z"
    }
  ]
}
```

### `POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/approve`

批准 pending 工具调用，并把恢复任务提交给后台 Runner。

请求：

```json
{
  "reason": "looks safe"
}
```

响应 `200`：

```json
{
  "intervention": {
    "session_id": "sesn_000001",
    "turn_id": "turn_000001",
    "call_id": "call_read",
    "tool_identifier": "default",
    "api_name": "read_file",
    "status": "approved",
    "decision_reason": "looks safe",
    "requested_at": "2026-07-08T06:00:00Z",
    "decided_at": "2026-07-08T06:01:00Z"
  },
  "events": [
    {
      "type": "runtime.tool_intervention_approved"
    }
  ]
}
```

HTTP 响应只确认决策已经持久化和恢复任务已经调度，不等待工具、LLM 或后续 Tool Loop。客户端断开不会取消已经提交给 Runner 的恢复任务。Postgres Store 会同时把 turn 标记为可恢复并记录 `resume_intervention_call_id`，后台 Runner 可通过 lease/claim 在当前进程或服务重启后继续领取。首次 approve 会写入：

```text
runtime.tool_intervention_approved
```

后台 AgentRuntime 随后可能写入：

```text
runtime.tool_result
runtime.llm_request
runtime.llm_response
runtime.tool_call
runtime.tool_intervention_required
runtime.completed
agent.message
session.status_idle
```

如果 continuation 又触发需要审批的工具调用，turn 会继续保持 waiting approval。

### `POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/reject`

拒绝 pending 工具调用。

请求：

```json
{
  "reason": "unsafe edit"
}
```

响应 `200`：`DecideSessionInterventionResult`。和 approve 一样，响应只确认决策持久化和后台恢复调度。

如果 intervention 带 continuation，AgentRuntime 会生成 rejected `runtime.tool_result` observation，把 `decision_reason` 作为 tool message 继续喂回模型；没有 continuation 的旧记录仍会 fail 当前 turn 并让 Session 回到 `idle`。

相同决定可以安全重试：不会重复写 approval/rejection 事件；如果 turn 仍在运行且没有新的 pending intervention，服务端会重新提交恢复任务，便于处理“决策已落库但首次调度失败或进程重启”的情况。turn 已完成时不会重新执行。对同一 intervention 改成相反决定、审批不存在的 call，仍返回 `400`。

## Session Summary

### `GET /v1/sessions/{session_id}/summary`

响应 `200`：

```json
{
  "session_id": "sesn_000001",
  "summary_text": "User prefers concise replies.",
  "source_until_seq": 12,
  "created_at": "2026-07-08T06:00:00Z",
  "updated_at": "2026-07-08T06:00:00Z"
}
```

### `PUT /v1/sessions/{session_id}/summary`

手动写入 summary。

请求：

```json
{
  "summary_text": "User prefers concise replies.",
  "source_until_seq": 12
}
```

响应 `200`：

```json
{
  "summary": {
    "session_id": "sesn_000001",
    "summary_text": "User prefers concise replies.",
    "source_until_seq": 12,
    "created_at": "2026-07-08T06:00:00Z",
    "updated_at": "2026-07-08T06:00:00Z"
  },
  "events": [
    {"type": "session.status_compacting"},
    {"type": "session.status_idle"}
  ]
}
```

手动 summary 会产生 `session.status_compacting` 和 `session.status_idle`。自动 just-in-time compaction 发生在 running 内部，会写 `runtime.context_compacting` / `runtime.context_compacted`，summary 保存是副作用，不切到 `session.status_compacting`。

## Object Refs And Artifacts

对象存储 API 管理 metadata，并提供受 Session/workspace 约束的上传与代理下载。Skill 安装等服务端流程也可直接写对象存储并创建引用；Postgres 不保存文件二进制。

文件内容应存放在 S3 兼容对象存储中，例如 RustFS、MinIO、AWS S3 或企业内部对象存储。Postgres 只保存对象引用、权限作用域、校验信息和 Session artifact 关系。客户端下载文件时走 TMA 代理端点，不直接暴露对象存储地址。

### `POST /v1/object-refs`

创建一个对象存储引用。

请求：

```json
{
  "workspace_id": "wksp_default",
  "storage_provider": "localfs",
  "bucket": "tma-artifacts",
  "object_key": "wksp_default/sesn_000001/output.txt",
  "object_version": "",
  "content_type": "text/plain",
  "size_bytes": 42,
  "checksum_sha256": "abc123",
  "etag": "",
  "visibility": "workspace",
  "metadata": {"source": "tool"},
  "created_by": "cli"
}
```

默认值：

| 字段 | 默认值 |
|---|---|
| `workspace_id` | `wksp_default` |
| `storage_provider` | `localfs` |
| `visibility` | `workspace` |
| `metadata` | `{}` |
| `created_by` | `system` |

`visibility` 当前支持：

```text
session
workspace
```

响应 `201`：`ObjectRef`。

### `GET /v1/object-refs/{object_ref_id}`

响应 `200`：

```json
{
  "id": "obj_000001",
  "workspace_id": "wksp_default",
  "storage_provider": "localfs",
  "bucket": "tma-artifacts",
  "object_key": "wksp_default/sesn_000001/output.txt",
  "content_type": "text/plain",
  "size_bytes": 42,
  "checksum_sha256": "abc123",
  "visibility": "workspace",
  "metadata": {"source": "tool"},
  "created_by": "cli",
  "created_at": "2026-07-08T06:00:00Z"
}
```

### `GET /v1/object-refs/{object_ref_id}/download`

通过 TMA 代理下载原始对象内容。这个端点要求带 `session_id` 查询参数作为权限上下文：

- `workspace` 可见对象：`session_id` 必填，且该 session 必须属于同一 workspace
- `session` 可见对象：`session_id` 必填，且该 session 的 artifacts 中必须引用这个 object ref

响应 `200`：对象字节流。

常见响应头同 artifact download：`Content-Type`、`Content-Disposition`、`X-Content-Type-Options`。

如果权限不足返回 `403`，不存在返回 `404`。

### `DELETE /v1/object-refs/{object_ref_id}`

删除 object ref 元数据。若该 object ref 仍被任一 session artifact 引用，返回 `409`。

响应 `204`：删除成功。

### `POST /v1/sessions/{session_id}/artifacts`

把一个 object ref 挂到 Session 上，形成可被 CLI / UI / Inspector 展示的 artifact。

请求：

```json
{
  "object_ref_id": "obj_000001",
  "environment_id": "env_000001",
  "turn_id": "turn_000001",
  "tool_call_id": "call_write",
  "name": "output.txt",
  "description": "Tool output",
  "artifact_type": "file",
  "metadata": {"preview": "hello"},
  "created_by": "cli"
}
```

Path 中的 `session_id` 是权威值；请求体里的 `session_id` 会被忽略。

默认值：

| 字段 | 默认值 |
|---|---|
| `environment_id` | Session 当前 `environment_id` |
| `name` | object ref 的 `object_key` |
| `artifact_type` | `file` |
| `metadata` | `{}` |
| `created_by` | `system` |

`artifact_type` 当前支持：

```text
file
snapshot
asset
```

响应 `201`：`SessionArtifact`。

创建 artifact 时会校验 Session 和 object ref 属于同一个 workspace；不一致返回 `400`。

### `GET /v1/sessions/{session_id}/artifacts`

列出 Session artifacts。

响应 `200`：

```json
{
  "artifacts": [
    {
      "id": "art_000001",
      "workspace_id": "wksp_default",
      "session_id": "sesn_000001",
      "environment_id": "env_000001",
      "object_ref_id": "obj_000001",
      "turn_id": "turn_000001",
      "tool_call_id": "call_write",
      "name": "output.txt",
      "artifact_type": "file",
      "metadata": {"preview": "hello"},
      "created_by": "cli",
      "created_at": "2026-07-08T06:00:00Z"
    }
  ]
}
```

### `GET /v1/sessions/{session_id}/artifacts/{artifact_id}/download`

通过 TMA 代理下载 artifact 对应的对象内容。这个端点会先校验 session / artifact / object ref 关系，再从对象存储读取字节流并原样返回，客户端不会拿到对象存储地址或 presigned URL。

响应 `200`：对象字节流。

常见响应头：

| Header | 说明 |
|---|---|
| `Content-Type` | 优先使用对象内容类型，其次使用 object ref 的 `content_type` |
| `Content-Disposition` | `attachment` 下载 |
| `X-Content-Type-Options` | `nosniff` |

如果 artifact、session 或底层对象不存在，返回 `404`。

### `DELETE /v1/sessions/{session_id}/artifacts/{artifact_id}`

删除 session artifact 元数据。成功后再删除对应 object ref 才不会命中引用冲突。

响应 `204`：删除成功。

### `POST /v1/sessions/{session_id}/artifacts/upload`

上传一个文件并创建对应的 object ref 和 Session artifact。

当前服务端默认使用本地文件对象存储后端，因此上传会直接落到磁盘并返回 `201`。如果你把 provider 切到暂未实现的 `s3`，则会返回 `503`：

```json
{
  "error": "object store client not configured"
}
```

请求类型：

```http
Content-Type: multipart/form-data
```

表单字段：

| 字段 | 必填 | 说明 |
|---|:---:|---|
| `file` | 是 | 上传文件 |
| `bucket` | 否 | 对象存储 bucket；为空时使用服务端对象存储默认 bucket |
| `object_key` | 否 | 对象 key；为空时服务端按 workspace/session/filename 生成 |
| `content_type` | 否 | 文件 MIME；为空时从 multipart header 或内容探测 |
| `visibility` | 否 | `session` 或 `workspace`，默认 `workspace` |
| `environment_id` | 否 | artifact 环境；默认 Session 当前 environment |
| `turn_id` | 否 | 关联 turn |
| `tool_call_id` | 否 | 关联 tool call |
| `name` | 否 | artifact 名称；默认上传文件名 |
| `description` | 否 | artifact 描述 |
| `artifact_type` | 否 | `file`、`snapshot`、`asset`，默认 `file` |
| `metadata` | 否 | JSON object |
| `created_by` | 否 | 创建者，默认 `system` |

成功响应 `201`：

```json
{
  "object_ref": {
    "id": "obj_000001",
    "workspace_id": "wksp_default",
    "storage_provider": "localfs",
    "bucket": "tma-artifacts",
    "object_key": "wksp_default/sesn_000001/uploads/output.txt",
    "content_type": "text/plain",
    "size_bytes": 42,
    "checksum_sha256": "abc123",
    "etag": "etag-from-object-store",
    "visibility": "workspace",
    "metadata": {"preview": "hello"},
    "created_by": "cli",
    "created_at": "2026-07-08T06:00:00Z"
  },
  "artifact": {
    "id": "art_000001",
    "workspace_id": "wksp_default",
    "session_id": "sesn_000001",
    "environment_id": "env_000001",
    "object_ref_id": "obj_000001",
    "name": "output.txt",
    "artifact_type": "file",
    "created_by": "cli",
    "created_at": "2026-07-08T06:00:00Z"
  },
  "workspace_path": "/workspace/uploads/art_000001/output.txt"
}
```

`workspace_path` 是文件同步到当前云沙箱后的稳定路径。Workbench 会把该路径作为结构化附件随 `user.message` 提交，运行时只在模型上下文中注入路径，聊天正文保持为用户原始输入。模型默认通过 `read_file` 或 `run_command` 处理文件；兼容 API `execute_code` 仅在 Agent 显式配置 `default.execute_code` 后可见。

## Usage

### `GET /v1/sessions/{session_id}/usage`

返回单个 Session 的 LLM usage 总量和明细。

响应 `200`：

```json
{
  "session_id": "sesn_000001",
  "summary": {
    "record_count": 2,
    "input_tokens": 17,
    "output_tokens": 8,
    "total_tokens": 25,
    "cached_input_tokens": 2,
    "reasoning_tokens": 1,
    "latency_ms": 200
  },
  "records": [
    {
      "id": "llmu_000001",
      "workspace_id": "wksp_default",
      "agent_id": "agt_000001",
      "agent_config_version": 1,
      "session_id": "sesn_000001",
      "turn_id": "turn_000001",
      "provider_id": "fake",
      "provider_type": "fake",
      "model": "fake-demo",
      "input_tokens": 10,
      "output_tokens": 5,
      "total_tokens": 15,
      "cached_input_tokens": 2,
      "reasoning_tokens": 1,
      "latency_ms": 120,
      "status": "completed",
      "created_at": "2026-07-08T06:00:00Z"
    }
  ]
}
```

### `GET /v1/llm-usage`

跨 Session 聚合 usage。

查询参数：

| 参数 | 说明 |
|---|---|
| `workspace_id` | 可选 |
| `provider_id` | 可选 |
| `model` | 可选 |
| `status` | 可选，例如 `completed`、`failed` |
| `group_by` | 可选；`provider_model`、`provider`、`model`，默认 `provider_model` |
| `from` | 可选；RFC3339 |
| `to` | 可选；RFC3339 |

响应 `200`：

```json
{
  "group_by": "provider_model",
  "filters": {
    "provider_id": "fake",
    "group_by": "provider_model"
  },
  "summary": {
    "record_count": 1,
    "input_tokens": 10,
    "output_tokens": 5,
    "total_tokens": 15,
    "cached_input_tokens": 0,
    "reasoning_tokens": 0,
    "latency_ms": 120
  },
  "groups": [
    {
      "provider_id": "fake",
      "model": "fake-demo",
      "summary": {
        "record_count": 1,
        "input_tokens": 10,
        "output_tokens": 5,
        "total_tokens": 15,
        "cached_input_tokens": 0,
        "reasoning_tokens": 0,
        "latency_ms": 120
      }
    }
  ]
}
```

## Skills Registry

新写入的 Agent `skills` 必须使用精确版本绑定：

```json
{
  "enabled": [
    {
      "skill": "code-review",
      "version": 1,
      "mode": "summary",
      "priority": 100,
      "inputs": {"review_style": "strict"}
    }
  ]
}
```

`mode` 支持 `full`、`summary`、`examples_only`，省略时默认 `summary`。`summary` 注入 metadata、manifest 摘要和 `skills.inspect` 按需读取提示，不注入完整 `content_text`；`full` 保留原来的全文注入语义。`version` 必须是正整数；identifier 必须由小写字母、数字、点、下划线或连字符组成。历史 legacy 配置仍可在 runtime 中读取，但 API 不再接受 legacy 写法作为新配置。

Skill version 可在 `manifest.inputs_schema` 中冻结非敏感参数契约。GitHub 与离线 Artifact package 也可在原始 `SKILL.md` YAML front matter 中声明：

```yaml
---
name: code-review
description: Review changes with a selected policy.
inputs_schema:
  type: object
  additionalProperties: false
  properties:
    review_style:
      type: string
      title: Review style
      enum: [strict, balanced]
      default: balanced
    max_findings:
      type: integer
      minimum: 1
      maximum: 20
    include_tests:
      type: boolean
  required: [review_style]
---
```

当前只支持完全离线的 JSON Schema Draft 2020-12 object contract。所有 object schema 必须设置 `additionalProperties: false`；`$ref` 只允许本地 `#` fragment，拒绝 `$id`、`$dynamicRef` 和远程引用，服务端不会加载 Schema URL。Schema 最大 32 KiB、深度 8、节点 512、属性总数 64；inputs 最大 16 KiB、深度 8、节点 512。

Inputs 会持久化到 Agent config，并可能出现在审批、工具结果和模型上下文，因此不得保存 Secret。`writeOnly: true`、`x-tma-sensitive: true` 或 `format: password` 的 Schema 会被拒绝；密码、token 和凭据应使用托管环境变量。发布/安装、Enable 和 Runtime Resolve 都会按精确 version 重复校验。校验错误只返回 instance path 和 keyword，不回显输入值；无 Schema 的历史 version 继续接受普通 JSON object。

### 对话工具

TMA Agent 可在正常 Tool Loop 中调用以下 server builtin 工具：

| 工具 | 说明 | 风险 |
|---|---|---|
| `skills.search` | 查询当前 Session workspace 已安装的 skills | `read` |
| `skills.inspect` | 按 `content_offset` / `content_max_chars` 分页读取指定 skill 精确版本或最新版本的 SKILL.md；响应返回 `next_offset` / `has_more` | `read` |
| `skills.discover` | 默认查询同 Organization 内部市场；显式 `provider=github` 时查询 GitHub 或验证精确 `owner/repo` | `read` |
| `skills.preview` | 安装前读取内部市场、GitHub 或当前 Session Artifact package，查看来源、license、warnings、资产索引和版本 diff | `read` |
| `skills.read_asset` | 按路径读取已安装 version 的 package 文本资产 | `read` |
| `skills.install` | 安装 inline、内部市场、GitHub 或离线 Artifact `SKILL.md` package；`upgrade_existing=true` 时发布下一版本 | `write`，遵循 Session 审批策略 |
| `skills.enable` | 将精确版本绑定写入当前 Agent 的新配置版本 | `write`，遵循 Session 审批策略 |
| `skills.disable` | 从当前 Agent 的新配置版本移除指定 binding；不归档或卸载 Skill | `write`，遵循 Session 审批策略 |

例如，用户可在 TMA 对话中要求“安装 code-review skill”。模型会先查询，再发起类似以下工具调用：

```json
{
  "identifier": "code-review",
  "title": "Code Review",
  "description": "Review code for correctness and regressions.",
  "content_format": "markdown",
  "content_text": "Inspect behavior, security, and missing tests before style."
}
```

GitHub 安装使用受限 source 坐标，不接受任意 URL：

```json
{
  "source": {
    "provider": "github",
    "repository": "owner/repository",
    "ref": "main",
    "path": "skills/code-review/SKILL.md"
  }
}
```

`repository` 必须是 `owner/repo`，`path` 必须是仓库内以 `SKILL.md` 结尾的相对路径。省略 `path` 时使用根目录 `SKILL.md`。identifier、title 和 description 可从 frontmatter 推导，也可以由调用方显式覆盖；远程内容、revision 和 URL 只能由服务端 GitHub 客户端写入，不能与 inline content 字段混用。

`skills.discover` 配置 GitHub token 时优先使用 code search，返回真实 `SKILL.md` 路径；无 token 时降级为 repository search，此时候选带 `verified=false`，最终安装仍会通过 Contents API 拉取并验证文件。私有仓库和更高 API rate limit 可配置 `TMA_SKILLS_GITHUB_TOKEN`。

局域网或完全离线部署可先把标准 ZIP 上传为当前 Session Artifact，再传受限 Artifact 坐标；不接受主机文件路径、任意 URL 或其他 Session 的 Artifact：

```json
{
  "source": {
    "provider": "artifact",
    "artifact_id": "art_000001"
  }
}
```

Artifact 必须是 `.zip` file，压缩包最大 8 MiB，且只能包含根目录 `SKILL.md` 或一个包装目录下的 `SKILL.md`。包内必须恰好一个 `SKILL.md`；最多 32 个依赖文件、4 层资产路径和 4 MiB 解压资产，拒绝绝对路径、`..`、反斜线、symlink、重复规范路径和未知扩展。服务端从 Session 绑定的 object ref 读取并校验实际 size/SHA-256，不访问 GitHub，也不执行脚本。

局域网内部市场是 `skills.discover` 的默认来源；可传 `query`、`category`、`tags` 和 `limit`，也可用空对象列出当前 Organization 已发布候选。返回项包含 `catalog_entry_id`，后续 Preview/Install 只接受该受控坐标：

```json
{
  "source": {
    "provider": "catalog",
    "catalog_entry_id": "sment_000001"
  }
}
```

默认 catalog Discover、Preview 和 Install 只访问 PostgreSQL 与已配置对象存储，不创建任何公网请求。只有显式传 `provider=github`，或旧调用传入 `repository`，才进入 GitHub Client。

安装或升级前可使用相同 source 坐标预览 package：

```json
{
  "identifier": "code-review",
  "source": {
    "provider": "github",
    "repository": "owner/repository",
    "ref": "main",
    "path": "skills/code-review/SKILL.md"
  }
}
```

`identifier` 可省略并由 frontmatter/repository/Artifact 名称或内部市场条目推导。`skills.preview` 会返回 canonical source、revision、source URL、frontmatter license、主文件字节数、warnings、不含正文的 asset index、二进制扫描结果和 SBOM。`install_state` 为 `new_install`、`upgrade`、`unchanged` 或 `blocked`；已安装版本存在时，`changes` 列出 `content_changed`、`added_files`、`removed_files` 和 `changed_files`。归档 skill、来源 provenance 不一致或 Marketplace policy 拒绝会返回 `blocked` 及 `block_reason`。GitHub 预览只执行允许的 GET；Artifact 预览只读取当前 Session object；Catalog 预览只读取同组织已发布精确版本的标准 ZIP。三种 Preview 均不上传安装对象、不创建 object ref/skill/version，也不需要写操作审批。

`policy` 包含整体 `allowed`、`repository_allowlist` / `commit_ref_pin` / `license` / `attestation` / `static_scan` / `binary_scan` checks、violations，以及 `policy_source`、`policy_id`、`policy_version`、`policy_revision`。优先级为 workspace > organization > Server fallback。repository/ref policy 只约束 GitHub；Artifact 和 Catalog 对这两项返回非强制通过，并以 ZIP SHA-256 作为 revision。license、attestation、静态扫描和 builtin 二进制扫描对三种来源一致执行。只要任一二进制文件扫描失败，`binary_scan` 强制失败，不受静态扫描阈值配置影响。

`security` 返回规范化 package `digest_sha256`、attestation 状态、scanned file 数、最高 finding severity、findings、逐文件 `binary_files` 扫描结果，以及格式为 `tma.skill.sbom.v1` 的 `sbom`。Package digest 和 SBOM checksum 均按解码后的原始二进制字节计算。Preview 和 Install 的 `binary_files[].scanner` 均为不联网的内置 `tma.skill.binary-scan.v1`。`external_scan` 字段为未来外部 Scanner 开发预留，当前为空。

Install 重新抓取 package 并执行内置 Policy、静态和二进制检查；全部通过后才继续 object PUT。当前生产工厂不构造外部 Scanner，也不会把原始 binary bytes 发送到安全扫描网络服务。ClamAV HTTP contract 与 `external_scan` provenance 代码仅作为未来开发基础保留。

持久化 workspace/org policy 下，`skills.install` 必须携带 preview pin：

```json
{
  "identifier": "code-review",
  "source": {
    "provider": "github",
    "repository": "owner/repository",
    "ref": "0123456789abcdef0123456789abcdef01234567",
    "path": "skills/code-review/SKILL.md"
  },
  "policy_id": "smpol_000001",
  "policy_version": 3,
  "policy_revision": "sha256-checksum-from-preview"
}
```

缺少 pin 或审批期间 policy revision 变化会返回 conflict；拒绝结果和 policy evaluation 会写入 operator audit。通过当前 pin 的 install 仍需正常 write-risk approval。

工具的 workspace 来自当前执行上下文，并会与 Session workspace 再次校验，模型不能通过参数跨 workspace 安装。重复 identifier 默认拒绝；显式传入 `upgrade_existing=true` 才会发布不可变的新版本。GitHub Skill 只能从原 repository/path 升级；Artifact Skill 可从当前 Session 的新 Artifact ID 升级；Catalog Skill 可从同一发布方 Skill 的后续已发布 entry 升级。三种 sourced Skill 都不能用 inline 内容替换来源。

Registry 会在 skill metadata 中记录 `source_type`、`source_locator`、`source_path`，并在每个 version 中记录 `source_ref`、`source_revision` 和 `source_url`。

Artifact 安装固定保存 `source_type=artifact`、`source_locator=session-artifact`、`source_path=SKILL.md`；version 的 `source_ref` 为 Artifact ID，`source_revision` 为上传 ZIP SHA-256，`source_url` 为空。

Catalog 安装固定保存 `source_type=catalog`、`source_locator=<publisher skill_id>`、`source_path=SKILL.md`；version 的 `source_ref` 为 Marketplace entry ID，`source_revision` 为标准 ZIP SHA-256，`source_url` 为空。

远程安装还会递归抓取 `SKILL.md` 明确引用的同 package 资产。文本支持 Markdown、配置和常见源码/脚本，单文件最大 100000 bytes；受控二进制 allowlist 为 PNG/JPEG/GIF/WebP、PDF、DOCX、XLSX 和 PPTX，单文件最大 512 KiB。package 最多 32 个依赖文件、4 层目录，依赖总量最大 4 MiB。仅显式引用、位于 package root 内且扩展名受支持的文件会被抓取；外部 URL、包外路径和其他扩展记录 warning。

二进制扫描使用 `tma.skill.binary-scan.v1`，校验 Base64、size、SHA-256、扩展名与检测 MIME，并阻止 PE/ELF/Mach-O magic、EICAR、PDF active/embedded content marker 和 Office macro/active-content marker。安装只接受 `scan_status=passed` 的二进制文件。文本脚本和所有二进制文件都不会自动执行。

Preview 不写对象存储。Install 在 write-risk 审批后重新抓取和扫描，并把通过的二进制上传到配置的 `TMA_OBJECT_STORAGE_BUCKET`；version asset bundle 只保存 `object_ref_id`、MIME、size、SHA-256、scan status 和 SBOM，不保存 `content_base64`。未配置可用对象存储或上传/registry 发布失败时安装失败；已上传对象和 object ref 会在未提交安装时回滚。

`search`、`inspect`、`preview` 和 `install` 的工具结果只返回 asset index，不返回依赖正文。模型需要时调用：

```json
{
  "identifier": "pdf",
  "version": 1,
  "path": "REFERENCE.md"
}
```

`skills.read_asset` 仅返回文本文件的精确内容、size、revision、source URL 和 `executable` 标记。二进制读取返回 forbidden；客户端应使用 asset index 的 `object_ref_id` 调用 `/v1/object-refs/{id}/download?session_id=...` 进行受控下载。读取脚本不等于执行脚本；执行仍需单独调用受审批策略控制的 command/code 工具。

`skills.enable` 会保留 Agent 已有的其他 skill bindings。只有 version、mode、priority 或 inputs 发生变化时才创建新的 Agent config version；完全相同的 binding 返回 `changed=false`，`previous_config_version` 与 `new_config_version` 相同。inputs 按 JSON 结构比较，对象键顺序不同不视为变化，空 inputs 与空对象等价。请求中的 `inputs` 会先按目标精确 version 的 `manifest.inputs_schema` 校验；失败返回 `400`，且不会发布 Agent config。`skills.disable` 只接受 `identifier`，只移除同名 binding，并返回被移除的完整 `binding`、`removed`、`new_config_version` 和 `requires_session_upgrade`。重复停用是幂等操作，`removed=false` 时不会创建空配置版本。

Enable/Disable 发布配置时携带读取到的 Agent current version；如果期间已有并发配置发布，操作返回 conflict，不会用旧快照覆盖 LLM、Tools、MCP 或其他 Skill bindings。正在运行的 Session 仍固定在原 config version；Workbench 使用响应中的 `new_config_version` 精确应用到 idle Session。停用不会删除 Registry metadata、不可变版本或 package objects，因此可以使用 Disable 响应中的原 binding 重新启用。

### Registry API

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/v1/skills` | 创建 workspace skill |
| `GET` | `/v1/skills?workspace_id=...&include_archived=false` | 列出 skills |
| `GET` | `/v1/skills/{skill_id}` | 获取 skill metadata |
| `POST` | `/v1/skills/{skill_id}/versions` | 发布不可变新版本，版本号自动递增 |
| `GET` | `/v1/skills/{skill_id}/versions` | 按版本倒序列出 |
| `GET` | `/v1/skills/{skill_id}/versions/{version}` | 获取冻结版本 |
| `GET` | `/v1/skills/{skill_id}/versions/{version}/package` | 下载标准 `SKILL.md` ZIP 文件包；legacy version 未回填时返回 404 |
| `POST` | `/v1/skills/{skill_id}/enable` | 为请求中的 Session 所属 Agent 创建包含该 Skill 的新配置版本 |
| `POST` | `/v1/skills/{skill_id}/disable` | 为请求中的 Session 所属 Agent 创建移除该 Skill binding 的新配置版本；幂等 |
| `POST` | `/v1/skills/{skill_id}/archive` | 归档并禁止新绑定/发布；未归档 Agent 当前配置仍绑定时返回 409，历史配置和 Session 仍可回放 |
| `POST` | `/v1/skill-packages/backfill` | 按 `workspace_id` 和 `limit` 幂等物化 legacy Skill versions；需要 control auth |

Version 响应包含冻结的 `manifest`，以及 `package_format`、`package_root`、`package_checksum_sha256`、`package_object_ref_id`、`skill_md_object_ref_id` 和 `package_manifest`。`manifest.inputs_schema` 存在时 Workbench 会在版本详情生成 typed controls，并提交精确 version。`package_format=tma.skill-package.v1` 时 runtime 优先从 `SKILL.md` object 读取；`content_text` 是迁移期兼容回退。

Enable 管理 API 请求示例：

```json
{
  "session_id": "sesn_000001",
  "version": 3,
  "mode": "full",
  "priority": 100,
  "inputs": {
    "review_style": "strict",
    "max_findings": 10,
    "include_tests": true
  }
}
```

binding 发生变化时响应 `201`：

```json
{
  "agent_id": "agt_000001",
  "previous_config_version": 4,
  "new_config_version": 5,
  "current_session_version": 4,
  "binding": {
    "skill": "code-review",
    "version": 3,
    "mode": "full",
    "priority": 100,
    "inputs": {"review_style": "strict"}
  },
  "changed": true,
  "requires_session_upgrade": true
}
```

相同 binding 重复提交时响应 `200`，`changed=false` 且不创建新配置版本。Disable 返回 `removed` 表示是否实际移除了 binding；两类操作都不会自动修改当前 Session。Workbench 会并列展示 Agent 最新 binding 与当前 Session binding，并只在 idle Session 上通过 `to_version=new_config_version` 精确应用配置。

### Marketplace Management API

Workbench Skills 管理页使用以下 control auth 端点。所有请求必须携带 `session_id`，服务端从 Session 固定 workspace，禁止客户端跨 workspace 指定目标。Preview 只读；install、enable 和 disable 写入 operator audit。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/v1/skills/marketplace/discover?session_id=...&query=...&repository=...&limit=10` | 关键词发现或精确验证 GitHub 仓库 |
| `POST` | `/v1/skills/marketplace/preview` | 预览 GitHub 或当前 Session Artifact，返回 provenance、Policy、Attestation、静态扫描、asset index 和版本差异 |
| `POST` | `/v1/skills/marketplace/install` | 使用 Preview 的 policy pin 安装或升级；服务端重新读取来源并重新执行安全检查 |

Preview 请求：

```json
{
  "session_id": "sesn_000001",
  "identifier": "code-review",
  "source": {
    "provider": "github",
    "repository": "owner/repository",
    "ref": "main",
    "path": "skills/code-review/SKILL.md"
  }
}
```

Install 使用 Preview 返回的 `policy_id`、`policy_version` 和 `policy_revision`；`install_state=upgrade` 时还需传 `upgrade_existing=true`。管理 API 与 `skills.preview` / `skills.install` Tool 使用同一 service，不接受客户端提交的 Policy 或 Security 报告。

Workbench 的“离线 ZIP”先调用 `POST /v1/sessions/{session_id}/artifacts/upload`，再用相同 `session_id` 和 `source.provider=artifact` 调用 Preview/Install。Artifact ID 不能跨 Session 复用。

聊天上传 ZIP 时，Workbench 会把上传响应转换为 `user.message.payload.attachments`：

```json
{
  "events": [{
    "type": "user.message",
    "payload": {
      "content": [{"type": "text", "text": "请将上传的 ZIP 作为离线 Skill 安装"}],
      "attachments": [{
        "artifact_id": "art_000001",
        "object_ref_id": "obj_000001",
        "name": "review-skill.zip",
        "content_type": "application/zip",
        "size_bytes": 260,
        "workspace_path": "/workspace/uploads/art_000001/review-skill.zip"
      }]
    }
  }]
}
```

Context Builder 会把 Session 级 `artifact_id` 和 ZIP 安装规则加入模型上下文。模型只能使用 `{"source":{"provider":"artifact","artifact_id":"art_000001"}}` 调用 `skills.preview` / `skills.install`；`workspace_path` 仅供执行环境访问普通附件，不能作为 Skill 来源。主机路径、bucket/key、任意 URL 和其他 Session 的 Artifact 均不接受。

对话安装顺序固定为：

1. `skills.preview` 只读执行，不需要 write approval。
2. 仅当 `policy.allowed=true` 且 `install_state` 为 `new_install` 或 `upgrade` 时调用 `skills.install`；必须原样携带 policy pin，升级还需 `upgrade_existing=true`。
3. `skills.install` 使用独立 write approval；`blocked`、`unchanged` 不安装。
4. 安装成功后不会自动启用。Workbench 的“请求启用”会发送新的用户消息，由模型调用 `skills.enable` 并产生第二个 write approval。
5. Enable 创建新的 Agent config version，当前 Session 仍固定旧版本，需显式升级 Session 或新建 Session 才会加载该 Skill。

### Internal Marketplace Catalog API

内部市场条目只引用当前 workspace 已安装 Skill 的精确不可变 version，不复制 `SKILL.md`、ZIP 或 object refs。生命周期固定为 `draft -> pending_review -> published -> withdrawn`，不包含其他状态，也不允许跳级或回退。草稿可以编辑市场摘要、分类和标签；提交审核后元数据冻结。一个 Skill 在同一 workspace 同时最多只能有一个已发布 version，旧版下架后才可发布新版。

创建、编辑、提交要求 `operator`；发布和下架要求 `admin`。所有管理请求按身份固定发布方 workspace：

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/v1/skill-marketplace-entries` | 为精确 `skill_id + skill_version` 创建草稿 |
| `GET` | `/v1/skill-marketplace-entries?workspace_id=...&status=...&include_withdrawn=true` | 查询市场条目 |
| `GET` | `/v1/skill-marketplace-entries/{entry_id}` | 获取 workspace 内单个条目 |
| `PATCH` | `/v1/skill-marketplace-entries/{entry_id}` | 编辑草稿摘要、分类和标签 |
| `POST` | `/v1/skill-marketplace-entries/{entry_id}/submit` | 草稿提交为待审核 |
| `POST` | `/v1/skill-marketplace-entries/{entry_id}/publish` | 审核并发布，可携带 `note` |
| `POST` | `/v1/skill-marketplace-entries/{entry_id}/withdraw` | 下架已发布条目，可携带 `note` |

创建草稿示例：

```json
{
  "workspace_id": "wksp_default",
  "skill_id": "skl_000001",
  "skill_version": 3,
  "summary": "团队代码审查规范",
  "category": "Engineering",
  "tags": ["review", "quality"]
}
```

`submit` 不需要额外字段；`publish` 的 `note` 保存为审核意见，`withdraw` 的 `note` 保存为下架原因。重复提交到当前状态按幂等成功处理；任何跨级、回退、审核后编辑、归档 Skill 发布或重复已发布 version 返回 `409`。每次创建、编辑和状态动作都会写入 `skill_marketplace_entry` operator audit。下架只阻止条目继续作为已发布市场版本展示，不归档 Skill，也不删除不可变 version 或 package objects。

消费端点按 `session_id` 固定 consumer workspace，只允许浏览同一 Organization 的 `published` 条目。GET 要求至少 `viewer`，Preview/Install 要求至少 `member`：

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/v1/skills/marketplace/internal?session_id=...&query=...&category=...&tag=...&limit=20` | 浏览同组织已发布条目，返回 `provider=catalog`、建议 identifier、消费者 `install_state` 和已有版本摘要 |
| `POST` | `/v1/skills/marketplace/internal/preview` | 用 `catalog_entry_id` 读取发布方不可变 ZIP 并执行统一 Policy/Security Preview |
| `POST` | `/v1/skills/marketplace/internal/install` | 携带 Preview policy pin 安装/升级到 consumer workspace |

Browse 候选的 `install_state` 为 `new_install`、`upgrade`、`unchanged` 或 `blocked`；已有 Skill 时同时返回 `existing.skill_id/version/source_ref/source_revision`。该状态用于列表快速标识，真正安装或升级前仍必须调用 Preview 重新读取不可变 ZIP、执行安全检查并生成文件差异。

Workbench Marketplace 默认展示该内部市场；“精确仓库/关键词”才使用 GitHub，“离线 ZIP”使用当前 Session Artifact。候选直接显示“可安装”“有新版本”“已安装”或“不可安装”。`upgrade` 必须先展示本地版本、目标版本和 package diff，再经二次确认发送 `upgrade_existing=true`；升级只追加 consumer 不可变 version，旧版本继续用于下载、回放和重新启用。下架后新的 Discover/Preview/Install 立即不可用，已经安装到 consumer workspace 的不可变版本继续保留。

迁移 `000063_skill_catalog_sources.sql` 增加 `catalog` provenance 和同组织 published-only SELECT RLS。可见性函数只返回布尔值，并使用固定 `search_path` 解除策略递归；普通条目、Skill 和 version 写策略仍只允许当前 workspace。生产 runtime role 自检会要求三条 catalog policy 存在，并继续要求非 superuser、无 `BYPASSRLS`、不是表 owner。

### Marketplace Policy API

以下控制面端点均使用 control auth：

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/v1/skill-marketplace-policies` | 创建 organization/workspace policy，并自动发布 version 1 |
| `GET` | `/v1/skill-marketplace-policies` | 按 organization/workspace/status 查询 policies |
| `GET` | `/v1/skill-marketplace-policies/{policy_id}` | 获取 policy metadata 和当前版本 |
| `POST` | `/v1/skill-marketplace-policies/{policy_id}/versions` | 发布不可变新版本并更新 current version |
| `GET` | `/v1/skill-marketplace-policies/{policy_id}/versions/{version}` | 获取精确策略版本 |
| `POST` | `/v1/skill-marketplace-policies/{policy_id}/archive` | 归档 policy；workspace policy 归档后回退 organization/Server |

创建 workspace policy 示例：

```json
{
  "scope_type": "workspace",
  "workspace_id": "wksp_default",
  "config": {
    "allowed_owners": ["anthropics"],
    "require_commit_sha": true,
    "allowed_licenses": ["mit", "apache-2.0"],
    "require_license": true,
    "require_attestation": true,
    "trusted_attestation_keys": {
      "release": "base64-ed25519-public-key"
    },
    "static_scan_block_severity": "high"
  }
}
```

每个 scope 同时只能有一个 active policy；归档后可以重新创建。创建、发布和归档操作写入 `operator_audit_log`。

### Skill Binary Asset Retention API

以下端点均使用 control auth。Preview 只列出候选，不调用对象存储删除；真实运行要求请求体包含 `"confirm":"DELETE"`，并且有效策略的 `enabled=true`。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/v1/skill-asset-retention/effective?workspace_id=...` | 返回 workspace > organization > Server fallback 的有效策略 |
| `POST` | `/v1/skill-asset-retention/policies` | 创建 organization/workspace 策略并发布 version 1 |
| `GET` | `/v1/skill-asset-retention/policies` | 查询策略记录 |
| `GET` | `/v1/skill-asset-retention/policies/{policy_id}` | 获取策略和当前不可变版本 |
| `POST` | `/v1/skill-asset-retention/policies/{policy_id}/versions` | 发布新版本 |
| `GET` | `/v1/skill-asset-retention/policies/{policy_id}/versions/{version}` | 获取精确不可变版本 |
| `POST` | `/v1/skill-asset-retention/policies/{policy_id}/archive` | 归档并回退下一优先级 |
| `POST` | `/v1/skill-asset-gc/preview` | 返回候选 object refs、字节数、原因和 scanner provenance |
| `POST` | `/v1/skill-asset-gc/run` | 显式确认后执行对象/ref 删除并写 tombstone |
| `GET` | `/v1/skill-asset-gc/runs?workspace_id=...` | 查询运行历史 |
| `GET` | `/v1/skill-asset-gc/runs/{run_id}` | 查询运行及逐对象状态 |
| `GET` | `/v1/skill-asset-gc/tombstones?workspace_id=...` | 查询删除后的持久化审计记录 |

创建 workspace 策略：

```json
{
  "scope_type": "workspace",
  "workspace_id": "wksp_default",
  "config": {
    "enabled": true,
    "retention_days": 30,
    "delete_limit": 100
  }
}
```

GC 运行中的每个对象都会在删除前重新验证引用。成功事务会删除 `object_refs` 并保留对象位置、SHA-256、size、scanner provider/version、原因、operator 和时间；对象存储失败的 item 标记 `failed`，下次运行可重试。控制面写入 `skills.asset_retention.policy_*`、`skills.asset_gc.preview`、`skills.asset_gc.run` 和 `skills.asset_gc.delete` operator audit。

写操作使用 control auth。版本请求包含 `content_format`（`markdown`、`json`、`hybrid`）、`manifest`、`content_text` 和可选 `assets`。manifest block type 支持 `instruction`、`constraint`、`checklist`、`example`。

### `POST /v1/skills/resolve-preview`

输入 `workspace_id`、`skills` canonical 配置和可选 `max_tokens`，返回规范化配置、精确命中的 skill/version、渲染文本、实际 mode、token 估算及 `resolved`、`degraded`、`skipped` 状态。

### `GET /v1/sessions/{session_id}/skill-usages`

返回该 Session 的 turn 级 skill usage；可通过 `turn_id` 过滤。runtime 同时产生 `runtime.skills_resolving`、`runtime.skills_resolved`、`runtime.skills_truncated`、`runtime.skills_failed` 事件。resolved/truncated 事件只包含 Skill/version、请求/实际 mode、token、优先级和状态，不重复持久化完整正文或渲染内容。

## 工作台任务模板

### `GET /v1/task-templates`

返回工作台内置任务模板，供新任务页一键填充 prompt、工具命名空间和 Skills，并可选启动顺序工作流。当前内置模板包括 AI 新闻汇总、代码审查、文档生成和数据整理。

该接口仅用于兼容现有 Web App，不属于 Server API v2。`GET /v2/task-templates` 固定返回 `404`，OpenAPI 和 Go Core SDK 不暴露该资源。

响应示例：

```json
{
  "templates": [
    {
      "id": "code_review",
      "title": "代码审查",
      "category": "代码",
      "description": "读取变更、验证行为并输出按严重程度排序的审查报告。",
      "prompt": "审查当前代码变更……",
      "tools": ["default"],
      "skills": ["code-review"],
      "workflow_steps": [
        {
          "id": "inspect",
          "title": "读取变更",
          "instruction": "读取仓库约束和当前 diff，识别受影响模块、接口和关键行为。"
        }
      ]
    }
  ]
}
```

工作台的“使用工作流”模式会把 `workflow_steps` 逐步发送为同一 Session 中的独立 turn，只有当前 turn 返回 Agent 消息且 Session 恢复 `idle` 后才发送下一步。运行进度保存在浏览器本地；它不是服务端持久化的 DAG 或 task group，跨浏览器不会恢复。

## 任务重跑与对比

### `POST /v1/sessions/{session_id}/rerun`

创建一个新 Session，复制来源 Session 的 workspace、owner、Agent、精确 Agent 配置版本、Environment 和 runtime settings，并立即重新发送来源用户消息。默认使用第一条 `user.message`；可通过 `message_seq` 指定其他用户消息。

请求可以覆盖以下字段：

```json
{
  "title": "代码审查 - 模型 B",
  "message_seq": 4,
  "llm_provider": "fake",
  "llm_model": "fake-v2",
  "intervention_mode": "approve_for_me",
  "tool_runtime": "cloud_sandbox"
}
```

未提供覆盖字段时即为按原参数一键重跑。响应包含 `source_session_id`、`source_event_seq`、新 `session` 和启动新 turn 写入的 `events`。创建或配置失败时会清理尚未启动的空 Session。

### `GET /v1/session-comparisons`

查询参数：

- `left_session_id`：基准 Session。
- `right_session_id`：变体 Session；必须与左侧不同且属于同一 workspace。

响应的 `left`、`right` 各自包含 Session 元数据、实际 `llm_provider` / `llm_model`、第一条 prompt、最后一条 Agent 回复或失败原因、从首条用户消息到最终回复/失败的 `duration_ms`、LLM usage 和结果文件列表。

## 推荐 CLI 对应关系

人类交互首选：

```bash
bin/tma session attach --session sesn_000001 --after 0
```

`session attach` 内部组合使用：

```text
GET  /v1/sessions/{session_id}/interventions?status=pending
GET  /v1/sessions/{session_id}/events/stream?after_seq=...
POST /v1/sessions/{session_id}/events
POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/approve
POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/reject
```

脚本和 UI 可以直接使用 HTTP API，但应遵守同样的状态机规则：一个 Session 同一时间只跑一个 turn；等待审批时先处理 pending intervention，再继续发送下一条用户消息。
