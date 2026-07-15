# Workbench Phase 0A 公共能力盘点

本文档记录科研纵向场景需要复用的 Dialog、Notification、File 和 Preview 能力现状，并确定 Phase 0A 的标准化边界。盘点基于当前 `web-app` 实现，目标是先形成可验证的公共契约，不在本阶段重写现有页面。

上位标准见 [TMA Workbench 插件开发标准](./workbench-plugin-standard.md)。

## 1. 盘点范围

首个科研纵向场景：

```text
科研项目插件
-> 创建或编辑项目
-> 选择实验资料
-> 调用已有 AI 能力分析
-> 预览分析结果
-> 导出 Artifact 或加入后续任务上下文
```

本轮只回答：

1. 当前 Workbench 已有哪些可复用行为。
2. 哪些行为仍与 `App.jsx` 或具体页面耦合。
3. Phase 0A 需要固定哪些最小数据和服务契约。
4. 哪些改造明确推迟到后续阶段。

## 2. 当前实现概览

当前前端是 React 19 + Vite 的 JavaScript 应用。主要工作台逻辑集中在：

```text
web-app/src/App.jsx
web-app/src/SkillsManagement.jsx
web-app/src/api.js
web-app/src/styles.css
```

公共能力目前以组件、局部 State 和 API 函数存在，尚未形成 Workbench Service 或插件可用的稳定契约。

| 能力 | 当前实现 | 主要问题 | Phase 0A 结论 |
|---|---|---|---|
| Dialog | 多个独立 Modal 组件和 `window.confirm` | 容器、焦点、关闭和风险确认不统一 | 先定义 `confirm`、`schema_form`、`custom_dialog_container` 三种模式 |
| Notification | 顶栏 `status`、页面局部 `message/error` | 没有统一严重级别、队列和生命周期 | 定义 `NotificationService.show`，暂不迁移所有现有提示 |
| File | Composer 上传 Session Artifact | 只面向当前 Session，文件和其他资源没有统一引用 | 使用 `ResourceRef` 统一文件、Artifact 和业务对象 |
| Preview | Artifact 文本/图片侧栏预览 | 只接受 Artifact，预览判断和下载路径直接耦合 Session API | 后续由 Related Resource Service 适配，首轮先固定 `ResourceRef` |
| Command | 页面事件直接调用本地函数或 API | 没有命名空间、权限、风险和审计契约 | 定义可序列化 `CommandDefinition` |

## 3. Dialog 盘点

### 3.1 已有形态

`App.jsx` 中至少存在以下独立 Dialog：

- `ToolPickerModal`
- `TaskTemplateModal`
- `SessionComparisonModal`
- `TaskMetadataModal`
- Approvals Modal

它们普遍使用 Backdrop + `role="dialog"` + `aria-modal="true"`，但容器类名、尺寸和内部结构分别实现。

删除环境变量、Provider、模型、Session 和中断任务等操作仍直接使用 `window.confirm`。

### 3.2 已有可复用点

- Backdrop 点击关闭。
- Dialog 语义标签。
- 标题、说明、操作区的基本结构。
- 多种内容宽度和滚动容器。
- 审批场景已有风险和状态表达。

### 3.3 缺口

- 没有统一 Dialog Stack 和层级管理。
- 没有统一 Escape 关闭、初始焦点和 Focus Trap。
- 没有统一的取消结果和 Promise 返回值。
- 未保存内容关闭行为由各页面自行决定。
- 危险确认有 Modal、内联确认和 `window.confirm` 三种形式。
- 插件无法通过稳定服务打开 Dialog。

### 3.4 Phase 0A 边界

首期标准化四种调用语义：

```text
dialog.confirm(options) -> Promise<boolean>
dialog.form(options) -> Promise<FormResult | undefined>
dialog.choice(options) -> Promise<string | undefined>
dialog.open(dialogID, input) -> Promise<unknown>
```

首轮契约完成后，已继续实现统一 React Dialog Host 和可排队的 Dialog Service：

```text
web-app/src/workbench/dialogService.js
web-app/src/workbench/DialogHost.jsx
```

当前支持：

- Promise 形式的 `confirm`、`form`、可搜索单选 `choice` 和受控自定义 `open`。
- Dialog 请求排队、取消、销毁和自定义 Renderer 注册。
- Backdrop、Escape、Focus Trap、滚动锁定和焦点恢复。
- `warning`、`danger` 与默认确认样式。
- 桌面居中和手机 Bottom Sheet 布局。
- Schema Form 的 string、number、boolean、enum 和 textarea 基础控件。
- Choice 的搜索、标题/描述分层、禁用项、空结果、键盘单选和移动端长列表滚动。

已迁移“删除任务”和“中断任务”两个确认场景。设置页中的环境变量、Provider 和模型删除仍使用 `window.confirm`，留待后续按页面逐步迁移。

## 4. Notification 盘点

### 4.1 已有形态

- `WorkbenchApp` 使用顶栏 `status` 展示运行状态和多数错误。
- 设置页和 `SkillsManagement` 使用局部 `message`、`error` 与 `.skills-notice`。
- Artifact Preview 使用独立 `.artifact-preview-error`。
- 部分成功消息会持续保留到下一次操作，没有统一自动关闭策略。

### 4.2 缺口

- 运行状态、成功反馈和错误反馈混用同一个字符串状态。
- 没有统一 `info/success/warning/error` 严重级别。
- 没有去重、超时、持久通知和操作按钮契约。
- 插件无法发送受控通知。

### 4.3 Phase 0A 边界

最小服务固定为：

```text
notifications.show(notification) -> notificationID
```

通知对象至少包含 `id`、`level`、`title`、`message` 和 `durationMs`。首轮只迁移删除任务和中断任务反馈，不批量替换其他页面的局部提示；通知 Action 要等 Command Registry 成立后再加入。

### 4.4 当前实现

首轮 Notification Service 和 Host 已实现：

```text
web-app/src/workbench/notificationService.js
web-app/src/workbench/NotificationHost.jsx
```

当前能力：

- `info`、`success`、`warning` 和 `error` 四个等级。
- SDK 使用 `durationMs`；`0` 表示持续显示直到用户关闭。
- 默认时长：Info 5 秒、Success 4 秒、Warning 8 秒、Error 持续显示。
- 使用 `dedupeKey` 替换同一业务反馈，避免重复堆叠。
- Host 同时最多展示最近 4 条通知。
- 自动关闭、鼠标悬停暂停和手动关闭。
- Success/Info 使用 `status`，Warning/Error 使用 `alert`。
- 桌面右上角和手机 Safe Area 内的响应式布局。
- 支持 `prefers-reduced-motion`。

Dialog Host 和 Notification Host 已提升到应用根部，普通工作台与设置页可以共享。当前已迁移删除任务和中断任务的成功、失败反馈；顶栏 `status` 继续表示运行状态，不被通知系统替代。

浏览器验证结果：

- 桌面 `1280 × 720`：通知宽 380px，右上角 16px，页面无横向溢出。
- 手机 `390 × 844`：左右各 8px，关闭按钮 `44 × 44`，页面无横向溢出。
- 错误通知保持显示并可手动关闭，ARIA Role 为 `alert`。

## 5. File 与 Related Resource 盘点

### 5.1 已有上传链路

当前 Composer 已支持：

- 点击、拖放和粘贴图片添加文件。
- 最多 10 个文件，单文件最大 64 MB。
- 上传进度、失败状态、移除和重试。
- 通过 `uploadSessionArtifact` 上传为 Session Artifact。
- 消息只保存 Artifact/Object Reference 和元数据，不嵌入文件字节。

上传结果主要包含：

```text
artifact_id
name
content_type
size_bytes
workspace_path
object_ref_id
```

### 5.2 已有相关文件展示

- 用户消息附件可以恢复为 Artifact。
- Agent 生成的文件可以显示在消息下方“相关文件”区域。
- 右侧 Results 使用 Artifact Tree 分组展示最终文件。
- 文件可以预览或下载。

### 5.3 已解决边界与剩余缺口

- Session Artifact 已通过 Adapter 进入统一 `ResourceRef`，后续 Provider 可以继续表达 Workspace 文件、任务、URL 和业务对象。
- Artifact 到公共资源的映射已从 `App.jsx` 移入 `sessionArtifactAdapter.js`。
- 上传、选择已有 Artifact 和选择 Workspace 文件尚未统一。
- 插件不得依赖 Artifact 内部结构；当前核心页面也已通过 `RelatedResourceService` 预览和打开资源。
- 资源来源和可执行动作已有最小契约，细粒度权限策略仍需在 Plugin Runtime 阶段接入。

### 5.4 Phase 0A 边界

使用 `ResourceRef` 作为 Workbench 与插件之间的稳定引用：

```text
id
type
title
source
mimeType
previewable
metadata
```

首期支持类型：

```text
file
artifact
task
session
url
business_object
```

`ResourceRef` 只保存稳定引用和展示所需元数据，不携带文件字节、Secret、完整业务对象或未授权下载地址。

## 6. Preview 盘点

### 6.1 已有能力

Artifact Preview 当前支持：

- PNG、JPEG、GIF、WebP、SVG 等图片。
- Text、Markdown、JSON、CSV、代码和日志等文本。
- JSON 格式化。
- 文本预览截断。
- 不支持格式提示下载。
- 可调整预览侧栏宽度。
- Object URL 生命周期清理和并发请求防抖。

### 6.2 已解决边界与剩余限制

- Preview 入口已改为 `ResourceRef`，Session Artifact 是首个 Provider，不再是 Preview UI 的输入契约。
- 下载地址由 Session Artifact Provider 解析，Preview UI 不再拼装 `sessionID + artifactID`。
- Preview Kind 判断已移入 Adapter，根据 `ResourceRef`、Content-Type 和文件扩展名判断。
- PDF、Office、音视频等没有统一 Renderer。
- Provider 已按 `sourcePrefix` 或 `supports(resource)` 发现；权限过滤仍需接入 Plugin Runtime。

### 6.3 Phase 0A 边界

本轮保持 Preview UI 的布局和交互不变，但数据入口已改为 `RelatedResourceService.preview(resourceRef)`。现有 Session Artifact Preview 是第一个 Adapter/Provider，核心页面已经成为该公共服务的首个消费者。

Provider 最小契约：

```text
id
sourcePrefix 或 supports(resource)
listRelated(context) -> ResourceRef[]
preview(resource, context) -> PreviewDescriptor
open(resource, context)
```

首期 `PreviewDescriptor` 支持：

```text
kind        -> image | text | download
contentType
text
objectUrl
downloadUrl
message
dispose()
```

`RelatedResourceService` 负责校验 `ResourceRef` 和 Preview Descriptor、选择 Provider、取消过期请求并释放上一个 Preview。Provider 负责资源解析、服务端请求以及自身创建的 Object URL；插件和 Preview UI 不得自行保留临时 URL。

Preview Service 必须保持：

- Server 端再次鉴权。
- 可取消和可释放临时资源。
- 大内容限制和下载回退。
- 插件不能获得内部 Token 或未授权 Object URL。

## 7. Command 盘点

### 7.1 当前形态

当前页面操作以 React Callback 和直接 API 调用为主，例如发送任务、中断、归档、审批、预览和下载。它们能够完成业务，但没有形成插件可以声明和执行的 Command Contract。

### 7.2 Phase 0A 最小契约

`CommandDefinition` 必须是可序列化声明，包含：

```text
id
title
risk
requiredPermissions
contexts
inputSchema
outputSchema
```

其中：

- `id` 必须使用命名空间。
- `risk` 只允许 `read`、`write`、`exec`、`external_effect`。
- 权限必须使用小写点分标识。
- Definition 不包含 Handler；Handler 由 Runtime 激活插件时注册。

本轮只实现定义和校验，不实现 Command Registry、执行、审批和审计。

## 8. 最小 PluginContext 决策

Phase 0A 的最小 `PluginContext` 只暴露科研纵向场景需要的公共边界：

```text
plugin       -> 插件 ID 和版本
scope        -> Organization、Workspace、User 和 Role
permissions  -> has / require
commands     -> register / execute
dialog       -> confirm / form / open
notifications -> show
resources    -> listRelated / preview / open
http         -> request
```

选择 `http.request` 是为了让插件通过受控、自动携带租户上下文的网关访问业务后端。它不是裸 `fetch`，不得向插件暴露 Token。

暂不加入：

- Workbench 内部 Router 和 Store。
- 原始 Auth Token 和 Secret。
- 任意 DOM 挂载能力。
- 未稳定的 Task、AI、File、Artifact 私有 API。

后续能力只有在两个真实业务场景证明需要后，才进入稳定 `PluginContext`。

## 9. 当前数据映射建议

现有 Artifact 进入 Workbench 公共层时建议映射为：

```text
ResourceRef.id          <- artifact.id
ResourceRef.type        <- artifact 或 file
ResourceRef.title       <- artifact.name || artifact.id
ResourceRef.source      <- tma.session-artifact:{session_id}
ResourceRef.mimeType    <- artifact.metadata.content_type
ResourceRef.previewable <- 当前 Preview Adapter 判断
ResourceRef.metadata    <- 经过白名单过滤的 size、path、turn_id、object_ref_id
```

禁止把完整 Artifact Metadata 无过滤地复制给插件。

当前 Adapter 只允许 `size_bytes`、`path`、`file_path`、`workspace_path`、`turn_id` 和 `object_ref_id` 进入 `ResourceRef.metadata`。下载 URL 由 Provider 在执行 `preview/open` 时生成，不进入 `ResourceRef`。

## 10. 本轮交付与后续工作

本轮交付：

- [x] Dialog、Notification、File 和 Preview 现状盘点。
- [x] `ResourceRef` 数据契约与运行时校验。
- [x] `CommandDefinition` 数据契约与运行时校验。
- [x] 最小 `PluginContext` 形状与运行时校验。
- [x] 契约单元测试。
- [x] 可排队的 Dialog Service 和统一 Dialog Host。
- [x] 删除任务和中断任务确认场景迁移。
- [x] 桌面及手机视口的 Dialog 视觉、焦点和溢出验证。
- [x] Notification Service、全局 Host、去重和自动关闭。
- [x] 删除任务和中断任务成功、失败反馈迁移。
- [x] 桌面与手机通知布局及 ARIA 验证。
- [x] Related Resource Service、Provider 注册/注销和资源标准化。
- [x] Session Artifact 到 `ResourceRef` 的白名单 Adapter。
- [x] Preview Descriptor、并发取消和临时资源释放。
- [x] 现有图片、文本、JSON、Markdown、截断和下载回退接入资源服务。
- [x] `tma.workbench_plugin.v1` Manifest Runtime Validator 与 JSON Schema。
- [x] Workbench API、Design System 和 Surface 兼容性状态。
- [x] Navigation、Route 和 Command Registry。
- [x] 静态 Plugin Catalog、激活事务、失败隔离和完整注销。
- [x] Manifest 权限约束与 Command 执行审计钩子。
- [x] 组织、工作区和角色 Scope Enablement Policy。
- [x] Workbench Shell 的静态 Catalog、Navigation 和 Hash Route 接线。
- [x] Route Permission Guard、加载/无权限/未找到状态和局部 Error Boundary。
- [x] 内置扩展诊断插件及桌面、手机端完整链路验证。

后续 Phase 0A 工作：

- [x] 从现有页面抽取 Dialog Service 和 Host。
- [ ] 迁移设置页剩余原生确认场景。
- [x] 从现有状态提示抽取 Notification Service 和 Host。
- [ ] 按业务优先级迁移其他页面的局部成功和错误提示。
- [x] 实现 Artifact 到 `ResourceRef` 的受控 Adapter。
- [x] 把现有 Artifact Preview 接入 Related Resource Service。
- [x] 实现静态 Plugin Registry 和 Manifest 校验。
- [x] 实现 Navigation、Route 和 Command 注册。
- [x] 增加租户/工作区/角色启用策略、Route Permission Guard 和 Error Boundary。
- [ ] 开发科研项目与报告插件完成纵向验证。

## 11. 验收条件

本轮完成必须满足：

1. 契约模块不依赖 React、DOM 或具体插件。
2. 非法资源类型、Command ID、权限和风险会被确定性拒绝。
3. `PluginContext` 缺少必需服务或方法时会拒绝创建。
4. 契约对象经过标准化并冻结，插件不能修改平台提供的身份和声明。
5. 当前 Workbench 业务结果不回退，确认交互统一进入受控 Host。
6. 前端生产构建继续通过。
7. Dialog 在桌面、`390 × 844` 和 `360 × 800` 视口无横向溢出，主要操作可达。
8. Dialog 打开时焦点进入主操作，关闭后返回触发控件。
9. Notification 的等级、默认时长、去重、关闭和 ARIA 语义经过测试。
10. Notification 在桌面和手机视口不遮出屏幕、不产生横向溢出。
11. `ResourceRef.metadata` 只包含 Adapter 明确允许的字段，下载地址不进入稳定引用。
12. 新 Preview 会取消旧请求；过期结果和当前结果分别只释放一次。
13. 图片、文本、Markdown、JSON、超大文本和不支持格式保持原有预览或下载回退行为。
14. Manifest 结构、协议、包内路径、路由前缀、权限和 Command 命名空间经过确定性校验。
15. 不兼容插件保留明确状态和原因，但 Navigation、Route 和 Command 不进入活动 Registry。
16. 激活失败只影响当前插件，并逆序注销本次已注册的全部 Contribution。
17. 停用或卸载后插件的 Navigation、Route、Command Handler 和声明全部消失。
18. Command 权限拒绝、成功和 Handler 错误都能进入统一执行结果钩子。
19. Scope 不满足组织、工作区或角色策略时，插件保持可审计的 `disabled` 状态且不产生 Contribution。
20. 插件 Hash Route 刷新可恢复，返回、任务切换和设置入口可以回到核心工作台。
21. Route 权限拒绝和渲染异常只影响插件区域，不导致 Workbench Shell 白屏。
22. 插件页面在桌面和 `390 × 844` 手机视口无整页横向溢出，返回和主操作可达。
