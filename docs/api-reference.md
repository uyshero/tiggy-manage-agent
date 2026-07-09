# TMA API Reference

本文档记录当前实现中的 HTTP API 契约。它不是未来 SDK 设计稿，也不是完整 OpenAPI；目标是让 CLI、测试脚本、UI 和后续 SDK 都能按同一份真实接口对齐。

当前默认服务地址：

```text
http://localhost:8080
```

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
| `404` | 资源不存在 |
| `409` | Session 已终止等冲突状态 |
| `500` | 服务端内部错误 |

### ID 和默认值

当前本地开发默认 workspace 为 `wksp_default`。创建资源时如果省略 `workspace_id`，Store 会使用默认 workspace。

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

创建或覆盖一个 LLM Provider。

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

响应 `201`：`LLMProvider`。

### `GET /v1/llm-providers`

响应 `200`：

```json
{
  "providers": [
    {
      "id": "fake",
      "provider_type": "fake",
      "enabled": true,
      "created_at": "2026-07-08T06:00:00Z"
    }
  ]
}
```

### `GET /v1/llm-providers/{provider_id}`

响应 `200`：`LLMProvider`。

### `PATCH /v1/llm-providers/{provider_id}`

部分更新 Provider。省略字段保持原值。

请求：

```json
{
  "base_url": "https://ark.cn-beijing.volces.com/api/v3",
  "enabled": false
}
```

响应 `200`：更新后的 `LLMProvider`。

### `POST /v1/llm-providers/{provider_id}/enable`

启用 Provider。请求体可为空对象 `{}`。

响应 `200`：更新后的 `LLMProvider`。

### `POST /v1/llm-providers/{provider_id}/disable`

禁用 Provider。请求体可为空对象 `{}`。

响应 `200`：更新后的 `LLMProvider`。

## LLM Model

### `POST /v1/llm-models`

创建或更新 Provider 下的模型元数据。

请求：

```json
{
  "provider_id": "volcengine-agent-plan",
  "model": "doubao-seed-2.0-pro",
  "context_window_tokens": 256000
}
```

响应 `200`：

```json
{
  "provider_id": "volcengine-agent-plan",
  "model": "doubao-seed-2.0-pro",
  "context_window_tokens": 256000,
  "created_at": "2026-07-08T06:00:00Z",
  "updated_at": "2026-07-08T06:00:00Z"
}
```

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

响应 `200`：`Worker`。

### `POST /v1/workers/diagnose`

按一次标准 tool invocation 解释当前 workspace 的在线 worker 是否能执行。CLI 和后续 UI/SDK 都应调用这个 server 侧接口，不在客户端复制 worker selector 逻辑。

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
| `tool_execution` | `payload` 必须是 `tma.work.v1`；当前 `tma-worker` 支持 `default.*`，并通过 `tools.DefaultRuntime + LocalSystemProvider` 在运行 worker 的机器上执行 |
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
    "args": ["-c", "printf hello"]
  }
}
```

`sandbox_command` payload 示例：

```json
{
  "command": "sh",
  "args": ["-c", "printf hello"],
  "work_dir": ".",
  "env": {}
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
    "exit_code": 0,
    "stdout": "hello",
    "stderr": ""
  }
}
```

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
  "system": "You are concise."
}
```

兼容字段：

| 字段 | 说明 |
|---|---|
| `model` | 当 `llm_model` 为空时会作为模型名使用 |

响应 `201`：更新后的 `Agent`。

已存在 Session 会继续固定到创建时的 `agent_config_version`；新 Session 使用 Agent 当前版本。

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

### `POST /v1/sessions/{session_id}/config/upgrade`

显式把一个 idle Session 升级到 Agent 当前 config version。已有 Session 默认继续固定创建时的 `agent_config_version`；这个接口用于用户明确选择“在当前会话后续 turns 使用最新版配置”的场景。

请求：

```json
{
  "to_current": true,
  "updated_by": "cli"
}
```

约束：

- 当前只支持 `to_current=true`。
- Session 必须是 `idle`，否则返回 `409`。
- 如果 Session 已经是最新版本，返回 `changed=false`，不写事件。
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

同时支持更新 `tool_runtime`、`cloud_sandbox_root`、`cloud_sandbox_image`、`cloud_sandbox_allow_network`。`cloud_sandbox_allow_network=true` 表示沙箱容器使用 Docker 默认网络并具备外网访问能力；设为 `false` 时容器会用 `--network none` 断网。具备外网能力的 `default.run_command` / `default.execute_code` 会进入 `network_access` 审批层，并按 `intervention_mode` 决定等待用户、自动批准或直接执行。如果请求体为空对象，会把 `runtime_settings` 写为 `{}`。

### `POST /v1/sessions/{session_id}/archive`

归档 Session。

响应 `200`：更新后的 `Session`。

### `DELETE /v1/sessions/{session_id}`

删除 Session。

响应 `204`，无响应体。

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

追加事件。当前主要由 CLI/UI 写入 `user.message` 和 `user.interrupt`。

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
user.interrupt
agent.message
```

Runtime：

```text
runtime.started
runtime.thinking
runtime.llm_request
runtime.llm_delta
runtime.llm_response
runtime.tool_call
runtime.tool_intervention_required
runtime.tool_intervention_approved
runtime.tool_intervention_rejected
runtime.tool_result
runtime.context_compacting
runtime.context_compacted
runtime.context_compaction_failed
runtime.completed
runtime.failed
```

普通 turn 失败不会把 Session 置为 `failed`，而是把对应 turn 标记为 failed，并写一条 `session.status_idle`，payload 中包含 `last_turn_status=failed` 和失败原因。

`runtime.tool_result` 的模型可见内容使用 `tools.ResultMessage()` 序列化。工具成功执行后，Runtime 会尽力把工具输出 JSON 写成 Session artifact；成功时 result 中带 `artifacts`，每个 artifact 提供 `artifact_id`、`object_ref_id`、名称、类型和 TMA 代理下载路径。artifact 记录失败只写入 `artifact_error`，不改变工具调用的 success/error 语义。

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

批准并执行 pending 工具调用。

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
  "events": []
}
```

approve 成功后会写入：

```text
runtime.tool_intervention_approved
runtime.tool_result
```

如果 intervention 保存了 LLM continuation，服务端会继续本轮工具循环，可能继续产生：

```text
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

响应 `200`：`DecideSessionInterventionResult`。

当前 reject 行为是 fail 当前 turn，并让 Session 回到 `idle`。拒绝原因会进入失败原因和 `runtime.tool_intervention_rejected` 事件，但不会作为观测继续喂回模型。

重复审批、审批不存在的 call、审批已经决定的 intervention 会返回 `400`。

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

对象存储 API 当前只管理 metadata，不上传、不下载、不保存文件二进制。

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
  }
}
```

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
