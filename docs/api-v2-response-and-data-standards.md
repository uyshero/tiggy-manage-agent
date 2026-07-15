# Server API v2 响应状态码与数据编码标准

本文档是 TMA Server API v2、OpenAPI 和 Core SDK 的强制契约。新增或修改公开接口必须遵守；`/v1` 只在明确记录的兼容范围内保留历史行为。

## 1. HTTP 状态码

成功响应：

- `200 OK`：查询、更新或动作执行成功，返回资源或结果。
- `201 Created`：首次创建资源成功。
- `202 Accepted`：任务已接收，但尚未创建明确结果资源；不能用于同步创建完成。
- `204 No Content`：删除成功且没有响应体。

Run 使用固定语义：首次 `POST /runs` 返回 `201`；相同幂等键和相同请求返回 `200` 及已有 Run；相同幂等键和不同请求返回 `409` 和 `idempotency_conflict`。

客户端错误：

| Status | 语义 | 默认 code |
|---|---|---|
| 400 | JSON、参数、Schema、必填条件头或请求格式错误 | `invalid_request` |
| 401 | Token 缺失、无效或未认证 | `unauthorized` |
| 403 | 身份有效但角色或权限不足 | `forbidden` |
| 404 | 资源不存在；跨租户资源可统一返回 404 | `not_found` |
| 405 | HTTP Method 不支持 | `method_not_allowed` |
| 409 | 状态冲突、Session busy 或幂等冲突 | `conflict`、`session_busy`、`idempotency_conflict` |
| 412 | ETag、revision 或 expected_version 不匹配 | `revision_conflict` |
| 413 | 上传内容超过限制 | `payload_too_large` |
| 415 | Content-Type 不支持 | `unsupported_media_type` |
| 422 | 请求结构正确但业务语义无法处理；不得与 400 混用 | `unprocessable_entity` |
| 429 | 限流或并发配额超过限制 | `rate_limited` |

服务端错误：

| Status | 语义 | 默认 code |
|---|---|---|
| 500 | 未分类 Server 内部错误 | `internal_error` |
| 502 | MCP、LLM 或外部 Provider 返回无效响应 | `upstream_error` |
| 503 | 依赖不可用或 Server 暂时不能处理 | `service_unavailable` |
| 504 | MCP、LLM、Worker 或外部 Provider 超时 | `upstream_timeout` |

禁止用 `200` 返回业务错误。跨租户读取应优先使用 `404`，避免暴露资源是否存在。

## 2. 稳定业务错误码

HTTP 状态码负责错误大类，`error.code` 负责程序判断。统一格式：

```json
{
  "error": {
    "code": "idempotency_conflict",
    "message": "idempotency key is already associated with another request",
    "request_id": "req_123",
    "retryable": false,
    "details": {
      "resource_type": "run"
    }
  }
}
```

错误码使用小写 `snake_case`。稳定核心码包括：

- `invalid_request`
- `unauthorized`
- `forbidden`
- `not_found`
- `method_not_allowed`
- `conflict`
- `idempotency_conflict`
- `revision_conflict`
- `session_busy`
- `rate_limited`
- `payload_too_large`
- `unsupported_media_type`
- `unprocessable_entity`
- `upstream_error`
- `service_unavailable`
- `upstream_timeout`
- `internal_error`

禁止设计 `10001`、`10002` 等私有数字错误码。新增字符串错误码必须进入 OpenAPI、Server 测试和 SDK 测试。

## 3. retryable

- `400/401/403/404/409/412/413/415/422` 默认 `false`。
- `429` 通常为 `true`，但 SDK 仍不自动重放普通写请求。
- `502/503/504` 通常为 `true`。
- `500` 由 Server 明确判断，默认 `false`，不得一律标记可重试。
- SSE 只对网络错误和 `5xx` 自动重连。
- `retryable=true` 只描述服务端恢复可能性，不授予 SDK 重放非幂等请求的权限。

## 4. JSON 数字

| 字段 | JSON/OpenAPI 类型 |
|---|---|
| `seq`、`revision` | `integer/int64` |
| 整数版本 | `integer/int32`；semver 使用 string |
| `attempt`、`limit`、`count` | `integer/int32` |
| `size_bytes`、token 数量 | `integer/int64` |
| `duration_ms`、`latency_ms` | `integer/int64` |
| `confidence`、`ratio` | `number/double`，并声明范围，通常为 0 到 1 |
| 资源 ID | string |

所有 JSON `int64` 的 OpenAPI Schema 必须声明：

```yaml
maximum: 9007199254740991
```

超过 JavaScript 安全整数范围的值必须编码为 string。金额禁止使用浮点数，使用 `amount_minor` integer 或 decimal string。

## 5. 时间

- 时间点使用 RFC3339 UTC string，OpenAPI 使用 `format: date-time`。
- Server 建议使用 RFC3339Nano 输出；Go `time.Time` 写出前必须归一化为 UTC。
- 持续时间使用带单位字段，例如 `duration_ms`。
- 禁止含糊的 `duration` 或数字 `timestamp`，禁止混用秒、毫秒和纳秒。

## 6. 枚举

状态、类型和模式使用字符串，不使用 `0/1/2`。例如 Run 状态为 `running`、`waiting_approval`、`completed`、`failed`、`interrupted`。

SDK 必须能保留或忽略未知枚举值。公开类型使用 string 或可前向兼容的 string alias，不因 Server 增加状态而导致 JSON 解码失败。

## 7. 分页与列表

请求使用正整数 `limit`，由 Server 声明默认值和最大值；使用不透明 string `cursor`，客户端不得计算或解析 cursor。新长期公共契约不得新增数字 offset 分页。

响应统一为：

```json
{
  "items": [],
  "next_cursor": "opaque-value",
  "has_more": true
}
```

列表为空必须返回 `[]`，不能返回 `null`。现有仍使用 offset 的查询属于迁移项；形成稳定 v2 类型化契约时必须改为 cursor，不把 offset 固化进 Core SDK。

## 8. OpenAPI 类型化与动态 JSON

所有 `/v2` 用户与控制面 operation 必须声明命名请求和响应 Schema、准确 Content-Type 以及确定的成功状态码。生成器发现未登记的公开路径时必须失败，禁止回退到 `schema: {}`、任意 object 或通用 `2XX` 响应。

`additionalProperties: true` 只能用于确实由扩展、事件类型、JSON Schema、Provider 或运行时决定形状的字段，例如：

- Event payload、Run input 和工具调用参数。
- Agent/Environment/runtime 的扩展配置。
- Skill inputs、metadata 和用户提供的 JSON Schema。
- Worker capabilities、payload/result。
- `error.details`、审计 details 和 Perfetto/OTLP 导出 JSON。

这些 Schema 必须声明 `x-tma-dynamic-json: true`，或引用带该标记的 `DynamicJSONValue`。固定控制面资源、列表 envelope、状态统计、Trace/Span、审计记录和 Observability 状态不得使用动态 JSON 代替类型定义。

## 9. 变更检查

公开接口变更至少验证：正确成功状态、统一错误 envelope、错误码和 retryable、跨租户 404、显式请求/响应 Schema、动态 JSON 标记、OpenAPI 数字 format/maximum、RFC3339 UTC 时间、未知枚举兼容、空列表 `[]`、cursor 分页，以及 SDK 不重放普通写请求。
