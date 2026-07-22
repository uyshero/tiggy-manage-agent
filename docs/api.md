# HTTP API

## 事实源

公开 v2 契约以 [`api/v2/openapi.yaml`](../api/v2/openapi.yaml) 为准。生成的 Go/TypeScript
类型不得手改；修改 OpenAPI 后运行仓库生成和 contract tests。本文只定义跨接口约定和入口，
不复制全部 147 条 route 的字段。

健康检查和少量兼容接口仍可能位于 `/healthz`、`/readyz` 或 `/v1`。新产品接口统一使用
`/v2`，客户端不依赖未写入 OpenAPI 的内部 Handler。

## 认证与作用域

支持 OIDC/JWKS、HS256 JWT 兼容模式、可信网关和开发模式。受保护请求解析 actor、Workspace、
roles/groups，并在 Handler/Store 双层执行 scope。Worker 和 control-plane 使用独立 service token。

不要从请求 body 接受授权 Workspace；路径/body 中的 Workspace ID 必须与身份 scope 一致。
读取不存在和无权读取的资源应避免泄漏跨租户存在性。

## 响应约定

- 成功创建返回 `201`，异步接受返回 `202`，无 body 成功返回 `204`。
- 参数/状态错误使用合适的 4xx；内部或依赖故障使用 5xx。
- 错误 envelope 提供稳定字符串 `code`、安全 `message`、`retryable` 和可选 details。
- `retryable` 描述同一请求在条件变化后是否值得重试，不代表客户端立即无限重放。
- ID、游标和可能超过 JavaScript safe integer 的数字使用字符串。
- 时间使用 UTC RFC3339；枚举使用稳定小写字符串。
- 列表使用 cursor pagination；cursor opaque，客户端不能解析。
- 动态 JSON 字段仍需在 OpenAPI 声明边界，不能用 `any` 绕过版本契约。

并发更新使用 revision/ETag 和 `If-Match`。过期 revision 返回 `412 revision_conflict`。创建、
审批和其他可重试写操作使用稳定 idempotency key 或服务端自然幂等键。

## 主要资源

| 资源组 | 典型前缀/用途 |
| --- | --- |
| Auth | `/v2/auth/*` 当前身份与登录配置 |
| Agents | `/v2/agents/*` Agent、配置版本、导入导出、schedule、tooling health |
| Sessions/Runs | `/v2/sessions/*` 任务、run、event、stream、控制、计划和对比 |
| Interventions | Session 下 approve/reject/respond/skip/cancel |
| Artifacts/Object refs | 上传、列表、下载和对象引用 |
| Runtime | runtime config/settings/capabilities、tool permission audit |
| LLM | Provider、Model、test、usage |
| MCP | Registry、版本、enable/disable、test、runtime status |
| Skills | Registry、package、marketplace、policy、retention 和 GC |
| Observability | status、retry、trace、integrity keys 和 operator audit |
| Workers | 注册、heartbeat、work claim/ack/result 和控制操作（兼容接口） |

## Session 与事件

Session 是用户任务容器，Run/Turn 是一次执行。发送消息、rerun 或 schedule 创建执行；Runner
通过 lease 异步完成。客户端以 Session/Run 状态和持久化事件判断结果，不以 HTTP 连接寿命判断。

事件 SSE 支持历史续传，使用递增 sequence 和 `Last-Event-ID`。live stream 只提供当前模型
可见文本片段，可能因断线丢失；最终 `agent.message` 和事件是事实源。客户端需要处理重复事件、
断线重连、终态后关闭和权限过期。

审批/澄清接口只提交人工决定。工具执行由后台 Runner 恢复，不在 HTTP 请求中同步发生。
重复提交同一决定应幂等；已完成 Turn 不得重复执行工具。

## Artifact 与下载

二进制和大结果保存在对象存储，API 返回 metadata/object ref。上传校验 Workspace、大小、MIME
和 checksum；下载重新授权并使用安全文件名/Content-Disposition。API 不返回对象存储永久凭据。

## 变更检查

```bash
go generate ./...
go test ./api/v2 ./sdk/tma/...
npm test --prefix sdk/typescript
git diff --exit-code -- api/v2/openapi.yaml sdk/
```

实际 target 及数据库集成测试见 [`TESTING.md`](../TESTING.md)。
