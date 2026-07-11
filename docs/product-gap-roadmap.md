# TMA Product Gap Roadmap

本文档记录 TMA 从当前 Agent Session / Event 骨架走向完整产品仍缺的关键模块。它不是排期承诺，而是帮助研发判断下一步先做什么、哪些抽象还不能过早固化。

## 当前真实位置

已经跑通的主链路：

```text
Agent / Environment / Session
  -> user.message
  -> AgentRuntime
  -> LLM request
  -> builtin tools
  -> human intervention
  -> session_events / SSE / CLI attach
```

这说明 TMA 已经有了 Session 事实源、turn 生命周期、审批挂起、LLM usage、summary 和 CLI 交互闭环。

但它还不是完整 Agent 平台。当前缺口主要集中在：

```text
安全隔离
能力扩展
跨环境文件系统
长期记忆
多租户与权限
调试与可观测
```

## 差距总览

| 模块 | 当前状态 | 主要缺口 | 依赖 |
|---|---|---|---|
| Tools | 有最小 `internal/tools` registry / executor | 工具版本、权限声明、schema 管理、错误回环、UI 展示 | Capability / Permissions / Sandbox |
| Skills | Agent config 有 raw `skills` 字段 | skill 安装、选择、版本、注入、审计 | ContextBuilder / Plugin |
| Memory | 有 session summary | 长期记忆、项目记忆、用户记忆、检索和遗忘策略 | Tenant / Permission / Object metadata |
| Sandbox | `cloud_sandbox` 已落到 `OnlyboxesProvider`，按 Session 和 scope 创建并复用容器，支持空闲 TTL 与最大寿命回收 | sandbox doctor/preflight、镜像策略、网络策略、文件隔离、资源限制，以及跨 Server 的容器归属协调 | Capability / Object Store |
| Multi-tenant | 有默认 org/workspace 数据 | 身份、成员、角色、租户隔离、配额 | Permission / Audit |
| Permission | 有 `intervention_mode` | RBAC、policy engine、tool/file/sandbox 权限、审批审计 | Multi-tenant / Tools |
| Object Storage | 部分实现 | S3 兼容对象存储、artifact、workspace snapshot、跨环境文件同步；下载必须走 TMA 代理 | Sandbox / File API / Permission |
| Plugin | 规划中 | manifest、安装、启用、tool/skill/hook 汇聚 | Tools / Skills / Permission |
| Inspector | 未实现 | trace、timeline、approval UI、context preview、artifact preview | Events / Observability |
| Observability | 有设计文档和事件事实源 | `/trace`、span mapper、metrics/exporter | Events / Usage |

## 对象存储与跨环境文件系统

不要把文件二进制直接存入 Postgres。

Postgres 应只保存文件和 artifact 的元数据、权限、索引和引用关系；文件内容、静态资源、workspace snapshot、tool 输出附件应进入 S3 兼容对象存储，例如 RustFS、MinIO、AWS S3 或企业内部对象存储。客户端下载时通过 TMA 代理，不直接暴露对象存储地址。

目标架构：

```text
Postgres
  files / artifacts / object_refs
    tenant_id
    workspace_id
    session_id
    environment_id
    object_key
    content_type
    size_bytes
    checksum
    version
    visibility
    created_by

S3-compatible Object Store
  org/{org_id}/workspace/{workspace_id}/session/{session_id}/...
  artifacts/{artifact_id}/...
  snapshots/{snapshot_id}/...
```

这样做的原因：

- Postgres 负责事务、索引、权限和审计，不承担大对象吞吐。
- 对象存储负责大文件、静态资源、二进制 artifact、快照和跨环境同步。
- 本地开发可以用 RustFS / MinIO；生产可以替换成企业 S3 兼容服务。
- UI、CLI、Sandbox、Plugin 都可以通过同一套 object reference 访问文件；对外下载统一走 TMA 代理端点。

### 跨环境文件系统

TMA 需要的是“跨环境文件系统”，不是单机目录。

典型场景：

```text
用户上传文件
  -> Object Store
  -> Postgres 记录 object_ref
  -> Session Context 引用 file/artifact
  -> Sandbox 启动时拉取或挂载需要的对象
  -> Tool 执行生成 artifact
  -> artifact 回写 Object Store
  -> Inspector / CLI / UI 展示下载链接
```

第一版可以做成显式同步，而不是立刻做复杂分布式文件系统：

```text
upload
download (via TMA proxy)
copy_to_environment
copy_from_environment
list_artifacts
```

后续再演进为：

```text
content-addressed storage
workspace snapshot
lazy mount
artifact versioning
signed URL
garbage collection
retention policy
```

### 边界原则

1. 二进制不进 Postgres。
2. 对象 key 必须带租户 / workspace / session 作用域。
3. Postgres 中的 object metadata 是权限判断入口。
4. Sandbox 不能直接拿全局 bucket 权限；只能拿 scoped credential 或服务端代理。
5. 文件引用进入 LLM 上下文时只给摘要、路径、MIME、大小、预览，不直接塞大文件内容。
6. Artifact 需要可审计：谁创建、来自哪个 turn、哪个 tool、输入输出是什么。

## 推荐落地顺序

### Phase 1: 安全和能力底座

目标：让工具执行和文件访问有可控边界。

1. 定义 Capability / Tool permission model。
2. 定义 Object Store metadata schema，不存二进制。
3. 引入本地 RustFS / MinIO 开发配置。
4. 增加 artifact / object reference 的最小 Store 接口。
5. 让 tool result 可以引用 object refs，而不是把大内容塞进 event payload。

### Phase 2: Sandbox 文件流

目标：让不同 environment 能围绕同一批对象协作。

1. Environment 启动时按 object refs 拉取输入。
2. Tool 执行输出写回 object store。
3. Session events 只记录 object refs 和摘要。
4. CLI 增加 artifact list / download 的最小命令，download 通过 TMA 代理返回字节流。

### Phase 3: Skills / Memory

目标：让上下文不只是聊天历史。

1. Skill registry：安装、版本、启用。
2. ContextBuilder 从 enabled skills 收集上下文。
3. Memory store 保存长期记忆和检索索引。
4. Memory 记录必须受 tenant / workspace / user 权限约束。

### Phase 4: Plugin

目标：让能力包可安装、可审计、可禁用。

1. 固定 plugin manifest schema。
2. Plugin 可以声明 skills、tools、hooks、assets。
3. Plugin tools 注册进统一 tools registry。
4. Plugin assets 进入对象存储，不进入 Postgres。
5. Plugin 权限声明进入审批和审计体系。

### Phase 5: Inspector / Observability

目标：让人能看懂 Agent 怎么跑的。

1. `/trace` API：从 `session_events` 投影 turn timeline。
2. Inspector 展示 LLM request、tool call、approval、artifact、summary。
3. Object refs 支持预览和下载。
4. 增加 metrics / spans exporter。

## 当前下一步建议

不要现在抽 SDK。

优先做一件更基础的事：**对象存储 + artifact metadata + tool result object refs 的最小闭环设计**。

这一步可以同时服务：

- 沙箱输入输出
- 跨环境文件系统
- Inspector artifact preview
- Plugin assets
- Memory 附件
- 大文件 token 预算控制

建议先写设计和 schema，再实现本地 RustFS / MinIO 可跑的最小验证。

## 未来实现：监控驱动的 Agent 自动进化闭环

在上面的安全、文件流、技能和观测底座之上，下一阶段要把 `Goal`、运行监控、失败检测和版本发布连接起来，让系统不只是“能跑一轮”，而是“能围绕完成标准持续收敛，并在真实业务中持续升级”。

这个方向是 Agent Cloud Runtime 的长期核心：模型本身继续进化会遇到阶段性瓶颈，企业 Agent 的提升空间会更多来自 Harness 层对运行数据的利用。Runtime 需要持续观察 Agent 的执行过程，识别重复失败、工具误用、上下文缺失、Skills 不匹配、调度低效和安全风险，然后生成可测试、可审批、可回滚的升级候选。

### 目标

1. 让 Session 或 Agent 配置显式携带 `goal`、验收标准、约束和预算。
2. 让 Runtime 能根据目标自动判断继续执行、回退计划还是停止。
3. 让完成状态可被规则或 Judge 验收，而不是只靠人工观察。
4. 让 Event、trace、tool result、summary、artifact 和用户反馈成为 Agent 改进的证据源。
5. 让 `system`、memory、tools、skills、多智能体 routing 和 runtime policy 的升级候选可版本化、可审批、可回滚。

### 建议的数据结构

```text
goal
  - title
  - description
  - success_criteria
  - constraints
  - budget
  - stop_conditions
  - judge_policy
```

### 运行时行为

1. 任务开始时，系统把 goal 注入上下文，作为计划和执行的主约束。
2. 每轮执行后，Runtime 读取 tool result、event、summary 和 artifact 状态，判断是否满足 success_criteria。
3. 如果未满足，则继续计划 - 执行 - 评估循环；如果接近预算上限，则降级为摘要式续跑或请求人工接管。
4. 如果满足 stop_conditions，则写入完成事件并结束 Session。
5. Session 结束后，后台检测器聚合运行证据，识别失败模式和能力缺口。
6. 检测器只生成升级候选，不直接覆盖线上 Agent；候选升级进入回放、Judge、人工审批和版本发布流程。

### 自动进化对象

| 对象 | 示例 | 默认治理方式 |
|------|------|--------------|
| `baseline` | 原则、边界、质量标准 | 人工维护，不自动改 |
| `system` | 岗位提示词、输出规范 | 候选 diff + 测试 + 审批 |
| `memory` | 项目状态、常见偏好、失败经验 | 带证据来源、可过期 |
| `tools` | 工具 schema、权限、默认参数 | 安全校验 + 版本化 |
| `skills` | L1 描述、SKILL.md、脚本资产 | AgentConfigVersion 绑定 |
| `routing` | 多智能体分工、委派策略 | Shadow 验证后灰度 |
| `runtime_policy` | 预算、压缩、审批、重试、调度 | 策略版本化 + 审计 |

### Judge 机制

1. 第一版优先用规则校验，例如测试是否全绿、文件是否生成、接口是否返回预期结果。
2. 后续再接模型 Judge，用于评估“是否真的达到目标”这类难以编码的结果。
3. Judge 结果必须可审计，不能只返回一个布尔值，至少要带理由、证据和失败点。

### 与现有模块的关系

- 目标定义依赖 `Session`、`Agent` 和 `Runtime Settings`。
- 完成判断依赖 `Context`、`Events`、`Artifacts` 和 `Observability`。
- 长期记忆可为下一轮 goal 提供历史偏好和项目状态，但不替代 goal 本身。
- `Goal` 不应塞进 prompt 里当纯文本说明，而应成为可持久化、可版本化、可审计的运行时字段。
- 自动进化依赖 `Observability`、`AgentConfigVersion`、`SkillVersion`、`Memory Store` 和后续 `RuntimePolicyVersion`。
- 交互界面不是核心，SDK 和默认 UI 只负责把业务目标、运行过程、审批和结果呈现给用户；Harness 工程负责持续运行和持续改进。
