# Workbench Frontend Product Gap Plan

本文档基于当前 TMA Workbench 截图和 Codex 桌面截图，重新从“用户前台体验”分析差距。重点不是运维 Trace、Usage 或内部观测，而是用户打开应用后是否能理解任务、看见进展、管理文件、确认风险、拿到结果。

企业工作台的插件化边界、扩展点、Manifest、SDK、权限和分阶段实施要求见 [TMA Workbench 插件开发标准](./workbench-plugin-standard.md)。本文档负责默认工作台体验，插件标准负责不同企业、部门和岗位如何在稳定 Shell 上扩展业务页面与操作。

## 一句话判断

当前 Workbench 已经从调试壳迈出了第一步：有三栏布局、聊天、审批、Activity、Artifacts 和 Session 信息。但它仍然更像“Agent runtime 可视化面板”，还不像一个面向用户的 Codex 桌面工作台。

最大差距不是能力缺失，而是信息架构还没按用户心智组织：

```text
用户想看到：
任务在做什么 -> 用了哪些资料/文件 -> 改了什么 -> 产出了什么 -> 哪些动作需要确认

当前更像看到：
session 状态 -> runtime event -> raw tool call -> artifact list -> session metadata
```

## 截图暴露的主要问题

### 1. 聊天区泄漏了内部协议

当前最严重的问题是 assistant message 直接展示了类似：

```text
<seed:tool_call><function name="web_search">...</function></seed:tool_call>
```

这会让用户感觉产品“坏了”或“还在调试”。用户不应该看见模型和工具之间的原始协议。

应改为：

```text
我准备搜索网页：“2025 年最新 AI 新闻”
状态：等待执行 / 已执行 / 需要确认 / 失败
结果：找到 5 条候选新闻
```

### 2. Activity 面板过细，重复事件太多

右侧 Activity 已经比纯日志好，但仍然出现很多重复的 `Writing reply`。这对开发调试有帮助，对普通用户没有帮助。

用户需要的是阶段性状态：

```text
正在思考
正在搜索网页
正在读取页面
正在整理答案
已完成
```

而不是每个 token streaming、span event 或内部 runtime tick。

### 3. 右侧优先级反了

当前右侧顺序是：

```text
Activity
Artifacts
Session
```

但用户最关心的是结果和可操作对象。建议改成：

```text
Results / Files
Activity
Details
```

其中 Details 才放 session id、agent id、environment id、原始事件入口等专家信息。

### 4. 左侧任务列表像日志列表，不像工作台导航

最近聊天列表现在信息密度高、标题截断明显、状态重复，缺少分组、搜索、固定、归档后的清理。Codex 桌面左侧的价值不是“列出 session”，而是让用户能回到项目、任务和上下文。

建议左侧心智改为：

```text
新建任务
当前项目
任务列表
已固定 / 最近 / 已归档
```

### 5. 任务控制按钮太运维化

`Interrupt`、`Retry last prompt`、`Archive`、`Delete` 全部裸露在左侧主区域，会让用户感觉自己在操作后台任务。

更好的分层：

```text
主操作：New Task, Send, Attach
运行中才出现：Stop
更多菜单：Retry, Archive, Delete
危险操作：二次确认
```

### 6. 文件输入闭环已形成，Review 闭环仍待完善

Codex 桌面截图右侧有文件树，底部有“已编辑文件 / review”卡片。当前 Workbench 已完成“上传文件 -> 作为结构化上下文 -> Agent 从云沙箱读取”，但完整 Review 工作流仍未形成：

```text
上传文件 -> 作为上下文使用 -> Agent 读取/修改 -> 预览结果 -> Review diff -> 下载/继续追问
```

对未来 Codex 桌面功能来说，文件闭环比 Usage/Trace 更重要。

## 目标信息架构

建议 Workbench 主界面按用户任务重新组织为三栏：

```text
左侧：任务与项目
  - New Task
  - 当前项目 / workspace
  - 最近任务
  - 固定任务
  - 已归档入口

中间：任务对话与动作流
  - 用户消息
  - Agent 自然语言回复
  - 工具动作卡
  - 审批卡
  - 完成总结卡
  - Composer + Attach

右侧：结果与上下文
  - Results / Artifacts
  - Files / Workspace
  - Changed files / Diff
  - Activity 折叠摘要
  - Details 专家信息
```

主界面默认不展示 Trace、Usage、span、raw event、session JSON。它们应该存在，但放在 `Details` 或 `Open Inspector`。

## P0: 先把 Workbench 变成“用户能读懂的任务界面”

P0 目标：用户不需要理解 runtime，也能看懂 Agent 正在做什么、为什么停住、下一步该点什么。

### P0.1 隐藏原始 tool XML / seed 协议

把 assistant message 中的 raw tool call 解析或过滤掉，渲染成动作卡。

建议动作卡类型：

```text
Search web
Open browser
Read file
Edit file
Run command
Ask approval
```

验收标准：

- 聊天区不再出现 `<seed:tool_call>`。
- 工具调用以卡片形式展示名称、目标、状态、结果。
- 工具失败时卡片显示用户可理解原因。

### P0.2 合并 Activity 噪音

Activity 面板不消费原始 Provider chunk。实时文本来自不落库的 `llm.text` Live SSE，最终以持久化 `agent.message` 收敛：

```text
连续 Writing reply -> 显示一条“正在写回复”
连续 tool step -> 合并成“已执行 3 个操作”
idle/running/status -> 只显示最新状态
```

验收标准：

- 同一秒内重复 streaming event 不刷屏。
- 用户能在 5 秒内理解当前任务状态。
- Activity 可展开查看详情，但默认保持简洁。

### P0.3 重排右侧面板

把右侧第一优先级从 Activity 改成 Results / Files。

建议顺序：

```text
Results
Files
Activity
Details
```

验收标准：

- 有 artifact 时优先可见。
- 没有 artifact 时显示“本任务还没有生成文件”，并提示可让 Agent 创建/导出。
- Session metadata 默认折叠。

### P0.4 主操作去运维化

把裸露的任务管理按钮收敛。

建议：

```text
New Task: 常驻
Refresh: 弱化或自动
Stop: 仅运行中出现
Retry / Archive / Delete: 放 More 菜单
Connection: 默认折叠到设置或 Details
```

验收标准：

- 新用户不会一眼看到 Delete/Archive 作为主要按钮。
- 运行中只有一个明确的停止入口。
- 危险操作有确认。

### P0.5 增加完成总结卡

每轮任务结束后，中间聊天流应该出现一张总结卡，而不是只靠最后一句话和右侧 Activity。

建议内容：

```text
本轮完成了什么
使用了哪些工具
生成了哪些文件
修改了哪些文件
建议下一步
```

验收标准：

- 用户能从一张卡知道结果在哪里。
- 文件、预览、diff 可直接跳转。

## P1: 做出文件与交付物闭环

P1 目标：让 Workbench 不只是聊天，而是能围绕文件完成工作。

### P1.1 Composer 支持附件

在输入框旁增加 Attach。

支持：

```text
上传本地文件
引用已有 artifact
引用 workspace 文件
移除附件
```

当前进度（2026-07-13）：

- [x] 点击附件按钮或拖放上传本地文件。
- [x] 发送前预览、移除、上传进度和失败重试。
- [x] 最多 10 个文件，单文件 64 MB；文件字节落 object store，event 仅保留 object/artifact ref 与元数据。
- [x] 云沙箱将上传文件同步到 `/workspace/uploads/{artifact_id}/{filename}`，模型上下文获得精确路径。
- [ ] 从 Composer 引用已有 artifact。
- [ ] 从 Composer 引用 workspace 文件。

格式说明：文本、代码、Markdown、JSON 和 CSV 可直接读取分析；PNG/JPEG/GIF/WebP 可由当前 `text_image` 模型或设置中的统一视觉模型解析；PDF/Office 需要沙箱解析工具或文档 Skill。

验收标准：

- 用户能把图片、文档、代码文件加入下一轮上下文。
- 附件在发送前可见。
- 大文件只传 object ref / metadata，不直接塞进 prompt。

### P1.2 Artifacts 升级为 Results

Artifacts 不应只是下载列表，而是结果工作区。

支持：

```text
图片内联预览
文本/Markdown/JSON 预览
代码文件高亮或纯文本预览
PDF/Office 文件下载 fallback
复制内容 / 下载 / 用于下一轮
```

验收标准：

- 用户能直接查看常见产物。
- 每个产物知道来自哪一轮、哪个工具。
- 产物可以重新作为上下文。

### P1.3 Changed files / Review 卡片

对 Codex 桌面功能来说，“改了什么”是核心。

建议在聊天底部或右侧 Results 中显示：

```text
已编辑 2 个文件
+133 -1
Review
```

验收标准：

- Agent 修改文件后，用户能看到文件列表和 diff 统计。
- 点击 Review 可看 diff。
- 用户可接受、继续修改或要求回退。

### P1.4 轻量文件树

先不做完整 IDE，但要有 workspace 文件浏览。

第一版支持：

```text
打开目录
搜索文件
预览文件
选中文件加入上下文
显示改动状态
```

验收标准：

- 用户能理解当前任务在哪个 workspace 下运行。
- 文件读写有路径边界。
- 右侧文件树和聊天中的文件引用可互相跳转。

## P2: 进入 Codex 桌面级能力

P2 目标：从 Web Workbench 变成桌面开发工作台。

### P2.1 本地 workspace 授权

桌面端需要让用户选择本地目录，并清楚展示 Agent 可访问范围。

验收标准：

- Agent 只能访问用户授权目录。
- 文件读取、写入、命令执行有明确权限边界。
- UI 能解释“为什么需要权限”。

### P2.2 本地 worker 生命周期

用户不应该手动启动 worker。

桌面端应管理：

```text
worker 启动/停止
心跳状态
能力注册
异常重启
权限缺失提示
```

验收标准：

- 打开桌面端即可使用本地能力。
- worker 异常时用户看到可恢复提示，而不是日志。

### P2.3 文件 diff 与命令输出

Codex 桌面核心不是完整 IDE，而是可信地展示 Agent 做了什么。

支持：

```text
命令卡：command, cwd, exit code, stdout/stderr 摘要
文件卡：read/edit/write, diff, review
审批卡：风险、影响范围、允许/拒绝
```

验收标准：

- 用户能审查高风险操作。
- Agent 修改的内容可被 diff 解释。
- 命令失败能自然追问或重试。

## 不建议优先做

以下能力可以保留，但不要放在主体验优先级：

- Trace 主面板
- Usage 主面板
- Span waterfall
- Raw event viewer
- Session / Environment / Agent ID 常驻展示
- 完整 IDE 编辑器
- 运维大屏

它们不是没用，而是不该成为用户第一次打开 Workbench 看到的东西。更合理的位置是：

```text
Details
Open Inspector
Debug mode
Admin / Ops workspace
```

## 建议下一步实现顺序

```text
1. 过滤/解析 seed tool_call，聊天区不再显示 raw XML
2. 把 tool_call / tool_result 渲染成动作卡
3. Activity 聚合去重，默认只显示阶段状态
4. 右侧改为 Results first，Session details 默认折叠
5. 主操作按钮收敛：Stop 运行中出现，Retry/Archive/Delete 放 More
6. Composer 增加 Attach 的 UI 骨架
7. Artifact 增加“用到下一轮”入口
8. 增加本轮完成总结卡
9. 增加 changed files / diff summary 卡
10. 再进入 workspace 文件树和桌面本地 worker
```

## 第一版验收场景

用一个简单用户任务验收：

```text
用户：搜索 5 篇 AI 新闻，整理成 Markdown 文件

期望界面：
1. 聊天区显示“准备搜索网页”的动作卡，不显示 raw XML。
2. Activity 只显示“搜索网页 -> 读取页面 -> 整理答案 -> 已完成”。
3. Results 第一时间出现 Markdown 文件，可预览、下载、继续作为上下文。
4. 结束时出现总结卡：搜索了什么、生成了什么文件、下一步可以做什么。
5. Session id、raw events、usage 不出现在默认主界面。
```

如果这条链路成立，Workbench 才真正从“能跑 Agent 的调试页”进入“用户能持续使用的工作台”。
