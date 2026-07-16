# Codex Desktop Workbench Gap Roadmap

本文档把当前 `apps/workbench` Workbench 的真实状态，和未来面向 Codex 桌面功能开发所需的能力做一次收敛。它不是排期承诺，而是用来判断下一步先补什么、哪些能力必须进桌面壳、哪些能力应继续留在 TMA runtime。

## 当前判断

当前 Workbench 已经具备最小闭环：

```text
Session
  -> upload file -> object ref / session artifact
  -> structured attachments -> user.message
  -> runner / AgentRuntime
  -> /workspace/uploads/{artifact_id}/{filename}
  -> read_file / run_command / execute_code
  -> event history
  -> approval
  -> artifact download
```

这证明后端主链路是成立的，但前端还主要是调试壳。它能发起任务、恢复最近 session、展示聊天、审批工具调用、下载 artifact；还不能承担桌面开发工作台的职责。

最大差距不是“有没有 API”，而是“Workbench 是否把 runtime 能力组织成开发者能连续工作的界面”。当前后端已有 SSE、trace、summary、usage、artifacts、workers、runtime settings、object refs 等能力，主工作台只接了其中很小一部分。

## 产品目标

未来桌面工作台应该是：

```text
本地桌面壳
  -> 加载本地 SPA
  -> 登录 / SSO / token safeStorage
  -> API 代理注入认证
  -> 本地工作区、文件、终端、系统能力桥接
  -> TMA runtime 负责长跑 agent / sandbox / event / audit
```

用户心智应简化为：

```text
任务
工作区
运行环境
变更
审批
产物
```

而不是要求用户理解 `agent_id`、`environment_id`、`session_id`、`tool_runtime`、`cloud_sandbox_allow_network` 这些底层字段。

## P0: 让 Workbench 可作为长跑任务主界面

P0 的目标是把“能跑”变成“看得懂、跟得上、可恢复”。

1. 接入 SSE 事件流

当前 `apps/workbench` 使用 `waitForReply` 轮询 session、events、interventions、artifacts。应改为基于 `/v1/sessions/{session_id}/events/stream?after_seq=...` 建立 EventSource，断线后用 `after_seq` 补历史。

验收标准：

- 发送消息后 UI 能实时追加 runtime / tool / agent events。
- 刷新页面后能从最后 seq 恢复。
- 长任务超过 60 秒时 UI 不丢失运行状态。
- 审批完成后不依赖轮询等待下一条 agent message。

2. 主界面展示运行过程

聊天流不能只显示 `user.message` 和 `agent.message`。应把关键 runtime events 映射为任务时间线。

建议展示：

- `runtime.started`
- `runtime.thinking`
- `runtime.llm_request`
- `runtime.tool_call`
- `runtime.tool_result`
- `runtime.tool_intervention_required`
- `runtime.failed`
- `session.status_idle`

验收标准：

- 用户能看到 Agent 正在调用哪个工具。
- 工具失败时能看到失败原因。
- 审批卡片和对应 tool call 在同一条时间线上。
- `agent.message` 仍然是最终回复，但不是唯一可见状态。

3. Session 生命周期操作补齐

当前有新建、刷新、打开最近聊天，但缺少 archive、delete、interrupt、retry、resume 等桌面工作台常用操作。

优先补：

- Archive session
- Delete session
- Interrupt current turn
- Retry failed turn
- Open inspector for current turn

验收标准：

- 用户不需要 CLI 才能清理或中断任务。
- 失败任务能从 UI 定位到 trace / events。
- 最近任务列表不会被已归档任务污染。

4. Artifact 预览进入主界面

当前只提供下载链接。Codex 桌面体验需要能直接预览常见产物。

优先支持：

- text / markdown / json
- image
- pdf download fallback
- artifact metadata
- artifact 来自哪个 turn / tool call

验收标准：

- 图片类 artifact 可内联预览。
- 文本类 artifact 可查看内容摘要。
- 每个 artifact 能追溯 turn 和 tool call。

## P1: 让 Web Workbench 进化成开发工作区

P1 的目标是把 Workbench 从聊天页面变成开发者工作台。

1. 工作区模型

把 `Environment` 和文件归属收束成用户可理解的 `Workspace`。

建议 UI：

- Cloud workspace
- Local workspace
- Device workspace

内部映射：

- Cloud workspace -> `cloud_sandbox`
- Local workspace -> `local_system`
- Device workspace -> worker / device gateway

验收标准：

- Web 端不出现“本机执行”误导选项。
- Desktop 端选择本机工作区时能清楚展示路径。
- 切换 workspace 时提示“当前 turn 不受影响，下一轮生效”。

2. 文件面板

桌面开发离不开文件系统。第一版不需要完整 IDE，但需要任务相关文件的可见性。

优先支持：

- session artifacts
- uploaded files
- object refs
- sandbox output files
- local workspace selected path

当前进度（2026-07-13）：

- [x] Composer 支持附件按钮、拖放、多文件、移除、进度和失败重试。
- [x] 上传文件持久化为 object ref + session artifact，不把二进制内容直接塞入 event payload。
- [x] `user.message.payload.attachments` 保留 artifact id、object ref id、MIME、size 和 `workspace_path`。
- [x] 云沙箱在 Agent 首次读取前同步文件到 `/workspace/uploads/{artifact_id}/{filename}`。
- [x] `ContextBuilder` 向模型提供精确路径，Agent 可以用 `read_file` / `run_command` / `execute_code` 读取和分析。
- [x] 用户消息恢复后可继续预览、下载附件。
- [ ] 生成文件的 diff / review 及“再次加入上下文”仍需补齐。
- [ ] `local_system` / device workspace 的用户上传落盘与路径语义需要单独设计；当前完整闭环以 `cloud_sandbox` 为准。

格式边界：文本/代码/Markdown/JSON/CSV 可直接读取；PDF/Office/图像的内容提取依赖沙箱中已安装的命令、库或文档处理 Skill。文件上传成功只代表 Agent 可访问该文件，不代表运行时已内置所有格式的解析器。

验收标准：

- 用户能上传文件作为任务输入。
- Agent 生成的文件能预览、下载、再次加入上下文。
- sandbox 和 local 的文件归属有明确标识。

3. 终端和命令输出

Codex 桌面类产品的关键体验是“Agent 做了什么命令、结果是什么、是否需要我接管”。

第一版可以不做完整 PTY，但要有命令事件视图：

- command
- cwd
- exit code
- stdout / stderr preview
- duration
- approval reason

验收标准：

- `default.run_command` 的输入输出能从 UI 查看。
- 高风险命令进入 approval。
- 长输出进入 artifact 或 object ref，不撑爆 event payload。

4. Trace/Inspector 与主界面打通

`/inspector` 可以继续作为专家页，但主界面需要给出入口和摘要。

建议：

- 当前 turn 显示 trace status。
- 每个 failed/tool event 提供 `Open Inspector`。
- 右侧 panel 展示 context budget、usage、summary。

验收标准：

- 用户从失败消息一键进入对应 trace。
- 不需要手动复制 session_id / turn_id。

## P2: 桌面壳与 Codex 级本地能力

P2 的目标是形成真正桌面客户端，而不是浏览器里打开 `/app`。

1. Electron / Tauri 桌面壳

能力清单：

- 本地 SPA bundle
- OAuth / SSO
- token 存 Keychain / safeStorage
- API proxy 注入认证
- 自动更新
- 企业配置下发
- deep link 打开任务或文件

验收标准：

- 渲染进程不直接持有长期 token。
- 企业可配置 API base URL。
- 登录状态可安全持久化。

2. 本地 worker 生命周期托管

桌面端应能管理本机 worker，而不是要求用户手动跑 `bin/tma-worker`。

能力清单：

- 启动 / 停止 local worker
- 查看 worker online / heartbeat
- 注册本机 capabilities
- 本机路径授权
- worker 日志查看

验收标准：

- 用户打开桌面端后，本机 worker 可自动注册。
- 权限不足时 UI 能引导开启系统权限。
- worker 异常退出后能提示并重启。

3. 本机文件和终端桥接

Codex 桌面要能安全访问用户明确授权的本地 workspace。

能力清单：

- 选择本地目录
- 路径 guard
- 文件读写 diff
- 命令执行审批
- 终端输出流
- 应用内打开文件

验收标准：

- Agent 只能访问授权目录。
- 文件修改必须能审查 diff。
- 执行命令前能按策略请求审批。

4. Computer Use 与 Browser Takeover

TMA 已有 `computer.*` 插件方向，桌面壳要把它变成可用入口。

能力清单：

- screenshot artifact 预览
- accessibility permission 状态
- browser takeover 操作入口
- 用户接管完成回传
- 高风险桌面操作审批

验收标准：

- 桌面权限缺失时有清晰状态。
- 用户可从 Workbench 发起或结束接管。
- 关键操作进入 event 审计。

## 不建议现在做

以下事项容易消耗大量时间，但不能立刻改善主体验：

- 先抽完整前端组件库。
- 先做多租户后台管理大屏。
- 先做复杂 plugin marketplace。
- 先做完整 IDE 编辑器。
- 先把所有 Inspector 功能搬到主界面。

更好的节奏是先把 P0 跑顺：SSE、任务时间线、artifact 预览、session 操作。只要 P0 成立，Workbench 就从“能发消息的壳”变成“能陪一个长任务跑完的界面”。

## 推荐实施顺序

```text
1. EventSource hook
2. event reducer / session state machine
3. timeline components
4. approval 与 timeline 合并
5. artifact preview
6. session actions: archive / interrupt / retry
7. workspace selector
8. desktop shell proof of concept
9. local worker managed by desktop shell
10. local filesystem / terminal bridge
```

## 第一阶段验收场景

第一阶段可以用以下脚本或手动路径验收：

```text
创建 session
发送需要工具调用的消息
UI 实时出现 runtime.started
UI 实时出现 tool_call
需要审批时出现 approval card
approve 后 UI 继续出现 tool_result
最终出现 agent.message
artifact 出现在右侧并可预览
刷新页面后事件不丢失
```

这条链路跑通后，Workbench 才真正具备 Codex 桌面产品的骨架。
