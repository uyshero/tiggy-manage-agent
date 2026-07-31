# Model Runtime 数据面

`tma-model-runtime` 是 `tma-platform` 仓库中的独立发布单元。它执行高吞吐 Provider 调用，但不是
面向应用的 API，也不是第二个控制面。

## 职责边界

`tma-server` 负责用户和应用认证、Workspace 隔离、模型目录、Provider 路由、Credential 解析、
并发/分钟配额、Invocation Usage/Audit 和对外错误契约。`tma-model-runtime` 只接收一次经过批准的
Route，执行公开同步 Model Runtime API 的 Generate、Embedding、Rerank、Agent Turn 的流式
Generate，以及 Realtime ASR/TTS，并返回 Provider 结果或规范化错误。

数据面不得连接 Platform PostgreSQL，不得解析用户/OBO/Service Credential，不得导入
`internal/httpapi`、`internal/managedagents`、Runner、Artifact 或应用包。该依赖方向由
`scripts/check-repository-boundaries.sh` 强制。

Agent Turn 的流式 Generate 已通过 `POST /internal/v1/generate-stream` 下沉：Server 显式传递完整
Message、Tool、Thinking Mode、输出限制、重试参数和批准后的 Provider Route；Runtime 以 NDJSON
依次返回 `delta`、最终 `response` 或规范化 `error`。客户端停止消费或 Turn 被取消时会关闭响应体，
Runtime request context 随即取消 Provider 请求；流开始后不在内部协议层自动重试。

内部请求上限为 64 MiB，以覆盖 Agent 上下文和现有最多 40 MiB 图片经 Base64 编码后的 Vision
fallback；面向应用的公开 Model Runtime API 仍执行自己的 2 MiB 请求限制。Runtime 返回体和单次
流的累计响应同样限制为 64 MiB。

Realtime Speech 已通过 `GET /internal/v1/speech/realtime` 下沉。Server 先完成用户/应用鉴权、模型
目录校验、Credential 解析、Quota 和最大会话时长判定，再通过专用 Bearer Token 建立内部 WebSocket，
首帧发送批准后的 Speech Route 与会话参数。后续只转发 Platform 通用 Speech JSON 事件和二进制
音频；豆包私有帧、握手和 Provider Header 只在数据面实现。任一侧取消或断开都会关闭内部连接和
Provider 连接，单帧限制为 1 MiB。

## 运行模式

本机开发默认不配置 `TMA_MODEL_RUNTIME_ENDPOINT`，Server 使用相同 Executor 在进程内执行。
独立模式启动数据面后再配置 Server：

```bash
TMA_MODEL_RUNTIME_AUTH_TOKEN='development-only-secret' make model-runtime-run

TMA_MODEL_RUNTIME_ENDPOINT='http://127.0.0.1:8090' \
TMA_MODEL_RUNTIME_AUTH_TOKEN='development-only-secret' \
TMA_DATABASE_URL='postgres://tma:tma@localhost:5432/tma?sslmode=disable' \
  make run
```

`GET /healthz` 和 `GET /readyz` 用于探针。`/internal/v1/*` 只用于 Server 到数据面的调用，不属于
Core OpenAPI/SDK，不能配置到公共 Ingress 或 API Gateway。

## 安全与运维

- 为此链路使用独立随机 Token，不复用用户 JWT、OIDC Client Secret、Worker Token 或 Gateway Token。
- 通过 Secret Manager 挂载 Token，先让数据面同时接受轮换窗口的新值，再滚动 Server；当前单 Token
  实现需要通过双部署/滚动编排完成无中断轮换。
- 使用 NetworkPolicy、Security Group 或专用 Docker network 限制只有 Server 能连接数据面。
- 生产启用 TLS/mTLS 或 Service Mesh 加密；控制面解析出的 Provider Credential 会随单次内部调用传输。
- 禁止记录请求 Body、Authorization Header、Route API Key、Prompt、文档内容、向量、音频和转录正文。
- 数据面可以按 CPU、连接数和 Provider 延迟独立扩缩容；Quota 与租户公平性仍由 Server/数据库执行。

数据面不可用时，Server 保持既有模型错误语义；公开 Model/Speech 调用继续将失败写入
`model_invocations`，Agent Turn 继续使用自身的 Run/Event/Usage 记录。切换为远程模式前应验证健康
探针、认证失败、超时、取消、流式 Tool/Reasoning/Usage 顺序、Speech ASR/TTS 双向事件和计量、
Provider `429/Retry-After` 以及无敏感信息日志。
