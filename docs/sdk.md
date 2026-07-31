# Core SDK

## 分层

Server 拥有认证、持久化、调度、权限和公开协议；Core SDK 封装公开 `/v2` API、错误、分页、
SSE 和常用工作流；对话工作台/业务应用只依赖 SDK 和自身 UI。SDK 不复制 Server 业务规则，
也不直接访问数据库。

```text
Application / 对话工作台 UI Extension
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

## Model Runtime 与 Speech

应用通过 `ModelRuntime.Generate` 执行领域中性的消息生成，通过 `ModelRuntime.Embed` 和
`ModelRuntime.Rerank` 使用模型目录中的默认模型或显式 Provider/Model。产品 Prompt、待排序文档
及业务阈值留在应用，Provider Endpoint 和 API Key 只由 Platform 解析。例如：

```go
vectors, err := client.ModelRuntime.Embed(ctx, tma.ModelEmbeddingRequest{
    Inputs: []string{"first document", "second document"},
})
ranked, err := client.ModelRuntime.Rerank(ctx, tma.ModelRerankRequest{
    Query: "deployment duration", Documents: documents, TopN: 5,
})
```

TypeScript 对应方法为 `client.modelRuntime.generate()`、`embed()` 和 `rerank()`。实时语音使用 Go
SDK 的 `Speech.DialRealtime`，支持 ASR 二进制音频流和可追加文本的 TTS 流。TypeScript SDK 提供
`speech.connectRealtime()`，浏览器场景使用同源登录 Cookie 完成 WebSocket 鉴权。

Speech 客户端只发送通用事件，不依赖豆包等供应商帧。Provider、Model、voice 和音频格式来自
Model Registry；应用可以覆盖 voice，但不能获得 Provider 凭据。Speech 容量或分钟配额超限时，
`SpeechEvent` 的 `code` 为 `speech_capacity_exceeded` 或 `speech_quota_exceeded`，并携带
`retry_after_seconds` 与 `limit_scope`；客户端应在等待该秒数后重连，不能立即循环重试。

Workspace 管理员可以通过 Go SDK 的 `ModelRuntime.Invocations` 或 TypeScript SDK 的
`client.modelRuntime.invocations()` 查询 Generate、Embedding、Rerank 和 Speech 的独立 Usage/Audit。
记录只包含主体、模型、状态、耗时和计量值，不保存 Prompt、文档或音频。现有 Session/Turn Usage
不会被伪造复用。HTTP Model Runtime 容量或分钟配额超限时返回 `429` 和 `Retry-After`；Gateway
仍应设置连接、请求体和总流量保护，但不再代替 Platform 的租户/应用配额执行。

## Application/Service Identity

应用后台不再借用用户或管理员 Token。Workspace 管理员通过 Go SDK 的 `ServiceIdentities` 或
TypeScript SDK 的 `serviceIdentities` 创建独立身份、分配角色和最小 Scope，再创建可轮换凭据：

```go
identity, err := client.ServiceIdentities.Create(ctx, tma.CreateServiceIdentityRequest{
    Kind: "application", Name: "tma-knowledge", Role: "member",
    Scopes: []string{"retrieval:read", "retrieval:write", "model:generate"},
})
created, err := client.ServiceIdentities.CreateCredential(ctx, identity.ID,
    tma.CreateServiceCredentialRequest{Name: "production"})
// created.Token is returned once; write it directly to the application's Secret Manager.
```

Platform 只保存凭据哈希和可显示的 Token 前缀。禁用身份或撤销凭据后，新请求立即失效。服务身份
不能成为 Workspace/Platform 管理员，也不能访问 Worker、Provider Registry、租户管理或其他未映射
控制面 API。Model/Speech Invocation 记录包含 `service_identity_id`，管理员可按该字段筛选用量。

用户触发的应用请求先通过应用 Service Credential 创建 SDK client，再使用 `Auth.Exchange`（Go）或
`client.auth.exchange()`（TypeScript）交换用户 JWT/OIDC Token。返回 Token 默认 5 分钟有效，只含
显式请求且已授予应用的 Scope；后续应使用新的用户请求 client 携带该 Token，不能把用户 Token 或
委托 Token 写入数据库、日志、队列 payload 或浏览器持久存储。

## 应用与扩展边界

- Core SDK 可以封装通用重试、SSE、分页、上传和错误归一化。
- 对话工作台 UI Extension 使用宿主提供的 SDK client，不自行持有服务 token。
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
