# TMA 对话工作台与 Inspector

## 产品边界

对话工作台是 Platform 官方的 Agent 任务客户端，不是 Runtime 调试器，也不是专业应用目录。
主流程应回答：任务正在做什么、使用了哪些
资料、修改了什么、产出了什么、哪些动作等待确认。底层 event、trace 和 raw payload 放在
Inspector/详情面板，不占据默认聊天界面。

稳定信息架构：

- 左侧：Workspace、任务/Session 和搜索。
- 中间：对话、计划、进行中状态、审批/澄清和最终结果。
- 右侧：相关文件、Artifact、变更、引用和上下文详情。
- Inspector：事件时间线、trace、usage、tool、approval、错误和导出。

移动端使用互斥视图/抽屉，不压缩成三栏。所有异步动作必须有 pending、success、error 和
retry 状态；长文本、文件名和错误码不能撑破容器。

## 核心工作流

1. 新建或恢复 Session，附加文件/对象引用。
2. 发送任务并通过 SSE 查看进度。
3. 在原上下文处理审批、澄清、中断和 follow-up。
4. 查看文件读取、变更和 Artifact，不展示内部协议噪音。
5. 预览/下载结果，必要时重跑并比较。
6. 从任务跳转 Inspector 定位一次 Turn。

对话工作台使用 TypeScript Core SDK 访问公开 API，不直接依赖 Server 内部 payload 或数据库字段，
也不拥有 Project、Repository、Notebook、数据集等领域业务表。

## 定时任务审批

新建定时任务默认使用 `request_approval`。当工具策略要求审批时，Agent Core 将调用写入
durable journal 和 `session_interventions`，Turn 进入 `waiting_approval` 并释放 Lease；用户可从
任务的最后 Session 打开待审批卡片，稍后批准或拒绝。决定落库后 Runner 使用既有 continuation
恢复同一 Turn，不重新执行已经完成的工具调用。

`approve_for_me` 和 `full_access` 继续作为显式选项，并保留已有任务的原配置。定时任务中的
澄清、表单和文件补充仍保持关闭并按 `fail` 处理；Parked Approval 只改变危险工具调用的审批
方式，不扩大无人值守任务的人机交互范围。

## Inspector

Inspector 以 `session_id` 和可选 `turn_id` 为入口，提供：

- 事件与 span 时间线、critical path、self duration 和层级。
- 模型/工具/审批/completion validation 过滤。
- context、summary、plan、usage 和 token 明细。
- Artifact 预览/下载与 trace 导出（JSON、Perfetto、OTel）。
- observability status、exporter 最近成功/失败和深链分享。

Inspector 不显示 token、secret、完整工具敏感参数或未授权 Workspace 数据。生产环境中的
审批仍走业务 API 和 RBAC，不能因为用户能查看 trace 就授予执行权限。

## 插件模型

对话工作台 UI Extension 是受信任的版本化轻量前端扩展。平台提供稳定 Shell、命令、Dialog、
Notification、File、Preview、Artifact 和 SDK context。扩展贡献可包括：

- 任务或 Artifact 详情面板。
- Command/菜单动作。
- 文件预览器和任务模板。
- 设置页入口。

扩展包声明 identifier、version、contributions、required roles/scopes、SDK range 和 integrity
metadata。扩展不能替换认证、全局错误边界、审批语义或数据隔离，也不能携带独立业务后端、
数据库迁移或领域 Runtime；需要这些能力时必须建立独立应用。

`PluginContext` 最小能力：

```ts
interface PluginContext {
  workspaceId: string;
  actor: { id: string; roles: string[] };
  api: CoreClient;
  dialog: DialogService;
  notify: NotificationService;
  files: FileService;
  preview: PreviewService;
  commands: CommandService;
}
```

Dialog 统一 focus trap、ESC、危险操作和异步提交；Notification 去重并支持可访问性；File
统一 object ref/artifact/session attachment；Preview 按 MIME、安全策略和大小选择内联、
下载或外部查看。插件不得自己复制这些实现。

## 加载与治理

Workspace installation 决定插件是否可用。Shell 在加载前校验版本、完整性、角色和功能
开关；失败时隔离单个插件并保留核心工作台。前后端贡献必须绑定同一 extension revision。

插件不得从任意 URL 执行脚本。生产使用受控 bundle、CSP、依赖锁定和发布审计。跨插件
通信通过 command/event 或公开 SDK，不访问其他插件内部 store。

## 与 R语言生存分析工作台的边界

不再提供顶部“扩展工作台”入口或通用专业工作台目录。R 生存分析静态插件已经从
`apps/workbench` 删除，独立仓库为同级 `tma-r-survival-workbench`，产品名称固定为
“R语言生存分析工作台”。

对话产品名称固定为“TMA 对话工作台”。其顶栏只提供跳转到独立应用的链接，默认指向网关路径
`/r-survival/`，也可以在构建时通过 `VITE_TMA_R_SURVIVAL_WORKBENCH_URL` 指向独立域名；该链接
不是 UI Extension 路由，不把 R 应用加载到对话工作台进程中。

独立应用拥有后端项目持久化、数据集、Git 风格目录、Notebook、远程 JupyterLab、R Runtime、
数据清洗/分析流程和专业 UI；它通过 Core SDK 关联 TMA Session/Run、Agent 对话和 Artifact。
配置 GitLab 后，新建项目可创建私有仓库并提交 R 生存分析模板。生产接入遵守以下边界：

- GitLab Token 进入 Secret/环境变量体系，不进入插件 localStorage 或项目元数据。
- JupyterLab 通过 TMA 同源 HTTP/WebSocket 代理访问，不直接暴露无认证端口。
- 原始或可识别数据进入受控对象存储，Git 仓库只保存代码、配置、Notebook 和脱敏样例。
- 运行代码、提交、Push 和覆盖文件通过 Platform Worker/Capability 契约使用权限、审批与审计语义。

独立应用使用 `/v2/r-survival-projects/*` 应用 API；迁移期 Platform 只提供不进入 Core
OpenAPI/SDK 的兼容代理。对话工作台不调用 `/v2/workbench-projects/*` 或
`/v2/r-survival-projects/*`。

## 验收

覆盖桌面/移动布局、键盘/焦点、加载/空/错/离线状态、RBAC、Workspace 切换、SSE 重连、
审批、Artifact、UI Extension 故障隔离、未知 contribution 降级和无横向溢出。浏览器自动化与截图
命令见 [`TESTING.md`](../TESTING.md)。
