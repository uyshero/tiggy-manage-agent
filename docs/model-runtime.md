# Model Runtime 数据面

`tma-model-runtime` 是 `tma-platform` 仓库中的独立发布单元。它执行高吞吐 Provider 调用，但不是
面向应用的 API，也不是第二个控制面。

## 职责边界

`tma-server` 负责用户和应用认证、Workspace 隔离、模型目录、Provider 路由、Credential 解析、
并发/分钟配额、Invocation Usage/Audit 和对外错误契约。`tma-model-runtime` 只接收一次经过批准的
Route，执行公开同步 Model Runtime API 的 Generate、Embedding、Rerank、Agent Turn 的流式
Generate、Realtime ASR/TTS 和 Multimodal Realtime Provider Adapter，并返回 Provider 结果或规范化错误。

数据面不得连接 Platform PostgreSQL，不得解析用户/OBO/Service Credential，不得导入
`internal/httpapi`、`internal/managedagents`、Runner、Artifact 或应用包。该依赖方向由
`scripts/check-repository-boundaries.sh` 强制。

Agent Turn 的流式 Generate 已通过 `POST /internal/v1/generate-stream` 下沉：Server 显式传递完整
Message、Tool、Thinking Mode、输出限制、重试参数和批准后的 Provider Route；Runtime 以 NDJSON
依次返回 `delta`、最终 `response` 或规范化 `error`。客户端停止消费或 Turn 被取消时会关闭响应体，
Runtime request context 随即取消 Provider 请求；流开始后不在内部协议层自动重试。

内部请求上限为 64 MiB，以覆盖 Agent 上下文和现有最多 40 MiB 图片经 Base64 编码后的 Vision
fallback；面向应用的公开 Generate API 使用 30 MiB HTTP Body 上限，并在解码后单独执行 2 MiB
文本、16 张图片、单张及合计 20 MiB 内联图片限制。Runtime 返回体和单次流的累计响应同样限制为
64 MiB。

公开 `POST /v2/model-runtime/generate` 的 `messages[].content` 兼容原有字符串，也可使用由 `text`
和 `image_url` 组成的结构化数组。图片只允许出现在 `user` 消息中，来源必须是 PNG/JPEG/WebP/GIF
的 Base64 data URL，或不带凭据且不指向本机/私网地址的公共 HTTPS URL。包含图片的请求必须显式
选择目录中 `capability_type=text_image` 的模型；Server 不会把图片发送给纯文本模型，也不会静默
改用默认 Vision 模型。Go SDK 使用 `ModelMessage.Parts`，TypeScript SDK 直接使用判别联合类型。

全新数据库由 Server 启动配置创建默认模型。视觉模型必须设置
`TMA_LLM_CAPABILITY_TYPE=text_image`；需要作为统一视觉模型时再设置
`TMA_LLM_IS_DEFAULT_VISION=true`。这两个值只负责首次登记，重启不会覆盖控制面中已有模型的能力、
默认路由或 revision。

Invocation 的 `input_items` 记录实际 content part 数量，`input_characters` 只记录文本 Unicode 字符，
`input_bytes` 记录 UTF-8 文本、解码后的内联图片以及远程图片 URL 本身的字节数。Platform 不代替
Provider 下载远程图片。视频、音视频混合实时流、对象引用和 backpressure 协议属于 Multimodal
Phase 2，不通过 `image_url` 模拟。Phase 2A 已固定
[`tma.multimodal.realtime.v1`](./multimodal-realtime-protocol.md) 的 track、二进制帧、ObjectRef 和
双向 credit window。Phase 2B 已实现 `tma_multimodal_websocket_v1` 原生 Provider Adapter、
`openai_realtime_websocket` OpenAI Adapter 和
`GET /internal/v1/multimodal/realtime` 内部 WebSocket：强制协商 v1 subprotocol，校验握手、track、
sequence 和双向 credit，并将批准后的 Provider Credential 只放入上游 Authorization Header。公共
`GET /v2/model-runtime/multimodal/realtime` 会先执行用户/Application Scope、目录、Credential 和
长会话 admission，再进入该内部链路。模型目录支持 `capability_type=multimodal_realtime`，并声明输入/输出媒体格式、
输出模态、最大 track 和最大帧；Server 会在连接前执行目录准入，Runtime 会限制 Provider 握手不得
突破批准值。Server ObjectRef 解析器执行 Workspace/Owner/Session Artifact ACL，并校验客户端、
track、数据库、对象存储元数据和实际内容后才生成 Runtime 媒体帧。Runtime 返回不含内容的媒体聚合
指标，Server recorder 使用 `multimodal_realtime` capability 记录音频时长、视频帧、`latest` 丢帧、
媒体跨度和稳定终态。Go/TypeScript Realtime Core SDK 已完成。OpenAI Adapter 当前只声明 24 kHz
单声道 PCM16、JPEG/PNG 独立图片帧以及文本/PCM16 音频输出，不声明连续视频能力。本地已验证慢
Provider 写超时、慢客户端 credit、双向断线和双 Server 共享配额；仍须完成真实账号及部署环境压测，
才适合一般应用接入。

Realtime Speech 已通过 `GET /internal/v1/speech/realtime` 下沉。Server 先完成用户/应用鉴权、模型
目录校验、Credential 解析、Quota 和最大会话时长判定，再通过请求绑定的短期 Token 建立内部 WebSocket，
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

`GET /healthz` 和 `GET /readyz` 用于探针。`/internal/v1/*`（包括 Multimodal Realtime）只用于 Server 到数据面的调用，不属于
Core OpenAPI/SDK，不能配置到公共 Ingress 或 API Gateway。

生产使用 `TMA_MODEL_RUNTIME_AUTH_MODE=signed`。Server 针对每次 HTTP/WebSocket 握手签发 HS256
Token，绑定 issuer、audience、HTTP method、internal path、jti 和最长 300 秒的有效期；共享签名
Secret 不通过网络传输。`static` 只为本机开发和兼容保留，生产配置会拒绝启动。

原生 mTLS 使用 `TMA_MODEL_RUNTIME_TLS_MODE=mtls`。Server 配置 CA、client certificate/key 和可选
server name；Runtime 配置 server certificate/key 和 client CA。已有 Service Mesh 负责双向身份时
使用 `service_mesh`，生产禁止 `disabled`。原生 mTLS 部署中，Runtime 可在独立的
`TMA_MODEL_RUNTIME_HEALTH_HTTP_ADDR` 提供无业务路由的 `/healthz`、`/readyz` 和 `/metrics`，供本地
探针和受限监控网络使用。

## 安全与运维

- 为此链路使用独立随机签名 Secret，不复用用户 JWT、OIDC Client Secret、Worker Token 或 Gateway Token。
- 通过 Secret Manager 挂载 Secret；轮换时先部署接受新 Secret 的 Runtime，再滚动 Server。当前单
  Secret 实现通过并行 Runtime Deployment 完成无中断轮换。
- 使用 NetworkPolicy、Security Group 或专用 Docker network 限制只有 Server 能连接数据面。
- 生产必须使用原生 mTLS 或 Service Mesh 双向身份；控制面解析出的 Provider Credential 会随单次内部调用传输。
- 禁止记录请求 Body、Authorization Header、Route API Key、Prompt、文档内容、向量、音频和转录正文。
- 数据面可以按 CPU、连接数和 Provider 延迟独立扩缩容；Quota 与租户公平性仍由 Server/数据库执行。

`/metrics` 提供 `tma_model_runtime_streams_active`、`stream_events_total`、
`stream_backpressure_events_total` 和 `stream_backpressure_seconds_total`。阻塞阈值由
`TMA_MODEL_RUNTIME_BACKPRESSURE_THRESHOLD_MS` 配置，指标只使用 protocol/direction 等有界标签，
不包含 Workspace、模型、Prompt、音频或 Credential。

数据面不可用时，Server 保持既有模型错误语义；公开 Model/Speech 调用继续将失败写入
`model_invocations`，Agent Turn 继续使用自身的 Run/Event/Usage 记录。切换为远程模式前应验证健康
探针、认证失败、超时、取消、流式 Tool/Reasoning/Usage 顺序、Speech ASR/TTS 双向事件和计量、
Provider `429/Retry-After` 以及无敏感信息日志。
