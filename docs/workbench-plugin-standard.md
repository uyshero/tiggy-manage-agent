# TMA Workbench 插件开发标准

本文档定义 TMA 面向企业定制场景的 Workbench 插件边界、扩展点、Manifest、SDK、权限、版本和交付要求。目标不是把 TMA 变成允许任意代码运行的通用插件市场，而是让不同企业、部门和岗位可以在同一个稳定工作台中装配自己的页面、工具、数据视图和业务操作。

配套文档：

- [Workbench Frontend Product Gap Plan](./workbench-frontend-product-plan.md)
- [Workbench Phase 0A 公共能力盘点](./workbench-phase0a-capability-inventory.md)
- [TMA Extension 与 Provider 治理标准](./extension-governance-standard.md)
- [Extension 设置页与配置贡献标准](./extension-settings-standard.md)
- [Tool Plugin SDK](./tool-plugin-sdk.md)

## 1. 产品定位

TMA Workbench 与 Local Work 类产品的重点不同：

| 产品方向 | 核心目标 | 主要扩展对象 |
|---|---|---|
| Local Work | AI 在本地文件、代码和命令环境中完成任务 | Agent、Tool、Runtime、Local Workspace |
| TMA Workbench | 企业按组织、岗位和业务场景装配工作台 | 页面、导航、组件、业务操作、数据视图、流程入口 |

TMA Workbench 的产品定义是：

> 面向企业业务场景的可扩展智能工作台。平台统一提供用户、组织、权限、任务、文件、数据、AI、审计和部署能力，业务团队通过受控插件装配岗位工作区和客户定制应用。

AI 是工作台提供的一项公共能力，不是所有插件的中心。多数客户项目的主要差异可能来自页面、字段、操作流程、系统连接和结果展示，AI 只参与其中一个处理节点。

典型链路：

```text
客户定制工作区
-> 页面、表单与业务操作
-> 数据采集与规则校验
-> 可选的 AI 处理
-> 人工确认或审批
-> 结果展示、导出或系统回写
```

## 2. 插件体系边界

TMA 必须区分三类扩展，不得使用一个含糊的 `plugin` 概念覆盖所有实现：

| 扩展类型 | 运行位置 | 负责内容 | 标准 |
|---|---|---|---|
| Workbench Plugin | 浏览器或桌面 WebView | 页面、导航、Widget、Command、详情面板 | 本文档 |
| Extension / Provider | Server、Worker 或外部服务 | 能力发现、配置、状态、路由和治理 | `extension-governance-standard.md` |
| Tool Plugin | Worker 进程 | 模型或业务可调用的具体工具 API | `tool-plugin-sdk.md` |

Workbench Plugin 可以调用平台已经授权的 Tool、AI、文件和数据服务，但不得自行成为未治理的后端执行环境。

### 2.1 第一版范围

第一版采用“可信、受控、随产品构建”的插件模型：

- 插件由 TMA 团队、企业内部团队或经过审核的交付团队开发。
- 插件源码进入受控代码仓和构建流程。
- 插件可以按租户、组织、工作区和角色启用。
- 插件使用统一 Workbench SDK 和设计系统。
- 插件异常必须被 Error Boundary 隔离。
- 插件不得从任意远程 URL 加载 JavaScript。
- 插件不得直接访问 TMA 数据库、Token、Secret 或工作台内部 Store。

第一版暂不包含：

- 用户上传任意 JavaScript 并立即执行。
- 不可信第三方插件沙箱。
- 公共插件市场、计费和商业分发。
- 跨 Workbench major 版本的无限期兼容。
- 插件自行注入全局 CSS、修改平台路由或覆盖内置组件。

### 2.2 与设置贡献标准的关系

Workbench Plugin 可以贡献业务页面，但 Extension 设置页仍遵守 Schema 驱动原则。插件不得通过设置贡献注入任意 React、HTML 或 JavaScript。

```text
业务工作区页面 -> 可以由受信任 Workbench Plugin 提供
平台设置页面   -> 由 App 根据 settings_contributions Schema 统一渲染
```

## 3. 总体架构

```text
Workbench Shell
├── 登录、组织、租户与角色
├── 导航、路由、标签页与布局
├── Extension Point Registry
├── Plugin Runtime 与生命周期
├── Workbench SDK
├── 统一设计系统
├── 错误隔离、日志与审计
└── Plugin Admin
          ↓
可信 Workbench Plugins
├── 页面与业务工作区
├── 首页 Widget
├── Command 与工具栏操作
├── 详情页 Panel
└── 平台能力调用
          ↓
平台公共服务
├── Auth / Permission
├── Task / Session / Workflow
├── Data / File / Artifact
├── AI / Tool / Skill
└── Notification / Audit
```

平台核心必须保持对插件的控制权：插件声明“贡献什么”，Workbench 决定“是否加载、放在哪里、当前用户能否看见和执行”。

## 4. 需要开发的平台模块

### 4.1 Workbench Shell

Workbench Shell 负责稳定的应用骨架：

- 全局导航、工作区、标签页和布局。
- 登录、租户、组织、角色和用户上下文。
- 任务中心、消息中心、审批和通知入口。
- 统一错误页、空状态、加载状态和无权限状态。
- 插件页面的挂载、卸载与错误隔离。

业务插件禁止重复实现登录、全局导航、权限系统和通知中心。

### 4.2 Plugin Catalog 与 Runtime

平台需要维护 Workbench Plugin Definition、安装状态和启用范围：

```text
Plugin Definition
-> 兼容性校验
-> 租户/工作区启用策略
-> 用户权限过滤
-> 激活插件
-> 注册 Contribution
-> 渲染或执行
```

Runtime 至少负责：

- 读取插件 Manifest。
- 检查协议、Workbench API 和设计系统版本。
- 检查依赖和冲突。
- 根据租户、工作区、角色和 Feature Flag 选择插件。
- 执行 `activate` 和 `deactivate` 生命周期。
- 注册并注销扩展点。
- 隔离插件渲染错误。
- 记录激活失败、Command 调用和权限拒绝。

### 4.3 Extension Point Registry

Workbench 的目标标准包含以下五类扩展点，但 MVP 只先实现 `navigation`、`route` 和 `command`。`widget` 与 `detail_panel` 必须在首个纵向业务场景通过后再进入 Phase 0B。

| 扩展点 | 用途 | 平台控制 | 阶段 |
|---|---|---|---|
| `navigation` | 二级菜单和业务入口 | 一级导航分组、排序上限、可见性 | Phase 0A |
| `route` | 完整业务页面 | 路径前缀、权限、错误边界 | Phase 0A |
| `command` | 按钮、工具栏和业务操作 | 权限、上下文、确认、审计 | Phase 0A |
| `widget` | 首页或工作区组件 | 尺寸、位置、刷新频率 | Phase 0B |
| `detail_panel` | 详情页标签或侧栏 | 可挂载对象类型、顺序、尺寸 | Phase 0B |

第一版禁止开放“任意 DOM 挂载点”。新增扩展点必须先定义输入上下文、输出契约、权限、布局约束、错误行为和版本策略，并至少有两个真实业务场景需要。

### 4.4 Workbench SDK

SDK 是插件访问平台能力的唯一入口，至少提供：

```ts
export interface PluginContext {
  auth: AuthService;
  permissions: PermissionService;
  navigation: NavigationService;
  commands: CommandService;
  events: EventService;
  http: ScopedHttpService;
  tasks: TaskService;
  files: FileService;
  artifacts: ArtifactService;
  ai: AIService;
  notifications: NotificationService;
  telemetry: PluginTelemetryService;
}
```

约束：

- SDK 只暴露稳定、可版本化的接口。
- 插件不得导入 Workbench 内部 Store、Router Instance 或私有组件。
- `ScopedHttpService` 必须自动携带租户和用户上下文，并限制允许访问的服务。
- SDK 返回平台统一错误结构，不向插件泄漏 Secret 或内部 Token。
- 实验性 API 必须放入显式的 `experimental` 命名空间，不承诺兼容。
- `TaskService.list` 与 `ArtifactService.list` 由 Workbench 宿主通过 Core SDK 实现，插件不得自行拼 Session 或 Artifact 公共 API URL。`ScopedHttpService` 仅允许 `/v2`，只作为尚无稳定 facade 时的受控过渡入口。

### 4.5 统一设计系统

平台需要提供表格、表单、筛选器、详情页、Dialog、Drawer、文件上传、Artifact 预览、权限提示和状态反馈等组件。

插件必须：

- 使用平台 Design Token 和基础组件。
- 支持平台明暗主题及企业主题变量。
- 遵守平台导航、间距、密度、响应式和无障碍规则。
- 使用平台的 Loading、Empty、Error 和 Permission Denied 状态。
- 不得注入全局 CSS，不得修改 `html`、`body` 或其他插件节点。
- 不得把 Plugin 自身品牌放在平台主品牌之前。

#### 4.5.1 多端适配标准

所有面向最终用户的 Workbench Plugin 默认必须支持桌面 Web、桌面壳、平板和手机。多端适配属于发布门禁，不是可选的视觉优化。

平台统一定义三种布局模式，插件不得自行建立互相冲突的全局断点体系：

| 模式 | 参考宽度 | 主要形态 |
|---|---:|---|
| `wide` | `> 1100px` | 多栏工作台、侧栏 Preview、并列操作区 |
| `medium` | `641px - 1100px` | 单主栏，辅助区域改为 Tab、Drawer 或纵向区域 |
| `compact` | `<= 640px` | 单栏、触控优先、全屏或近全屏 Dialog/Preview |

参考宽度与当前 Workbench CSS 保持一致。插件组件应优先使用容器查询、弹性布局、Grid 的 `minmax()` 和平台布局 Token，不应直接根据设备名称判断布局。

强制规则：

- 页面在 `320px` 到宽屏桌面范围内不得出现非业务必要的整页横向滚动。
- 固定格式元素必须使用稳定尺寸和响应式约束，动态内容不得导致布局跳动或控件重排。
- 不使用随视口宽度缩放的字体；文字必须换行、截断或提供完整内容入口，不得遮挡其他控件。
- 插件不得写死工作台侧栏宽度、顶部高度或可视区域高度。
- 使用 `100dvh`、Safe Area 和平台滚动容器，避免移动浏览器地址栏和刘海区域遮挡操作。
- 触控设备上的主要交互目标建议不小于 `44px × 44px`。
- Hover 只能作为增强效果，任何操作都必须能通过点击、触控和键盘完成。
- Drag and Drop 不能是上传或排序的唯一入口，必须提供文件选择或明确按钮。
- 页面缩放到 `200%` 时，核心信息和操作仍然可达。
- 插件必须支持平台明暗主题、系统字体缩放、键盘焦点和减少动画偏好。

常见组件的多端行为由 Workbench Host 统一控制：

| 能力 | `wide` | `medium` | `compact` |
|---|---|---|---|
| Dialog | 居中，受控最大宽高 | 居中或大尺寸 Drawer | 全屏、近全屏或 Bottom Sheet，操作区保持可见 |
| Related Resource | 侧栏或并列区域 | Drawer、Tab 或纵向区域 | 全屏选择器或单栏列表 |
| Preview | 可调整宽度的侧栏 | 页面内纵向区域或 Drawer | 全屏页面，关闭和下载操作固定可达 |
| Navigation | 完整侧栏 | 可折叠侧栏 | Drawer 或底部/顶部受控入口 |
| Form | 多列但字段有最小宽度 | 减少列数 | 单列，标签和错误不被截断 |
| Table | 展示主要列 | 隐藏低优先级列或局部滚动 | 关键列列表化或表格容器内滚动，不压缩到不可读 |
| Command Actions | 工具栏 | 可折叠工具栏或 More Menu | 主操作保留，次操作进入 Menu，危险操作单独确认 |

插件只提供业务内容和布局意图，不得自行复制 Dialog Shell、全局 Drawer、Preview Shell 或移动导航。平台公共容器必须负责 Focus Trap、Escape/返回键、滚动锁定、Safe Area、层级和关闭行为。

最低验证视口：

```text
1440 × 900   桌面宽屏
1024 × 768   平板横屏 / 小型桌面
768 × 1024   平板竖屏
390 × 844    主流手机
360 × 800    紧凑手机
```

每个 Route、Dialog、Related Resource 和 Preview 场景必须至少验证：

- 无内容重叠、非预期裁切和整页横向滚动。
- 菜单、主操作、关闭、取消和错误恢复始终可达。
- 长标题、长文件名、空状态、加载状态、错误状态和无权限状态不破坏布局。
- 软键盘打开后，表单当前字段和提交/取消操作仍可访问。
- 键盘 Tab 顺序、焦点可见性和触控操作成立。
- 页面刷新或断点切换不丢失未提交业务状态，确需重置时必须提示用户。

### 4.6 Plugin Admin

管理员至少能够：

- 查看插件 ID、名称、来源、版本和兼容状态。
- 按组织、工作区和角色启用或停用插件。
- 查看插件声明的权限、扩展点和配置。
- 查看加载失败、调用失败和审计记录。
- 回滚到上一可用版本。

远程安装和插件市场不属于第一版前置条件。

### 4.7 开发工具链

需要提供：

- 插件项目脚手架。
- Manifest JSON Schema 和校验命令。
- Workbench SDK TypeScript 类型包。
- 本地模拟租户、角色和权限的开发环境。
- 示例插件和扩展点 Storybook/Playground。
- Build、Test、Package 和兼容性检查命令。
- 插件开发、升级和故障排查文档。

## 5. 插件包标准

### 5.1 目录结构

```text
research-workbench/
├── plugin.json
├── package.json
├── src/
│   ├── index.ts
│   ├── pages/
│   ├── widgets/
│   ├── commands/
│   └── panels/
├── assets/
├── tests/
└── README.md
```

### 5.2 Manifest

Workbench Plugin 必须提供 `plugin.json`：

```json
{
  "protocol_version": "tma.workbench_plugin.v1",
  "id": "com.example.research-workbench",
  "name": "科研工作台",
  "description": "项目、资料和科研报告工作区",
  "version": "1.2.0",
  "entry": "./dist/index.js",
  "surfaces": ["web_desktop", "desktop_shell", "web_tablet", "web_mobile"],
  "engines": {
    "workbench_api": ">=1.0.0 <2.0.0",
    "design_system": ">=1.0.0 <2.0.0"
  },
  "permissions": [
    "files.read",
    "files.write",
    "ai.invoke",
    "research.projects.read"
  ],
  "contributes": {
    "navigation": [
      {
        "id": "research",
        "group": "workspace",
        "title": "科研项目",
        "route": "/plugins/com.example.research-workbench/projects"
      }
    ],
    "routes": [
      {
        "id": "projects",
        "path": "/plugins/com.example.research-workbench/projects",
        "component": "ProjectsPage",
        "required_permissions": ["research.projects.read"]
      }
    ],
    "commands": [
      {
        "id": "com.example.research-workbench.generate-report",
        "title": "生成报告",
        "required_permissions": ["ai.invoke"],
        "risk": "write"
      }
    ]
  },
  "configuration": {
    "schema": "./config.schema.json",
    "scope": "workspace"
  }
}
```

### 5.3 Manifest 字段约束

| 字段 | 要求 |
|---|---|
| `protocol_version` | 固定协议版本，第一版为 `tma.workbench_plugin.v1` |
| `id` | 全局唯一、发布后不可修改，建议使用反向域名格式 |
| `version` | 遵循 SemVer |
| `entry` | 只能指向插件包内经过构建的入口，不得是远程 URL |
| `surfaces` | 声明已完成验证的终端；用户界面插件默认必须覆盖桌面、平板和手机 |
| `engines` | 声明 Workbench API 和 Design System 兼容范围 |
| `permissions` | 插件可能使用的平台能力全集 |
| `contributes` | 静态声明扩展点，运行时不得注册未声明的扩展 |
| `configuration` | 只引用 Schema，不包含 Secret 明文或执行脚本 |

Manifest 不得包含 Secret、访问 Token、租户私有配置或可执行内联脚本。

`surfaces` 只允许 `web_desktop`、`desktop_shell`、`web_tablet` 和 `web_mobile`。平台只能在插件声明并完成验证的终端上启用该插件；面向通用企业用户的插件缺少平板或手机支持时，必须记录原因和替代入口，不能在未验证终端静默降级渲染。

Phase 0A 的 `contributes` 只接受 `navigation`、`routes` 和 `commands`。Widget 与 Detail Panel 即使出现在未来规划中，也不能提前写入当前 v1 Manifest；必须等 Phase 0B 固定 Schema 和 Host 行为后再开放。

当前实现：

- `apps/workbench/src/workbench/plugin.schema.json` 提供 CLI 和 IDE 可使用的 JSON Schema。
- `pluginManifest.js` 提供浏览器与 Node 共用的运行时校验、标准化和冻结。
- Engine Range 首期接受精确 SemVer 或由空格连接的 `>=`、`>`、`<=`、`<`、`=` 比较器。
- 结构非法、包路径逃逸、路由越界、Command 命名空间越权和未声明权限会直接拒绝。
- Workbench API、Design System 或 Surface 不匹配时保留插件记录并标记 `incompatible`，不静默加载。

## 6. 插件代码契约

```ts
export interface WorkbenchPlugin {
  id: string;
  activate(context: PluginContext): void | Promise<void>;
  deactivate?(): void | Promise<void>;
}
```

示例：

```ts
export const plugin: WorkbenchPlugin = {
  id: "com.example.research-workbench",

  async activate(context) {
    context.commands.register(
      "com.example.research-workbench.generate-report",
      async input => {
        await context.permissions.require(["ai.invoke"]);
        return context.http.request("/v1/research/reports", { method: "POST", body: input });
      }
    );
  },

  async deactivate() {
    // Runtime 负责释放通过 SDK 注册的资源。
  }
};
```

Runtime 必须保证：

- 同一插件同一作用域只激活一次。
- 激活失败不影响 Shell 和其他插件。
- 停用时注销 Command、Event Subscription 和临时资源。
- Manifest 声明与运行时注册不一致时拒绝该 Contribution。
- 插件 ID 与代码导出的 ID 不一致时拒绝激活。

静态插件包在 `apps/workbench/src/plugins/index.js` Catalog 中按以下结构注册：

```ts
{
  manifest,
  plugin,
  components: { ProjectsPage }
}
```

Manifest 中的 Route `component` 必须能在 `components` 中找到同名导出。Navigation 和 Route 由 Runtime 自动注册；Command 声明由 Runtime 注册，插件只能在 `activate()` 中为已声明 Command 绑定 Handler。

Runtime 把激活视为事务：任一路由组件缺失、Contribution 冲突、Command 未声明、Handler 缺失或插件代码报错，都会逆序注销本次已注册的全部资源。批量加载 Catalog 时，一个插件失败不会阻断其他兼容插件；停用或卸载时，即使插件自身 `deactivate()` 报错，Workbench 仍会清除它的 Navigation、Route 和 Command。

静态 Catalog 包可以额外声明由部署方控制的 `enablement`，它不属于插件可自行申请的 Manifest 权限：

```ts
{
  defaultEnabled: false,
  organizations: ["org_research"],
  workspaces: ["wksp_lab"],
  roles: ["researcher", "admin"],
  excludedOrganizations: [],
  excludedWorkspaces: [],
  excludedRoles: ["suspended"]
}
```

组织、工作区条件分别精确匹配，角色条件命中任意一个即可；排除条件优先。`defaultEnabled: false` 且存在显式白名单时，匹配白名单的 Scope 可以启用。未启用插件保留 `disabled` 状态和原因，但不能注册任何 Contribution。

Workbench Shell 通过 Registry Subscription 获取活动 Navigation 和 Route。插件逻辑路径使用 `/plugins/{plugin_id}/...`，当前 Web Host 以 Hash 保存该逻辑路径，使 `/app/assets/` 部署刷新后仍能恢复插件页面，同时不要求 Server 为每个插件添加前端回退路由。

所有插件 Route 统一经过 `PluginRouteHost`：进入页面前调用 Permission Service 校验 `required_permissions`，渲染异常由局部 Error Boundary 捕获并进入统一 Notification，不得导致 Shell 白屏。前端 Guard 只控制可见性和交互，插件通过 `http.request` 访问的 Server API 仍必须依据真实 Principal 和 Resource 再次鉴权。

## 7. 扩展点详细要求

### 7.1 Navigation

- 插件默认只能向平台定义的导航分组贡献二级入口。
- 平台决定最终排序、折叠、可见性和移动端呈现。
- 菜单可见不代表路由有权访问，进入路由时必须再次鉴权。
- 插件不得覆盖平台内置入口或注册根路径。

### 7.2 Route

- 路径必须位于 `/plugins/{plugin_id}/...`。
- 每个插件页面由平台 Error Boundary 和 Suspense Boundary 包裹。
- Route Loader 必须支持取消，页面卸载后不得继续修改状态。
- 插件不得直接重定向登录、修改全局 Router 或拦截其他插件路由。

### 7.3 Widget

- 只能挂载到平台公布的 Slot。
- 必须声明允许尺寸，组件不得自行改变网格结构。
- 自动刷新必须使用平台 Scheduler，并遵守最小刷新间隔。
- Widget 失败时显示局部错误，不影响整个首页。

### 7.4 Command

- Command ID 必须使用插件 ID 作为命名空间。
- Command 必须声明权限和风险级别。
- 有写入、执行或外部影响的 Command 必须接入平台确认或审批机制。
- Command 结果应返回结构化数据、Artifact Reference 或统一错误，不得返回大二进制。
- 平台必须记录发起人、作用域、输入摘要、结果、耗时和错误。

### 7.5 Detail Panel

- 必须声明可挂载的 `resource_types`。
- 平台向 Panel 传递稳定的资源引用，不直接传递内部数据库对象。
- Panel 只能通过 SDK 请求资源详情和执行操作。
- Panel 不得改变宿主详情页的主状态，除非通过已注册 Command。

## 8. 权限、数据和安全

权限检查必须同时发生在三个位置：

```text
Manifest 声明
-> Workbench 可见性与交互检查
-> Server API 强制鉴权
```

前端隐藏按钮不是安全边界。所有数据读取和业务写入必须由 Server 根据 Principal、Organization、Workspace、Role 和 Resource 再次授权。

强制要求：

- 插件只获得当前会话必要的最小权限。
- 插件不得读取其他租户的缓存、状态或浏览器存储。
- Secret 只能通过受控连接或 Secret Reference 使用，不进入前端插件。
- 插件 HTTP 请求通过 `ScopedHttpService`，不得自行持有平台 Token。
- 插件不得使用 `eval`、动态脚本标签或未声明的远程代码。
- 文件、AI、Tool 和外部系统调用沿用平台审批及审计规则。
- 插件遥测不得包含正文、Secret、完整文件内容或未经允许的个人数据。

## 9. 插件通信标准

跨插件协作只能使用以下方式：

1. 平台 Command。
2. 版本化 Domain Event。
3. 稳定 Resource Reference。
4. Server API。

禁止：

- 插件直接导入另一个插件的源码。
- 读取另一个插件的 React Context 或内部 Store。
- 通过全局变量共享状态。
- 依赖 DOM 选择器调用另一个插件。

Event 名称必须使用命名空间并声明 Schema 版本：

```text
com.example.research.project.updated.v1
```

事件只能表达已经发生的事实；要求其他模块执行动作时应使用 Command。

## 10. 版本与兼容性

- 插件版本遵循 SemVer。
- `protocol_version` major 不同视为不可加载。
- `engines.workbench_api` 不匹配时状态为 `incompatible`。
- Workbench API minor 版本只能增加可选能力，不得改变既有字段语义。
- 删除 API、修改字段语义或改变权限模型必须升级 major。
- 插件不得假设未知字段不存在，应忽略未知可选字段。
- 平台至少保留一个上一稳定插件版本用于回滚。

第一版插件随主应用构建，可以在 CI 中完成完整兼容性检查。进入动态加载阶段后，必须增加包哈希、签名、来源验证和灰度发布。

## 11. 开发与发布流程

```text
创建插件脚手架
-> 声明 Manifest 和权限
-> 实现页面与 Contribution
-> 本地模拟租户/角色调试
-> 单元测试和组件测试
-> Manifest / API 兼容性检查
-> 安全与权限检查
-> 构建并进入受控插件目录
-> 按租户/工作区灰度启用
-> 监控错误并支持回滚
```

每个插件至少需要：

- Manifest Schema 校验。
- `activate` / `deactivate` 生命周期测试。
- 每个 Route、Widget 和 Panel 的错误状态测试。
- 权限允许和拒绝测试。
- 租户隔离测试。
- Workbench API 兼容性测试。
- 关键 Command 的成功、失败、取消和审批测试。
- 桌面、平板和手机最低视口的响应式截图或视觉回归测试。
- Dialog、Related Resource、Preview 和软键盘场景的多端交互测试。
- 至少一个端到端业务场景。

## 12. 分阶段实施路线

### Phase 0：协议与静态注册

目标是让客户定制不再直接修改 Workbench 核心源码。

- 固定 `tma.workbench_plugin.v1` Manifest Schema。
- Phase 0A 先实现 Navigation、Route 和 Command Registry。
- 把 Dialog、Notification 和 Related Resource 抽成最小 Workbench Service。
- 提供最小 Workbench SDK 和设计系统出口，不追求一次覆盖所有现有能力。
- 插件随主应用构建，通过静态 Registry 加载。
- 支持按租户、工作区和角色启用。
- 先完成一个真实科研业务插件，而不是只做 Hello World。
- 纵向场景通过后，Phase 0B 再增加 Widget 和 Detail Panel。

后续示例：

1. 科研工作台：项目页面、进度 Widget、报告 Command、分析 Panel。
2. 企业运营工作台：任务列表、待办 Widget、审批 Command、客户 Panel。

### Phase 1：独立包与受控动态加载

- 插件独立构建和发布。
- 插件 Catalog、版本、依赖和兼容性管理。
- 受控静态域或内部制品库动态加载。
- 哈希、签名、来源校验、灰度和回滚。
- 插件加载性能预算和故障熔断。

### Phase 2：开发者生态

只有在内部插件协议稳定并积累足够真实插件后再进入：

- 第三方开发者门户。
- 插件审核、认证和市场。
- 更强的不可信代码隔离。
- 商业授权、计费和分发。
- 长期 API 兼容和弃用机制。

## 13. Phase 0 验收标准

Phase 0 完成必须同时满足：

1. 新业务插件不修改 Workbench Shell 核心源码即可注册页面、导航和 Command。
2. 同一部署中，不同租户和角色可以看到不同插件组合。
3. 禁用插件后，其路由、菜单、命令、事件订阅和页面资源全部失效。
4. 一个插件渲染或激活失败不会导致整个 Workbench 白屏。
5. 未声明或未授权的权限在前端和 Server 两端都被拒绝。
6. 插件只通过 Workbench SDK 使用平台能力。
7. 插件 UI 与核心工作台保持统一设计和交互规范。
8. 插件版本不兼容时给出明确状态和处理建议，不得静默加载。
9. 管理员能够按工作区启停插件并查看失败原因。
10. 至少一个真实科研业务插件完成端到端验证，并证明 Dialog、Related Resource 和结果交付闭环成立。
11. 科研业务插件在标准桌面、平板和手机视口完成视觉与交互验证，不出现重叠、不可达操作和非预期整页横向滚动。

## 14. 当前决策

TMA 应先建设“可扩展工作台”，而不是立即建设“开放插件市场”。首期最关键的交付物是：

```text
稳定的 Workbench Shell
+ 明确的 Extension Points
+ tma.workbench_plugin.v1 Manifest
+ 受控 Workbench SDK
+ 统一设计系统
+ 租户/角色启用和权限治理
```

这套能力成立后，企业定制才会从“修改主应用源码”变成“选择通用插件、配置工作区、开发少量业务插件”，并让一次客户交付逐步沉淀为可复用的产品资产。

## 15. 智能体驱动的插件开发规划

### 15.1 目标定义

TMA 的长期目标是让智能体根据企业需求自主开发 Workbench Plugin，实现受控的自我扩展：

> 智能体可以创建、修改、测试和提交业务插件，但不得修改 Workbench Shell、Plugin Runtime、权限系统、审计系统和其他核心代码。

这里的“自我开发”不是运行中的系统重写自身，也不是模型绕过研发流程直接修改生产环境，而是：

```text
稳定核心平台
+ 机器可读的插件标准
+ 受控插件开发环境
+ 自动验证与发布流水线
-> 智能体持续开发新的业务插件
```

建议对外使用以下产品术语：

- 智能体驱动的插件开发。
- 受控的智能体自我扩展。

禁止使用容易产生无限权限误解的“系统自动修改自己”作为工程定义。

### 15.2 不可突破的核心边界

智能体开发任务必须使用路径和能力白名单。无论智能体是否具备文件编辑或代码执行能力，都必须遵守：

```text
可写：指定插件工作区、插件测试和插件资产
只读：Workbench SDK、设计系统、扩展点目录、示例插件和公开文档
禁止：Shell、Plugin Runtime、Auth、Permission、Audit、Secret 和发布策略核心
```

具体规则：

1. 智能体只能通过 Workbench SDK 使用平台能力。
2. 智能体不得直接修改核心源码、核心配置和核心依赖。
3. 智能体不得自行增加 Manifest 未声明的权限。
4. 智能体不得关闭测试、签名、审批、审计或回滚机制。
5. 智能体不得直接写入生产插件目录或全量启用插件。
6. 智能体不得读取生产 Secret、其他租户数据或未授权业务数据。
7. 智能体发现 SDK 或扩展点不足时，只能输出 Capability Gap Proposal，不得越过边界修改核心。

Capability Gap Proposal 至少包含：

```json
{
  "requested_by": "plugin-development-run-id",
  "plugin_id": "com.example.research-workbench",
  "missing_capability": "workbench.data_view.register",
  "business_reason": "科研项目页面需要注册可复用的数据视图",
  "requested_contract": {
    "input": "DataViewDefinition",
    "output": "Disposable"
  },
  "alternatives_considered": [],
  "security_impact": "read",
  "blocking": true
}
```

是否扩展核心 SDK 由平台研发流程单独决定，不属于当前插件开发任务。

### 15.3 标准开发闭环

```text
用户提出业务需求
-> 智能体澄清角色、数据、操作和验收目标
-> 查询已有插件、SDK、组件和连接器
-> 判断配置复用、扩展已有插件或创建新插件
-> 生成开发计划、Manifest 和权限清单
-> 在隔离插件工作区实现代码与测试
-> 运行构建、静态检查、权限检查和兼容性测试
-> 启动临时预览环境并生成验收说明
-> 提交代码、测试结果、权限差异和风险报告
-> 策略审批或人工审核
-> 签名、灰度启用和运行监控
-> 成功后扩大范围，失败时自动停用或回滚
```

如果现有插件已经满足需求，智能体应优先生成租户配置或组合已有 Contribution，不重复创建功能相同的新插件。

### 15.4 智能体可以完成的工作

插件开发智能体可以：

- 把自然语言需求拆解为页面、Widget、Command、Detail Panel 和数据依赖。
- 搜索并复用已有插件、组件、SDK API、Tool 和 Provider。
- 创建符合 `tma.workbench_plugin.v1` 的插件项目。
- 生成 Manifest、配置 Schema、页面、组件、Command 和测试。
- 根据业务操作推导权限建议和风险级别。
- 运行格式化、类型检查、单元测试、组件测试和端到端测试。
- 在模拟租户、角色和权限下执行测试矩阵。
- 生成插件预览、变更摘要、权限差异和已知限制。
- 根据测试或审核反馈继续修改插件。
- 提交新的插件版本并请求灰度启用。
- 根据插件运行数据提出修复或优化版本。

插件开发智能体不能：

- 直接修改核心平台以让测试通过。
- 绕过 SDK 调用内部 API 或数据库。
- 自行批准新增高风险权限。
- 将失败测试标记为通过或删除强制质量门禁。
- 在没有发布策略授权时直接部署生产。
- 因插件能力不足而修改其他插件的内部实现。

### 15.5 平台需要补齐的开发能力

要让智能体可靠开发插件，平台必须把隐含经验变成机器可读取和自动验证的契约。

| 平台能力 | 交付物 | 作用 |
|---|---|---|
| 插件协议 | `plugin.schema.json` | 校验 Manifest、权限和 Contribution |
| SDK Catalog | API 类型、版本、示例、权限和风险元数据 | 让智能体正确选择平台能力 |
| Extension Point Catalog | Slot、Context、布局和生命周期 Schema | 避免智能体猜测挂载方式 |
| Component Catalog | 组件 Props、示例和设计约束 | 生成一致的企业 UI |
| Plugin Scaffold | 标准目录、构建和测试配置 | 消除重复样板代码 |
| Plugin Sandbox | 路径、网络、资源和 Secret 隔离 | 限制智能体开发权限 |
| Validation CLI | Manifest、类型、权限、依赖和兼容性检查 | 提供确定性质量门禁 |
| Test Harness | 租户、角色、资源和审批模拟 | 验证权限与业务行为 |
| Preview Environment | 临时 URL、模拟数据和验收记录 | 让用户在发布前确认结果 |
| Release Service | 签名、制品、灰度、启停和回滚 | 受控进入生产环境 |
| Plugin Observability | 加载、错误、性能和 Command 审计 | 支持上线判断与自动回滚 |

SDK Catalog 的每个 API 至少应该提供：

```text
稳定标识
版本和弃用状态
输入/输出 Schema
所需权限
风险级别
适用扩展点
最小示例
常见错误
是否允许插件开发智能体调用
```

仅有面向人阅读的说明文档不足以支撑稳定的自主开发。JSON Schema、TypeScript 类型、可执行示例和验证器必须保持一致，并在 CI 中检查漂移。

### 15.6 插件开发智能体的工具集合

建议提供专用的 `Plugin Developer Agent`，而不是直接给通用 Agent 开放整个主仓库。它的工具集合应限定为：

```text
workbench.search_sdk
workbench.search_components
workbench.search_plugins
plugin.scaffold
plugin.read_workspace
plugin.write_workspace
plugin.validate_manifest
plugin.check_permissions
plugin.build
plugin.test
plugin.preview
plugin.package
plugin.submit_release
```

工具实现可以复用底层文件、命令、Git、Artifact 和 Sandbox 能力，但上层策略必须限制工作目录、命令集合、网络访问、资源消耗和可读取数据。

`plugin.submit_release` 只负责提交候选制品，不等于批准发布。发布权限由组织策略决定。

### 15.7 开发运行记录

每次智能体插件开发都应产生一个可追踪的 Development Run：

```json
{
  "run_id": "pdr_01",
  "request_id": "req_01",
  "plugin_id": "com.example.research-workbench",
  "base_version": "1.1.0",
  "target_version": "1.2.0",
  "workspace_ref": "plugin-workspace-ref",
  "requested_permissions": ["files.read", "ai.invoke"],
  "changed_permissions": ["ai.invoke"],
  "validation_status": "passed",
  "preview_ref": "preview-ref",
  "artifact_ref": "signed-candidate-ref",
  "approval_status": "pending",
  "created_by": "agent-id"
}
```

Development Run 必须关联：

- 原始需求和验收标准。
- 使用的模型、Agent 配置、SDK 和插件基线版本。
- 代码及 Manifest 变更。
- 执行过的命令和测试结果。
- 权限新增、删除和风险变化。
- 预览、审核意见和发布决策。
- 灰度指标、停用或回滚记录。

### 15.8 审核与自动化策略

发布门禁按风险分层：

| 变更类型 | 最低要求 |
|---|---|
| 文案、只读视图、无新增权限 | 自动测试通过后可按组织策略自动进入预览或低比例灰度 |
| 新增写入操作或外部系统调用 | 业务负责人和权限负责人审核 |
| 新增 `exec`、高敏数据或外部影响权限 | 安全审核，默认禁止自动发布 |
| 修改核心平台 | 退出插件开发流程，进入独立核心研发流程 |

即使允许低风险插件自动发布，也必须保留：

- 制品签名和不可变版本。
- 小范围灰度。
- 错误率和性能阈值。
- 自动停用或回滚。
- 完整审计记录。

### 15.9 成熟度路线

#### Level 1：AI 辅助插件开发

- 智能体生成代码和测试。
- 开发人员在本地运行、审核和发布。
- 依赖 Phase 0 的 Manifest、SDK、扩展点和脚手架。

#### Level 2：需求到预览

- 用户用自然语言描述工作台需求。
- 智能体独立完成复用分析、实现、构建和测试。
- 平台自动生成可交互预览和权限差异。
- 人员主要负责业务验收和发布批准。

#### Level 3：受控发布

- 低风险插件通过策略自动灰度。
- 高风险变更进入明确的人工审批。
- 平台根据错误率、性能和业务指标自动扩大范围或回滚。

#### Level 4：主动自我扩展

- 智能体从用户反馈、重复操作和能力缺口中发现插件机会。
- 智能体主动提出需求说明、价值、风险和插件实现方案。
- 获得立项授权后进入标准 Development Run。
- 智能体仍然不能修改核心代码或绕过发布治理。

Level 4 的“主动”只表示可以主动提出和实现插件候选，不代表可以自行决定扩大平台权限或修改生产核心。

### 15.10 首个验收场景

建议用“科研项目进度与报告插件”验证完整链路：

```text
需求：
为科研人员增加项目列表、进度 Widget、实验资料上传、AI 分析和报告导出。

期望结果：
1. 智能体发现并复用平台 File、Artifact、AI 和 Notification SDK。
2. 智能体生成独立插件，不修改 Workbench 核心目录。
3. 插件注册 Navigation、Route 和 Command，并复用标准 Dialog、Related Resource 与结果预览能力。
4. Manifest 完整声明权限，Server 对每项操作再次鉴权。
5. 权限、租户隔离、失败和审批测试全部通过。
6. 用户通过临时预览完成业务验收。
7. 插件以不可变版本进入单一测试工作区灰度。
8. 出现错误时只停用或回滚该插件，不影响 Workbench Shell。
```

完成该场景后，才能认为 TMA 初步具备“智能体开发插件、不修改核心代码”的闭环能力。

## 16. 复杂度控制与 MVP 收敛规划

### 16.1 复杂度来源

Workbench 插件化本身可以渐进实现。当前方案显得复杂，是因为以下四个不同阶段的问题被同时展开：

1. 把现有 Workbench 公共能力标准化。
2. 建立可信的前端插件协议和静态注册机制。
3. 建立动态安装、签名、灰度和插件市场。
4. 让智能体自主开发和发布插件。

当前只实施前两项。第三项和第四项保留架构承接点，但不得成为 Phase 0 的交付前置条件。

### 16.2 Phase 0A 唯一目标

Phase 0A 只验证一个产品和工程命题：

> 新增一个真实业务页面及其操作时，不修改 Workbench Shell 核心代码。

首期最小范围固定为六项：

| 类型 | 首期能力 | 说明 |
|---|---|---|
| 插件协议 | `plugin.json` | ID、版本、兼容性、权限和 Contribution |
| 扩展点 | Navigation + Route | 注册菜单和完整业务页面 |
| 扩展点 | Command | 注册有权限和审计的业务操作 |
| 公共服务 | Dialog + Notification | 统一确认、表单、反馈和错误行为 |
| 公共服务 | Related Resource + Preview | 统一相关文件、Artifact 和业务资源 |
| 治理 | Permission + Error Boundary | Server 再鉴权，插件故障局部隔离 |
| 体验 | Multi-surface Validation | 桌面、平板和手机视口的响应式与交互验证 |

Phase 0A 明确不做：

- Widget 和 Detail Panel 扩展点。
- 远程动态 JavaScript 加载。
- 公共插件市场、计费和商业分发。
- 第三方不可信代码沙箱。
- 自动签名和全自动生产发布。
- 智能体主动发现需求并自行上线插件。
- 一次性标准化 Workbench 中的所有组件和交互。

### 16.3 公共能力标准化原则

公共能力按三层整理：

```text
UI Primitive
-> Dialog、Drawer、Menu、Toast、Form、Table、Loading、Empty、Error

Workbench Service
-> Dialog、Notification、Related Resource、Preview、Permission、Command

Semantic Extension Point
-> Navigation、Route、Command，后续再增加 Widget 和 Detail Panel
```

Phase 0A 不要求把所有 UI Primitive 都封装进 SDK，只标准化纵向场景实际使用的能力。

平台与插件的责任边界：

> 插件负责业务内容，Workbench 负责公共交互、资源管理和安全边界。

例如弹窗首期支持四种模式：

```text
confirm
schema_form
searchable_choice
custom_dialog_container
```

Workbench 负责遮罩、层级、焦点、尺寸、键盘操作、取消、未保存提醒、主题和无障碍；插件只提供标题、字段、业务内容和提交 Command。

相关文件首期统一为 `ResourceRef`，不针对每种业务对象设计独立文件协议：

```ts
export interface ResourceRef {
  id: string;
  type: "file" | "artifact" | "task" | "session" | "url" | "business_object";
  title: string;
  mimeType?: string;
  source: string;
  previewable?: boolean;
  metadata?: Record<string, unknown>;
}
```

Workbench 根据权限和资源类型统一提供预览、下载、打开详情和加入任务上下文。插件不得重新实现全局文件选择器或 Artifact 预览框架。

Phase 0A 的资源 Provider 使用稳定 `sourcePrefix` 或 `supports(resource)` 声明负责的资源，并实现 `listRelated`、`preview`、`open` 中适用的方法。Preview Provider 只返回平台定义的 `image | text | download` Descriptor；Provider 创建的 Object URL 必须通过 `dispose()` 交给 Workbench 统一释放。任何 Provider 都不得把 Secret、原始 Token、完整内部对象或永久下载 URL 放入 `ResourceRef.metadata`。

### 16.4 渐进改造策略

当前 Workbench 不需要为了插件化进行整体重写。采用从现有实现向公共服务逐步抽取的方式：

```text
识别真实业务场景需要的现有能力
-> 保持现有页面行为不变，先抽出 Service Interface
-> 让原有页面改用该 Service
-> 通过 Workbench SDK 暴露稳定子集
-> 增加静态 Plugin Registry
-> 用真实插件验证边界
-> 验证后再开放下一个扩展点
```

抽取公共服务时必须先让核心页面成为第一个消费者。只有插件使用、核心页面不使用的“公共服务”，通常仍然只是插件私有封装。

### 16.5 扩展点准入规则

新增扩展点必须同时满足：

1. 至少两个真实业务场景需要。
2. 无法通过现有 Route、Command 或公共服务合理完成。
3. 输入和输出可以形成稳定 Schema。
4. 权限、取消、错误、布局和生命周期行为明确。
5. 插件停用后可以完整注销，不残留全局状态。
6. 有独立测试和向后兼容策略。

不满足上述条件时，能力继续保留在具体插件内部，不进入 Workbench 标准。

### 16.6 Phase 0A 实施顺序

当前进度（2026-07-14）：

- [x] 完成科研纵向场景的 Dialog、Notification、File 和 Preview 盘点，见 [Workbench Phase 0A 公共能力盘点](./workbench-phase0a-capability-inventory.md)。
- [x] 定义并实现 `ResourceRef`、`CommandDefinition` 和最小 `PluginContext` 契约，代码位于 `apps/workbench/src/workbench/contracts.js`。
- [x] 增加契约单元测试和 `apps/workbench` 测试命令。
- [x] 固定桌面、桌面壳、平板和手机的多端适配与验证标准。
- [x] 实现可排队的 Dialog Service 与响应式 Dialog Host，并迁移删除任务和中断任务确认。
- [x] 增加标准 Searchable Choice Dialog，科研插件的任务与 Artifact 选择不再使用长下拉或自定义选择器。
- [x] 实现 Notification Service 与全局响应式 Host，并迁移删除任务和中断任务反馈。
- [x] 实现 Related Resource Service、Session Artifact Adapter，并把现有 Artifact Preview 接入 `ResourceRef`。
- [x] 实现静态 Plugin Registry、v1 Manifest Schema 与 Navigation/Route/Command 注册。
- [x] Workbench Shell 接入静态 Catalog、Navigation/Route Subscription、Scope Enablement、Route Permission Guard 与 Error Boundary。
- [x] 增加内置扩展诊断插件，验证 Manifest、Navigation、Route、Command、Notification 和多端布局链路。
- [x] 增加构建期插件 package 自动发现，新增插件目录不再修改 Shell 或共享 Catalog 清单。
- [x] 增加 `plugin:create` 脚手架和 `plugin:check` 确定性校验，覆盖 Manifest、入口、重复 ID、package 文件和目录越界导入。
- [ ] 完成科研项目与报告插件纵向验证（项目阶段、搜索筛选、资料、研究发现、摘要导出已进入前端验证；AI 报告生成和生产持久化仍未完成）。

本地可信插件开发入口：

```bash
cd apps/workbench
npm run plugin:create -- --id com.example.due-diligence --name 企业尽调
npm run plugin:check
npm test
npm run build
```

脚手架只在 `src/plugins/{pluginPackage}/` 下创建 `plugin.json`、独立实现、Route 页面、样式、package 导出和测试。Vite 只在构建期扫描 `src/plugins/*/package.js(x)`；这不是远程加载、动态代码执行或插件市场。目录已存在时脚手架必须失败，验证器禁止插件通过 `../` 或绝对路径导入 Shell 与其他插件源码。

```text
1. 盘点科研纵向场景使用的 Dialog、Notification、File 和 Preview 行为
2. 定义 ResourceRef、Command 和最小 PluginContext
3. 从现有 Workbench 抽取 Dialog、Notification 和 Related Resource Service
4. 实现静态 Plugin Registry 与 Manifest 校验
5. 实现 Navigation、Route 和 Command 注册
6. 增加租户/角色启用、权限检查和 Error Boundary
7. 开发科研项目与报告插件
8. 验证上传资料、AI 分析、结果预览和导出闭环
9. 根据真实问题修订 v1 协议
10. 再决定是否进入 Widget、Detail Panel 或动态加载
```

### 16.7 首个纵向验收场景

```text
科研项目插件
-> 注册“科研项目”菜单和页面
-> 使用标准 Dialog 创建或编辑项目
-> 使用 Related Resource 选择实验资料
-> 通过 Command 调用已有 AI 能力
-> 使用统一 Preview 展示报告
-> 导出 Artifact 或加入下一轮任务上下文
```

当前纵向实现采用“前端先验证、后端暂不固化”的边界：

- 插件目录为 `apps/workbench/src/plugins/researchProjects/`，通过静态 Catalog 注册，不修改 Shell Router 和业务页面。
- 当前支持创建、编辑、归档和恢复科研项目，按进行中/已归档筛选并搜索项目、目标与下一步。
- 项目详情包含概览、资料和研究发现三个工作视图；概览展示研究阶段、下一步、资料/发现/待验证统计和最近沉淀，研究发现区分结论、假设与待验证问题。
- 插件先通过宿主 `TaskService.list` 让用户选择最近任务，再调用 `ArtifactService.list` 获取该任务已有成果，转换为标准 `ResourceRef` 关联到项目，不要求用户手工输入 Session ID，也不直接拼 `/v2` URL；资料记录来源 Session 和关联时间。
- Markdown 项目摘要包含研究阶段、目标、范围、下一步、研究发现和关联成果，不生成未经 Agent 处理的伪 AI 结论。
- 项目摘要由确定性模板导出为 Markdown，不冒充 AI 生成结论；AI 报告生成必须等任务调用契约稳定后再接入。
- 项目草稿当前使用插件私有 Repository 和浏览器存储开发适配器，按 `workspace_id + user_id` 分区。它不是生产级插件存储，不承担服务端权限、审计、共享、备份或跨设备同步。
- 当前不修改 Task、Session、Artifact、Organization、Workspace、User、Role 和 Permission 的数据模型，也不新增科研业务表。
- 至少第二个真实插件证明 Record 数据形态可以复用后，才评审是否提供通用插件存储；复杂业务直接使用插件独立后端，不能把任意业务 JSON 长期堆入核心 Store。
- 平板和手机打开插件路由时，Shell 隐藏无关的智能体选择与任务列表，只保留扩展导航、设置和返回工作台入口，避免插件主流程被长任务列表推到首屏之后。

验收必须证明：

- 插件没有修改 Shell、Router、Auth、Permission 和公共文件面板核心代码。
- 禁用插件后，菜单、路由、Command 和订阅全部消失。
- 弹窗、通知、文件和预览体验与核心页面一致。
- 插件只能使用 Manifest 声明且当前用户获准的能力。
- 插件失败只影响自己的页面或操作。
- 新增第二个相似业务插件时能够复用同一套标准。
- 桌面、平板和手机均能完成创建项目、选择资料、生成报告、预览和导出流程。

### 16.8 进入下一阶段的条件

满足以下条件后才进入 Phase 0B 或 Phase 1：

- 科研纵向场景稳定运行。
- 至少第二个业务需求证明现有协议可复用。
- Manifest 和 SDK 不再因每个新页面频繁修改。
- 核心页面与插件共同使用公共 Workbench Service。
- 权限、错误隔离和停用清理经过测试。
- 团队能够在不修改 Shell 的情况下交付新插件。

如果这些条件尚未成立，继续完善静态插件和公共服务，不启动动态加载、插件市场或智能体自动发布。
