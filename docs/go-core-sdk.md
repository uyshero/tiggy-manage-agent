# TMA Go Core SDK

TMA Go Core SDK 是 `tma-server` 用户与控制面 API 的参考客户端。它服务 CLI、Go 企业服务和自动化集成；Worker poll/ack/heartbeat/result 机器协议不属于本 SDK。

## 包位置

```go
import "tiggy-manage-agent/sdk/tma"
```

当前 SDK 与仓库根 module 一起版本化。公共包不导入任何 Server `internal` 实现。

## 客户端

```go
client, err := tma.NewClient(
    "https://tma.example.com",
    tma.WithBearerToken(token),
)
```

需要轮换访问令牌时使用 `WithTokenSource`。调用方也可以通过 `WithHTTPClient`、`WithStreamHTTPClient` 或 `WithTransport` 注入传输实现；`WithTransport` 同时用于普通请求和 SSE，且不会修改调用方传入的 `http.Client`。SDK 不执行 OIDC browser/device flow、不持久化 refresh token，也不依赖系统 Keychain；它只使用调用方通过静态 Token 或 `TokenSource` 提供的 Bearer Token。

仓库 CLI 是 SDK 的一个应用层消费者：`tma auth login` 通过类型化 `Auth.Configuration` 发现 Server 认证配置，在 CLI 层完成 Device Authorization Flow，并将凭据放入系统 Keychain；随后把可刷新 token source 注入 Core SDK。其他 App 可以采用自己的 BFF、企业 credential broker 或 secret store，不应依赖 CLI 的 Keychain 实现。Server 负责发布非敏感认证发现信息、验证 access token 和执行 RBAC，不保存 CLI refresh token。

所有方法接收 `context.Context`。取消 Context 只停止本地请求或等待，不隐式取消已经提交到 Server 的 Run。

## Agent Run

`/v2` 把现有 Session Turn 暴露为一等 Run。Run 仍由 Server 保存和调度，SDK 的 `RunHandle` 只提供高层操作：

```go
session, err := client.Sessions.Create(ctx, tma.CreateSessionRequest{
    AgentID: "agt_general",
    Title:   "资料分析",
})

run, err := client.Runs.Start(ctx, session.ID, tma.StartRunRequest{
    Input:          tma.TextInput("分析材料并生成报告"),
    IdempotencyKey: "business-task-001",
})

result, err := run.Wait(ctx)
```

Run 状态固定为 `running`、`waiting_approval`、`completed`、`failed` 和 `interrupted`。`Wait` 使用 `after_seq` 恢复 SSE，忽略未知事件，并只处理目标 `turn_id`。网络断开和 5xx 可以重连；认证、权限、资源不存在和 Schema 错误立即返回。

同一 Session 中重复提交相同 `idempotency_key` 和相同请求返回原 Run；相同 key 对应不同请求返回 `idempotency_conflict`。

## 服务分组

SDK 公开 Auth、Agents、Environments、Sessions、Runs、Interventions、Artifacts、ObjectRefs、LLM、Workers 控制面、WorkerWork 控制面、MCP、Skills/Marketplace、Orchestration、Observability、Audit 和 EnvironmentVariables 服务。

`Orchestration` 包含只读的 `TaskGroupTemplates` 和 `DiscussionStrategies`，分别对应 `/v2/agent/task-group-templates` 与 `/v2/agent/discussion-strategies`。`GET /v1/task-templates` 是现有 Web App 的 Workbench 兼容接口，不属于 `/v2`，不进入 OpenAPI 和 Go Core SDK，SDK 不提供 `Templates` 服务分组。

生成的低层客户端覆盖 OpenAPI 中全部 `/v2` 用户与控制面 operation；手写层只补认证、错误、SSE、上传下载和 Run 状态机。

Agents、Environments、LLM、Sessions、Runs、Interventions、Artifacts、ObjectRefs、Traces、Workers 控制面、WorkerWork 控制面、MCP、Skills、Marketplace、Auth、Audit、EnvironmentVariables 和 Observability 提供类型化高层方法。Agents 额外覆盖 Default、portable Import/Export、config Rollback 和 ToolingHealth；Sessions 覆盖 metadata、Delete、Rerun、Compare、RuntimeConfig 与 RuntimeCapabilities；Orchestration 覆盖 Deliberation、Task Group、cancel/retry 和 orphan reap；Traces 覆盖 catalog List/Get 与 Span List/Get。CLI 的 Agent/config、Environment、Provider/Model、LLM 聚合用量、Session 生命周期、runtime/config、summary/usage、Event、审批、Artifact、Object、Trace、Worker 管理、Work 管理和 Observability 命令直接使用这些方法；通用 `DoJSON` 只保留给尚未形成稳定高层抽象的领域，不应被用于重复实现上述路径。

Trace 与 Span catalog 使用 `TracePage`/`TraceSpanPage` 的 `Items`、`NextCursor` 和 `HasMore`。下一页必须原样提交 Server 返回的不透明 cursor；SDK 不公开也不计算 offset。Server 暂时继续接受旧 `offset` 供历史消费者迁移，但 `/v2` OpenAPI 和公共 Go SDK 不包含该参数。cursor 与资源类型和筛选条件绑定，切换筛选条件后必须从第一页重新查询。

```go
page, err := client.Traces.List(ctx, tma.TraceListQuery{Limit: 50})
if page.HasMore {
    page, err = client.Traces.List(ctx, tma.TraceListQuery{Limit: 50, Cursor: page.NextCursor})
}
trace, err := client.Traces.Get(ctx, page.Items[0].TraceID)
spans, err := client.Traces.ListSpans(ctx, tma.TraceSpanListQuery{TraceID: trace.TraceID, Limit: 100})
```

`client.MCP` 覆盖 Registry list/create/get/update/enable/disable/archive/test、不可变版本列表与 restore，以及按 Workspace 查询 runtime circuit 状态。canonical `config` 使用公共 `MCPServerConfig`；`MCPConfigLiteral`、`MCPConfigEnvRef` 和 `MCPConfigSecretRef` 构造 string/`env_ref`/`secret_ref` 联合值。OpenAPI 和生成客户端使用同一精确 schema。Agent config 中需要保留兼容 envelope 的 MCP 字段仍使用 `json.RawMessage`。

`client.Auth.Configuration` 返回公开的 Server 认证模式和 CLI OIDC Device Flow 配置，`client.Auth.Me` 返回认证状态和类型化 Principal；实际 browser/device login、token refresh、revocation 和凭据持久化不是 Core SDK API。`client.Audit` 提供全局/Session lineage operator audit、security audit key 状态和 dead-letter replay。`client.EnvironmentVariables` 只返回 `name/configured/created_at/updated_at`，写入值标记为 write-only，Server 和 SDK 响应都不会返回明文。

`client.Skills` 覆盖 Skill 创建/列表/详情/归档、不可变版本、package 下载、resolve preview、Session usage、package backfill，以及 asset retention policy 和 GC。manifest、asset bundle、SBOM、package manifest、retention 和 GC 均为公共类型；`inputs_schema`、每次启用的 `inputs` 和 asset metadata 是明确保留的动态 JSON 扩展点。历史版本的 `assets: []` 与新 bundle 对象都可解码。

`client.Marketplace` 覆盖 GitHub Marketplace discover/preview/install、内部目录 browse/preview/install、installed Skill enable/disable、目录条目 draft/review/publish/withdraw 工作流，以及 organization/Workspace Marketplace policy 和不可变策略版本。Source、Candidate、package preview、policy decision、security report、SBOM、entry 和 policy 都是公共类型；只有 Skill inputs 保留动态 JSON。Marketplace 安装不会绕过 Server 的来源、许可证、attestation、静态扫描和二进制扫描策略。

CLI 的 `tma skill` 和 `tma marketplace` 命令直接使用上述两个类型化服务。`skill` 覆盖资源/版本/package、resolve、usage、retention 和 GC；`marketplace` 覆盖外部与内部来源、installed Skill 启停、entry 审核流和 policy 版本。来源统一用 `--source` JSON 表达，例如 `{"provider":"github","repository":"owner/repo","ref":"<commit>","path":"SKILL.md"}`；CLI 不保存 Marketplace 凭据，也不提供工具创建或直接执行入口。

`WorkersService` 只包含 list/get/archive/reap/diagnose，`WorkerWorkService` 只包含 enqueue/get/cancel/requeue/reap/diagnose。Worker register/heartbeat 与 work poll/ack/heartbeat/result 不由 Core SDK 暴露，继续属于 Worker 消费者机器协议，后续如需复用应建立独立 Worker SDK。CLI 中只有这些机器动作和 `/health` 仍使用通用请求入口。

Worker 选择失败时，Server 将候选诊断放入 v2 `error.details`。`WorkerWork.EnqueueWithDiagnostics` 在同一次写请求中同时返回 `APIError` 和结构化 `WorkerWorkConflict`；SDK 不会为了读取诊断重放 enqueue。

LLM 控制面使用 revision 做乐观并发控制。Provider 的 update、enable、disable、delete 需要调用方传入最近一次读取的 `Revision`，SDK 将它编码为 `If-Match`。Model 创建使用 `If-None-Match: *`；更新和删除使用 `If-Match`：

```go
provider, err := client.LLM.GetProvider(ctx, "production")
baseURL := "https://llm.example.com/v1"
provider, err = client.LLM.UpdateProvider(ctx, provider.ID, provider.Revision,
    tma.UpdateLLMProviderRequest{BaseURL: &baseURL},
)
```

SDK 不会在 revision 冲突后自动重新读取并覆盖写入。应用应把冲突呈现给操作者，或在确认业务变更仍适用后显式重新读取、合并和提交。CLI 的 `provider update/enable/disable` 与 `model upsert` 已采用读取后条件写入；并发修改会返回冲突，不会静默覆盖。

OpenAPI 生成器对全部 `/v2` 用户与控制面 operation 明确声明请求、响应、参数、Content-Type、成功状态和条件头 Schema。Agent 可移植性与 tooling health、Session 生命周期、Artifact multipart、Deliberation、Task Group、Trace/Span 和 Observability 均使用固定资源类型；生成器遇到未登记路径会直接失败。事件 payload、运行输入、JSON Schema、扩展配置和 metadata 等有意保留的动态 JSON 使用 `DynamicJSONValue` 或 `x-tma-dynamic-json: true` 标记，`api/v2/contract_test.go` 会阻止未标记的任意 object、空 Schema 和通用 `2XX` 回退。

## 错误与重试

HTTP 状态、错误码、数字、时间、枚举和分页遵循 [Server API v2 响应状态码与数据编码标准](./api-v2-response-and-data-standards.md)。

`/v2` 错误解码为 `*tma.APIError`：

```json
{
  "error": {
    "code": "session_busy",
    "message": "session already has a running turn",
    "request_id": "req_000001",
    "retryable": false,
    "details": {}
  }
}
```

SDK 不自动重试普通 HTTP 写操作。调用方只有在接口明确支持幂等键时才应重试写请求。下载和事件流均支持流式处理，不把大文件完整读入内存。

文件上传使用 `client.Artifacts.Upload` 或底层 `client.Upload`，以流式 multipart 请求发送，不在 SDK 中完整缓存文件。CLI 的 Session attach 和 event stream 消费 SDK 解码后的结构化 `Event`，不自行解析 HTTP SSE 帧。

多实例恢复使用 Session 单调 `seq`。已连接 Server A 的 SDK 可以收到 Server B 提交到共享 PostgreSQL 的事件；连接断开后，以最后成功处理的 `seq` 作为 `after_seq` 重新连接任一实例，Server 从持久化事件补发缺口。SDK 按序号去重，Server 不依赖进程内通知作为唯一事件来源。

## 验收

运行 `make test-sdk-e2e` 可执行 SDK 到真实 Server handler 的跨层验收。Run 链路使用公开 SDK 创建 Agent、Environment 和 Session，启动 Run，处理审批，通过 SSE 等待完成，并使用 LocalFS 对象存储上传、列出和下载 Artifact；管理面链路覆盖 Auth 状态、MCP Registry/版本、脱敏 EnvironmentVariables 和 Operator Audit；Skills 链路覆盖 Skill/version、preview、Session usage、retention policy 和 asset GC。测试不绕过 HTTP 调用业务操作；Runner fixture 只模拟执行侧产生审批请求和审批后的完成结果。

## 兼容策略

- `/v1` 保持现有行为，供 Web App 和历史脚本迁移。
- `/v2` 是 Go Core SDK 的契约源。
- minor 更新只能增加可选字段、operation 或事件；接收方必须忽略未知可选字段和未知事件。
- 删除字段、改变语义或收紧已发布输入需要新 API major。
