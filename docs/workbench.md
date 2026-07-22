# Workbench 与 Inspector

## 产品边界

Workbench 是任务工作台，不是 Runtime 调试器。主流程应回答：任务正在做什么、使用了哪些
资料、修改了什么、产出了什么、哪些动作等待确认。底层 event、trace 和 raw payload 放在
Inspector/详情面板，不占据默认聊天界面。

稳定信息架构：

- 左侧：Workspace、任务/Session、搜索和插件导航。
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

Workbench 使用 TypeScript SDK 访问公开 API，不直接依赖 Server 内部 payload 或数据库字段。

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

Workbench Plugin 是受信任的版本化前端扩展。平台提供稳定 Shell、路由、导航、命令、
Dialog、Notification、File、Preview、Artifact 和 SDK context。插件贡献可包括：

- 页面与导航项。
- Dashboard widget 和实体详情面板。
- Command/菜单动作。
- 文件预览器和任务模板。
- 设置页入口。

插件包声明 identifier、version、routes、contributions、required roles/scopes、SDK range 和
integrity metadata。插件不能替换认证、全局错误边界、审批语义或数据隔离。

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

## 验收

覆盖桌面/移动布局、键盘/焦点、加载/空/错/离线状态、RBAC、Workspace 切换、SSE 重连、
审批、Artifact、插件故障隔离、未知 contribution 降级和无横向溢出。浏览器自动化与截图
命令见 [`TESTING.md`](../TESTING.md)。
