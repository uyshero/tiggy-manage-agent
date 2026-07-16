# TMA Extension 设置页与配置贡献标准

本文档定义 Provider、Tool、Plugin、MCP 和其他 Extension 如何向 TMA App 设置页贡献配置、状态、实例、能力、权限和诊断入口。

上位标准见 [TMA Extension 与 Provider 治理标准](./extension-governance-standard.md)。

## 1. 设计目标

1. 不同 Provider 可以展示适合自己的设置项。
2. App 保持统一的信息架构、权限、审批和审计。
3. 插件声明“配置什么”，App 决定“如何安全展示”。
4. Provider 下线时 Definition 和配置仍然可见。
5. 用户可以区分安装、配置、在线、能力可用和 Agent 授权。
6. 第一版不允许插件向主 App 注入任意 React 或 JavaScript。

## 2. 核心对象

设置页必须区分：

| 对象 | 示例 |
|---|---|
| Extension Definition | Git Worktree 扩展 |
| Provider Instance | Viito Mac 上的 Local Provider |
| Extension Config | Checkout 根目录、Hooks 策略 |
| Capability | `code_checkout.prepare` |
| Agent Permission | 编程 Agent 允许创建 Worktree |
| Effective Availability | 当前 Session 是否真正可用 |

Provider 配置决定“平台能否提供能力”；Agent Permission 决定“这个 Agent 是否允许使用”。二者禁止混为一个开关。

## 3. 可用性链路

设置页必须展示完整链路：

```text
已安装
-> 已配置
-> Provider Instance 在线
-> Capability 可用
-> Agent 已授权
-> Session Environment 匹配
```

不可用时必须指出失败环节和操作建议，而不是只显示“不可用”。

示例：

```text
Git Worktree 已安装
配置有效
Viito Mac 已离线
受影响能力：创建代码副本、查看 Diff、执行 Git 命令
可执行操作：重新检测、启动本地 Worker、选择其他环境
```

## 4. 设置页信息架构

建议固定一级导航：

```text
设置
├── 连接
│   ├── 模型 Provider
│   ├── MCP
│   └── 外部服务
├── 执行环境
│   ├── 本机 Worker
│   ├── Onlyboxes
│   └── Remote Provider
├── 工具与扩展
│   ├── Git Worktree
│   ├── Browser
│   ├── Computer Use
│   └── Office / Robot
├── Agent 权限
├── Secrets
└── 诊断
```

插件不得默认增加一级侧边栏入口。插件必须进入平台定义的分组，避免扩展数量增加后设置导航失控。

## 5. 扩展详情页

扩展详情页使用固定标签：

```text
概览
实例
配置
能力
权限
诊断
```

| 标签 | 内容 |
|---|---|
| 概览 | 版本、来源、信任状态、总体状态 |
| 实例 | Worker、Sandbox、Remote Instance |
| 配置 | Schema 驱动配置表单 |
| 能力 | API、Capability、Runtime、风险 |
| 权限 | Agent/User 的使用权限和策略来源 |
| 诊断 | Doctor、连接测试、错误和受控 Action |

扩展可以省略没有内容的标签，但不能重定义标签语义。

## 6. Settings Contribution

Extension Manifest 可以包含设置贡献：

```json
{
  "settings_contributions": [
    {
      "id": "git-worktree-config",
      "group": "execution",
      "title": "Git Worktree",
      "description": "隔离开发目录与 Git 行为",
      "renderer": "schema_form",
      "scope": "worker_instance",
      "authority": "worker",
      "required_role": "member",
      "schema_version": 1,
      "schema": {},
      "ui_schema": {}
    }
  ]
}
```

| 字段 | 含义 |
|---|---|
| `id` | Contribution 唯一 ID |
| `group` | 平台定义的导航分组 |
| `renderer` | 标准渲染器 |
| `scope` | 配置作用域 |
| `authority` | server、worker、external |
| `required_role` | 最低角色 |
| `schema_version` | 配置 Schema 版本 |
| `schema` | 数据约束 |
| `ui_schema` | 排序和控件提示，不包含脚本 |

## 7. 标准 Renderer

第一版只允许：

```text
schema_form
status_summary
instance_list
capability_list
permission_matrix
diagnostic_actions
```

未知 Renderer 必须显示 `incompatible_settings_renderer`，不得降级执行插件脚本。

## 8. 标准控件

| Schema / Format | 控件 |
|---|---|
| `boolean` | Toggle |
| `string` | Text Input |
| `enum` | Select |
| `integer` / `number` | Number Input |
| `secret-ref` | Secret Selector |
| `local-directory` | 本机目录选择器 |
| `resource-ref` | Provider/Worker 选择器 |
| `capability-list` | 能力列表 |
| `duration` | 时长输入 |
| `bytes` | 容量输入 |

插件不得通过 `ui_schema` 注入 HTML、CSS、JavaScript、远程 URL 或事件处理器。

## 9. 配置作用域

支持作用域：

```text
server
organization
workspace
user
worker_instance
agent
session
```

| Scope | 示例 |
|---|---|
| server | 默认 Sandbox 镜像 |
| organization | 合规和许可证策略 |
| workspace | 默认 Search Provider |
| user | 用户交互偏好 |
| worker_instance | 本机 Checkout 根目录 |
| agent | Tool Permission |
| session | 临时 Runtime 覆盖 |

## 10. 配置继承与强制策略

值解析默认顺序：

```text
Session
-> Agent
-> User / Worker Instance
-> Workspace Default
-> Organization Default
-> Server Default
```

Organization 或 Workspace 可以发布强制策略。强制策略先限制允许范围和锁定字段，再执行值解析。

设置接口必须返回来源：

```json
{
  "value": "disabled",
  "source_scope": "workspace",
  "source_id": "wksp_default",
  "editable": false,
  "reason": "enforced by workspace policy"
}
```

UI 必须展示来源和不可编辑原因。

## 11. 配置 Authority

### 11.1 Server-owned

Server 持有并验证配置。Provider Instance 离线时仍可编辑，例如 LLM Base URL、Workspace Search Policy、Onlyboxes 默认镜像策略。

### 11.2 Worker-owned

配置只对某台 Worker 有意义，必须由该 Worker 验证，例如：

- 本地 Checkout 路径
- 浏览器可执行文件路径
- 本机应用授权
- 设备端口

Worker 下线时：

- 可以查看最后配置和最后验证时间。
- 配置变为只读或未验证状态。
- 不得静默排队等待应用。
- 不得由 Server 假装验证本机路径。

### 11.3 External-owned

外部系统持有配置，TMA 只保存连接和引用，例如 SaaS OAuth Connection。

## 12. Secret 规则

Secret 明文不得进入 Extension Descriptor、Tool Manifest、普通 Config JSON、审计 Details 或前端状态。

配置只保存引用：

```json
{
  "api_key": {
    "secret_ref": "secret_openai_prod"
  }
}
```

Secret Selector 必须按 Principal 和 Scope 过滤；前端不得读取 Secret 明文。

## 13. 配置保存流程

标准流程：

```text
编辑 Draft
-> 前端基础 Schema 校验
-> Server 权限和完整 Schema 校验
-> Provider Validate / Preview
-> 展示影响
-> 用户确认
-> 发布 Config Revision
-> Provider Apply
-> Health Check
-> 刷新 Extension Catalog Revision
-> 通知受影响 Session
```

写入 API 必须使用乐观锁 Revision，防止两个管理员相互覆盖。

高风险修改必须展示影响并二次确认，例如：

- 更换 Sandbox 镜像
- 开启网络
- 允许 Git Hooks
- 修改 Worker 可写目录
- 删除 Provider Config
- 变更 Object Store

## 14. 配置版本

建议持久化：

```text
extension_definitions
extension_configs
extension_config_versions
```

版本记录至少包含：

```text
extension_id
scope_type
scope_id
instance_id
schema_version
config_json
revision
created_by
created_at
```

必须支持配置 Diff、修改人和时间、发布结果、Rollback 和 Operator Audit。

## 15. Schema 版本升级

- Config 必须记录 `schema_version`。
- 新版本增加可选字段可以保持兼容。
- 删除、重命名、改变类型必须提供迁移。
- App 不支持当前 Schema/Renderer 时必须只读展示并提示升级。
- 插件升级不得静默丢弃未知配置字段。

## 16. Diagnostic Action

插件可以声明受控 Action：

```json
{
  "actions": [
    {
      "id": "doctor",
      "title": "运行检查",
      "risk": "read",
      "required_role": "member"
    },
    {
      "id": "prune",
      "title": "清理无效 Worktree",
      "risk": "write",
      "required_role": "operator",
      "human_intervention": "always"
    }
  ]
}
```

统一接口：

```text
POST /v1/extensions/{extension_id}/instances/{instance_id}/actions/{action_id}
```

平台负责权限、Schema 校验、审批、Work 调度、超时、取消、结果、Artifact 和审计。插件不得自行绕过审批。

## 17. Provider 离线时的 UI

Provider 下线后：

- Definition 和设置入口继续显示。
- Instance 显示 `unavailable` 和最后在线时间。
- Worker-owned 设置只读。
- 受影响 Capability 明确列出。
- 显示允许的操作，不显示不可执行按钮。
- 不自动切换 Provider。

提示示例：

```text
本地开发能力不可用

设备：Viito Mac
最后在线：2 分钟前
受影响：文件修改、命令执行、Git Worktree

[重新检测] [启动本地 Worker] [选择其他环境]
```

选择其他环境进入 `provider_failover` 人工决策流程。

## 18. Provider 类型示例

| Provider | 典型设置 |
|---|---|
| Local System | 设备、授权目录、命令审批、环境变量 |
| Git Worktree | Checkout Root、Hooks、清理策略、分支规则 |
| Onlyboxes | 镜像、CPU、内存、网络、TTL、挂载 |
| Remote Provider | Endpoint、区域、凭据、项目映射 |
| Browser | 浏览器类型、Profile、下载目录、Takeover |
| LLM | Base URL、Secret、模型目录、超时 |
| MCP | Transport、Command/URL、Secret、工具发现 |
| Search | Provider、地区、Safe Search |
| Object Store | Endpoint、Bucket、Region、Secret |
| Robot / Office | 设备实例、操作范围、安全边界 |

## 19. API 草案

```text
GET  /v1/extensions
GET  /v1/extensions/{extension_id}
GET  /v1/extensions/{extension_id}/instances
GET  /v1/extensions/{extension_id}/settings-schema
GET  /v1/extensions/{extension_id}/effective-config
POST /v1/extensions/{extension_id}/validate-config
POST /v1/extensions/{extension_id}/config-versions
POST /v1/extensions/{extension_id}/config-versions/{version}/rollback
POST /v1/extensions/{extension_id}/instances/{instance_id}/actions/{action_id}
GET  /v1/extensions/{extension_id}/audit
```

状态响应必须包含：

```text
status
reason_code
reason_message
last_checked_at
available_actions
catalog_revision
config_revision
```

## 20. 前端代码边界

建议将当前设置页从单体 `App.jsx` 拆出：

```text
apps/workbench/src/settings/
├── SettingsShell.jsx
├── settingsRegistry.js
├── ExtensionDirectory.jsx
├── ExtensionDetail.jsx
├── SchemaSettingsForm.jsx
├── ProviderInstances.jsx
├── CapabilityStatus.jsx
├── PermissionMatrix.jsx
└── DiagnosticActions.jsx
```

Settings Registry 合并 Core Settings Contributions 和 Extension Manifest Contributions。Core 页面可以继续使用受信任 React Component；Extension 默认只能使用标准 Renderer。

## 21. 高级自定义 UI

第一版禁止第三方 UI 代码进入主 App。

未来确有复杂需求时，可以支持签名、沙箱化 iframe：

- 独立 Origin
- 严格 CSP
- 不持有主 App Token
- 不直接访问 DOM、Cookie 或 LocalStorage
- 只能通过受控 Message Bridge 调用允许的 API
- Manifest 声明所需权限
- 安装和升级需要签名验证

在完成签名、权限和沙箱前不得实现该能力。

## 22. 实施顺序

1. 从 `App.jsx` 拆出 Settings Shell 和 Core Registry。
2. 增加 Extensions 一级入口和 Extension Directory。
3. 实现 Definition / Instance / Capability 状态页。
4. 实现 Schema Form 和标准控件。
5. 实现 Scope、Effective Config 和来源展示。
6. 实现 Validate、Preview、Revision 和 Rollback。
7. 实现 Diagnostic Action。
8. 接入 Worker 下线状态和人工 Provider 切换。
9. 用 LLM Provider、Local Worker、Git Worktree 验证三种 Authority。

## 23. 新设置贡献 Checklist

- [ ] Contribution 属于哪个平台分组？
- [ ] 使用哪个标准 Renderer？
- [ ] 配置 Scope 是什么？
- [ ] Authority 是 Server、Worker 还是 External？
- [ ] 最低角色是什么？
- [ ] 是否引用 Secret？
- [ ] 是否提供完整 JSON Schema？
- [ ] 是否需要 Provider 侧 Validate / Preview？
- [ ] 变更影响哪些现有 Session？
- [ ] 是否需要二次确认？
- [ ] Provider 离线时页面如何展示？
- [ ] 是否支持配置版本和 Rollback？
- [ ] 是否提供诊断 Action？
- [ ] 未知 Renderer 或新 Schema 版本如何降级？
- [ ] 是否有权限、离线、冲突和回滚测试？

