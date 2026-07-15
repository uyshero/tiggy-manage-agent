# TMA TypeScript Core SDK

`@tma/core-sdk` 是 TMA `/v2` 用户与控制面 API 的 Node 20+ TypeScript SDK。当前版本为仓库内 `0.1.0-alpha`，尚未发布到 npm。

## 使用

```ts
import { TMAClient } from "@tma/core-sdk";

const client = new TMAClient("https://tma.example.com", {
  tokenSource: async (signal) => credentialBroker.accessToken(signal),
});

const session = await client.sessions.create({
  agent_id: "agt_000001",
  environment_id: "env_000001",
});
const run = await client.runs.start(session.id, {
  input: { content: [{ type: "text", text: "Analyze the repository" }] },
  idempotency_key: "business-task-001",
});
const result = await run.wait();
```

静态 Token 可使用 `token`；需要轮换时使用异步 `tokenSource`。SDK 不执行 OIDC 登录，不保存 refresh token，也不访问系统 Keychain。AbortSignal 只取消本地请求或等待，不会隐式取消远端 Run。

## 服务范围

当前 `/v2` 用户与控制面领域均有类型化高层服务：Auth、Agents、Environments、Sessions、Runs/RunHandle、Interventions、Artifacts、ObjectRefs、LLM、Workers、WorkerWork、MCP、Skills、Marketplace、Orchestration、Traces、Observability、Audit 和 EnvironmentVariables。

`client.raw` 是由同一 OpenAPI 生成的 `openapi-fetch` 低层客户端，用于访问完整 `/v2` 契约或新 operation 的过渡期接入。高层服务统一 camelCase 方法参数、snake_case wire JSON、认证、错误、资源 ID 转义、下载和 SSE。

不包含 Worker register/heartbeat/poll/ack/result 机器协议、`/v1/task-templates`、Tool Catalog、工具创建或直接执行接口。

Trace/Span 只公开不透明 cursor。SSE 对网络错误和 5xx 持续重连，退避存在上限；401/403/404 和事件 Schema 错误立即返回。未知事件与状态字符串会被保留或忽略，不会造成运行时解码失败。

## 开发

```bash
make generate-typescript-sdk
make test-typescript-sdk
make test-typescript-sdk-e2e
```

`npm run check:generated` 会在临时文件中重新生成 OpenAPI 类型并逐字节比较，不修改工作区。

`npm run verify` 还会编译公共 API 类型契约、在不加载 Node 类型的条件下检查浏览器兼容性，并检查 npm dry-run 包内容。当前 `private: true` 是 Alpha 阶段的防误发布门禁；正式发布前必须单独完成版本、许可证、registry 和 provenance 审核。

仓库内 Web App 已用本地 package dependency 迁移类型化读取与管理写操作，覆盖 Auth、Sessions、Runs、Artifacts、Interventions、Agents、LLM、MCP、Skills、Marketplace、EnvironmentVariables、Observability、Orchestration、Environments 和 Traces。Agent export 的类型化 JSON 由 App 适配层包装为浏览器下载 `Response`。Trace/Span helper 已切换为不透明 cursor，并保留 `{ traces }` / `{ spans }` 数组包装。ObjectRef 与 Skill package 原生链接使用 `/v2`；Workbench `tasks/artifacts` facade 由宿主 SDK 提供，插件不拼公开 URL。普通消息创建 Run，排队消息调用 `SessionsService.appendEvents`，中断取消活动 Run，SSE 由 SDK 重连。`/v1/task-templates` 是生产 Web App 唯一保留的 v1 请求。

Web App 的 Session create/archive/restore/rerun/delete、metadata、runtime settings 和 config upgrade 已使用 `SessionsService`；消息/interrupt 事件仍与 Run/SSE 迁移一起单独处理。

Web Inspector 已使用 Core SDK 查询 Session、Session Events/Usage/Summary、Artifacts、Interventions、Observability、Session Trace、Trace/Span 详情和 cursor 目录，并使用 ArtifactsService 下载预览、InterventionsService 提交 Approve/Reject。Inspector 不再公开或计算数字 offset，`Load more` 只消费 `next_cursor`；Metrics、事件发送和其他写动作仍保留原接口。
