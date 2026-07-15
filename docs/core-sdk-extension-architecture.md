# TMA Server、Core SDK 与应用扩展边界

本文档固定 TMA 后续功能开发的分层规则。目标不是要求每个功能重复实现三遍，而是明确权威状态、编程接口和用户体验分别由谁负责。

## 1. 分层

```text
App / CLI / enterprise service
              |
        Core SDK / web client
              |
          TMA Server API
              |
 Server runtime / worker / provider / sidecar
```

- **Server Core** 是状态、权限、调度、执行、审批、审计和租户隔离的事实源。
- **Core SDK** 是 Server 能力的稳定编程入口。它封装 HTTP、SSE、认证、错误和 Agent Run 状态机，但不复制服务端业务规则。
- **Capability Extension** 在 Server、Worker、MCP Server 或 Sidecar 中增加工具、Provider、数据和执行能力。
- **App Extension** 增加页面、导航、视图、表单和交互编排，不保存服务端 Secret，不直接访问数据库，也不绕过审批执行外部副作用。
- **Solution Package** 把精确版本的 Capability Extension、领域 SDK 和 App Extension 组合交付；各组件仍有独立权限、版本和生命周期。

依赖必须保持单向：

```text
App -> Domain SDK -> Core SDK -> Server API
```

禁止 SDK 引用 Server `internal` 包，禁止 Server 依赖 SDK，禁止 App 直接持有企业凭据或把浏览器判断作为安全边界。

## 2. 功能归属判断

一个需求只有在引入新的权威状态、权限或运行语义时才修改 Server。只有新增公共调用能力时才扩展 Core SDK；只有需要用户界面时才修改 App。

| 需求 | 实现位置 |
|---|---|
| Runtime 缺陷修复 | Server |
| 新 Agent Run 生命周期操作 | Server + Core SDK，App 按需 |
| 新 Dashboard 或业务页面 | App |
| 新机器人、ERP、Office 等外部业务工具 | MCP Server，App 按需 |
| 使用现有 API 的新 CLI 命令 | CLI |
| 扩展认证和生产启用门禁 | Server + Core SDK，管理 App 按需 |

外部业务工具统一通过 MCP 接入。TMA 不设计 `POST /v2/tools`，也不设计把任意 URL、方法和请求模板保存为工具的 Declarative HTTP Tool。ERP、Office、机器人和企业 SaaS 的鉴权、协议适配与工具 Schema 属于对应 MCP Server；TMA 负责 MCP 版本治理、权限、运行保护、审批、事件与 Artifact。

后续若需要让 App、CLI 或管理员查看“当前有哪些工具”，只增加统一的只读 Tool Catalog。Catalog 聚合内置工具、已启用 MCP Server 和运行时可用能力的脱敏元数据，不执行工具、不保存第三方凭据，也不成为第二套工具注册或调用协议。Core SDK 只读取 Catalog；实际业务调用继续通过 Agent Run 和 MCP 工具链路完成。

## 3. 扩展认证

认证的状态、策略、审批和生产运行门禁属于 Server 控制面。Manifest lint、测试、恶意文件扫描、SBOM、兼容性和故障注入由隔离的 Certification Runner、CI 或 Worker 执行，结果作为签名证据提交给 Server。

认证必须绑定以下不可变事实：

```text
extension_id + version + package_checksum + manifest_checksum
```

App 和 SDK 只能申请、查询和审批认证，不能自行决定扩展可信。任何代码、Manifest、权限或依赖变化都必须形成新版本并重新认证。统一 Extension Certification Registry 尚未实现；现有 Skill Marketplace 审核、包扫描、MCP 版本治理和插件 smoke 是后续汇聚的基础。

## 4. 多 Server 约束

- Session、Run、Event、审批、扩展状态和门禁不能只保存在进程内存。
- SDK 使用单调递增的 Session Event `seq` 和 `after_seq` 恢复流。
- PostgreSQL Store 当前通过 `LISTEN/NOTIFY` 唤醒其他实例，并每秒从持久化事件表补偿查询；进程重启和通知丢失不会造成事件永久缺口。
- 写操作必须显式声明幂等语义。SDK 不自动重放普通写请求。
- 新旧 Server 滚动部署期间必须同时遵守已发布 API major 和 Event Schema 的兼容规则。

## 5. 版本与发布

Server API、Core SDK 和 Extension 分别版本化。API major 不兼容时使用新路径；TMA Go Core SDK 第一阶段使用 `/v2`，现有 `/v1` 保留给迁移中的 Web App 和脚本。

Core SDK 与当前根 Go module 共同发布，先位于 `sdk/tma`。当外部 module path 和独立发布流程确定后再拆 module，不提前拆仓。

公开响应、错误、数字、时间、枚举和分页必须遵守 [Server API v2 响应状态码与数据编码标准](./api-v2-response-and-data-standards.md)。该标准属于 Server、OpenAPI、SDK 三层共同契约，不允许各层自行解释。
