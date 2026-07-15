# TMA TypeScript / Node Core SDK

TypeScript Core SDK 位于 `sdk/typescript`，包名为 `@tma/core-sdk`。首版要求 Node.js 20+，核心实现只依赖标准 Fetch、Web Streams、FormData、Blob 和 AbortSignal，因此后续 Web App 可以复用同一包，不需要另建浏览器协议层。

## 分层

- `src/internal/generated/schema.ts`：从 `api/v2/openapi.yaml` 生成的 paths/components/operations，只能通过生成命令更新。
- `client.raw`：`openapi-fetch` 低层客户端，覆盖 OpenAPI 中全部 `/v2` operation。
- 高层 services：稳定命名、统一认证与错误、资源 ID 转义、cursor、multipart、下载和 SSE。
- `RunHandle`：events、wait、cancel、approve、reject。AbortSignal 取消只影响本地调用。

当前 OpenAPI 中全部 `/v2` 用户与控制面领域已有高层服务：Auth、Agents、Environments、Sessions、Runs、Interventions、Artifacts、ObjectRefs、LLM、Workers、WorkerWork、MCP、Skills、Marketplace、Orchestration、Traces、Observability、Audit 和 EnvironmentVariables。公共资源与请求/响应类型直接引用生成的 OpenAPI Schema；动态 JSON 字段保持开放并原样传输。LLM Provider/Model 条件写由 `LLMService` 统一生成带引号的 `If-Match` 或互斥的 `If-None-Match: *`。

`client.raw` 继续作为完整 `/v2` 契约的低层入口，适合调试、访问尚未完成高层命名稳定化的新 operation，或在 Server 与 SDK 同步发布期间短暂过渡。应用代码应优先使用高层 service。

## 认证与错误

```ts
const client = new TMAClient(baseURL, { token: accessToken });
```

或：

```ts
const client = new TMAClient(baseURL, {
  tokenSource: async (signal) => broker.getAccessToken(signal),
});
```

静态 Token 优先于 TokenSource。SDK 不负责 browser/device OIDC、refresh token 持久化或 Keychain；这些属于 App。非 2xx 响应抛出 `APIError`，包含 `status/code/requestId/retryable/details`。普通 HTTP 写请求从不自动重试。

## SSE

Sessions 和 Runs 返回 `AsyncGenerator<Event>`。重连携带最后成功消费的 `after_seq` 并丢弃重复 seq。网络错误和 5xx 使用最高 10 秒的指数退避持续重连；认证、权限、不存在与 Schema 错误立即失败。未知事件类型仍作为 Event 返回，`RunHandle.wait` 只处理它理解的 lifecycle event。

## 兼容边界

Worker 消费者机器协议不进入本包。`GET /v1/task-templates` 只供旧 Web App 使用。TypeScript SDK 不新增 Tool Catalog、工具创建或直接执行接口。Trace/Span 使用 `items/next_cursor/has_more`，不公开 offset。

Workers 只包含 list/get/archive/reap/diagnose，WorkerWork 只包含 enqueue/get/cancel/requeue/reap/diagnose。register、heartbeat、poll、ack 和 result 仍由现有 Worker 实现负责。

## 验证

`make test-typescript-sdk` 检查 OpenAPI 生成物、公共导出面、无 Node 全局依赖的浏览器编译、Vitest、TypeScript build 和 npm dry-run 包内容。公共 API 类型测试同时禁止 Worker 机器方法与 Templates 服务重新进入 Core SDK。测试覆盖并发 revision Header、LLM provider/model diagnostic、查询与 ID 编码、201/204、multipart、下载、动态 JSON、未知状态、SSE 重连，以及 Skills/Marketplace 生命周期。`make test-typescript-sdk-e2e` 使用真实 Server handler 验证 Agent Run、LLM、ObjectRefs、WorkerWork 控制面、MCP、环境变量脱敏、Skills、Marketplace policy、Audit 和 Observability。

当前包保持 `private: true`，只用于仓库内 Alpha 验收。解除发布保护前需要确认版本号、许可证、npm registry scope/access、provenance 和 changelog；这些发布决策不由构建脚本自动执行。

## Web App 试点

Web App 通过本地 `file:../sdk/typescript` 依赖消费同一个包。当前 37 个只读方法已迁移：`Auth.me`；Session list/get/events/runtime-config/runtime-capabilities/usage/summary/compare；Artifact list；Intervention list；Agent default/list/get/config-versions；LLM provider/model list；MCP list/runtime-status/versions；Skills list/versions、retention effective/policies、GC runs/tombstones；Marketplace external/internal discover、entry/policy list/get；EnvironmentVariables list；Observability status；Orchestration task-group templates/list/get。LLM provider/model diagnostic、Artifact upload/download 和 Intervention approve/reject 也使用类型化 SDK。`web-app/src/core-sdk.js` 使用同源 base URL，并动态调用 `globalThis.fetch`，因此继续经过 App 已有的 401 refresh/login 拦截。

`web-app/src/api.js` 保留原函数名和响应适配层，例如列表仍向 React 返回 `{ sessions }`、`{ events }`、`{ artifacts }`、`{ interventions }`、`{ agents }`、`{ providers }`、`{ models }`、`{ servers }`、`{ skills }`、`{ versions }` 或 `{ variables }`，避免协议迁移与界面重构同时发生。EnvironmentVariables 只传输脱敏 metadata。Artifact Preview 通过 `ArtifactsService.download()` 取得响应并透传 AbortSignal；原生 Download 链接指向同一个 `/v2` 资源。用户附件和离线 Skill package 上传通过 `ArtifactsService.upload()`，multipart boundary 由 Fetch 自动生成。Approve/Reject 保持 `{ intervention, events }` 响应，现有 continuation 和事件合并逻辑不变。Agent create/update/import/rollback/tooling-health 直接调用 `AgentsService`；export 取得类型化 JSON 后由适配层包装为带文件名的浏览器 `Response`，保持现有下载流程。MCP create/update/enable/disable/test/archive/version restore、EnvironmentVariables put/delete，以及 LLM Provider/Model create/update/enable/disable/delete 均已迁移到对应高层服务；workspace query、动态 config、204 删除和 revision 条件写语义不变。Environment create、Observability retry、Skill archive、Marketplace preview/install/enable/disable/entry/policy 生命周期及 Skill retention/GC 写操作也已迁移。Marketplace entry transition 只接受 OpenAPI 定义的 `submit/publish/withdraw`。ObjectRef 与 Skill package 原生下载链接已指向 `/v2`。Workbench Plugin Context 新增 `tasks.list` 与 `artifacts.list`，由宿主 SDK 适配层提供；Research Projects 不再拼公开 API URL，Scoped HTTP 仅允许 `/v2`。普通 Session 消息通过 `RunsService.start` 创建 Run；RunHandle 保留只读初始 Events 与 created 标志。已处于 busy 状态或发生 `session_busy` 竞态时，通过 `SessionsService.appendEvents` 保留现有排队语义。Session SSE 使用 SDK AsyncGenerator，中断通过活动 Run cancel。Vite 新增 `/v2` 与 `/auth` 代理，使 SDK 请求和现有 refresh/login 流程都到达同一后端；`GET /v1/task-templates` 是生产 Web App 唯一保留的 v1 请求。`build-app-ui` 先执行 TypeScript SDK 完整验证，防止 Web App 打包过期或未生成的 SDK。

Session create/archive/restore/rerun/delete、metadata、runtime settings、config upgrade、事件追加和 SSE 已迁移到 `SessionsService`；正常消息与 interrupt 分别使用 Run start/cancel。排队消息仍是 Session 级事件语义，但已通过类型化 v2 SDK 调用。

Web App 的 Session Trace、Trace/Span 详情和目录 helper 已迁移到 `TracesService`。目录保留 `{ traces }` / `{ spans }` 数组包装，但分页契约明确改为 `cursor/next_cursor/has_more`；不接受或返回数字 offset，避免继续固化旧契约。

独立 Web Inspector 也通过本地 package dependency 使用 Core SDK。Session Trace、Trace/Span 详情和目录、Session get/events/usage/summary、Artifact list/download、Intervention list/approve/reject 与 Observability status 均走 `/v2`；所有支持取消的调用透传 AbortSignal。目录只接受不透明 `cursor`，UI 的 `Load more` 使用 `next_cursor`，不再保存或计算 offset。Inspector 适配层继续向现有组件提供 `traces/spans` 数组，并从当前累计页面计算 kind/status/critical 聚合。Metrics 和事件发送仍保留原接口。

SessionsService 公开 `summary()` 与 `usage()`，返回 OpenAPI 生成的 `SessionSummary` 和 `SessionUsage`。Artifact 面板的 Preview 使用 `ArtifactsService.download()`；浏览器 Download 直链使用对应 `/v2` URL，因为原生链接不经过 JavaScript SDK。
