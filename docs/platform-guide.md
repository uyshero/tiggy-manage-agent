# TMA Platform 使用说明书

本文面向接入 TMA 的应用开发者、Platform 管理员和 Runtime/Extension 开发者，说明
`tma-platform` 当前提供什么公共能力、应用如何使用这些能力，以及哪些能力仍在规划中。

Platform 的职责边界以 [platform-boundary.md](./platform-boundary.md) 为准；HTTP 字段和状态码
以 [`api/v2/openapi.yaml`](../api/v2/openapi.yaml) 为准；SDK 约定见 [sdk.md](./sdk.md)。

## Platform 是什么

`tma-platform` 是通用 Agent Runtime 控制面：

> Platform 管 Agent 如何被安全、可靠地执行；应用管为什么执行以及执行什么业务。

应用通过 HTTP/SSE 或 Core SDK 使用 Platform。应用不能 import Platform `internal` 包，不能
访问 Platform 数据库，也不能要求 Platform 保存应用领域对象。

## 使用者

| 使用者 | 使用方式 |
| --- | --- |
| 业务应用 | 发布 Agent/Skill，创建 Session/Run，消费 Event，管理 Artifact |
| 对话工作台 | 通过 Core SDK 使用 Agent、Session、Run、Event、Artifact 和审批能力 |
| `tma-console` | 管理 Workspace、成员、RBAC 和 Platform 资源 |
| Inspector/Space | 查看 Session、Trace、审批、评估和实验 |
| Worker Runtime | 通过 Worker Protocol 领取任务、续租并回写结果 |
| MCP/Extension | 提供具体工具能力，由 Platform 注册、授权和调用 |

## 当前公共能力

| 能力组 | Platform 当前负责 |
| --- | --- |
| 身份与租户 | OIDC/JWT/可信网关认证、Workspace、成员关系、平台角色、Application/Service Identity、Scope 和 RBAC |
| Agent Registry | Agent、不可变配置版本、Environment、模型/Skill/MCP 绑定 |
| Agent Runtime | Session、Turn/Run、Event、取消、重试、恢复和实时文本流 |
| 人机协作 | Approval、Intervention、Plan、Budget 和权限判定 |
| Worker 控制面 | Worker 注册、队列、Lease、Heartbeat、结果回写和诊断 |
| 能力治理 | Tool/Capability Schema、Capability Discovery、Workspace Policy、MCP Registry、Skill Registry |
| 模型治理 | Provider/Model 配置、能力描述、Generate、Embedding、Rerank、Session/Run 用量、独立 Invocation 审计和 Workspace/Application Quota Policy |
| Artifact | ObjectRef、Session Artifact、上传下载、引用和生命周期 |
| Retrieval Runtime | Collection、Document、Chunk、通用文档解析/切块、摄取任务、索引、Search 和 Citation |
| 平台运营 | Schedule、可靠 Event Subscription/Webhook、Trace、Audit、Metrics、Evaluation 和 Experiment |

当前已经提供通用 Generate、Embedding、Rerank、Realtime ASR/TTS、独立 Invocation Usage/Audit、
Application/Service Identity、用户委托 Token Exchange、`/v2/retrieval/*` 公共 API及 Go/TypeScript
Core SDK，以及 Capability Discovery、可管理的 Model/Speech Quota 套餐、预算告警和容量保护。同步模型调用、Agent Turn 流式 Generate 和
Realtime ASR/TTS 已可使用独立 Model Runtime 数据面；内部链路也已支持短期签名调用凭证。旧
`/v2/knowledge/*` Handler 只用于迁移兼容，不是 Core 契约，也不会进入 Core SDK。

## 现有应用如何使用 Platform

| 应用 | 使用的公共能力 | 应用自己拥有 |
| --- | --- | --- |
| Console | 身份、Workspace、成员、平台角色和 RBAC | 管理 UI |
| Inspector | Agent、Session、Event、Trace、Metrics 和 Intervention | 调试诊断 UI |
| Space | Agent、Session、Run、对比、Dataset 和 Evaluation | 评估分析 UI |
| 对话工作台 | Agent、Session、Run、Event、Artifact、Skill、MCP、Environment 和权限 | 对话任务 UI 和本地展示状态，不拥有领域业务表 |
| R语言生存分析工作台 | 身份、Agent/Run、Artifact、Skill、MCP、Worker/Capability 和 Model | 生存分析 Project、数据集、GitLab、Notebook、R Runtime、分析流程和专业 UI |
| Biography | Agent/Skill 发布、Environment、Session、Run、实时事件和 ASR/TTS | 录音领域数据、章节、项目进度、采访流程和声音选择 |
| Knowledge | Retrieval、Agent/Run、Model 和 Artifact | Knowledge Service、场景 Prompt、敏感词/拒答、Web fallback、问答审计和公开分享 |

应用之间的差异应该只是公共能力组合不同：

```text
Biography = Agent + Skill + Session + Run + Live Event + Speech Model
Knowledge = Retrieval + Model/Agent + Artifact + Capability
对话工作台 = Agent + Session + Run + Event + Artifact + Approval
R语言生存分析工作台 = Agent + Skill + MCP + Artifact + Worker + R Runtime
Inspector = Session + Event + Trace + Audit
Space     = Run + Dataset + Evaluation
Console   = Identity + Workspace + RBAC
```

R Survival 的应用 API 使用同一个 Platform Bearer Token 调用 `/v2/auth/me`，只接受认证结果中的
Workspace 和 Owner；浏览器提供的 Workspace/User 配置、请求头或请求体都不是授权依据。

## 应用接入流程

### 1. 确定身份和 Workspace

用户直接操作时使用用户 OIDC/JWT，Platform 从身份中解析 Workspace 和角色。应用后台使用
Workspace 管理员创建的 Application/Service Identity；不能复用 Worker Token、管理员 Token 或
数据库凭据。凭据明文只在创建时返回一次，Platform 只保存哈希，可以并行创建新凭据完成轮换，
再撤销旧凭据。服务身份角色只允许 `viewer`、`member` 或 `operator`，不能成为管理员。

每个身份必须声明最小 Scope。当前 Scope 覆盖 `applications:publish`、`capabilities:read`、`agents:*`、`sessions:*`、`skills:*`、`mcp:*`、
`environments:*`、`artifacts:*`、`evaluations:*`、`events:manage`、`retrieval:*`、四类 `model:*` 和
`speech:realtime`、`secrets:*`；未映射的 API 默认拒绝。需要代表用户权限的请求通过 `/v2/auth/token-exchange`
换取最长 15 分钟的 OBO Token，不能改用服务身份扩大权限。

交换请求必须同时提供应用 Service Credential 和原始用户 JWT/OIDC Token。Platform 重新验证用户
Token，要求双方 Workspace 相同，并只签发请求 Scope 与应用 Scope 的交集。委托 Principal 保留用户
Subject、Owner、角色和资源边界，同时携带应用身份；委托 Token 不能再次交换。禁用应用身份或撤销
凭据后，未过期的委托 Token 也立即失效。签名密钥独立于 JWT/OIDC/Web Session 等密钥，默认有效期
5 分钟、最大 15 分钟，Token 正文不会持久化或进入审计日志。

Go SDK 使用 `client.ServiceIdentities`，TypeScript SDK 使用 `client.serviceIdentities` 管理身份和凭据。
生产部署应把创建时返回的 Token 写入应用 Secret Manager，不得写入应用数据库、日志或仓库。

### 2. 发布运行资源

应用仓库保存 Environment、Agent、Skill 和 MCP 的声明源文件，部署或 bootstrap 程序通过 SDK 的
Application Manifest 幂等发布：

```text
应用 Environment -> environments
应用 Agent 声明  -> agents / agent_config_versions
应用 SKILL.md    -> skills / skill_versions
应用 MCP 配置    -> mcp_registry_servers / mcp_registry_server_versions
```

已发布记录属于 Platform 运行资源；Prompt、`SKILL.md`、MCP Server 源码和业务含义仍属于应用。
用户直接在 Platform 创建的个人 Agent/Skill 则以 Platform 中的版本为事实源。

应用身份需要 `applications:publish` Scope，调用 `POST /v2/application-manifests/publish`。应用凭据
不能指定其他 `app_id`；用户凭据发布时必须显式指定目标 Application Identity，且调用者至少为
Workspace Operator。Manifest 的 `schema_version` 固定为 `tma.application-manifest.v1`，每个资源必须
使用稳定且在该应用内唯一的 `external_ref`。

```go
result, err := client.ApplicationManifests.Publish(ctx, tma.PublishApplicationManifestRequest{
    Manifest: tma.ApplicationManifest{
        SchemaVersion: tma.ApplicationManifestSchemaV1,
        Revision:      releaseRevision,
        Environments: []tma.ApplicationManifestEnvironment{{
            ExternalRef: "environment/default", Name: "Default", Config: json.RawMessage(`{}`),
        }},
        Agents: []tma.ApplicationManifestAgent{{
            ExternalRef: "agent/interviewer", EnvironmentRef: "environment/default",
            Name: "Interviewer", LLMModel: "production-chat", System: interviewerPrompt,
        }},
    },
})
```

Platform 按 Environment、Skill、MCP、Agent 的依赖顺序调和，逐项返回 `created`、`updated` 或
`unchanged`。只有配置内容变化时才增加 Agent/MCP/Skill 版本。发布不是跨所有资源的单一事务；
中途失败时用同一 revision 重试，已完成且未变化的资源会返回 `unchanged`，不会重复创建。
V1 只调和 Manifest 中声明的资源，不会删除或归档本次省略的资源；下线资源必须走显式归档流程。

旧 Biography bootstrap 仍是迁移期兼容实现，后续应用应直接保存 Manifest 并通过 SDK 发布，不再
各自实现查找、创建和版本比较逻辑。

### 3. 创建 Session 并启动 Run

Go SDK 的最小调用流程：

```go
client, err := tma.NewClient(
    "https://tma.example.com",
    tma.WithBearerToken(token),
)
if err != nil {
    return err
}

session, err := client.Sessions.Create(ctx, tma.CreateSessionRequest{
    WorkspaceID:   workspaceID,
    AgentID:       agentID,
    EnvironmentID: environmentID,
    Title:         "application task",
})
if err != nil {
    return err
}

handle, err := client.Runs.Start(ctx, session.ID, tma.StartRunRequest{
    Input:          tma.TextInput(prompt),
    IdempotencyKey: requestID,
})
if err != nil {
    return err
}

result, err := handle.Wait(ctx)
```

应用必须为可重试的 Run 使用稳定 `idempotency_key`。HTTP 请求超时不等于 Run 失败；应用应根据
Run 状态和持久化 Event 判断终态。

Run 是独立持久化实体，并保持当前兼容约定 `run_id == turn_id`。Worker 每次领取或恢复工作都会
创建不可变编号的 Attempt；租约过期重抢不会覆盖旧 Attempt。应用可通过
`GET /v2/sessions/{session_id}/runs/{run_id}/attempts` 查询完整历史，或使用 Go SDK
`client.Runs.ListAttempts` / TypeScript SDK `client.runs.listAttempts`。Attempt 状态含
`running`、`suspended`、`completed`、`failed`、`interrupted`、`abandoned`：等待审批或人工输入时
当前 Attempt 终止为 `suspended`，恢复后创建下一次 Attempt。持久化 Event 同时携带 `run_id` 和
可用时的 `attempt_id`，因此应用不需要从 payload 猜测归属。

### 4. 消费事件和实时输出

- 持久化 Event 是事实源，支持按 sequence 续传和重放。
- Run Event Stream 用于跟踪指定 Run。
- Live Stream 用于低延迟文本展示，可能因断线丢失，不能作为最终结果事实源。
- 客户端必须处理重复事件、断线重连、Token 过期和终态关闭。

Biography 当前先打开 Session Live Stream，再启动带幂等键的 Run；断线时仍以 Run Result 和
持久化 Event 恢复状态。这是实时应用的参考模式。

应用后台需要可靠获得终态时，使用 `events:manage` Scope 创建 Event Subscription。应用凭据只能
管理自身 `app_id` 的订阅；Workspace Admin 可以为指定应用管理。当前公共类型为
`run.completed`、`run.failed`、`intervention.required` 和 `artifact.created`：

```go
created, err := client.EventSubscriptions.Create(ctx, tma.CreateEventSubscriptionRequest{
    Name:        "application-events",
    EndpointURL: "https://app.example.com/webhooks/tma",
    EventTypes:  []string{"run.completed", "run.failed", "artifact.created"},
})
// created.Secret 只返回一次，立即写入应用 Secret Manager。
```

Platform 在 Session Event 或 Artifact 与业务状态相同的 PostgreSQL 事务中写 delivery outbox，再由
多副本安全的租约 worker 至少一次投递。应用必须以 `X-TMA-Delivery-ID` 幂等去重，返回任意 2xx
表示接收成功；网络错误和非 2xx 使用同一 delivery ID 指数退避重试，达到上限进入
`dead_letter`，管理员或应用可通过 SDK replay。HTTP 410 直接进入死信。

请求包含 `X-TMA-Event`、`X-TMA-Timestamp`、`X-TMA-Secret-Version` 和
`X-TMA-Signature: v1=<hex>`。签名内容为
`<unix_timestamp>.<delivery_id>.<raw_body>` 的 HMAC-SHA256。消费者应先拒绝过旧时间戳，再用创建或
轮换时一次性返回的 `whsec_` secret 做常量时间比较。请求体 schema 固定为 `tma.event.v1`；
secret rotation 只影响之后新建或 replay 的 delivery，不改变已经排队事件的验签版本。

### 5. 处理 Artifact

大文本、文件和二进制结果使用 Artifact/ObjectRef，不放入应用自定义 Event payload。应用通过
API 上传、下载和引用对象，不能取得 Platform 对象存储的永久凭据。

业务原始文件可以继续存放在应用自己的 Bucket。需要交给 Agent 处理或把结果交给另一个应用时，
使用 Signed Artifact Exchange 建立 Platform ObjectRef，而不是让两个服务共享 Bucket 凭据。

导入流程由持有 `artifacts:write` Scope 的应用签发一次性上传：

```go
size := int64(len(content))
grant, err := client.ArtifactExchanges.CreateImport(ctx, tma.CreateArtifactImportExchangeRequest{
    SessionID:         sessionID,
    Filename:          "input.csv",
    ContentType:       "text/csv",
    ExpectedSizeBytes: &size,
    MaxSizeBytes:      &size,
})
result, err := client.ArtifactExchanges.Upload(
    ctx, grant, "text/csv", size, bytes.NewReader(content),
)
// result.ObjectRef 和 result.Artifact 是 Platform 已持久化的资源。
```

导出流程由 `artifacts:read` Scope 签发；Artifact 与 ObjectRef 二选一，ObjectRef 导出仍必须提供
用于授权的 `session_id`：

```go
grant, err := client.ArtifactExchanges.CreateExport(ctx, tma.CreateArtifactExportExchangeRequest{
    SessionID:  sessionID,
    ArtifactID: artifactID,
})
err = client.ArtifactExchanges.Download(ctx, grant, output)
```

交换默认 15 分钟过期，可配置范围为 1 分钟到 24 小时；上传正文最大 64 MiB。Token 为 256-bit
随机值，Platform 只保存 SHA-256，绑定 Workspace、exchange ID、方向和过期时间。第一次传输请求
会原子地把状态从 `pending` 改为 `processing`；并发请求、重放、错误 Token 和过期 Token 均返回
404，不泄露交换是否存在。大小、Content-Type 或 SHA-256 校验失败后状态为 `failed`，必须重新签发。

导入先写对象存储，再在同一数据库事务中创建 ObjectRef、SessionArtifact 并完成 exchange；数据库
失败会补偿删除对象。导出在返回正文前完成 exchange。应用可以通过
`GET /v2/artifact-exchanges/{exchange_id}` 查询 `pending/processing/completed/failed/expired`，但查询结果
永远不包含 Token。传输响应使用 `Cache-Control: no-store`；Ingress、APM 和访问日志必须对
`/v2/artifact-exchanges/*/content` 的 query string 做脱敏，不能记录 `token`。

### 6. 提供业务工具

业务数据访问和外部操作通过 MCP、Worker Capability 或独立 Extension 提供：

- Platform 保存注册、版本、绑定、权限和调用记录；
- 应用/Extension 实现具体服务、凭据使用和业务授权；
- Agent 配置只引用版本化 Skill/MCP/Capability；
- Platform 不能 import 具体 Provider，也不能直接查询应用数据库。

### 能力发现

应用启动或切换 Workspace 时，可以通过 `GET /v2/capabilities` 查询 Platform 当前暴露的公共能力；
Application/Service Identity 必须持有 `capabilities:read` Scope。
结果始终带有 Workspace、契约版本和生成时间；每项能力包含 `status`、`health`、可用 provider，
模型能力还会列出可用的 provider/model 路由：

```go
snapshot, err := client.Capabilities.List(ctx)
if err != nil {
    return err
}
for _, capability := range snapshot.Capabilities {
    if capability.ID == "speech.asr" && capability.Status == "available" {
        // 根据 capability.Models 选择 provider/model，再调用 client.Speech。
    }
}
```

TypeScript SDK 对应为 `client.capabilities.list()`。当前标准能力 ID 包括
`model.generate`、`model.embedding`、`model.rerank`、`speech.asr`、`speech.tts`、
`retrieval.search`、`artifact.exchange`、`events.subscribe` 和 `worker.execute`。
能力不可用时仍返回该项并标记 `status: unavailable`，应用应据此降级或提示，而不是猜测默认模型。

能力发现遵循调用者的 Workspace/RBAC 边界。返回内容不会包含 Provider Base URL、API key 环境变量、
secret ref 正文或内部凭证；worker 只返回声明能力和聚合数量，不返回工作负载正文。能力快照是发现信息，
不是执行授权，实际调用仍必须通过对应 Scope、Session/Run、Artifact、Event 或 Worker Protocol 校验。

### 应用密钥引用

应用通过 `client.EnvironmentVariables`（TypeScript 为 `client.environmentVariables`）管理加密 Secret
Reference。Application Identity 使用 `secrets:read` / `secrets:write` Scope 时只能访问自己的记录；
Workspace Operator/Admin 可以通过 `app_id` 显式管理目标 Application Identity：

```go
_, err := client.EnvironmentVariables.Put(ctx, "SPEECH_API_KEY", tma.EnvironmentVariableQuery{
    AppID: biographyAppID,
}, tma.PutEnvironmentVariableRequest{Value: secret})
```

写入响应和后续列表只返回 `configured`、`scope: application`、`app_id` 与时间戳，绝不返回密钥正文。
应用配置保存 `secret_ref: env:SPEECH_API_KEY`，不能把正文写入 Agent、Environment、MCP、日志或业务库。
Session Runtime 根据持久化的 `app_id` 自动合并对应应用 Secret；应用值优先于同名用户或 Workspace
变量，防止用户变量替换应用凭据。交给 Worker 时仍使用与 Workspace/Session/Turn 绑定的加密信封。
删除引用会使之后的 Run 无法解析该密钥；轮换使用同名 PUT 原子覆盖，不需要修改应用配置。

### 7. 保存业务状态

应用数据库保存自己的业务状态，并记录 Platform 资源 ID 作为外部关联：

```text
app_id / project_id / external_ref
workspace_id
agent_id
session_id
run_id
artifact_ref
```

这些 ID 不能形成跨数据库外键，也不能依赖跨服务事务。应用负责重试、补偿和最终一致性。

## Retrieval Runtime 与 Knowledge 的接入方式

通用文档底座已经收敛为 Platform Retrieval Runtime，Knowledge 产品逻辑由 `tma-knowledge`
拥有。Platform 中仍存在旧 Knowledge Handler/Store 和应用表，专门支持真实数据与流量切换期间
回滚；它们不能被新应用依赖：

```text
文档上传、解析、切块、Embedding、索引、检索、Citation
    -> Platform Retrieval API / Core SDK
    -> Platform Model Runtime + Parser/Index Adapter

回答问题
    -> tma-knowledge 的场景、Prompt、拒答、Web fallback 和分享策略
    -> retrieval.search Capability / Core SDK
    -> Platform Session / Run / Event
```

Core API 使用领域中性命名，包含：

```text
/v2/retrieval/collections
/v2/retrieval/collections/{collection_id}/documents
/v2/retrieval/documents/{document_id}
/v2/retrieval/ingestion-jobs/{job_id}
/v2/retrieval/search
```

Go/TypeScript Core SDK 对应提供 `Retrieval.Collections`、`Retrieval.Documents`、
`Retrieval.IngestionJobs` 和 `Retrieval.Search`。Agent 通过受治理的 `retrieval.search`
Capability 使用相同能力，不直接查询 Retrieval 表。

Platform 保存 `RetrievalCollection`、`RetrievalDocument`、`RetrievalChunk`、摄取任务和索引元数据。
`tma-knowledge` 保存 Knowledge Service、Collection/Document ID 选择、产品 Prompt、策略、Share 和
Question Audit。跨服务 ID 是普通字符串，不建立数据库外键，也不使用跨服务事务。

## Biography 的语音接入方式

Biography 生产路径通过 Core SDK 的 `Speech.DialRealtime` 使用 Platform Speech，不再接收
豆包 Endpoint、Resource ID 或 API Key。调用关系是：

```text
麦克风、录音分段、采访状态
    -> tma-biography

音频转文本
    -> Platform ASR / Realtime Speech API

文本转音频
    -> Platform TTS / Realtime Speech API

采访 Agent
    -> Platform Session / Run / Live Event
```

Platform 负责语音模型注册、Provider Adapter、共享凭据、路由、健康检查、限流、用量和审计。
Biography 负责何时开始/停止识别、采访上下文、回复文本、声音/情绪参数、录音留存和业务进度。
音频及转录内容仍按 Biography 的数据策略保存；Platform 只保存完成模型调用所需的短期输入输出
和通用用量/审计记录。

高吞吐音频流可以由独立 Model/Speech Runtime 数据面处理，`tma-server` 负责控制面和统一入口。
这是 Platform 内部部署细节；Biography 只面向 Platform SDK/API，不直接连接豆包等供应商协议。

当前实时协议支持 `session.start`、二进制音频、`audio.commit`、`text.append`、
`text.commit` 和 `session.cancel`；服务端返回 `transcript.partial`、`transcript.final`、
二进制音频、`audio.done` 和结构化错误。豆包帧只存在于 Platform Provider Adapter 内。

豆包 ASR/TTS 模型目录可通过 CLI 注册：

```bash
tma provider create --id doubao-asr --type doubao \
  --base-url wss://openspeech.bytedance.com/api/v3/plan/sauc/bigmodel_async \
  --api-key-env TMA_LLM_API_KEY
tma model upsert --provider doubao-asr --model seed-asr-2.0 \
  --capability speech_to_text --protocol doubao_realtime_asr \
  --resource-id volc.seedasr.sauc.duration --audio-format pcm_s16le --sample-rate 16000

tma provider create --id doubao-tts --type doubao \
  --base-url wss://openspeech.bytedance.com/api/v3/plan/tts/bidirection \
  --api-key-env TMA_LLM_API_KEY
tma model upsert --provider doubao-tts --model seed-tts-2.0 \
  --capability text_to_speech --protocol doubao_bidirectional_tts \
  --resource-id seed-tts-2.0 --default-voice zh_female_kefunvsheng_uranus_bigtts \
  --audio-format pcm_s16le --sample-rate 24000
```

Multimodal Realtime 模型目录使用独立的 `multimodal_realtime` capability。公共入口为
`GET /v2/model-runtime/multimodal/realtime`，Application Identity 需要 `model:realtime` Scope：

```json
{
  "provider_id": "enterprise-realtime",
  "model": "realtime-v1",
  "capability_type": "multimodal_realtime",
  "capabilities": {
    "protocol": "tma_multimodal_websocket_v1",
    "realtime": {
      "input_formats": [
        {"kind": "audio", "content_type": "audio/pcm", "codec": "pcm_s16le"},
        {"kind": "video", "content_type": "video/h264", "codec": "h264"}
      ],
      "output_modalities": ["text", "audio"],
      "output_formats": [
        {"kind": "audio", "content_type": "audio/pcm", "codec": "pcm_s16le"}
      ],
      "max_input_tracks": 2,
      "max_frame_bytes": 1048576
    }
  }
}
```

管理 API `POST /v2/llm-models` 会规范化并校验格式组合；`GET /v2/capabilities` 通过
`model.multimodal_realtime` 返回可用 Provider、模型、协议和完整 Realtime 约束。Server 公共入口
只批准目录允许的输入格式和输出模态，Runtime 还会拒绝超出目录最大帧或输出格式的 Provider 握手。
内部 ObjectRef 解析器要求调用者可访问 `session_id`，并在 Workspace/Session Artifact ACL、大小、
Content-Type 和 SHA-256 全部通过后才将媒体 payload 交给 Runtime；bucket、key 和存储 Credential 不跨越边界。

OpenAI Realtime 使用相同公共入口和 SDK，只替换模型目录中的 Provider Adapter：

```json
{
  "provider_id": "openai-realtime",
  "model": "realtime",
  "capability_type": "multimodal_realtime",
  "capabilities": {
    "protocol": "openai_realtime_websocket",
    "upstream_model": "gpt-realtime",
    "realtime": {
      "input_formats": [
        {"kind": "audio", "content_type": "audio/pcm", "codec": "pcm_s16le"},
        {"kind": "image", "content_type": "image/jpeg", "codec": "jpeg"},
        {"kind": "image", "content_type": "image/png", "codec": "png"}
      ],
      "output_modalities": ["text", "audio"],
      "output_formats": [
        {"kind": "audio", "content_type": "audio/pcm", "codec": "pcm_s16le"}
      ],
      "max_input_tracks": 2,
      "max_frame_bytes": 1048576
    }
  }
}
```

对应 Provider 的 `base_url` 配置为 `wss://api.openai.com/v1/realtime`，`api_key_env` 指向受管密钥；
API Key 不能放在 URL。音频 track 必须声明 `sample_rate_hz=24000`、`channels=1`。摄像头输入使用
`kind=image` 的 JPEG/PNG 独立帧，不使用 `kind=video` 或 H264。`upstream_model` 允许 Platform 模型名
保持稳定，同时独立切换厂商模型 ID。

## 规划中的公共能力

| 优先级 | 能力 | 目的 |
| --- | --- | --- |
| 已完成 | Application/Service Identity | 每个应用拥有独立身份、最小 Scope、可轮换凭据和审计主体；Quota 执行单独推进 |
| 已完成 | On-behalf-of 用户委托 | 应用安全代表当前用户调用 Platform |
| 已完成 | Resource Ownership | Agent、Environment、Session、Skill、MCP 使用 Application Identity、稳定 `external_ref` 和受限 labels |
| 已完成 | Declarative Application Manifest | SDK 幂等发布 Agent、Skill、MCP 和默认 Environment，并返回逐资源调和状态 |
| 已完成 | Reliable Event Subscription/Webhook | Outbox、稳定 delivery ID、HMAC 签名、指数退避、死信和 replay |
| 已完成 | First-class Run Persistence | 独立 Run/Attempt、租约历史、恢复状态和 Event 归属 |
| 已完成 | Model Runtime 基础数据面 | 公开同步 Generate/Embedding/Rerank、Agent Turn 流式 Generate 和 Realtime ASR/TTS 可独立部署，并具备 mTLS/Service Mesh、短期凭证和背压指标 |
| 已完成 | Model/Speech 基础生产治理 | Workspace/Identity/Route 并发保护、跨副本分钟配额、Speech 最大时长和拒绝审计 |
| 已完成 | Signed Artifact Exchange | 短期一次性导入/导出、大小与 SHA-256 校验、原子防重放和补偿清理 |
| 已完成 | Capability Discovery | 查询 Workspace 当前可用能力、版本和健康状态；返回模型路由、worker 声明能力和内建 Runtime 状态 |
| 已完成 | App-scoped Secret Reference | Application Identity 隔离、加密存储、元数据只读返回、Runtime 按 `app_id` 注入和 SDK 管理 |
| 已完成 | Quota Policy Administration | Workspace 套餐、Application Identity 覆盖、版本化管理 API、跨副本分钟执行和月度预算告警 |
| 已完成 | Multimodal Generate Phase 1 | Generate 兼容文本和图片结构化消息，执行 URL/MIME/大小限制、Vision capability gate 和 Invocation 审计 |
| 已完成 | Multimodal Stream Phase 2A | 固定 v1 track、28 字节媒体帧、ObjectRef 和双向 credit window，并提供共享编解码/校验实现 |
| 已完成 | Multimodal Stream Phase 2B Runtime 切片 | 提供 TMA-native WebSocket Adapter、签名认证的 Server→Runtime 内部路由、严格握手/帧/双向 credit 校验和取消传播 |
| 已完成 | Multimodal Stream Phase 2B 目录准入 | 提供独立 realtime capability、媒体格式/模态/上限声明、Capability Discovery，以及 Server/Runtime 双层约束校验 |
| 已完成 | Multimodal Stream Phase 2B ObjectRef 解析 | 提供 Workspace/Owner/Session Artifact ACL、受限读取、元数据与实际内容校验，并只向 Runtime 传递媒体帧 |
| 已完成 | Multimodal Stream Phase 2B Invocation 审计 | 聚合媒体 item/bytes/audio/video 指标、稳定终态、单次落库、长会话 admission，并通过管理 API/SDK 查询 |
| 已完成 | Multimodal Stream Phase 2B 公共治理路由 | 串联 Scope、目录、Credential、ObjectRef、admission、Runtime proxy 和 Invocation recorder |
| 已完成 | Multimodal Realtime Core SDK | Go/TypeScript 帧编解码、credit 等待、`latest` 丢弃、discontinuity 和有界事件队列 |
| P1 | Multimodal Realtime 生产验证 | OpenAI Adapter 及本地慢链路/断线/共享配额测试已完成；继续真实账号和部署环境 PostgreSQL 多副本压测 |

规划能力进入 Platform 前仍需通过 [边界准入检查](./platform-boundary.md#新功能准入检查)。
Multimodal Phase 2A/2B 的完整协议和开放门槛见
[Multimodal Realtime v1](./multimodal-realtime-protocol.md)。公共 WebSocket 和 Core SDK 已用于协议联调；
一般应用在 OpenAI 真实账号和端到端生产验证完成前继续使用图片 Generate 或现有 Realtime Speech。

## 不属于 Platform 的能力

- Knowledge Service、产品问答/检索策略、敏感词/拒答、Web fallback、公开分享和问题审计；
- Biography 章节、录音留存、采访进度、声音/情绪业务策略和语音交互编排；
- R 生存分析 Project、GitLab Repository、Notebook、数据集和数据清洗/分析模板；
- 应用页面、领域权限、产品工作流和应用部署；
- 任何只服务一个应用的数据库表和 HTTP Handler。

多个应用需要同一种具体能力时，优先建设独立 MCP、Extension 或服务，而不是把业务实现放回
`tma-server`。

## Multimodal Generate 使用

原有纯文本请求无需修改。图片输入时，把 `messages[].content` 改成结构化数组并显式选择
`text_image` 模型：

```json
{
  "provider_id": "openai",
  "model": "vision-model",
  "messages": [{
    "role": "user",
    "content": [
      {"type": "text", "text": "请检查这张图"},
      {"type": "image_url", "image_url": {"url": "https://images.example.com/scan.png", "detail": "high"}}
    ]
  }]
}
```

支持 `data:image/png|jpeg|webp|gif;base64,...` 和公共 HTTPS URL；不支持 HTTP、凭据 URL、本机或
私网地址。每次请求最多 128 条消息、256 个 content part、16 张图片；文本不超过 2 MiB，内联
图片单张及合计均不超过解码后 20 MiB。图片只能放在 `user` 消息中。远程 URL 由 Provider 获取，
Platform 不缓存或持久化图片；需要受控留存时由应用先创建 Artifact/ObjectRef。

## Quota Policy 使用

Workspace Operator/Admin 通过 `/v2/quota-policies` 管理策略。Workspace 策略定义租户总量和默认 Identity
限额；Application 策略只能覆盖该应用的 Identity 限额与月度预算，不能修改 Workspace 总量。
创建策略时不发送 `If-Match`，更新或归档时发送响应中的 revision，例如 `If-Match: "2"`。

应用凭证具备 `quota:read` 后，可调用 `GET /v2/quota-policies/effective` 查询自己的生效策略、
本月 Model/Speech 用量以及 `warning`/`exceeded` 告警；不能查询其他应用，也不能修改策略。
`quota:write` 可供 `kind=service` 的受控 Operator 身份执行管理自动化；普通 Application Credential
即使误配该 scope 也不能修改策略。月度预算当前只产生状态和告警，
不会硬拒绝请求；每分钟限额继续复用 PostgreSQL 原子 quota bucket 执行。

## 新应用验收清单

新应用接入完成时必须满足：

1. 应用只通过 Core SDK/API、Event、Worker Protocol 或 MCP 使用 Platform。
2. 应用源码不 import Platform `internal` 包，也不连接 Platform 数据库。
3. Agent/Skill/MCP 声明可以幂等发布并固定版本。
4. 每个 Run 使用稳定幂等键，并能在进程重启后恢复状态。
5. 业务状态和 Platform ID 的关联由应用数据库维护。
6. 大对象通过 Artifact/ObjectRef 交换，不共享永久对象存储凭据。
7. 删除应用不会影响 Platform 启动；增加应用不需要修改 Platform 路由或迁移。

## 相关文档

- [Platform 边界](./platform-boundary.md)
- [仓库拆分方案](./repository-split.md)
- [HTTP API](./api.md)
- [Core SDK](./sdk.md)
- [架构](./architecture.md)
- [MCP](./mcp.md)
- [Extension](./extensions.md)
- [部署](./deployment.md)
- [运维](./operations.md)
