# 仓库拆分方案

当前仓库已经同时承载控制面、执行面、多个 Web 产品、SDK 和自传业务。短期继续使用
monorepo 便于联调，但发布、权限、依赖和数据库变更的边界已经开始分化。本文件定义目标
仓库、暂时保留的边界以及迁移顺序。

## 目标仓库

| 仓库 | 负责内容 | 发布单元 | 依赖方向 |
| --- | --- | --- | --- |
| `tma-platform` | `tma-server`、Agent/Session/Run 控制面、Postgres Store、API Handler、SQL 迁移 | `tma-server` + 数据库迁移 | 只依赖版本化 API/SDK 契约 |
| `tma-worker-runtime` | `tma-worker`、Worker 协议、Capability Provider、沙箱和插件执行 | `tma-worker`、运行时镜像 | 依赖 Worker 协议和工具 Schema |
| `tma-console` | Workbench、Inspector、Space | Web 静态资源 | 依赖 TypeScript SDK |
| `tma-knowledge` | 知识库管理、文档摄取、检索问答、公开分享和 Knowledge Web | Knowledge API + Web 静态资源 | 依赖平台身份/Workspace 契约、模型服务和对象存储契约 |
| `tma-biography` | 自传移动端、语音网关、自传 Agent bootstrap、录音和章节业务 | 移动端 + `tma-biography-voice-gateway` | 通过 SDK/API 依赖平台，不依赖平台 `internal` 包 |
| `tma-sdk` | OpenAPI v2、事件 Schema、Go SDK、TypeScript SDK | SDK 包和 API 契约版本 | 不依赖具体 Server 实现 |

`tma-extensions`（浏览器网关、OnlyBoxes、R Notebook、示例插件）暂时作为独立发布目录
维护；只有在插件 ABI 和镜像发布频率明显独立后再建立独立仓库。

## 当前目录映射

第一阶段不移动代码，只建立映射和依赖规则：

```text
platform:  cmd/tma-server, internal/{agent*,httpapi,managedagents,runner,...},
           sql/ 中除独立业务迁移外的 Platform Schema
worker:    cmd/tma-worker, internal/{capability,execution,tools,workruntime,workerselect}
console:   apps/{workbench,inspector,space}
knowledge: apps/knowledge, internal/httpapi/knowledge_service.go,
           internal/managedagents/knowledge_service.go,
           api/v2 中 /v2/knowledge/* 与 /v2/public/knowledge-shares/* 契约，
           sql/migrations/{000097_knowledge_service,
                           000098_knowledge_share_history,
                           000099_knowledge_service_documents}.sql
biography: cmd/tma-biography-*, internal/biographyvoice, apps/biography-mobile,
           skills/{conduct-biography-interview,structure-biography-chapters,
                   verify-biography-facts},
           sql/migrations/{000103_biography_voice_persistence,
                           000104_biography_recording_segments}.sql
sdk:       api/v2, sdk/tma, sdk/typescript
```

目录映射不是最终的拆分方式。`internal/managedagents`、`internal/objectstore` 等平台包被
多个边界使用，迁移时必须先通过 API、协议或独立公共包替换直接 Go import。

## 必须保持的边界

1. Biography 只能调用公开 HTTP API/SDK。`tma-biography` 不得导入 `internal/httpapi`、
   `internal/managedagents` 或其他平台实现包。
2. Worker 不能直连 PostgreSQL。Worker 与 Platform 之间只使用版本化的 Worker HTTP
   协议和工具/能力 Schema。
3. Console 不复制 OpenAPI 类型。所有 API 类型、SSE 解析和分页契约来自 `tma-sdk`。
4. Knowledge 拥有自己的知识库、文档、Chunk、服务和分享数据；Platform 不能直接读写
   Knowledge 表。Workspace 身份、模型调用和对象引用通过版本化契约集成。
5. 每个服务拥有自己的业务 Schema 和迁移包。迁移可以由统一部署流程编排，但不能由服务
   启动时隐式执行，也不能跨服务直接修改对方的数据表。
6. SDK 的破坏性变更必须先更新契约版本，再更新 Platform、Console、Knowledge 和
   Biography 的消费方。

## 迁移顺序

### 0. 基线（当前阶段）

- 保持当前 monorepo 可构建、可测试、可部署。
- 将边界规则写入各项目的 CI 检查，禁止新增跨边界 `internal` import。
- 为 Worker 协议和 Biography 网关补充独立的契约测试。

### 1. 抽离 Biography

先复制而非删除以下内容到 `tma-biography`，确保两个仓库可以并行验证：

- `apps/biography-mobile`
- `cmd/tma-biography-voice-gateway`
- `cmd/tma-biography-agent-bootstrap`
- `internal/biographyvoice` 中与网关相关的业务代码
- 三个 Biography Skills 和 `000103`、`000104` 两条 SQL 迁移

抽离前要完成两项改造：

- 把 `internal/biographyvoice` 依赖的 `objectstore`、认证和 TMA 调用收敛为小型接口或
  公共 SDK；
- 给网关和移动端建立独立的配置、数据库迁移、Docker 镜像和发布流水线。

### 2. 建立并抽离 Knowledge 服务

Knowledge 目前不只是前端，核心实现还嵌在 `internal/httpapi` 和 `internal/managedagents`。
先建立独立的 Knowledge 应用层和 Store 接口，再迁移以下能力：

- `/v2/knowledge/*` 和 `/v2/public/knowledge-shares/*` API；
- 文档上传、文本提取、切块、Embedding、检索、问答和联网补充；
- 知识库、文档、Chunk、服务、分享及问答审计数据；
- `apps/knowledge` 和 `000097` 至 `000099` 三条迁移。

迁移期间由 Platform API Gateway 代理原路径，外部客户端不更换 URL。Knowledge 不直接
读取 Platform 数据库；Workspace 身份、LLM 和对象存储通过服务凭据及明确契约接入。

### 3. 抽离 Console

将 Workbench、Inspector、Space 和共享的前端构建配置迁移到 `tma-console`。Server 只保留构建产物的
可选嵌入能力，生产部署优先使用独立静态站点/CDN。

### 4. 抽离 SDK/契约

把 OpenAPI、事件 Schema、Go SDK 和 TypeScript SDK 变成独立版本库。Platform 发布 API
时引用固定 SDK/契约版本，避免 Server 构建过程依赖工作区相对路径。

### 5. 抽离 Worker

当 Worker HTTP 协议、工具 Schema 和 Capability Provider 已有兼容性测试后，再迁移
`tma-worker` 和运行时镜像。Platform 保留队列、租约和结果回写；Worker 不携带数据库模型。

## 当前不做的事情

- 不把每个 `internal` 包拆成单独 Go module。
- 不在没有协议版本和契约测试前拆 Server/Worker。
- 不让 Biography 直接复用平台数据库 Store 实现。
- 不把 Knowledge 只当作静态前端拆走并继续跨仓库操作 Platform 数据库。
- 不为了拆仓库重写现有 API 或改变 Session/Turn 持久化语义。

## 验收标准

拆分完成后，下面四条命令应分别在对应仓库执行，不需要读取其他仓库源码：

```bash
make test-platform
make test-worker
make test-console
make test-knowledge
make test-biography
make test-sdk
```

跨仓库联调只允许通过已发布的 API、SDK、Worker 协议或容器镜像完成。
