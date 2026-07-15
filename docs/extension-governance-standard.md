# TMA Extension 与 Provider 治理标准

本文档定义 TMA 中 Extension、Plugin、Provider、Tool、Capability、Runtime 和 Worker Instance 的统一治理规则。新增工具、执行环境、设备、外部服务或生命周期能力前，应先按本文档完成分类、声明、发现、兼容性判断、路由和下线行为设计。

配套文档：

- [Extension 设置页与配置贡献标准](./extension-settings-standard.md)
- [Tool / Runtime Standard](./tool-runtime-standard.md)
- [Capability Provider Design](./capability-provider.md)
- [Tool Plugin SDK](./tool-plugin-sdk.md)
- [Workbench 插件开发标准](./workbench-plugin-standard.md)

本文档主要治理 Server、Worker、Provider、Tool 和外部服务扩展。面向企业业务页面、导航、Widget、Command 和详情面板的前端扩展遵循 Workbench 插件标准；两者可以属于同一个交付包，但运行边界、权限和生命周期必须分别声明。

## 1. 目标与非目标

本标准必须让平台能够回答：

1. 当前有哪些 Extension、Provider 和运行实例。
2. 某项能力为什么可用或不可用。
3. 多个 Provider 或 Worker 同时满足条件时如何选择。
4. Worker 下线后如何撤销能力、终止调用并提示用户。
5. 某个资源或调用当前由哪个实例执行，以及为什么。

本标准不要求所有扩展实现同一个巨型 Go 接口，也不替代各领域已有接口。LLM、Tool、Object Store、Capability Provider 继续使用各自接口；Extension Catalog 只统一元数据、发现、状态、选择决策和治理。

## 2. 规范术语

本文中的“必须”“禁止”表示实现必须遵守；“应该”表示默认应遵守，偏离时必须记录原因；“可以”表示可选行为。

### 2.1 Extension

所有可扩展模块的总称。来源可以是 Server 内置、部署配置、Worker、进程插件、MCP Server 或商业模块。

### 2.2 Plugin

可安装或加载的软件包。一个 Plugin 可以贡献 Tool、Skill、Provider、Hook、Asset、设置页声明和诊断 Action。

### 2.3 Provider

某类能力的具体后端实现，例如 `LocalSystemProvider`、`OnlyboxesProvider`、`OpenAICompatibleProvider`、`LocalGitWorktreeProvider`。

### 2.4 Capability

用于权限判断和调度匹配的原子能力，例如：

```text
filesystem.read
filesystem.write
code.execute
code_checkout.prepare
browser.capture
robot.motion.write
```

Capability 不是 Tool API，也不携带协议版本。

### 2.5 Tool API

可调用操作，使用 `namespace.api` 唯一标识，例如 `default.run_command`、`browser.capture`、`robot.move_to`。

### 2.6 Runtime

能力执行位置，而不是业务命名空间：

```text
server_builtin
local_system
cloud_sandbox
remote
```

不得创建 `local_robot`、`cloud_browser` 这类把执行位置写入 namespace 的命名。

### 2.7 Provider Instance

Provider 的具体运行实例。`LocalSystemProvider` 是实现类型，`worker-viito-mac/local-system` 是实例。需要实例级路由时必须保存 `provider_instance_id`，不能只保存类型。

### 2.8 Placement

某个 Session、资源或调用当前使用的 Provider Instance。Placement 是可审计的选择结果，不代表永久占用或永久可用。

### 2.9 Definition 与 Instance

Definition 描述扩展是什么；Instance 描述它当前在哪里运行：

```text
Definition: Git Worktree Extension
Instance: worker-viito-mac/git-worktree
Instance: worker-windows-dev/git-worktree
```

Instance 下线时，Definition 和配置不得从 Catalog 或设置页消失。

## 3. 扩展分类

| Kind | 示例 | 主要选择依据 |
|---|---|---|
| `llm_provider` | OpenAI-compatible | AgentConfig / Model |
| `execution_provider` | LocalSystem、Onlyboxes | Environment / Runtime |
| `tool` | Browser、Web、Robot | Namespace / API / Capability |
| `worker_plugin` | Computer、Office | Manifest / Online Worker |
| `mcp` | stdio、HTTP MCP | AgentConfig |
| `object_store` | LocalFS、S3 | Workspace / Deployment |
| `search_provider` | SearXNG、Tavily | Workspace Policy |
| `observability_exporter` | OTel、Perfetto | Export Policy |
| `lifecycle_provider` | CodeCheckout | Environment / Provider Instance |
| `policy_provider` | Marketplace、Retention | Organization / Workspace Policy |

新增扩展必须选择一个主 Kind。一个 Plugin 可以贡献多个不同 Kind 的 Definition。

## 4. Extension Descriptor

所有扩展必须能够投影为统一 Descriptor：

```json
{
  "protocol_version": "tma.extension.manifest.v1",
  "id": "git-worktree",
  "kind": "lifecycle_provider",
  "version": "1.0.0",
  "source": "worker_plugin",
  "meta": {
    "title": "Git Worktree",
    "description": "为开发任务创建隔离代码目录"
  },
  "protocols": ["tma.checkout.v1"],
  "apis": ["code_checkout.prepare", "code_checkout.diff"],
  "capabilities": ["code_checkout.prepare", "code_checkout.diff"],
  "runtimes": ["local_system"],
  "platforms": ["darwin", "linux", "windows"],
  "scope": "worker_instance",
  "risk": "write"
}
```

| 字段 | 含义 |
|---|---|
| `id` | 稳定唯一标识 |
| `kind` | 扩展分类 |
| `version` | 扩展实现版本 |
| `source` | builtin、worker、plugin、mcp、config、enterprise |
| `protocols` | 支持的调用协议 |
| `capabilities` | 原子能力 |
| `runtimes` | 执行位置 |
| `scope` | server、organization、workspace、user、worker_instance、agent、session |
| `risk` | read、write、exec、external_effect |

Descriptor 禁止包含 Secret 明文。

## 5. 命名和版本

### 5.1 Capability 命名

Capability 使用小写点分格式：

```text
<domain>.<resource-or-action>[.<risk-or-operation>]
```

协议版本不得写入 Capability 名称：

```text
capability = code_checkout.prepare
protocol = tma.checkout.v1
```

### 5.2 Tool API 命名

- Tool API 使用 `namespace.api`。
- API 使用动词或动词短语。
- builtin namespace 默认不可被插件覆盖。
- 不同实例声明相同完整 API 时必须进行 Schema Hash 校验。

### 5.3 协议兼容

- major 版本不同视为不兼容。
- minor 增量只能增加可选字段或能力。
- 接收方应该忽略未知可选字段。
- 删除字段、改变字段语义或收紧已发布 Schema 必须升级 major。

## 6. Extension Catalog

平台必须提供统一 Catalog，聚合：

```text
Server Builtin
Deployment Configuration
Worker Register / Heartbeat
Process Plugin Manifest
MCP Configuration
LLM Provider Configuration
Enterprise Extension
```

Catalog 必须区分 Definition 与 Instance，并维护实例状态：

| 状态 | 含义 |
|---|---|
| `available` | 在线且可接受新调用 |
| `degraded` | 部分功能或部分实例不可用 |
| `draining` | 不接受新调用，已有调用可收敛 |
| `unavailable` | 不可用于调用 |
| `disabled` | 被配置或策略关闭 |
| `incompatible` | 协议、平台或 Schema 不兼容 |
| `misconfigured` | 缺少凭据或必要配置 |

Catalog 查询必须按 Principal、Organization、Workspace、User 和 Environment 过滤。其他 Workspace 的 Worker 不得成为候选。

## 7. Worker 发现

Worker register 和 heartbeat 应声明：

```json
{
  "instance_id": "worker-viito-mac",
  "status": "online",
  "runtime": "local_system",
  "platform": "darwin",
  "protocols": ["tma.work.v1", "tma.checkout.v1"],
  "namespaces": ["default", "code_checkout"],
  "apis": ["default.run_command", "code_checkout.prepare"],
  "capabilities": ["exec", "filesystem.write", "code_checkout.prepare"],
  "schema_hashes": {
    "code_checkout.prepare": "sha256:..."
  },
  "heartbeat_at": "2026-07-13T12:00:00Z"
}
```

Worker 状态建议经历：

```text
available -> suspected -> unavailable
available -> draining -> unavailable
```

`suspected` 是短暂防抖状态，不接受新调用。连续超时后进入 `unavailable`。

## 8. Effective Capability

平台必须区分：

```text
Extension 已安装
配置有效
Provider Instance 在线
Capability 可用
Agent 已授权
Session Environment 匹配
```

只有全部满足时，Capability 才对当前 Session 有效。

AgentConfig 应区分：

```json
{
  "required_capabilities": ["filesystem.read", "filesystem.write"],
  "optional_capabilities": ["browser.capture"]
}
```

- Required 缺失：阻止创建新 Turn，并返回明确原因。
- Optional 缺失：从本轮 Tool Registry 移除，Turn 可以继续。

每个 Turn 开始时必须重新计算 Effective Capability；Tool 调用执行前必须再次校验。

## 9. 候选资格与选择

### 9.1 硬性过滤

候选必须同时满足：

```text
Principal / Workspace / User 授权
Environment 和 Runtime 匹配
协议 major 版本兼容
Tool Schema Hash 兼容
Capability 完整
平台和架构兼容
数据或资源可访问
安全和合规策略允许
实例在线且可接受任务
```

不满足硬性条件的实例不得展示给用户，也不得因用户优先级提高而绕过。

### 9.2 软性排序

合格候选可以按以下因素排序：

```text
用户明确选择
当前 Placement 亲和性
用户服务等级和任务优先级
交互式或批处理类型
当前负载和排队时间
区域和数据位置偏好
成本
缓存和环境预热状态
历史健康度和成功率
```

用户优先级只能影响合格候选顺序，不能扩大授权范围。

### 9.3 稳定选择

Provider 执行采用统一规则：当前实例仍然合格且在线时继续使用；实例不可用时停止当前调用并进入人工决策。不得按 map 遍历、注册顺序或“第一个匹配”选择实现。

初次有多个等价候选且用户没有默认实例时，平台应要求用户选择或使用 Workspace 明确配置的默认实例。无法唯一决策时返回 `ambiguous_provider`。

## 10. Worker 下线标准

Worker 下线时必须：

```text
1. 将 Worker Instance 标记为 unavailable。
2. 撤销由该实例提供的 Effective Capability。
3. 从新 Turn 的 Tool Registry 移除相关工具。
4. 立即终止正在等待该 Worker 的 Tool Call 或 Work Lease。
5. 返回结构化 capability_unavailable 错误。
6. 查找合格替代实例。
7. 要求用户选择切换或停止。
```

平台不得等待 Worker 自动恢复，不得为写操作自动切换或自动重试。

```json
{
  "type": "capability_unavailable",
  "api": "code_checkout.prepare",
  "reason": "provider_offline",
  "instance_id": "worker-viito-mac",
  "last_seen_at": "2026-07-13T12:00:00Z",
  "retryable": false,
  "action_required": "select_provider_or_stop"
}
```

## 11. 多个 Worker 提供同一工具

相同 API 不代表执行环境等价。只有满足以下条件才可列为替代候选：

```text
同一授权范围
相同 API
兼容协议
相同 Schema Hash
满足全部 Capability
安全策略允许
当前在线
具备所需数据，或能明确重建所需状态
```

一个实例下线但仍有其他实例时：

- Catalog 层的能力可以显示 `degraded`。
- 当前 Tool Call 必须先停止。
- 平台展示合格候选及切换影响。
- 用户明确选择候选或停止任务。
- 平台不得静默切换。

## 12. 人工 Provider 切换

复用 Session Intervention，增加 `provider_failover`：

```json
{
  "type": "provider_failover",
  "tool_call_id": "call_123",
  "failed_instance": "worker-viito-mac",
  "reason": "worker_offline",
  "candidates": [
    {
      "instance_id": "sandbox-cn-01",
      "runtime": "cloud_sandbox",
      "state_available": false,
      "recreation_required": true,
      "warnings": ["本地未提交修改不会自动带入"]
    }
  ]
}
```

用户操作：

```text
切换并重试当前调用
切换本 Session 后续调用
停止任务
```

用户确认时必须再次验证候选仍然在线且合格。

## 13. Placement Epoch 与防脑裂

每次切换必须增加 `placement_epoch`：

```text
worker-A: epoch=3
切换到 worker-B: epoch=4
worker-A 恢复并提交 epoch=3: Server 拒绝
worker-B 提交 epoch=4: Server 接受
```

所有写调用还必须携带稳定 `tool_call_id` 和 attempt，避免重复副作用。

## 14. 组合和冲突规则

| 类型 | 组合方式 |
|---|---|
| Tool / Skill Directory | 聚合，但完整名称不能冲突 |
| Observability Exporter | Fan-out，失败进入独立重试 |
| Object Store | 每个作用域一个权威实现 |
| LLM / Search | 明确配置的 Provider 和有序候选 |
| Execution / Lifecycle Provider | 当前实例可用时保持；切换必须人工确认 |

冲突处理：

1. 相同 Tool API 且 Schema Hash 不同：`incompatible`。
2. 相同 Extension ID、来源不同且无显式覆盖策略：`ambiguous_extension`。
3. builtin namespace 被插件占用：拒绝注册。
4. 实例声明不支持的平台能力：拒绝成为候选。
5. 任何冲突都必须可查询，不能静默覆盖。

## 15. API、事件与审计

API 草案：

```text
GET  /v1/extensions
GET  /v1/extensions/{extension_id}
GET  /v1/extensions/{extension_id}/instances
GET  /v1/capabilities/effective
GET  /v1/workers/{worker_id}/capabilities
GET  /v1/sessions/{session_id}/tooling-health
POST /v1/sessions/{session_id}/tooling-preflight
```

状态响应应包含：

```text
status
reason_code
reason_message
last_checked_at
available_actions
catalog_revision
```

建议事件：

```text
worker.available
worker.suspected
worker.draining
worker.unavailable
capability.available
capability.degraded
capability.unavailable
session.required_capability_lost
session.tool_registry_changed
provider.failover_requested
provider.failover_approved
provider.failover_rejected
```

启停扩展、修改 Provider 配置、人工切换、强制停止和高风险诊断必须写入 Operator Audit。

## 16. 建议代码边界

```text
internal/extensions/
├── descriptor.go
├── catalog.go
├── compatibility.go
├── status.go
├── decision.go
└── binding.go
```

现有模块通过 Adapter 注册到 Catalog，不替换原业务接口：

```text
tools.Registry          -> Tool Extension Adapter
Worker heartbeat        -> Worker Instance Adapter
SessionProviderResolver -> Execution Provider Adapter
llm.Manager             -> LLM Provider Adapter
MCP config              -> MCP Extension Adapter
ObjectStore config      -> Resource Provider Adapter
```

现有 `ProviderResolver` 后续应返回包含候选、选择原因、实例和 Catalog Revision 的结构化 Decision，而不是只返回一个 Provider 对象。

## 17. 实施顺序

1. 恢复当前测试和稳定构建基线。
2. 建立只读 Extension Catalog 和 Descriptor。
3. 扩展 Worker heartbeat，加入协议、实例和 Schema Hash。
4. 增加 Effective Capability 与 Session Tooling Preflight。
5. 增加 Worker 下线事件和 Tool Registry 动态撤销。
6. 增加 `provider_failover` Intervention。
7. 增加 Placement Epoch 和旧结果拒绝。
8. 接入设置页贡献标准。
9. 最后实现 CodeCheckout / Git Worktree 扩展。

## 18. 新扩展验收 Checklist

- [ ] Extension Kind 是什么？
- [ ] Definition ID、Instance ID 如何生成？
- [ ] 提供哪些 API 和 Capability？
- [ ] 使用哪些协议版本？
- [ ] 运行在哪些 Runtime 和平台？
- [ ] 配置和 Secret 属于哪个作用域？
- [ ] 如何发现实例并判断健康？
- [ ] 多个实例如何判定 Schema 兼容？
- [ ] Worker 下线时哪些工具应撤销？
- [ ] 当前调用如何停止？
- [ ] 是否存在合格替代实例？
- [ ] 用户切换前展示哪些影响？
- [ ] 如何防止旧实例回传和重复副作用？
- [ ] 哪些操作需要审批和审计？
- [ ] 设置页使用哪些标准 Renderer？
- [ ] 是否有单元、集成、下线和冲突测试？
