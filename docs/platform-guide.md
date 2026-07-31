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
| 能力治理 | Tool/Capability Schema、Workspace Policy、MCP Registry、Skill Registry |
| 模型治理 | Provider/Model 配置、能力描述、Generate、Embedding、Rerank、Session/Run 用量及独立 Invocation 审计 |
| Artifact | ObjectRef、Session Artifact、上传下载、引用和生命周期 |
| Retrieval Runtime | Collection、Document、Chunk、通用文档解析/切块、摄取任务、索引、Search 和 Citation |
| 平台运营 | Schedule、Trace、Audit、Metrics、Evaluation 和 Experiment |

当前已经提供通用 Generate、Embedding、Rerank、Realtime ASR/TTS、独立 Invocation Usage/Audit、
Application/Service Identity、用户委托 Token Exchange、`/v2/retrieval/*` 公共 API及 Go/TypeScript
Core SDK，以及 Model/Speech 基础 Quota 和容量保护。同步模型调用、Agent Turn 流式 Generate 和
Realtime ASR/TTS 已可使用独立 Model Runtime 数据面；尚未完成的公共能力包括可靠 Webhook、可管理
的租户 Quota 套餐和内部短期调用凭证。旧
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

每个身份必须声明最小 Scope。当前 Scope 覆盖 `agents:*`、`sessions:*`、`skills:*`、`mcp:*`、
`environments:*`、`artifacts:*`、`evaluations:*`、`retrieval:*`、三类 `model:*` 和
`speech:realtime`；未映射的 API 默认拒绝。需要代表用户权限的请求通过 `/v2/auth/token-exchange`
换取最长 15 分钟的 OBO Token，不能改用服务身份扩大权限。

交换请求必须同时提供应用 Service Credential 和原始用户 JWT/OIDC Token。Platform 重新验证用户
Token，要求双方 Workspace 相同，并只签发请求 Scope 与应用 Scope 的交集。委托 Principal 保留用户
Subject、Owner、角色和资源边界，同时携带应用身份；委托 Token 不能再次交换。禁用应用身份或撤销
凭据后，未过期的委托 Token 也立即失效。签名密钥独立于 JWT/OIDC/Web Session 等密钥，默认有效期
5 分钟、最大 15 分钟，Token 正文不会持久化或进入审计日志。

Go SDK 使用 `client.ServiceIdentities`，TypeScript SDK 使用 `client.serviceIdentities` 管理身份和凭据。
生产部署应把创建时返回的 Token 写入应用 Secret Manager，不得写入应用数据库、日志或仓库。

### 2. 发布运行资源

应用仓库保存 Agent、Skill 和 MCP 的声明源文件，部署或 bootstrap 程序通过 SDK 幂等发布：

```text
应用 Agent 声明  -> agents / agent_config_versions
应用 SKILL.md    -> skills / skill_versions
应用 MCP 配置    -> mcp_registry_servers / mcp_registry_server_versions
```

已发布记录属于 Platform 运行资源；Prompt、`SKILL.md`、MCP Server 源码和业务含义仍属于应用。
用户直接在 Platform 创建的个人 Agent/Skill 则以 Platform 中的版本为事实源。

Biography 的 bootstrap 是当前参考实现：它查找或创建 Environment、发布 Skill 版本，再创建或更新
采访 Agent 和整理 Agent。未来会用声明式 Application Manifest 取代每个应用自写 bootstrap。

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

### 4. 消费事件和实时输出

- 持久化 Event 是事实源，支持按 sequence 续传和重放。
- Run Event Stream 用于跟踪指定 Run。
- Live Stream 用于低延迟文本展示，可能因断线丢失，不能作为最终结果事实源。
- 客户端必须处理重复事件、断线重连、Token 过期和终态关闭。

Biography 当前先打开 Session Live Stream，再启动带幂等键的 Run；断线时仍以 Run Result 和
持久化 Event 恢复状态。这是实时应用的参考模式。

### 5. 处理 Artifact

大文本、文件和二进制结果使用 Artifact/ObjectRef，不放入应用自定义 Event payload。应用通过
API 上传、下载和引用对象，不能取得 Platform 对象存储的永久凭据。

业务原始文件可以继续存放在应用自己的 Bucket。需要交给 Agent 处理时，应通过受控上传或未来的
签名 Artifact Exchange 建立 Platform ObjectRef，而不是让两个服务共享 Bucket 凭据。

### 6. 提供业务工具

业务数据访问和外部操作通过 MCP、Worker Capability 或独立 Extension 提供：

- Platform 保存注册、版本、绑定、权限和调用记录；
- 应用/Extension 实现具体服务、凭据使用和业务授权；
- Agent 配置只引用版本化 Skill/MCP/Capability；
- Platform 不能 import 具体 Provider，也不能直接查询应用数据库。

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

## 规划中的公共能力

| 优先级 | 能力 | 目的 |
| --- | --- | --- |
| 已完成 | Application/Service Identity | 每个应用拥有独立身份、最小 Scope、可轮换凭据和审计主体；Quota 执行单独推进 |
| 已完成 | On-behalf-of 用户委托 | 应用安全代表当前用户调用 Platform |
| P0 | Resource Ownership | 为 Agent/Skill/Session 增加 `app_id`、`external_ref` 和 labels |
| P0 | Declarative Application Manifest | 幂等发布 Agent、Skill、MCP 和默认 Environment |
| P0 | Reliable Event Subscription/Webhook | 应用可靠消费 Run 完成、失败、审批和 Artifact 事件 |
| P0 | First-class Run Persistence | 明确 Session、Turn、Run、Attempt、重试和恢复关系 |
| 已完成 | Model Runtime 基础数据面 | 公开同步 Generate/Embedding/Rerank、Agent Turn 流式 Generate 和 Realtime ASR/TTS 均可独立部署；内部 mTLS、短期凭证和背压指标继续硬化 |
| 已完成 | Model/Speech 基础生产治理 | Workspace/Identity/Route 并发保护、跨副本分钟配额、Speech 最大时长和拒绝审计 |
| P1 | Signed Artifact Exchange | 不共享对象存储凭据地交换文件 |
| P1 | Capability Discovery | 查询 Workspace 当前可用能力、版本和健康状态 |
| P1 | App-scoped Secret Reference | 安全引用应用密钥，不返回密钥正文 |
| P1 | Quota Policy Administration | 在默认执行基础上增加每租户套餐、按应用覆盖、管理 API 和预算告警 |
| P1 | Multimodal Stream 扩展 | 在现有 Realtime Speech 基础上扩展图片/视频等多模态流 |

规划能力进入 Platform 前仍需通过 [边界准入检查](./platform-boundary.md#新功能准入检查)。

## 不属于 Platform 的能力

- Knowledge Service、产品问答/检索策略、敏感词/拒答、Web fallback、公开分享和问题审计；
- Biography 章节、录音留存、采访进度、声音/情绪业务策略和语音交互编排；
- R 生存分析 Project、GitLab Repository、Notebook、数据集和数据清洗/分析模板；
- 应用页面、领域权限、产品工作流和应用部署；
- 任何只服务一个应用的数据库表和 HTTP Handler。

多个应用需要同一种具体能力时，优先建设独立 MCP、Extension 或服务，而不是把业务实现放回
`tma-server`。

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
