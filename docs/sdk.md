# Core SDK

## 分层

Server 拥有认证、持久化、调度、权限和公开协议；Core SDK 封装公开 `/v2` API、错误、分页、
SSE 和常用工作流；Workbench/业务应用只依赖 SDK 和自身 UI。SDK 不复制 Server 业务规则，
也不直接访问数据库。

```text
Application / Workbench Plugin
  -> Go or TypeScript Core SDK
  -> HTTP / SSE v2 contract
  -> TMA Server
```

每个 SDK 版本声明兼容的 Server API 范围。OpenAPI 生成类型位于 internal/generated 或等价目录，
公开包提供稳定、符合语言习惯的 facade。生成代码不手工修改。

## Go

包位于 `sdk/tma`。客户端接受 base URL、token/credential provider、HTTP client、timeout 和可选
重试策略。服务按 Agents、Sessions/Runs、Artifacts、Administration、LLM、MCP、Skills 和
Observability 分组。

Agent Run helper 应覆盖创建/恢复 Session、发送任务、消费 SSE、处理终态和取消。错误类型保留
HTTP status、稳定 code、retryable、request ID 和安全 message。分页 iterator 保留 opaque cursor。

```go
client, err := tma.NewClient(tma.ClientOptions{
    BaseURL: "http://localhost:8080",
    Token:   os.Getenv("TMA_AUTH_TOKEN"),
})
if err != nil { /* handle */ }

session, err := client.Sessions.Create(ctx, tma.CreateSessionRequest{/* ... */})
```

具体签名以 `go doc ./sdk/tma` 为准。

## TypeScript

包位于 `sdk/typescript`，目标 Node 20+ 和现代浏览器。公开入口导出 `CoreClient`、service、公开
types 和 typed error；`src/internal/generated` 只作为实现细节。

Transport 基于注入的 `fetch`，支持 token provider、AbortSignal、超时和 request headers。
SSE 实现处理 event ID、重连、终态和取消，不能把 live text 当作持久化结果。

```ts
const client = new CoreClient({
  baseUrl: "http://localhost:8080",
  token: async () => authToken,
});

const session = await client.sessions.create({/* ... */});
```

具体导出以 `sdk/typescript/src/index.ts` 和类型测试为准。

## 应用与扩展边界

- Core SDK 可以封装通用重试、SSE、分页、上传和错误归一化。
- Workbench Plugin 使用宿主提供的 SDK client，不自行持有服务 token。
- 企业业务流程、页面状态和展示模型留在应用/插件。
- 多 Server 场景由应用明确选择 client；SDK 不在请求间隐式切换 Server。
- 扩展认证通过用户委托或受限 service identity，不能复用 Server/Worker 管理 token。

## 兼容与验证

响应状态、错误、数字、时间、枚举和分页遵循 [api.md](./api.md)。新增接口流程：先修改
OpenAPI 和 Server contract test，再生成 SDK，最后补 facade 与公开类型测试。

```bash
go test ./sdk/tma/...
npm test --prefix sdk/typescript
npm run typecheck --prefix sdk/typescript
```
