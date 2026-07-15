# Skills 模块设计与规划

> 实现状态（2026-07-14）：Phase 0 至 Phase 3.5 已形成可用闭环，包括严格 binding schema、registry、不可变版本、标准 `SKILL.md` 文件包、离线 ZIP、内部市场发布与跨 Workspace 消费、对象资产、管理 UI、离线 `inputs_schema` 校验、运行时渲染预算、事件与 turn usage 审计。Plugin 汇聚仍属于后续阶段；外部 Scanner/ClamAV 继续关闭，生产仅使用不联网的 builtin scanner。

本文档给出 TMA `skills` 模块的落地设计。目标不是单纯增加一个字段，而是把当前 Agent 配置里的原始 `skills` JSON，演进为一套可安装、可选择、可版本化、可审计、可回放的上下文能力模块。

## 1. 当前现状

当前仓库已具备独立的 `skills` 后端模块：

- `AgentConfigVersion.skills` 使用 canonical schema 精确绑定 registry 版本，并持久化到 `agent_config_versions.skills_json`。
- `skills` 与 `skill_versions` 提供 workspace registry、不可变版本和归档能力。
- Runner 在进入 `AgentRuntime` 前解析、渲染并按预算降级，`ContextBuilder` 只负责注入已渲染上下文。
- Runtime event 与 `session_turn_skill_usages` 记录本轮命中的版本、mode、token 估算和状态。
- HTTP API 提供 registry 生命周期、resolve preview 和 session usage 查询。
- Skill version 可在 `manifest.inputs_schema` 中冻结非敏感输入契约；发布、Enable 和 Runtime Resolve 均按同一版本校验。
- Workbench 根据 Schema 生成枚举、布尔、数字、文本和 JSON 参数控件，并始终提交精确版本。
- 内部 Marketplace 按 Organization 共享 published catalog；Workbench 和 TMA 对话均可默认离线发现、Preview 和安装。
- `skills` 仍不参与 tool provider 选择或 worker capability 匹配，保持上下文能力边界。

当前剩余扩展项主要是 plugin 安装汇聚和更完整的 Schema 编辑体验；这些不影响现有离线安装、参数绑定与历史回放闭环。

## 2. 设计定位

`skills` 的定位应明确为：

- `skills` 是可复用的上下文能力包，不是执行器。
- `skills` 不直接执行命令，不替代 `tools`。
- `skills` 主要解决“模型该怎么想、怎么做、参考什么结构”的问题。
- `tools` 解决“模型能做什么”，`skills` 解决“模型该如何更好地做”。

因此模块边界建议保持为：

```text
AgentConfigVersion
  -> Skills config（启用哪些 skill、固定到哪些版本、传哪些参数）
  -> Skill Resolver（解析）
  -> Skill Renderer（渲染为上下文）
  -> ContextBuilder（拼到 system messages）
```

而不是：

```text
Skill -> 直接变成 worker/plugin/runtime
```

这个边界和当前代码是一致的，也避免和 `internal/tools`、`capability.Provider`、worker plugin 协议重叠。

## 3. 目标与非目标

### 3.1 目标

第一阶段 `skills` 模块应满足：

1. 技能可注册、查询、归档。
2. 技能可版本化，并能绑定到 `AgentConfigVersion`。
3. Runtime 每次执行都能确定“本轮用了哪些 skill 版本”。
4. Skill 能渲染成稳定的 system context。
5. Skill 启用结果可审计、可复放、可灰度。
6. 后续可自然接入 plugin 安装源和 marketplace。

### 3.2 非目标

第一阶段不做：

- 不把 skill 设计成 tool runtime。
- 不在 skill 内直接嵌入任意代码执行。
- 不引入复杂的在线规则引擎。
- 不先做自动生成 skill 的平台。
- 不和 memory 混成一个模块。

`memory` 是事实与经验存储，`skills` 是可控、可复用、可发布的能力包，二者应分开治理。

## 4. 核心设计原则

### 4.1 精确版本绑定

一份 `AgentConfigVersion` 必须绑定精确 skill 版本，而不是“总是取最新”。

原因：

- 便于回放历史会话。
- 便于灰度和回滚。
- 便于比较不同 skill 版本效果。
- 避免线上配置悄悄漂移。

### 4.2 配置与内容分离

`AgentConfigVersion.skills` 保存的是“启用配置”，不是整份 skill 全文。

skill 正文、模板、示例、资产等应保存在独立 registry 中，否则：

- agent config 会越来越大。
- 多 agent 复用困难。
- 无法做统一审计和版本治理。

### 4.3 渲染结果可预测

skill 必须先被解析为结构化内容，再渲染为上下文消息；不能任由不同调用路径随意拼 prompt。

### 4.4 与 plugin 解耦但兼容

第一版先做原生 skill registry；后续 plugin 只是多一种 skill 来源。

也就是：

- 先有 `skills` 模块。
- 再让 plugin 可以“贡献” skills。
- 不把 skills 完全依附在 plugin 生命周期上。

## 5. 模块分层

建议新增以下子模块：

```text
internal/skills/
  types.go
  schema.go
  registry.go
  renderer.go
  resolver.go
  audit.go
```

各层职责如下。

### 5.1 Registry

负责 skill 元数据、版本、归档状态的读写：

- 创建 skill
- 创建 skill version
- 查询 skill / version
- 列出 workspace 可用 skills
- 归档 skill / version

### 5.2 Resolver

把 `AgentConfigVersion.skills` 解析成运行时可用的 `ResolvedSkills`：

- 校验 schema
- 解析 enabled skill refs
- 加载对应版本
- 合并变量与覆盖项
- 输出渲染顺序

### 5.3 Renderer

把 skill version 内容渲染成 context blocks：

- system instruction
- checklist
- examples
- constraints
- asset refs

Renderer 输出结果再交给 `ContextBuilder` 拼装，而不是直接操作 LLM client。

### 5.4 Audit

记录一次 turn 实际启用了哪些 skill 版本、渲染了多少 token、是否有解析失败或降级。

## 6. 数据模型设计

### 6.1 Skill

表示一个稳定标识符，例如：

- `code-review`
- `enterprise-search`
- `release-manager`
- `frontend-design-system`

建议字段：

```text
skills
  id
  workspace_id
  identifier
  title
  description
  owner_type         (builtin / workspace / plugin)
  source_plugin_id   nullable
  status             (active / archived)
  created_by
  created_at
  archived_at
```

### 6.2 SkillVersion

表示 skill 的某个冻结版本。

建议字段：

```text
skill_versions
  id
  skill_id
  version
  content_format     (markdown / json / hybrid)
  manifest_json
  content_text
  assets_json
  checksum
  created_by
  created_at
```

其中：

- `manifest_json` 保存结构化 metadata 与可选参数 schema。
- `content_text` 在兼容阶段保存主要说明文本或 SKILL.md 回退副本；`tma.skill-package.v1` version 的主读取源是对象存储中的 `SKILL.md`。
- `assets_json` 保存关联对象引用，如模板、示例文件、截图。

### 6.3 AgentConfig 绑定

`AgentConfigVersion.skills` 仍保留，但语义收敛为“启用配置对象”。

推荐 schema：

```json
{
  "enabled": [
    {
      "skill": "code-review",
      "version": 3,
      "mode": "full",
      "priority": 100,
      "inputs": {
        "review_style": "strict"
      }
    },
    {
      "skill": "repo-conventions",
      "version": 2,
      "mode": "summary"
    }
  ]
}
```

说明：

- `skill` 是稳定标识符。
- `version` 是精确版本号。
- `mode` 控制渲染强度，例如 `full` / `summary` / `examples_only`。
- `priority` 控制拼接顺序。
- `inputs` 是 skill 参数。

这样可以兼容当前 `json.RawMessage` 结构，不需要立刻改变 `AgentConfigVersion` 类型。

### 6.4 Runtime 审计记录

建议新增 turn 级记录，用于复盘：

```text
session_turn_skill_usages
  id
  session_id
  turn_id
  agent_id
  agent_config_version
  skill_id
  skill_version
  render_mode
  estimated_tokens
  status              (resolved / skipped / failed)
  failure_reason
  created_at
```

如果暂时不建表，第一步也至少应在 runtime event payload 中记录。

## 7. Skill 内容模型

第一版不建议把 skill 仅仅当作一大段文本，而是定义可渲染的内容块：

```json
{
  "identifier": "code-review",
  "title": "Code Review",
  "description": "Review code with a bug-first mindset.",
  "system_role": "You are performing a code review.",
  "blocks": [
    {
      "type": "instruction",
      "title": "Primary objective",
      "content": "Prioritize correctness, regressions, and missing tests."
    },
    {
      "type": "checklist",
      "title": "Review checklist",
      "items": [
        "Check behavioral regressions.",
        "Check unsafe assumptions.",
        "Check test coverage gaps."
      ]
    },
    {
      "type": "example",
      "title": "Finding format",
      "content": "Severity + file/line + impact + fix direction."
    }
  ]
}
```

这样做的好处：

- 可控渲染。
- 可按 mode 裁剪。
- 更适合 token 预算管理。
- 未来更容易做 UI 展示和编辑器。

### 7.1 Version 输入契约

参数契约属于不可变 Skill version，保存在 `manifest.inputs_schema`。GitHub 与离线 ZIP package 可在 `SKILL.md` YAML front matter 中声明同名字段，安装时只把该字段转换到 version manifest，原始 `SKILL.md` 保持不变。

```yaml
---
name: code-review
description: Review changes with a selected policy.
inputs_schema:
  type: object
  additionalProperties: false
  properties:
    style:
      type: string
      title: Review style
      enum: [strict, balanced]
      default: balanced
    max_findings:
      type: integer
      minimum: 1
      maximum: 20
    include_tests:
      type: boolean
  required: [style]
---
```

当前契约是完全离线的 JSON Schema Draft 2020-12 子集：

- 根必须是 `type: object`，所有 object schema 必须设置 `additionalProperties: false`。
- `$ref` 只允许 `#...` 本地 fragment；拒绝非 fragment `$ref`、`$dynamicRef` 和 `$id`，编译器的外部 URL loader 固定失败。
- Schema 最大 32 KiB、深度 8、节点 512、属性总数 64；单次 inputs 最大 16 KiB、深度 8、节点 512。
- `writeOnly: true`、`x-tma-sensitive: true` 和 `format: password` 一律拒绝。Inputs 会进入 Agent config、审批事件、工具结果和模型上下文，因此只允许非敏感上下文参数；凭据必须使用托管环境变量。
- 无 `inputs_schema` 的历史 version 保持 object-only 兼容，不改变既有回放行为。

校验固定发生在三个边界：版本发布或 Marketplace 安装前、Enable 写 Agent config 前、Runtime Resolver 渲染精确冻结版本前。错误只返回 instance path 与失败 keyword，不回显输入值。

Workbench 对根 `properties` 做确定性映射：`enum` 使用下拉框，`boolean` 使用复选框，`integer/number` 使用数字输入，string 使用文本输入，`x-tma-control: textarea` 使用多行输入，对象和数组使用 JSON 文本回退。客户端负责基础必填、范围和 JSON 解析提示，服务端始终是最终校验边界。

## 8. Runtime 集成

### 8.1 解析时机

建议在 `ResolveAgentRuntimeConfig(sessionID)` 之后、`ContextBuilder.Build(...)` 之前完成 skill 解析。

链路变成：

```text
Store.ResolveAgentRuntimeConfig
  -> SkillsResolver.Resolve(config.Skills)
  -> SkillsRenderer.Render(resolved)
  -> ContextBuilder.Build(... rendered skill context ...)
  -> LLM request
```

### 8.2 与 ContextBuilder 的关系

当前 `ContextBuilder` 直接把 `skills` 原始 JSON 注入上下文，这只是临时能力。

改造后建议：

- `ContextBuildRequest.Skills` 从“原始 agent 配置”改成“已渲染的 skill context JSON 或文本块”。
- `ContextBuilder` 不负责理解 skill 业务逻辑，只负责拼接。

也就是说：

- Skill 选择、校验、版本绑定在 `internal/skills`
- Context 拼接仍在 `internal/agentruntime`

### 8.3 Token 预算

Skill 是 system context，必须纳入预算控制。

建议规则：

1. 每个 skill 渲染前先估算 token。
2. 超预算时按 `priority` 和 `mode` 降级。
3. 仍超预算时丢弃低优先级 skill，而不是无声地挤掉过多历史。
4. runtime event 中记录哪些 skill 被降级或跳过。

推荐新增事件：

- `runtime.skills_resolving`
- `runtime.skills_resolved`
- `runtime.skills_truncated`
- `runtime.skills_failed`

## 9. API 规划

建议新增一组独立 API，而不是继续完全复用 agent update。

### 9.1 Registry API

```text
POST   /v1/skills
GET    /v1/skills
GET    /v1/skills/{skill_id}
POST   /v1/skills/{skill_id}/versions
GET    /v1/skills/{skill_id}/versions
GET    /v1/skills/{skill_id}/versions/{version}
POST   /v1/skills/{skill_id}/enable
POST   /v1/skills/{skill_id}/disable
POST   /v1/skills/{skill_id}/archive
```

`disable` 与 `archive` 必须分离：Disable 只发布移除目标 binding 的 Agent config version，保留 Skill 和所有不可变版本；Archive 阻止未来绑定和发布。只要任一未归档 Agent 的当前配置仍引用目标 Skill，Archive 必须返回 conflict，要求先完成 Disable 或显式配置迁移。

### 9.2 Agent 绑定 API

保留现有：

```text
PATCH /v1/agents/{agent_id}
POST  /v1/agents/{agent_id}/config-versions
```

但要求 `skills` 字段逐步收敛到统一 schema，例如：

```json
{
  "skills": {
    "enabled": [
      { "skill": "code-review", "version": 3 }
    ]
  }
}
```

### 9.3 预览 API

建议补一个很实用的接口：

```text
POST /v1/skills/resolve-preview
```

输入 agent skills config，输出：

- 实际命中的 skill 版本
- 渲染后的 context
- token 估算
- 降级信息

这对 UI、Inspector、调试 CLI 都很重要。

## 10. 与 Plugin 的关系

后续 plugin 应能声明：

- skills
- tools
- hooks
- assets

但 skills 模块本身不应依赖 plugin 才能工作。推荐关系：

```text
Plugin installer
  -> 写入 skills / skill_versions
  -> 写入 plugin ownership metadata
  -> Agent 选择时与原生 skills 无差别
```

这样有几个好处：

- plugin 卸载和 skill 归档可以治理得更细。
- UI 不需要区分“手工建 skill”还是“插件提供 skill”。
- Runtime 永远只消费统一的 skill registry。

## 11. 权限与治理

企业场景下，skills 不能只是 prompt 片段仓库，至少要考虑以下治理：

### 11.1 写权限

- 谁可以创建 workspace skills
- 谁可以发布新版本
- 谁可以归档技能

### 11.2 使用权限

- 某些高风险 skill 只能给指定 agent 或 workspace 使用
- 某些 plugin-supplied skill 需要审批后启用

### 11.3 审计

需要回答：

- 某轮回答用了哪些 skill？
- 使用的是哪个版本？
- 是否发生了降级或渲染失败？
- 这个版本是谁发布的？

## 12. 渐进式实施方案

### Phase 0：统一 schema，兼容现有 raw JSON

状态：已完成。新写入严格校验 canonical schema；历史 legacy 配置保留运行时兼容并产生明确事件。

目标：

- 定义 `AgentConfigVersion.skills` 的规范格式。
- 增加解析器，但仍允许 legacy raw JSON。
- ContextBuilder 仍可继续注入 skill 内容。

产出：

- `internal/skills/schema.go`
- legacy -> normalized config 转换
- 基础单测

### Phase 1：引入 skill registry

状态：已完成。已提供 Postgres registry、不可变版本与 ResolvePreview HTTP API。

目标：

- 新增 `skills`、`skill_versions` 表。
- 新增 CRUD API。
- Agent config 支持引用 skill/version。

产出：

- store 接口扩展
- Postgres migration
- HTTP API
- `ResolvePreview`

### Phase 2：运行时解析与审计

状态：已完成。runner 在进入 AgentRuntime 前解析精确版本，按预算降级渲染并记录 runtime event 与 turn usage。

目标：

- Runtime 不再直接注入原始 `skills` JSON。
- 改为 Resolver + Renderer。
- 记录 turn skill usage 与截断信息。

产出：

- runtime events
- usage audit
- Inspector 可查看本轮 skill 明细

### Phase 3：参数化与资产化

状态：已完成首版。标准 package/object refs、离线 Artifact ZIP、mode 降级、`inputs_schema` 三段校验与 Workbench typed controls 已落地；外部 Scanner 不属于本阶段开放范围。

目标：

- skill 支持参数 schema
- skill 支持 object refs / assets
- skill 支持 mode 降级

产出：

- 复杂业务 SOP skill
- 模板、示例文件、截图等资产接入对象存储

## 标准文件包存储

主流 Skill 以目录为能力边界，TMA 的 canonical layout 为：

```text
skills/<workspace>/<identifier>/versions/<version>/
├── SKILL.md
├── references/
├── examples/
├── scripts/
└── .tma/package.zip
```

`SKILL.md` 和文本文件以独立对象保存，便于检查和按路径读取；`.tma/package.zip` 是确定性完整导出。二进制资产继续复用安装阶段内置扫描通过后创建的 workspace object ref，ZIP 生成时按 checksum 读取并纳入 archive，不复制额外 binary object。

PostgreSQL 保存不可变版本 metadata、package checksum、archive/`SKILL.md` object ref 和 `skill_version_package_files` 文件索引。兼容迁移分两阶段：

1. 新版本写 `tma.skill-package.v1`，读取优先 package 并回退旧 DB 正文。
2. 使用受控 backfill 原位物化 legacy version；所有版本迁移并稳定运行后，才评估停止保存 `content_text/assets_json` 正文副本。

对象上传发生在 version 事务提交前，数据库事务负责原子创建引用与文件索引；提交失败会删除本次新对象。Skill 发布和 retention GC 共享 workspace advisory lock，避免 package 建立期间被并发回收。

## 离线 ZIP 导入

局域网部署不依赖 GitHub Marketplace。用户可在 Workbench 或 TMA 对话中先把标准 ZIP 上传为 Session Artifact，再调用统一的 Preview/Install service：

```json
{
  "source": {
    "provider": "artifact",
    "artifact_id": "art_000001"
  }
}
```

信任边界固定为 Session：服务端用 `GetSessionArtifact(session_id, artifact_id)` 解析对象，复核 workspace、Artifact 类型、`.zip` 扩展、object ref size 和 SHA-256。接口不接受主机路径、任意 URL、bucket/key，也不能读取其他 Session 的 Artifact。

ZIP parser 不落盘、不执行文件，只允许根目录 `SKILL.md` 或单层包装目录，并要求恰好一个 `SKILL.md`。压缩包上限 8 MiB；资产最多 32 个、4 层路径、总计 4 MiB；文本单文件 100000 bytes，二进制单文件 512 KiB。路径穿越、反斜线、绝对路径、NUL、symlink、重复规范路径和未知扩展全部拒绝。

解析后转换为与 GitHub 相同的 `skillmarketplace.Package`，继续走 license、attestation、静态扫描、builtin binary scan、SBOM、Policy pin、写审批和不可变发布。GitHub repository/ref check 对 Artifact 为非强制项；版本 revision 使用 ZIP SHA-256。Skill provenance 保存 `artifact / session-artifact / SKILL.md`，version `source_ref` 保存 Artifact ID。Artifact-sourced Skill 只能从同类新 Artifact 升级，不能被 inline 内容覆盖。

### Phase 3.5：内部市场管理

内部 Marketplace Catalog 与已安装 Registry 分离：Registry 保存能力包和不可变版本，Catalog 只保存面向用户的发布状态及展示元数据，并通过 `skill_id + skill_version` 引用精确版本。Catalog 不复制正文、ZIP、assets 或 object refs。

条目生命周期只有四态：

```text
draft -> pending_review -> published -> withdrawn
草稿      待审核             已发布        已下架
```

- 草稿可编辑摘要、分类和标签，也可选择一个已安装 Skill 的精确 version。
- 提交审核后条目冻结，不允许修改版本或展示元数据。
- 待审核只能发布，不允许跳级、回退或引入隐含“拒绝/弃用”状态。
- 同一 workspace 的同一 Skill 同时最多一个已发布 version；旧版下架后可发布新版。
- 已下架是终态，只影响市场展示，不归档 Skill、不删除历史版本和 package objects。
- 创建、编辑、提交、发布和下架均进入 operator audit；Store 与 API 同时校验 workspace 边界。

Workbench 将“Marketplace”保留为发现、Preview 和安装入口，另设“市场管理”视图承载发布方生命周期，避免安装操作与审核治理混在同一页面。

发布后消费边界：

- Marketplace 默认来源为同 Organization 内部 Catalog；GitHub 与离线 Artifact 仍作为显式来源保留。
- consumer 通过 Session 固定 workspace，只能看到同组织 `published` 且源 Skill 仍 active 的精确版本；跨 Organization、草稿、待审核和已下架条目不可见。
- Catalog Preview 从发布方 version 的标准 ZIP object ref 读取，复核 workspace、size、object SHA-256、version checksum 和 package format，再进入统一 license、attestation、静态扫描、builtin binary scan、SBOM 与 Policy pin。
- Catalog Install 在 consumer workspace 创建独立 registry Skill，provenance 固定为 `catalog / publisher skill_id / SKILL.md`，version `source_ref` 固定 entry ID；发布方 package 和 consumer package 均保持不可变。
- Catalog Browse 会按 consumer registry 返回 `new_install / upgrade / unchanged / blocked` 和当前本地 version/source ref；该状态只用于列表提示，Preview 仍是升级前的最终安全与差异判定。
- Workbench 对 `upgrade` 显示本地 vN、目标 vN+1、主文件和 asset diff；第一次点击只进入确认态，明确旧版继续保留，第二次确认才发送 `upgrade_existing=true`。
- Upgrade 只在 consumer Skill 下追加新不可变 version，不自动修改 Agent binding 或当前 Session；历史 version 继续支持 package 下载、回放和精确重新启用。
- 发布中的源 Skill 禁止归档；下架后禁止新的 Discover/Preview/Install，但不会删除 consumer 已安装版本。
- PostgreSQL RLS 只额外开放同组织 published-only SELECT，所有 INSERT/UPDATE/DELETE 继续严格限制在当前 workspace。受控可见性函数只返回布尔值，用于避免 RLS 策略递归。
- `skills.discover` 默认 `provider=catalog`，不会联网；只有显式 `provider=github` 或兼容旧调用的精确 `repository` 才进入 GitHub Client。

角色边界固定为：operator 创建/编辑/提交，admin 发布/下架，viewer/member 浏览，member Preview/Install；Install 仍需要独立 write approval。

### Phase 4：Plugin 汇聚

目标：

- plugin 能安装和贡献 skills
- plugin supplied skills 纳入同一 registry 和审计体系

产出：

- plugin ownership metadata
- 安装 / 卸载 / 归档流程

## 13. 推荐优先级

结合当前仓库状态，建议优先级如下：

1. 先定义 `skills` 配置 schema。
2. 再做 `internal/skills` 的 resolver / renderer。
3. 再做 skill registry 与 migration。
4. 最后做 plugin 汇聚和 UI。

原因很实际：

- 当前已有 `skills_json` 和 ContextBuilder 通路。
- 最先缺的是规范化与可预测性，不是 marketplace。
- 如果 schema 不先定，后面的 registry、plugin、audit 都会反复返工。

## 14. 推荐最小落地范围

如果只做一个可交付的第一版，建议范围控制为：

1. `AgentConfigVersion.skills` 收敛到统一 schema。
2. 新增 `internal/skills` 的 normalize / resolve / render。
3. `ContextBuilder` 接收已渲染 skill context。
4. runtime 记录本轮解析到的 skill 列表与 token 成本。

这样就能先解决四个核心问题：

- skill 配置不再散乱
- skill 使用可复现
- 上下文注入更稳定
- 后续 registry/plugin 有明确承接点

## 15. 总结

TMA 的 `skills` 模块不应该被做成“另一套工具系统”，也不应该继续停留在“Agent 配置里塞一段任意 JSON”。

更合适的方向是：

- 用 `skills` 表达可复用的上下文能力包；
- 用 `AgentConfigVersion` 绑定精确 skill 版本；
- 用 `Resolver + Renderer` 把 skill 变成稳定上下文；
- 用 runtime audit 保证可观测、可回放、可治理；
- 再在此基础上承接 plugin、memory 和自动进化。

这条路径和仓库当前的 `tools / capability / plugin / context builder` 分层是顺的，工程风险也最低。
