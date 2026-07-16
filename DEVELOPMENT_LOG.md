# TMA 研发记录

本文档记录 Tiggy Manage Agent (TMA) 的阶段性研发决策、实现内容、验证结果和后续待办，方便之后回溯为什么这样设计。

最后更新：2026-07-14

---

## 当前结论

2026-07-14 Workbench 结果预览增加“预览 / MD”切换：Markdown 产物默认以 GFM 语义渲染标题、列表、代码块、表格和任务列表，“MD”模式保留原始源码查看；普通文本、代码、JSON 和图片继续使用原有预览，非 Markdown 文件的 MD 入口置灰，避免产生错误语义。切换状态在打开新文件或关闭预览时重置。

2026-07-14 将智能体管理入口统一收口到 `设置 > Agent`：工作区侧栏移除“编辑智能体”和“新建智能体”，只保留智能体选择与“新建任务”；Agent 设置页新增可用的“新建 Agent”表单，支持名称和默认模型，创建后自动选中并可继续使用完整配置编辑器维护 System Prompt、Tools、Skills、MCP 与版本。

2026-07-14 优化 Workbench 附件入口：从审批设置栏前移除回形针按钮，在输入文本区下方、设置栏上方增加独立的“+”上传按钮；保留原文件选择、数量限制和禁用逻辑，并避免窄屏工具栏换行时发生重叠。

2026-07-13 压缩 Workbench 任务列表密度：任务卡片上下内边距从 8px 降到 4px、列表间距降到 6px，同时保留 36px 操作按钮点击区。修复运行状态圆环不显示：消息发送响应和事件流中的 Session 状态事件都会立即同步当前任务与列表状态；等待回复阶段也会立即把当前任务显示为旋转圆环。

2026-07-13 修复任务列表置顶与菜单交互：取消置顶 API 明确返回 `pinned_at: null`，前端置顶/取消采用乐观更新并在失败时回滚，无需刷新即可连续切换；更多操作菜单在点击菜单和触发按钮之外的空白区域、滚动或窗口尺寸变化时自动关闭。

2026-07-13 Workbench 任务列表卡片简化为只显示标题，不再在标题下展示摘要或标签小字；运行中与中断中的状态点改为旋转圆环；置顶和更多操作按钮扩大到 36px 点击区，去除会遮挡菜单的原生 `title` tooltip，并为置顶、菜单展开状态增加明确反馈。更多操作菜单改为基于按钮位置的页面顶层浮层，自动选择向上或向下展开，避免被滚动任务面板裁切。

2026-07-13 Workbench 将“回车发送，Shift+Enter 换行。”合并到输入框灰色占位提示，移除工具栏中的静态快捷键与任务启动提示；运行中的动态状态提示保持不变。

2026-07-13 Workbench 输入框下方工具栏移除“模板”入口，保留欢迎页任务模板卡片与已有工作流能力，减少常用输入区的非必要操作项。

2026-07-13 完成模型能力与图片视觉路由：`llm_models` 新增 `capability_type` 和全局唯一的 `is_default_vision`，支持 `text`、`text_image`、`image_generation`、`video_generation` 四种显式类型。Workbench 模型管理可编辑模型能力，并通过独立的“统一图片视觉模型”选择器配置视觉兜底。当前模型是 `text_image` 时，Runtime 直接发送 OpenAI Chat Completions 兼容的图文 `image_url` 内容；当前模型是纯文本时，先调用统一视觉模型提取描述/OCR/任务信息，再把分析文本交回当前模型。两者都未配置时，Composer 会明确提示并阻止发送，Runtime 也会拒绝 API 绕过。

2026-07-13 图片输入安全边界：Runner 只在当前轮从 object store 按 `object_ref_id` 读取 PNG/JPEG/GIF/WebP，校验 workspace 与真实文件签名，单张限制 20 MB、单轮合计限制 40 MB。Base64 data URL 仅出现在发往模型的内存请求中，不进入 event payload/PostgreSQL。运行时会汇总视觉预处理与主模型的 token usage，并以 `phase=vision_analysis` 的 LLM request/response 事件记录路由。当前实例已将 `volcengine-agent-plan / doubao-seed-2.0-pro` 标记为 `text_image` 并设为统一视觉模型。

2026-07-13 打通 Workbench 用户文件上传与 Agent 分析闭环：Composer 支持点击附件按钮、拖放、多文件预览、移除、上传进度和失败重试，最多 10 个文件且单文件上限 64 MB。前端在发送 `user.message` 前通过 `POST /v1/sessions/{session_id}/artifacts/upload` 持久化 object ref 与 session artifact，并把 `artifact_id`、MIME、size 和服务端返回的 `workspace_path` 作为结构化 `attachments` 写入用户事件。云沙箱将 artifact 同步到 `/mnt/data/uploads/{artifact_id}/{filename}`；`ContextBuilder` 只向模型上下文注入这些精确路径，不把内部路径显示在聊天正文。Agent 因此可以直接使用 `default.read_file`、`default.run_command` 或 `default.execute_code` 读取和分析上传文件；仅选择附件、不输入文字时，也允许以默认“请处理我上传的文件”发起任务。文件历史恢复后仍可预览/下载；中文文件名在沙箱路径中保留。

2026-07-13 为上传闭环补齐运行时回归：`OnlyboxesProvider.ReadFile/EditFile` 在首次访问 `/mnt/data` 前主动同步 session artifacts，避免必须先执行一次 command 才能看到上传文件。测试覆盖上传 API/object store、`workspace_path` 响应、中文文件名安全路径、附件模型上下文以及“首次直接 `read_file`”。已通过 `go test ./...`、`npm --prefix apps/workbench run build` 和 Workbench 桌面/窄屏布局点检。

当前边界：文本、代码、Markdown、JSON 和 CSV 可直接使用 `read_file` 分析；PNG/JPEG/GIF/WebP 现在可由 `text_image` 模型或统一视觉模型理解。PDF、DOCX、XLSX 和 PPTX 仍需要沙箱镜像中的对应解析命令/库，或已启用的文档处理 Skill。

2026-07-10 补上真实浏览器 Inspector smoke：新增 `make verify-inspector-browser-smoke`，会 build `web/`、启动真实 TMA server、启动 headless Chrome，通过 HTTP API 造 25 条 trace、25 个 session 内 span 和一个大文本 artifact，然后在 `/inspector` 页面实际点击 Recent Traces `Load more`、Session filter、Span Search `Load more`、trace card、artifact `Preview`，并校验 Timeline 截断提示、artifact 10KB 截断提示和 console error 计数。`web/` 前端源码未被 `.gitignore` 忽略，后续提交需要把 `web/index.html`、`web/app.html`、`web/src`、`web/public`、`web/package*.json`、`web/vite*.config.js` 一并纳入版本控制。

2026-07-10 继续推进 Inspector 四项完善：`GET /v1/traces` / `GET /v1/spans` 增加 `offset` 分页并返回 `limit`、`offset`、`next_offset`、`has_more`，React Inspector 的 Recent Traces / Span Search 增加 `Load more`；新增 `runtime.span_started` / `runtime.span_event` / `runtime.span_ended` 常量和投影测试，trace projector 可按原生生命周期事件生成 span；`verify-inspector-ui` 现在依赖 `build-inspector-ui`，静态资源从 `web/src` 经 Vite 构建；Timeline 和 Artifact Preview 会显示截断元数据，避免误判大结果内容。

2026-07-10 收敛审批 continuation 执行边界：approve/reject HTTP Handler 现在只持久化决定并唤醒后台 `WorkerRunner`，不再绑定 `r.Context()` 直接执行工具、调用 LLM 或维护独立 Tool Loop。Postgres 决策事务把 turn 标记为可恢复并保存 `resume_intervention_call_id`，Runner 通过 lease/claim 在当前进程或服务重启后领取；`runner.TurnRequest` 携带持久化 intervention，`AgentRuntimeTurnExecutor` 解码 continuation 后统一交给 `agentruntime.Runtime`，批准、拒绝 observation、后续 Tool Loop 和二次审批共享同一套运行语义。相同决定支持幂等重试，已完成 turn 不会重复执行。

2026-07-10 收敛 Inspector 大文本展示和事件体积：artifact 文本/JSON/XML/YAML inline preview 从 64KB 降到 10KB，只用于页面快速查看，完整内容仍通过 Download / Copy CLI 获取；runtime tool result 事件和 intervention continuation 工具结果事件统一使用截断后的 observable payload，避免大文件、大日志、长文本结果直接撑爆 Timeline / Raw JSON，完整工具结果仍保存在 session artifact。

2026-07-10 对 Inspector 做了一轮真实浏览器 smoke：`make verify-inspector-ui` 通过后，用 `/inspector` 页面实际点击 Recent Traces 和 Span Search 卡片，确认能加载 trace、定位 span、渲染 Waterfall/Spans/Timeline/Raw JSON，且 console 无 error。过程中修正了 Inspector catalog 卡片的点击可访问性与布局溢出问题：可点击 trace/span/turn 条目改为语义化 `button.turn-item`，turn 事件绑定限定在 `#turns`，catalog 卡片绑定限定在各自容器；CSS 补 `min-width:0` / `overflow-wrap:anywhere`，避免长 JSON/路径把侧栏卡片撑到主内容区导致点击落点错误。`TESTING.md` 已补浏览器 smoke 步骤。

2026-07-10 继续完善 Inspector catalog 收窄工作流：`GET /v1/traces` 支持 `session_id` / `turn_id` 过滤，索引命中和 fallback 投影路径都按 session 收敛；Inspector 的 Recent Traces 增加 `Filter by Session` / `Clear`，并让 Span Search 跟随当前 Session / Turn。Session 输入框按 Enter 可直接过滤，Trace ID 按 Enter 可加载，Span Search 输入框按 Enter 可搜索。静态验收和浏览器 smoke 文档已同步。

2026-07-10 补上 trace/span index retention 的 server 后台 worker：新增 `TMA_TRACE_INDEX_RETENTION_ENABLED`、`TMA_TRACE_INDEX_RETENTION_DAYS`、`TMA_TRACE_INDEX_RETENTION_INTERVAL_MS`、`TMA_TRACE_INDEX_RETENTION_LIMIT`。默认关闭；开启后只删除过期 `trace_indexes` 并级联 `trace_span_indexes`，不删除 `session_events` 事实源，缺失索引仍可由 HTTP fallback 重新投影回填。`cmd/tma-server` 与 `serverconfig` 单元测试、配置文档和 observability 文档已同步。

2026-07-10 给 trace/span 补第一版持久索引和 retention 能力：新增 `trace_indexes` / `trace_span_indexes` migration、可选 `TraceIndexStore`、Postgres/testStore 实现，以及 `PruneTraceIndexes(before, limit)`。`session_events` 仍是事实源；`GET /v1/sessions/{id}/trace` 生成完整 trace 后会刷新索引，`/v1/traces` 和 `/v1/spans` 优先读索引，索引不足时回退投影并回填。测试文档已补 focused 命令和 Postgres 点检 SQL。

2026-07-10 给 worker work 补显式 requeue：新增控制面 `POST /v1/worker-work/{work_id}/requeue` 与 `bin/tma work requeue --work ... [--worker WORKER_ID|--clear-worker]`。第一版不做自动 retry，只允许 failed/canceled work 被人工复制成新的 pending work，原记录保留用于审计；默认复制原 worker 绑定，`--clear-worker` 可让新 work 重新被同 workspace worker poll。

2026-07-10 收口 worker work cancel 验证脚本和配置文档：`make verify-worker-work-cancel` 作为独立真实验收目标保留，脚本里的 worker registry 读操作使用 control token，避免与 `GET /v1/workers` 的控制面鉴权策略冲突；`docs/configuration.md` 补齐 worker / worker work reaper、worker 控制面 token 覆盖范围和 cancel/reap-expired 维护边界。

2026-07-10 收紧 worker registry 读接口：`GET /v1/workers` 和 `GET /v1/workers/{worker_id}` 会暴露 worker 名称、capabilities、last_seen 和 lease 信息，配置 `TMA_WORKER_CONTROL_AUTH_TOKEN` 后改为必须使用 control token。`tma-worker` 常驻进程不依赖这两个读接口；真实验收脚本中配置了 control token 的 worker list/get 调用已改用 control token。

2026-07-10 补齐 worker work cancel 的验收与文档：已有 `POST /v1/worker-work/{work_id}/cancel` / `bin/tma work cancel --work ...` 现在补上控制面鉴权测试、API 文档、TESTING 手动命令和 `make verify-worker-work-cancel` 真实脚本覆盖。取消后的 work 状态为 `canceled`，worker 后续 heartbeat/result 会看到 canceled 状态，不会覆盖成 completed。

2026-07-09 给 `tma-worker` 补 running work 续租：worker 进程 heartbeat 只证明 worker 活着，长任务还需要对当前 `worker_work` lease 续约。`tma-worker` 新增 `--work-heartbeat-interval` / `TMA_WORKER_WORK_HEARTBEAT_INTERVAL`，在 ack 后、result 前周期性调用 `POST /v1/workers/{worker_id}/work/{work_id}/heartbeat`，默认 15s，lease 秒数复用 `TMA_WORKER_LEASE_SECONDS`。单元测试覆盖 handler 阻塞期间会发 work heartbeat；新增 `make verify-worker-work-heartbeat` 真实验收，在 work reaper 开启、lease 3s、任务 6s 的情况下确认真实 worker 会续租并最终 completed，避免长时间执行被 work reaper 标记为 expired。

2026-07-09 给 `tma-worker` 补停机 drain：新增 `--shutdown-timeout` / `TMA_WORKER_SHUTDOWN_TIMEOUT`，默认 30s。收到 SIGINT / SIGTERM 后，worker 不再继续 poll，新任务选择侧会因为 worker heartbeat `draining` 而避开它；已 running 的 work 使用独立执行 context 继续完成并提交 result。超时后取消本地执行并退出，剩余 work 仍由 lease / reaper 兜底失败。单元测试覆盖 shutdown 时 running work 不被立即取消、release 后能正常 complete；新增 `make verify-worker-shutdown-drain` 真实验收，在 work running 时给真实 worker 发 SIGTERM，确认 draining heartbeat、result 上报和进程正常退出。

2026-07-09 给 `tma-worker` 补本地并发消费：新增 `--concurrency` / `TMA_WORKER_CONCURRENCY`，默认仍为 1；大于 1 时 worker 会按可用 slot 连续 poll 多条 `worker_work`，并发 ack/execute/result，不改 server 队列协议。单元测试覆盖两个 work 同时进入执行 handler，确认不是串行消费；文档同步提醒并发前要确认本地文件路径、外部凭据和工具实现能承受并行。

2026-07-09 收紧 worker archive 鉴权边界：`POST /v1/workers/{worker_id}/archive` 不再在配置 token 后公开可调用，改为允许 worker token 或 control token 二选一。worker token 保留给 `tma-worker doctor` / worker 自清理，control token 用于运维归档；未配置 token 的本地开发仍保持开放。

2026-07-09 收紧 worker diagnose 鉴权边界：`POST /v1/workers/diagnose` 会暴露 worker 名称、能力和 lease 诊断信息，配置 token 后也改为 worker token 或 control token 二选一。worker token 保留给 `tma-worker doctor` 自检，control token 用于 CLI / 运维诊断。

2026-07-09 给 worker 自身过期收敛补控制面兜底入口：新增 `POST /v1/workers/reap-expired` 与 `bin/tma worker reap-expired [--limit N]`，用于手动触发过期 online worker -> offline；该 endpoint 走 `TMA_WORKER_CONTROL_AUTH_TOKEN`，不允许普通 worker consumer token 修改 registry 状态。真实 `verify-worker-work-reap-expired` 脚本已覆盖该 CLI。

2026-07-09 补齐 worker 自身过期收敛：新增 `Store.ReapExpiredWorkers`，把 `lease_expires_at` 已过期的 `online` worker 自动标记为 `offline`，不 archive、不删除，保留 last_seen/lease 信息用于排障；server 新增后台 worker reaper，配置为 `TMA_WORKER_REAPER_ENABLED`、`TMA_WORKER_REAPER_INTERVAL_MS`、`TMA_WORKER_REAPER_LIMIT`。`make verify-worker-work-reap-expired` 现在同时验证 worker work 过期自动 failed，以及短 lease worker 无 heartbeat 后自动 offline。

2026-07-09 给 worker work 队列补单 job 诊断入口：新增 `GET /v1/worker-work/{work_id}/diagnose` 与 `bin/tma work diagnose --work ... [--json]`，会结合 work 状态、assigned worker、worker lease 和 work lease 输出 reasons/actions。`worker_work` 明确作为第一版队列，一条 work 是一个 job/task；当前 `tma-worker` 串行消费，后续可扩展 `--concurrency N` 并发 lease 多条 work。真实 `verify-worker-work-reap-expired` 验收脚本已在 running/failed 两个阶段调用 `work diagnose`。

2026-07-09 把 worker work 过期收敛接成 server 内部 maintenance loop：新增 `TMA_WORKER_WORK_REAPER_ENABLED`、`TMA_WORKER_WORK_REAPER_INTERVAL_MS`、`TMA_WORKER_WORK_REAPER_LIMIT`，`tma-server` 启动后会后台周期性调用 `Store.ReapExpiredWorkerWork`，自动把 lease 已过期的 `leased/running` work 标为 `failed`，但仍不自动重试。`scripts/verify_worker_work_reap_expired.sh` / `make verify-worker-work-reap-expired` 也已切到验证“后台自动回收”而不是手动触发 CLI reap。

2026-07-09 增加 Inspector UI 轻量真实验收：新增 `scripts/verify_inspector_ui.sh` 与 `make verify-inspector-ui`，会启动真实 TMA server、读取 `/inspector` HTML，并校验 `Download`、`Copy CLI`、`data-copy`、`bin/tma session artifact download --session ...` 等关键内容。用于弥补本地浏览器插件可能拦截 localhost 时无法点检的问题。

2026-07-09 给 worker work 生命周期补第一版过期收敛：新增 `Store.ReapExpiredWorkerWork`、HTTP `POST /v1/worker-work/reap-expired` 和 CLI `bin/tma work reap-expired [--limit N]`，由 server 控制面把 `leased/running` 且 lease 已过期的 work 标记为 `failed`，错误信息写明 lease expired。第一版不自动重新入队，避免重复执行带副作用的工具；接口走 `TMA_WORKER_CONTROL_AUTH_TOKEN` 控制面鉴权。

2026-07-09 Inspector artifact 操作补齐：Timeline 中的 tool result artifact 不再只显示数量，而是列出 artifact id/name/type、代理下载路径、CLI 下载命令，并提供 `Download` 与 `Copy CLI` 操作；Artifacts 面板也同步提供 `Download` 与 `Copy CLI`。复制逻辑用页面级事件委托处理 `data-copy`，减少内联 JS 字符串转义风险。

2026-07-09 补齐 tool-result artifact 自身的上下文：`ToolArtifactRecorder` 写入 objectstore 的结构化 JSON 现在包含原始 `ExecutionResult.Artifacts`、`artifact_error` 和 `exported_files` 元信息，避免只看落盘 artifact 时丢失 worker 已上传文件或导出异常线索；同时上传 object body 改为直接用 bytes reader，避免二进制内容经过 string 转换。

2026-07-09 Inspector artifact 交互再补半步：timeline 里的 trace artifact 摘要和 Artifacts 面板现在都直接渲染 `Download` 按钮，命中 `/v1/sessions/{session_id}/artifacts/{artifact_id}/download` 代理下载；同时保留 CLI 命令提示，兼顾浏览器点取和终端复现。

2026-07-09 修正 worker 已上传 artifact ref 被工具输出 recorder 覆盖的问题：`RegistryExecutor` 现在会保留 runtime/provider 已返回的 `ExecutionResult.Artifacts`，再追加 `ToolArtifactRecorder` 生成的 tool result / exported file artifacts。新增回归测试覆盖“worker/runtime artifact + recorder artifact”同时出现在最终 tool result 中；已验证 `go test ./...`、`make verify-worker-backed-large-local-export` 和 `make verify-worker-backed-local-export` 均通过。

2026-07-09 trace / Inspector 也补上 artifact 取回提示：`observability.ProjectTurnTrace` 现在会把 `runtime.tool_result` 中的 `artifacts` / `artifact_error` 投影到 `TraceStep`，`bin/tma trace show` 会像 `event stream` 一样显示 artifact 下载路径和 `bin/tma session artifact download --session ... --artifact ...` 命令提示，Inspector timeline 也直接展示同一份摘要。这样调试 worker / sandbox 导出时，不需要在 trace 和 artifact 列表之间来回切换猜 artifact id。

2026-07-09 收尾 `worker-backed local_system` artifact export：补上 `output_paths` 从 `default.run_command` / `default.execute_code` 进入 `capability.RunCommandRequest` / `ExecuteCodeRequest` 的透传，避免 server 侧工具层看见了导出路径、但转到 `WorkerBackedProvider` 后丢参，导致 worker 根本不知道要回传哪些文件。新增 runner 闭环测试，覆盖 `AgentRuntime -> WorkerBackedProvider -> tool_result.exported_files -> ToolArtifactRecorder -> session artifact download` 整链路，确认 worker 生成文件能真正落到 session artifact，而不是只停在 tool result JSON 里。

2026-07-09 继续补监控/反馈闭环：`session intervention reject` 不再在存在 continuation messages 时直接 fail turn，而是先写出 `runtime.tool_result` 的 rejected observation（`tool_rejected_by_user` + `decision_reason`），再把该 observation 作为 `tool` role message 送回同一条 continuation LLM loop，让模型根据用户拒绝原因继续改计划。没有 continuation 的旧记录仍保持原来的 fail turn fallback。

2026-07-09 增加第一版 Inspector / observability 落地入口：新增 `internal/observability.ProjectTurnTrace`，从 `session_events` 投影 turn timeline 与 tool trace summary；HTTP 新增 `GET /v1/sessions/{id}/trace?turn_id=...`，CLI 新增 `bin/tma trace show --session ... [--turn ...]`。同时 `WorkerRunner` 和审批 continuation completion 现在会在 turn 完成后把本轮 tool/approval 轨迹追加到 `session_summaries`，使后续 turn 的 ContextBuilder 能系统性看到跨 turn 的工具轨迹，而不只是在历史截断时做对话压缩。

2026-07-09 继续把 observability 从“纯 timeline JSON”推进到可面向人使用的表面：`TurnTrace` 现在额外投影 `trace_id + spans`；HTTP `GET /v1/sessions/{id}/trace` 支持 `format=perfetto|otel`，可导出 Perfetto / OTel 风格 JSON；新增 `GET /metrics` 输出 Prometheus 文本指标（当前覆盖 LLM usage 聚合与 worker 数量）；新增 `GET /inspector` 内置最小 Inspector 页面，直接查看 trace、usage、summary 与 raw export。当前 OTel 仍是 export-shaped JSON，不是 collector push/exporter SDK 集成，后续再补。

TMA 当前定位为一个 Postgres 持久化的 Agent Session / Event 管理服务。

核心闭环已经具备：

- Agent / Environment / Session 基础资源
- Session Event 历史查询
- SSE 历史续传和实时推送
- CLI 验证入口
- Postgres 持久化
- 可替换 Runner 层和异步 WorkerRunner
- 当前服务端固定使用 `AgentRuntimeTurnExecutor + agentruntime.DemoRuntime`
- `user.interrupt` 中断路径
- `turn_id` 标识一次用户消息对应的一次执行
- `session_turns` 持久化每次执行的生命周期状态
- JSON 结构化日志记录 session / turn / event 关键字段
- Capability Provider 能力接口骨架
- 原生 tool calling -> `internal/tools.Executor` 执行闭环
- `tma.tool_call.v1` 文本 fallback
- Session 级 `runtime_settings.intervention_mode` 热更新
- CLI `session runtime get/update`
- CLI `event stream` 对 chat message、session status、LLM delta、tool intervention 和 tool result 的可读展示
- CLI `session attach` 可在同一个交互式会话命令中发送 user message、监听事件并处理审批
- `session attach` 中发送消息、interrupt、审批决策失败时只打印错误并继续会话，避免一次可恢复 API 错误退出整个终端交互
- pending approval 当前不自动过期；`session attach` 启动时会主动查询并恢复当前 pending 审批，避免长期等待后因为 `--after` 跳过历史事件而看不到审批
- pending tool intervention 持久化记录
- HTTP / CLI 非交互式 `approve/reject` 决策入口
- pending intervention 内保存 LLM tool loop continuation messages
- `session_turns.status=waiting_approval` 支持真正挂起 turn
- reject 会 fail turn 并让 session 回到 idle

2026-07-09 继续收口 worker 安全边界：`tma-worker` 明确不对外暴露 HTTP 端口，只作为常驻 outbound client 主动消费 `tma-server` 的 worker registry / poll / ack / heartbeat / result API。server 新增 `TMA_WORKER_AUTH_TOKEN`，worker 侧新增 `TMA_WORKER_TOKEN`，当 server 配置了 token 时，worker 消费端点强制使用 `Authorization: Bearer ...`；本地开发未配置 token 时保持开放，避免打断现有调试流程。相关测试已补：server 侧 worker consumer endpoint 鉴权、worker HTTP client bearer header 发送。

2026-07-09 继续补 execution scope 透传：`ProviderResolver` 现在接收 `workspace_id + session_id + environment_id`，`runner.AgentRuntimeTurnExecutor` 会把同一组 scope 注入 `agentruntime.Config` 和 `tools.ExecutionContext`，`DemoRuntime` 执行工具时也会补齐 `WorkspaceID / SessionID / EnvironmentID / TurnID`。这里的 `environment_id` 只作为 Session / resource 归属上下文，不应被理解成一层 execution environment selector。已补 focused test 锁定 resolver request 和 tool execution context 的 scope 一致性。

2026-07-09 继续推进 capability-driven provider 选择：`AgentConfigVersion.tools` 现在不再只作为 context 文本，而是会被解析成启用工具策略，支持 `["default.read_file"]` 这类细粒度工具选择；`tools.Manifest.API` 也补了 `capabilities` / `risk` / `runtime` 元数据。`AgentRuntimeTurnExecutor` 会先按工具策略筛选 `ModelTools` / `ToolRegistry`，再把 tool runtime 传给 `ProviderResolver`，让 session 的 runtime settings、工具能力和最终执行环境一起决策。审批续跑路径也已同步到同一套 configured registry。

2026-07-09 修正 worker/work 抽象方向：不能继续把 `local` / `sandbox` / `worker` 抽成一层 execution environment。新的标准口径是 **tool/capability registry + work invocation**：work 表达一次 tool/api 调用、能力需求、风险等级、输入和结果；worker 注册自己能提供的 tools / capabilities / constraints；server 根据 agent config、tool policy、审批状态和 worker capabilities 做匹配。`LocalSystemProvider`、sandbox、remote worker 都只是 capability 实现，不是业务层主语。刚开始写的 `internal/workerexec` 错误抽象未接入代码，已删除。

2026-07-09 进一步澄清 tool namespace 与 runtime 的关系：新增 [docs/tool-runtime-standard.md](./docs/tool-runtime-standard.md)。当前最终口径是 namespace 表达能力域，runtime 表达显式运行位置 / 优先级策略。第一版显式 runtime 只保留 `auto`、`cloud_sandbox`、`local_system`；server 是默认内置承载面，不作为 runtime 暴露。第一版 namespace 暂定 `default.*`、`artifact.*`、`browser.*`、`agent.*`；`default.*` 作为稳定通用能力域存在，不作为模糊别名。每个 namespace 下的 API 需要明确 capabilities、risk、runtime 默认值和第一版实现位置。

2026-07-09 将 tool/runtime 标准落到代码合同：`internal/tools` 新增第一版 namespace、runtime、risk、implementation 常量，新增 `RuntimePolicy` 和 `WorkInvocation` 标准结构，并补 `NormalizeToolRuntime` / `ValidateWorkInvocation` 等 focused test。既有 `tma.local_system` 执行链路已改名为 `default.*`，不保留旧 namespace 兼容。

2026-07-09 收口 runtime/provider 映射：`cloud_sandbox` 当前落到 `capability.OnlyboxesProvider`，`local_system` 落到 `capability.LocalSystemProvider`，未设置 `TMA_TOOL_RUNTIME` 时默认就是 `cloud_sandbox`，不需要在 `.env` 显式写 `TMA_TOOL_RUNTIME=cloud_sandbox`；切换 `local_system` 走 session 级 `runtime_settings.tool_runtime` 热更新，不写 `.env`。`TMA_TOOL_RUNTIME` 和 session `runtime_settings.tool_runtime` 只接受 `auto`、`cloud_sandbox`、`local_system`；旧 `workspace` / `local` / `docker_onlyboxes` / `onlyboxes` 不再作为 runtime 值。`tma-worker` 默认注册能力也改成声明 namespaces / apis / runtimes / capabilities，供后续 server 做 capability match。

2026-07-09 明确 `cloud_sandbox` 第一版生命周期：不做 per-session 常驻沙箱，也不由 TMA 自动启动 Docker daemon / Onlyboxes Console。`OnlyboxesProvider` 按工具调用 just-in-time 执行 `docker run --rm`，命令结束即删除容器；下一阶段优先补 sandbox doctor/preflight、镜像策略和错误可读性，而不是引入后台沙箱生命周期管理。

2026-07-09 消除“双层 sandbox provider”歧义：对外 runtime/provider 语义只保留一层 `cloud_sandbox -> OnlyboxesProvider`。原 `SandboxedProvider` 改名为 `WorkspacePathGuardProvider`，只作为内部路径护栏复用，不声明 `ToolRuntime` / `ToolCapabilities`，避免被误解成第二套沙箱执行面。

2026-07-09 增加本地 sandbox doctor：`bin/tma sandbox doctor` 会读取当前目录 `.env`，检查 runtime、workspace root、docker 命令、Docker daemon 和本地 sandbox 镜像是否可用；doctor 默认在镜像缺失时执行 `docker pull`，可用 `--pull=false` 做纯检查。实际工具执行路径也改为 `docker run --pull missing --rm`。Onlyboxes 默认镜像名统一为 `coolfan1024/onlyboxes-runtime:default`。

2026-07-09 补齐 worker work 标准落地：`POST /v1/worker-work` 会校验 `tool_execution` payload 必须符合 `tma.work.v1`，`bin/tma work enqueue` 可用 `--api` / `--capabilities` / `--risk` / `--runtime` / `--input` 生成标准 `WorkInvocation`，`tma-worker` 对 `default.*` tool_execution 通过 `tools.DefaultRuntime + LocalSystemProvider` 在运行 worker 的机器上执行并回传 tool result。

2026-07-09 加入第一版 server-side worker capability match：`POST /v1/worker-work` 对未指定 `worker_id` 的 `tool_execution` 会读取同 workspace 在线 worker，并按 `namespaces`、`apis`、`runtimes`、`capabilities` 匹配后绑定 worker；无匹配 worker 时返回 `409`，避免标准 work 被任意 worker poll 走。显式指定 `worker_id` 的调试路径仍保留。

2026-07-09 将“模型可见工具过滤”接入 AgentRuntime：`capability.Provider` 可选实现 `CapabilityDescriptor` 声明 `ToolRuntime` 和 `ToolCapabilities`，`LocalSystemProvider`、`OnlyboxesProvider` 已声明第一版能力；`AgentRuntimeTurnExecutor` 会先按 AgentConfigVersion.tools 过滤，再按当前 provider runtime/capabilities 过滤 `ToolRegistry` / `ModelTools`，避免模型看到当前执行面无法提供的工具。

2026-07-09 抽出 worker capability selector：新增 `internal/workerselect.Selector`，把 `tool_execution` 的 namespace / api / runtime / capabilities 匹配从 HTTP handler 移出，HTTP enqueue 只调用统一 selector。已补 focused test 覆盖能力匹配、过期 worker 跳过、workspace/status 查询和无匹配返回 `409`，后续 AgentRuntime / 调度器可以复用同一套匹配合同。

2026-07-09 将 worker registry 能力接入模型可见工具过滤：`workerselect.AvailableFromWorkers` 会聚合同 workspace 在线 worker 声明的 namespaces / apis / runtimes / capabilities；当本轮 provider runtime 为 `local_system` 时，`AgentRuntimeTurnExecutor` 会在 agent config 和 provider capability 过滤后，再按在线 worker 能力收窄 `ToolRegistry` / `ModelTools`。没有注册 worker 时暂不清空工具集，保留当前 server 进程内 `LocalSystemProvider` 的调试路径；`cloud_sandbox` 不受本机 worker registry 约束。

2026-07-09 接入真实 S3-compatible objectstore provider：`TMA_OBJECT_STORAGE_PROVIDER=s3` 现在会构造 `internal/objectstore.S3Client`，使用标准库实现 AWS SigV4，支持 RustFS / MinIO / AWS S3 风格 endpoint 的 `PutObject`、`GetObject`、`DeleteObject` 和内部 `PresignGetObject`。新增纯内存 HTTP transport 单元测试覆盖签名头、path-style URL、读写删、404 映射和 presign 不泄露 secret；新增 `scripts/verify_objectstore_s3.sh` 与 `make verify-objectstore-s3`，用于启动真实 TMA server 后验证 artifact upload -> S3 -> TMA 代理 download 的闭环。

2026-07-09 接上输出回收闭环第二段：`worker-backed local_system` 现在也支持 `output_paths`。worker 侧 `workruntime.Executor` 会在 `tool_result.exported_files` 中回传 `content_base64`，server 侧 `WorkerBackedProvider` 解码后把内容注入 `capability.CommandResult.ExportedArtifacts`，`ToolArtifactRecorder` 优先使用这些回传字节落 objectstore，因此不需要 server 直接访问 worker 文件系统。新增 focused tests 覆盖 worker result 编码/解码与 recorder 落盘，也新增 `scripts/verify_worker_backed_local_export.sh` 与 `make verify-worker-backed-local-export`，用于真实验证 worker 机器生成文件 -> session artifact 下载 的闭环。

2026-07-09 接入第一版 worker-backed local_system 执行路径：新增 `execution.WorkerBackedProvider`，把 `capability.Provider` 的 read/write/edit/run/execute 调用桥接成标准 `tma.work.v1 tool_execution` work，按 `workerselect.Selector` 选择同 workspace 在线 worker，入队后等待 worker result，再把 `tool_result.state` 还原成原有 capability result。`AgentRuntimeTurnExecutor` 在 `local_system` 且存在匹配 worker 时会把工具执行 provider 切到 worker-backed；没有 worker 时默认不暴露 `local_system` 工具，server 进程内 `LocalSystemProvider` fallback 只允许显式开发开关。

2026-07-09 压实 worker-backed AgentRuntime 验证和调试入口：新增 runner 级 tool-loop 测试，覆盖 `DemoRuntime -> default.run_command -> WorkerBackedProvider -> worker work -> tool result -> final LLM response` 的完整闭环。Worker work 控制面新增 `GET /v1/worker-work/{work_id}`，CLI 新增 `bin/tma work get --work ...`，用于排查 worker-backed execution 卡在 pending/running/failed/completed 的位置。

2026-07-09 增加 worker-backed local_system 真实验收入口：CLI `agent config update` 新增 `--tools JSON`，可以通过公开 API 把 AgentConfigVersion.tools 设置为 `{"tools":["default"],"runtime":"local_system"}`。新增 `scripts/verify_worker_backed_local_system.sh` 和 `make verify-worker-backed-local-system`，会启动真实 `tma-server` 和 `tma-worker`，触发 fake LLM 的 `default.run_command`，并用事件历史 + `bin/tma work get --work ...` 校验工具执行确实经过 worker-backed work。

2026-07-09 收口 cloud_sandbox 的数据挂载语义：`OnlyboxesProvider` 继续保持 JIT `docker run --rm`，但现在会按 session id 在 `TMA_CLOUD_SANDBOX_DATA_ROOT` 下创建 host 数据目录并挂载到容器 `/mnt/data`，同 session 复用、不同 session 隔离，过期目录由后续 sandbox 调用按 TTL 清理。这个挂载是给用户数据加工和中间产物复用用的，不是 browser sandbox；`browser.*` 仍然留给后续独立实现。

2026-07-09 把用户上传文件接入 session sandbox 数据目录：`OnlyboxesProvider` 在每次 `RunCommand` 前会把当前 session 的 `ArtifactTypeFile` 上传内容同步到 `/mnt/data/uploads/{artifact_id}/{filename}`，并且跳过已存在文件，避免覆盖同 session 里工具自己改过的中间结果。这样上传接口、对象存储和 sandbox 文件系统形成最小闭环，不需要额外后台同步器。

2026-07-09 增加 cloud_sandbox 上传数据真实验收入口：新增 `scripts/verify_cloud_sandbox_upload_data.sh` 和 `make verify-onlyboxes-upload-data`，会启动临时 server，上传 file artifact，然后通过 fake LLM 两轮 tool call 验证 `/mnt/data/uploads/{artifact_id}/input.txt` 可读、`/mnt/data/state.txt` 能跨同 session 后续 sandbox 调用保留。

2026-07-09 明确 agent config version 的真实产品语义：已有 session 默认继续 pin 创建时的 `agent_config_version`，agent 发布新版本只影响新 session。`session attach` 现在会在进入时查询 session 与 agent 当前版本，如果发现旧 session 落后于 agent latest，会打印提示建议创建新 session 使用最新配置。需要原地升级时必须显式执行 `bin/tma session config upgrade --session ... --to-current`，且只允许 idle session；升级会写入 `session.config_updated` 审计事件。

2026-07-09 补齐 `session.config_updated` 的 CLI 可读展示：`event stream` / `session attach` 现在会把配置升级事件格式化成 `session config updated`，展示 seq、`agent_config_version: v旧 -> v新` 和 `updated_by`，避免用户只看到 raw SSE JSON。

2026-07-09 worker-backed local_system 真实验收已实测通过：`make verify-worker-backed-local-system` 成功跑完，启动临时 server + local worker，创建 session `sesn_000047`，执行 turn `turn_000001`，并确认 worker work `work_000001` completed。当前 `local_system` 已具备可重复验证的 worker-backed session 级闭环。

2026-07-09 将 worker 执行面抽成可扩展 runtime 接口：新增 `internal/workruntime.Executor` 和 `WorkHandler`，并将 worker 注册能力 JSON 收口为 `tools.WorkerCapabilities`。`tma-worker` 现在只负责注册、心跳、poll、ack、result，上层 work 执行委托给 workruntime。默认 executor 从 tools manifest 推导本地 worker 注册能力，内置支持 `tool_execution` 和 `sandbox_command`，并允许后续为 `artifact_sync`、`browser.*` 或 specialized runtime 注册自定义 handler。

2026-07-09 继续减少 work 标准漂移：`internal/tools.Registry` 新增 `WorkInvocation` 生成入口，统一从 manifest 的 namespace / api / capabilities / risk 派生标准 `tma.work.v1` payload。`workerselect.AvailableRegistryFromWorkers` 和 `execution.WorkerBackedProvider` 已改为复用这条路径，避免 worker 可见工具过滤、调度入队和 worker 执行之间各自手写能力元数据。

2026-07-09 收紧 `local_system` 存在性判断：真实部署里不再默认认为 server 主机拥有本机执行面。`SessionProviderResolver` 默认把 `local_system` 解析为 unavailable，AgentRuntime 只有在同 workspace 存在匹配在线 worker 时才暴露并切到 worker-backed provider；没有 worker 时隐藏工具。`TMA_ALLOW_SERVER_LOCAL_SYSTEM=true` 仅作为受信任本地开发 fallback。

2026-07-09 抽出共享 ToolExecutionResolver：新增 `internal/execution.ResolveToolExecution`，统一负责 agent tools policy、ProviderResolver、worker registry、模型可见工具过滤和最终 `tools.ExecutionContext` 生成。`runner.AgentRuntimeTurnExecutor` 与 HTTP approval continuation 已改为共用这条入口，避免主 turn 和批准续跑各维护一套 runtime/provider/worker 选择规则。已补 focused test 覆盖默认 `cloud_sandbox`、无 worker 的 `local_system` 隐藏、匹配 worker 切 `WorkerBackedProvider`、显式 server-local fallback 四个分支。

2026-07-09 增强 worker 可观测性：`workerselect` 新增 `DiagnoseInvocation`，能对每个 worker 返回是否匹配以及 status、lease、runtime、namespace、API、capability 的具体不匹配原因。新增 server 侧 `POST /v1/workers/diagnose` 作为统一诊断接口，CLI `worker list` 现在默认展开 runtimes / apis / capabilities 摘要，`bin/tma worker diagnose --api ... --runtime ... --capabilities ...` 调用 server 诊断接口解释某个 tool invocation 为什么找不到匹配 worker。

2026-07-09 将 worker diagnostics 接入真实 enqueue 失败路径：`POST /v1/worker-work` 在 `tool_execution` 未指定 worker 且没有匹配在线 worker 时，会返回带 invocation / matches / diagnostics 的 `409`；`bin/tma work enqueue` 会读取该错误响应并打印每个 worker 的不匹配原因。

2026-07-09 收口 worker capabilities 来源：`workruntime.Executor` 新增 `WorkerCapabilities()` / `WorkerCapabilitiesJSON()`，默认从 tools manifest 推导 local_system 能力，也允许自定义 executor 通过 `DeclaredCapabilities` 显式声明能力。`tma-worker` 注册、heartbeat、执行现在复用同一个 executor 实例，避免注册能力和实际执行逻辑漂移。

2026-07-09 增加 `tma-worker doctor`：新增 `bin/tma-worker doctor --base-url ...`，会展示当前 executor capabilities，并通过 outbound HTTP 依次检查 server health、临时 worker register、heartbeat、poll、server-side diagnose 和 archive。doctor 会注册 `<name>-doctor` 临时 worker，检查完成或中途失败后归档，仍然不要求 worker 暴露任何 inbound 端口。

2026-07-09 继续收紧 worker 鉴权边界：server 配置新增 `TMA_WORKER_CONTROL_AUTH_TOKEN`，专门保护 `POST /v1/worker-work` 和 `GET /v1/worker-work/{work_id}` 这类控制面 enqueue/get API，不再复用 worker consumer token。CLI 新增 `--auth-token` / `TMA_WORKER_CONTROL_TOKEN`，并统一对 JSON、下载和 SSE 请求注入 Bearer header；相关 server/CLI 测试与文档已同步补齐。

2026-07-09 给 worker-backed artifact 回传补大小护栏：`output_paths` 通过 work result 传输文件内容时，单文件限制为 8 MiB。worker 侧超限会跳过该导出并写入 `artifact_error`，server 侧解码 `content_base64` 时也会拒绝超限 payload，避免大文件裸塞进 worker work result / Postgres。更大产物后续应走 worker 直传 artifact/objectstore，只回传 object ref。

2026-07-09 接上 worker-backed 大文件 artifact 正式通道：`tma-worker` 现在实现 `workruntime.ArtifactUploader`，当 `output_paths` 导出的文件超过 8 MiB 内联上限时，会通过 `POST /v1/sessions/{session_id}/artifacts/upload` 主动上传到 server，再把 artifact ref 放进 worker tool result。server 侧 `WorkerBackedProvider` 会把这些 refs 传回最终 `runtime.tool_result.artifacts`，不再要求 server 访问 worker 本机路径，也避免大文件进入 `worker_work.result`。新增 `make verify-worker-backed-large-local-export` 真实验收。

当前刻意不保留生产代码里的 MemoryStore，避免同时维护两套状态机。单元测试使用 `_test.go` 内部 testStore，不进入正式构建。

2026-07-08 继续收敛 API 边界：新增 [docs/api-reference.md](./docs/api-reference.md)，按当前 handler / test 的真实行为记录 HTTP 契约、SSE 格式、pending intervention、summary、usage 和错误 envelope。这个文档是当前 API 合同，不提前抽 SDK；SDK 仍等接口稳定后再做。

2026-07-08 继续补产品差距地图：新增 [docs/product-gap-roadmap.md](./docs/product-gap-roadmap.md)，明确 tools、skills、memory、sandbox、多租户、权限、plugin、Inspector、observability 都还只是部分完成或规划中；同时确定文件二进制不进 Postgres，后续用 S3 兼容对象存储（RustFS / MinIO / S3）承载 artifact、静态资源、workspace snapshot 和跨环境文件系统，Postgres 只保存 metadata、权限和 object refs。

2026-07-08 对象存储底座第一步落地：新增 `000015_object_refs_artifacts.sql`，引入 `object_refs` 和 `session_artifacts` 两张 metadata 表及 `obj_` / `art_` ID sequence；Go model / Store interface / PostgresStore / testStore 已支持 `CreateObjectRef`、`GetObjectRef`、`CreateSessionArtifact`、`ListSessionArtifacts`。当前只保存对象存储引用和 artifact 关系，不保存文件二进制，也暂不接 RustFS / MinIO 客户端。

2026-07-08 对象存储 metadata 开始暴露 HTTP API：新增 `POST /v1/object-refs`、`GET /v1/object-refs/{object_ref_id}`、`POST /v1/sessions/{session_id}/artifacts`、`GET /v1/sessions/{session_id}/artifacts`。这些 API 只管理对象引用和 Session artifact 关系，不承担文件上传下载；创建 artifact 时会校验 object ref 与 session 的 workspace 一致。

2026-07-08 对象存储 metadata 增加 CLI 调试入口：新增 `bin/tma object create/get` 和 `bin/tma session artifact create/list`，用于手动验证 object ref -> session artifact 的 metadata 流。`TESTING.md` 已补分步骤命令；CLI 仍不上传/下载文件，只调用 metadata API。

2026-07-08 对象存储配置层落地：`serverconfig.Config` 新增 `ObjectStore`，解析 `TMA_OBJECT_STORAGE_PROVIDER`、`ENDPOINT`、`REGION`、`BUCKET`、`ACCESS_KEY_ENV`、`SECRET_KEY_ENV` 和 `USE_PATH_STYLE`。默认按 S3 兼容本地服务处理，适配 RustFS / MinIO / 企业 S3；当前只解析配置，不创建真实客户端。

2026-07-08 对象存储客户端边界先落接口：新增 `internal/objectstore`，定义 `Client` 的 `PutObject`、`GetObject`、`DeleteObject`、`PresignGetObject` 契约，以及 `NoopClient`。当前 no-op 会明确返回 `ErrNotConfigured`，用于在接真实 S3 SDK 前固定代码边界；metadata 阶段不需要本地启动 RustFS / MinIO。

2026-07-08 server 启动路径已接入对象存储依赖注入：`cmd/tma-server` 会把 `serverconfig.ObjectStore` 转换成 `objectstore.Config`，构造 no-op objectstore client，并注入 HTTP server；日志会记录 provider / endpoint / region / bucket / path-style，但不会记录密钥。当前 HTTP 行为不变，后续真实上传下载只需替换 client 构造和新增 handler。

2026-07-08 artifact upload 管道先接到 objectstore interface：新增 `POST /v1/sessions/{session_id}/artifacts/upload` multipart endpoint。handler 会读取上传文件、计算 sha256、调用注入的 `objectstore.Client.PutObject`，随后创建 `object_refs` 和 `session_artifacts` metadata；默认 no-op client 会返回 `503 object store client not configured`。测试使用 fake objectstore 验证完整管道，真实 RustFS / MinIO client 仍未接入。

2026-07-08 对象存储默认后端改为 `localfs`：`serverconfig.ObjectStore` 新增 `root_dir`，`cmd/tma-server` 通过 `objectstore.NewClient` 默认构造本地文件后端，上传会直接落到磁盘；RustFS / MinIO 只作为后续切换到 S3-compatible provider 时的验证选项，不再是当前默认启动前提。

2026-07-08 对象下载路径改为 TMA 代理：新增 `GET /v1/sessions/{session_id}/artifacts/{artifact_id}/download` 和 `bin/tma session artifact download`，客户端只通过 TMA 拉取字节流，不拿对象存储地址或 presigned URL。未来切到真实 S3-compatible 后端时，下载仍保持这层代理，避免把底层 bucket 暴露给外部。

2026-07-08 对象存储继续收口清理能力：新增 `DELETE /v1/object-refs/{object_ref_id}` 和 `DELETE /v1/sessions/{session_id}/artifacts/{artifact_id}`，删除 object ref 前会先检查是否仍被 session artifact 引用，若仍有引用则返回 `409`。CLI 也补了 `object delete` / `session artifact delete`，确保对象生命周期能先挂载、再下载、最后安全清理。

2026-07-08 tool result artifact 最小闭环落地：`internal/tools.ExecutionResult` 增加 `artifacts` / `artifact_error`，`RegistryExecutor` 在工具成功执行后调用 `ArtifactRecorder`；`runner.ToolArtifactRecorder` 会把工具输出 JSON 写入 objectstore，并创建 `object_refs` + `session_artifacts`，再把 TMA 代理下载路径回填给模型可见的 tool result。记录失败只进入 `artifact_error`，不让工具执行本身失败；下载仍只通过 TMA API，不暴露底层对象存储地址。

2026-07-08 event stream 补 artifact 可读展示：`runtime.tool_result` 事件 payload 现在包含 `artifacts` / `artifact_error`；`bin/tma event stream` 会在 `tool result` 下展示 artifact id、名称、类型和 TMA 代理下载路径，方便 CLI / Inspector 直接跳转下载，不需要暴露 objectstore bucket/key。

2026-07-08 CLI artifact list 变为默认可读输出：`bin/tma session artifact list --session ...` 现在展示 artifact id、名称、类型、object ref、turn/call 和 TMA 代理下载路径；新增 `--json` 保留原始 API 响应，供脚本和调试使用。

2026-07-08 开始拆分 `tma-server` / `tma-worker` 的执行边界：新增 [docs/worker-server-split.md](./docs/worker-server-split.md) 记录控制面、执行面、worker 身份、安全和调度的落地顺序；代码层新增 `internal/execution.ProviderResolver`，并让 `runner.AgentRuntimeTurnExecutor` 和 `httpapi.Server` 统一从可替换的 resolver 取执行 provider。当前默认仍回落到 `LocalSystemProvider`，但后续已经可以把本机执行迁到独立 worker，不再把 provider 写死在 server 里。

2026-07-08 worker registry 第一版落地：新增 `000016_workers.sql`，引入 `workers` 表记录 workspace 归属、worker 类型、状态、capabilities、metadata、last_seen 和 lease；Store / PostgresStore / HTTP API 增加 `POST /v1/workers`、`GET /v1/workers`、`GET /v1/workers/{id}`、`POST /v1/workers/{id}/heartbeat` 和 `POST /v1/workers/{id}/archive`。worker 只通过 HTTP API 与 server 通信，不直连数据库；真正 work poll / ack / result 回传还未实现。

2026-07-08 worker 可见性继续补齐：CLI 增加 `bin/tma worker register/list/get/heartbeat/archive`，HTTP 测试覆盖 worker 注册、列表、心跳和归档生命周期，CLI 测试覆盖请求路径和 payload。`ProviderResolver` 输入从单一 session id 升级为 workspace / session / environment 上下文，当前默认仍使用现有 provider / sandbox mode；后续需要继续把这个方向修正为 tool/capability registry + work invocation，避免把 environment 误抽成执行环境层。

2026-07-08 Onlyboxes provider 验证入口落地：`OnlyboxesProvider` 增加默认跳过的真实 Docker 集成测试，`make verify-onlyboxes` 可显式开启并验证 workspace 挂载、容器内执行和输出回写。`TESTING.md` 已补分步骤命令，也支持临时用 `TMA_ONLYBOXES_TEST_IMAGE=busybox:latest` 验证基础 Docker 链路。

2026-07-08 cloud_sandbox session 级验收入口落地：fake LLM 增加窄触发词 `tma.verify_tool_call`，只用于验证时生成 `default.run_command` tool call；收到 tool result 后回写最终 agent message。新增 `scripts/verify_cloud_sandbox_session.sh` 和 `make verify-onlyboxes-session`，可启动真实 TMA server，默认注入 `TMA_TOOL_RUNTIME=cloud_sandbox`，创建 session，触发 tool loop，并校验 `runtime.tool_call` / `runtime.tool_result` / `agent.message` 中包含 `tma-session-tool-ok`。

2026-07-08 cloud_sandbox session 验收进一步收紧：`verify_cloud_sandbox_session.sh` 不再只看工具输出 marker，还要求 `runtime.tool_result.data.content` 包含 `/workspace`，避免本地 provider 也能打出同样 marker 导致误判。这样验收能证明命令实际运行在容器挂载路径内。

2026-07-09 cloud_sandbox / Onlyboxes session 级验收已实测通过：`make verify-onlyboxes-session` 成功跑完，说明默认 `TMA_TOOL_RUNTIME=cloud_sandbox` 能在真实 TMA server、fake LLM tool loop、Onlyboxes provider 以及事件回放之间形成闭环；当前这条路径已经不是纸面实现，而是可重复验证的工作流。

---

## 关键设计决策

### 1. Event 表是事实源

`session_events` 是 Session 运行过程的事实源。

SSE 只是投递通道：

- 断线后用 `after_seq` 从 `session_events` 续传
- 实时推送从当前进程内订阅中心发送
- Postgres 模式下，历史事件可跨重启恢复

当前边界：

- 多 API 进程共享实时 SSE fanout 尚未实现
- 后续可用 Postgres `LISTEN/NOTIFY` 或消息队列解决

### 2. Store 只保留 PostgresStore

早期有 MemoryStore，用于快速开发和测试。

后来删除生产 MemoryStore，原因：

- 状态机已经包含异步执行、中断、turn_id，双 Store 容易分叉
- 目标产品需要持久化、审计、回放和续传
- Postgres 是正式路径，越早收敛越简单

现在：

- `cmd/tma-server` 缺少 `TMA_DATABASE_URL` 会直接失败
- `make run` 默认使用本地 Postgres URL
- 单元测试用 `internal/httpapi/test_store_test.go`

### 3. 异步执行代替同步 mock

早期 `user.message` 会同步写入：

```text
session.status_running
user.message
agent.message
session.status_idle
```

这导致 `user.interrupt` 几乎没有成功窗口。

现在改为：

```text
POST user.message
  -> session.status_running
  -> user.message
  -> HTTP 立即返回

background MockRunner
  -> agent.message
  -> session.status_idle
```

中断路径：

```text
user.interrupt
session.status_interrupting
session.status_idle
```

### 4. turn_id 和 session_turns 串联一次执行

`turn_id` 标识一次用户消息触发的一次执行。

事件 payload 中仍保留 `turn_id`，方便 SSE 和事件历史直接回放：

```json
{
  "turn_id": "turn_000001"
}
```

同时，`session_turns` 表持久化一次执行的生命周期：

```text
running -> completed
running -> interrupted
running -> failed
```

同一次执行的事件都带同一个 `payload.turn_id`：

```text
session.status_running
user.message
agent.message
session.status_idle
```

中断时：

```text
user.interrupt
session.status_interrupting
session.status_idle
```

失败时：

```text
session.status_idle
```

失败原因会写入 idle 事件 payload 的 `reason`，并带上 `last_turn_status=failed`，同时保存到 `session_turns.error_message`。Session 自身回到 `idle`，避免普通 turn 失败阻塞后续对话。

保护逻辑：

- 后台 mock completion 必须匹配当前 running turn
- 如果 turn 已被 interrupt 带回 idle，后台 completion 不再补 `agent.message`
- `user.message` 创建 `session_turns` 记录
- mock completion 将 turn 标记为 `completed`
- `user.interrupt` 将 turn 标记为 `interrupted`
- Runner 启动或执行失败会通过 `FailSessionTurn` 将 turn 标记为 `failed`，并让 Session 回到 `idle`

---

## 已实现内容

### HTTP API

```text
GET  /health
POST /v1/agents
POST /v1/environments
POST /v1/sessions
GET  /v1/sessions/{id}
PATCH /v1/sessions/{id}/runtime-settings
POST /v1/sessions/{id}/archive
DELETE /v1/sessions/{id}
POST /v1/sessions/{id}/events
GET  /v1/sessions/{id}/events
GET  /v1/sessions/{id}/events/stream
```

### CLI

```text
bin/tma health
bin/tma agent create
bin/tma env create
bin/tma session create
bin/tma session get
bin/tma session runtime get
bin/tma session runtime update
bin/tma session intervention list
bin/tma session intervention approve
bin/tma session intervention reject
bin/tma session archive
bin/tma session delete
bin/tma event send
bin/tma event interrupt
bin/tma event list
bin/tma event stream
```

### 数据库

迁移文件：

```text
sql/migrations/000001_init.sql
sql/migrations/000002_session_turns.sql
sql/migrations/000003_id_sequences.sql
...
sql/migrations/000011_session_interventions.sql
sql/migrations/000012_session_intervention_continuations.sql
sql/migrations/000013_session_turn_waiting_approval.sql
sql/migrations/000014_drop_session_intervention_expires_at.sql
```

当前表：

- `organizations`
- `workspaces`
- `agents`
- `agent_config_versions`
- `environments`
- `sessions`
- `session_events`
- `session_turns`
- `session_interventions`
- `session_summaries`
- `llm_providers`
- `llm_models`
- `llm_usage_records`

默认数据：

- `org_default`
- `wksp_default`

---

## 验证记录

常规验证：

```bash
make fmt
make test
make build
make build-cli
make verify-agent-runtime
make verify-agent-runtime-full
```

2026-07-06 配置层抽取后重新验证：

```text
make fmt                         pass
make test                        pass
make build                       pass
make build-cli                   pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000013
turn_id=turn_000001
```

2026-07-06 CommandTurnExecutor 协议版本化后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
protocol_version=tma.command.v1
session_id=sesn_000015
turn_id=turn_000001
```

2026-07-06 Capability Provider 能力层调整后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000016
turn_id=turn_000001
```

2026-07-06 Runner / TurnExecutor 概念重命名后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000017
turn_id=turn_000001
```

2026-07-06 Sandbox 从 turn-level executor 调整为 Provider 能力层后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000018
turn_id=turn_000001
```

2026-07-06 CommandTurnExecutor 底层统一到 LocalSystemProvider 后重新验证：

```text
make fmt                          pass
make test                         pass
make build                        pass
make build-cli                    pass
make verify-agent-runtime-full pass
```

完整验收结果：

```text
session_id=sesn_000019
turn_id=turn_000001
```

2026-07-06 移除 `process` 执行器入口，统一为 `command` 后重新验证：

```text
make fmt                           pass
make test                          pass
make build                         pass
make build-cli                     pass
make verify-agent-runtime-full  pass
```

完整验收结果：

```text
session_id=sesn_000020
turn_id=turn_000001
```

2026-07-06 配置与 Provider 分层收口后重新验证：

```text
make fmt                           pass
make test                          pass
make build                         pass
make build-cli                     pass
make verify-agent-runtime-full  pass
```

本次收口：

```text
TMA_TURN_QUEUE_SIZE=16
TMA_TURN_TIMEOUT_MS=3600000
internal/capability.Provider
capability.LocalSystemProvider
```

完整验收结果：

```text
session_id=sesn_000021
turn_id=turn_000001
```

2026-07-07 tool loop、session runtime settings 和 CLI 可读性补齐后重新验证：

```text
make test                         pass
```

本次新增验证点：

- `openai-compatible` 请求侧 `tools` / 响应侧 `tool_calls` 适配
- Runtime 同时支持原生 tool calls 和 `tma.tool_call.v1` fallback
- 旧 `tma.agent_runtime.demo.v1` tool envelope 已移除，仅保留为 demo `agent.message` payload 协议名
- Session 级 `intervention_mode=request_approval|approve_for_me|full_access`
- `PATCH /v1/sessions/{session_id}/runtime-settings`
- `bin/tma session runtime get/update`
- `bin/tma event stream` 对审批和工具结果事件的可读展示

数据库验证：

```bash
make db-up
make migrate-up
make test-postgres
make run
```

手动验证过的关键路径：

- 创建 Agent / Environment / Session
- 创建 Session 后自动写入 provisioning / idle 事件
- `event list --after` 历史续传
- `event stream --after` SSE 续传
- `event send` 立即返回 running / user.message
- 后台 mock 自动补 agent.message / idle
- `event interrupt` 生成 user.interrupt / interrupting / idle
- 中断后的后台 mock 不会再补 agent.message
- 同一个执行链路中的事件保持相同 `payload.turn_id`
- 全局 Agent / Environment / Session / Event ID 使用 Postgres sequence 递增

实际 Postgres 验证样例：

```text
turn_000001
  seq=3  session.status_running
  seq=4  user.message
  seq=5  agent.message
  seq=6  session.status_idle

turn_000002
  seq=7   session.status_running
  seq=8   user.message
  seq=9   user.interrupt
  seq=10  session.status_interrupting
  seq=11  session.status_idle
```

ID sequence 迁移验证样例：

```text
迁移前最大 ID:
  agt_000003
  env_000003
  sesn_000003
  evt_000044

迁移后新建资源:
  agt_000004
  env_000004
  sesn_000004
  evt_000045+
```

---

## 当前边界和风险

### Tool intervention 已有 pending 队列和交互式决策入口

当前已经具备：

- Session 级 `intervention_mode` 热更新
- `request_approval` 时拦截敏感工具
- `runtime.tool_intervention_required` / `runtime.tool_intervention_approved` / `runtime.tool_intervention_rejected` 事件
- CLI 能看见更可读的审批提醒
- `session_interventions` pending 记录
- `GET /v1/sessions/{session_id}/interventions?status=pending`
- `POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/approve`
- `POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/reject`
- `bin/tma session intervention list/approve/reject`
- `bin/tma session attach --session ...` 可以作为人的交互式入口，持续监听事件、发送 user message，并在审批点提示 `approve/reject/skip`
- approve 后会消费保存的 tool call，执行工具并写出 `runtime.tool_result`
- pending 记录已保存 `continuation_messages_json` / `continuation_round`
- request approval 会把 turn 标记为 `waiting_approval`，WorkerRunner 不再补临时 `agent.message`
- approve 后会读取 continuation，把 tool result 作为 `tool` role message 发回 LLM，生成最终 `agent.message`，再 complete turn / 回 idle
- continuation 支持最多 4 轮 tool loop；续跑中再次遇到敏感工具会再次 pending / waiting approval
- continuation LLM 调用会写入 `llm_usage_records`
- continuation 多轮 tool loop 已有 fake LLM 端到端测试覆盖
- reject 后写 `runtime.tool_intervention_rejected`，随后 `FailSessionTurn`，不会把拒绝原因喂回模型
- pending intervention 不自动过期；用户 approve/reject 前会一直保持 pending / waiting approval
- waiting approval 期间收到新的 `user.message` 时，不开启新 turn；系统返回提醒并重新发送审批事件

但还没有：

- 更完整的终端 UI 体验，例如稳定提示符、输入历史、多行输入和更好的事件折叠
- UI Inspector 审批面板

因此当前能力已经能“拦住、挂起 turn、持久化、让人用命令做决策、批准后执行工具、把结果送回 LLM 并完成本轮”。

长期无人审批时，系统不会自动 approve / reject / expire。pending 记录会保留在 `session_interventions` 中，可继续通过 `session attach` 恢复提示，或通过 list 查询并由用户后续决策。

### Session runtime settings 是热更新入口

`intervention_mode` 不挂在 `AgentConfigVersion`，而是挂在 Session 本身的 `runtime_settings_json`：

- 同一个 Agent 的不同 Session 可以使用不同权限等级
- 更新后影响下一轮 turn，不需要新建 Session
- 当前只存 `intervention_mode`

这条边界是刻意保持窄的，后续再决定是否扩展更多 session 级运行时开关。

### ID 生成方式已改为数据库 sequence

早期 `PostgresStore` 用 `count(*) + 1` 生成全局 ID，这不适合并发环境。

现在已改为 Postgres sequence：

```text
tma_agent_id_seq
tma_environment_id_seq
tma_session_id_seq
tma_event_id_seq
```

新增 migration：

```text
sql/migrations/000003_id_sequences.sql
```

该 migration 会根据已有数据把 sequence 对齐到当前最大 ID，避免存量数据库迁移后生成重复 ID。

`turn_id` 仍然是 Session 内编号，例如 `turn_000001`。它依赖同一个 Session 行的 `FOR UPDATE` 锁串行化生成，避免同一个 Session 并发创建重复 turn。

### Session 状态仍是单 running turn

当前一个 Session 同时只能 running 一个 turn。

这是合理的 P1 约束，但需要明确：

- 并发 `user.message` 会被拒绝
- 并发 interrupt / completion 依赖事务和当前状态判断
- 后续真实 Runner 要继续强化并发控制

### 实时 SSE 只支持单进程 fanout

历史续传没问题，因为读 Postgres。

实时推送目前只发给当前 server 进程内的订阅者。

多进程部署前需要：

- Postgres `LISTEN/NOTIFY`
- Redis Pub/Sub
- NATS / Kafka
- 或其他消息总线

### Runner 层已从 HTTP 抽出

HTTP 层现在只负责：

- 接收 `user.message` / `user.interrupt`
- 让 Store 先完成事件和状态落库
- 根据已落库事件调用 `runner.Runner`

Runner 接口位于：

```text
internal/runner
```

HTTP Server 通过 `NewServerWithStoreAndRunner` 接收可替换实现。当前不再提供默认 mock 构造函数，调用方必须显式传入 Runner。

早期默认实现是 `MockRunner`，只做：

- 延迟一小段时间
- 生成 mock agent.message payload
- 调用 Store 完成 turn
- 写入 idle
- turn 不匹配时放弃 completion
- 维护当前进程内 active turn registry
- 收到 interrupt 时取消对应后台 turn，避免继续尝试 completion
- 重复启动同一个 session/turn 会返回 `ErrTurnAlreadyRunning`
- Runner 启动失败时，HTTP 会调用 Store 的 `FailSessionTurn`，记录 `session.status_idle` 和失败原因

同时新增了 `WorkerRunner` 骨架：

- 内部队列接收 turn
- worker goroutine 调用 `TurnExecutor.RunTurn`
- `TurnExecutor` 成功时用返回的 payload 调 `CompleteSessionTurn`
- `TurnExecutor` 失败时调 `FailSessionTurn`
- `InterruptTurn` 会 cancel 正在执行的 TurnExecutor context
- `Close` 会停止接收并 cancel active turns

服务端运行时不再暴露 `mock|echo|command` 选择。真实启动固定走：

```text
cmd/tma-server
  -> WorkerRunner
  -> CommandTurnExecutor
  -> capability.LocalSystemProvider.RunCommand
```

当前保留的运行时配置：

```text
TMA_TURN_QUEUE_SIZE=16
TMA_TURN_TIMEOUT_MS=3600000
```

`MockRunner` / `EchoExecutor` 只作为测试辅助和早期验证代码存在，不再作为服务端启动模式。

`CommandTurnExecutor` 接外部命令：

- stdin 输入 `session_id`、`turn_id`、`user_payload`
- stdout 输出 `agent.message` payload JSON
- 非 0 退出、超时、空 stdout、非 JSON stdout 都会进入 `FailSessionTurn`
- 内部通过 `capability.LocalSystemProvider.RunCommand` 执行命令，不再直接散落 `os/exec` 调用
- 示例脚本位于 `scripts/command_turn_echo.sh`
- 协议文档位于 `docs/command-turn-protocol.md`
- 排障和修正记录位于 `docs/troubleshooting.md`
- 配置总览位于 `docs/configuration.md`
- 服务端配置解析集中在 `internal/serverconfig`，并有单元测试覆盖默认值、`.env`、shell 优先级和非法配置

CommandTurnExecutor 协议已版本化：

- 当前版本固定为 `tma.command.v1`
- TMA 发送给外部命令的 stdin 会包含 `protocol_version`
- stdout payload 必须输出同一个 `protocol_version`
- stdout 缺少 `protocol_version` 会被拒绝
- stdout 带了非 `tma.command.v1` 的版本会被拒绝

能力方向已从 turn-level executor 调整为 Provider 能力层：

- 代码位于 `internal/capability/provider.go`
- 本地实现位于 `internal/capability/local.go`
- 设计文档位于 `docs/capability-provider.md`
- 当前协议版本为 `tma.capability.v1`
- `capability.Provider` 定义底层能力：`RunCommand`、`ExecuteCode`、`ReadFile`、`WriteFile`
- `capability.LocalSystemProvider` 已实现本地命令执行、代码执行、文件读写
- `RequestMeta` 负责携带 `session_id`、`turn_id`、`deadline`
- 当前不引入 `ToolManifest` / `ToolRegistry` / `ToolExecutor`
- 当前不把 local system / cloud sandbox 暴露成 turn mode
- 未来 AgentRuntime / Tool Calling 成形后，再把具体 Provider 包装成 builtin tools

Runner / TurnExecutor 概念已收敛：

- `Runner` 管 turn 生命周期：启动、排队、中断、取消、成功/失败状态回写
- `TurnExecutor` 管 turn 的具体执行：输入 `TurnRequest`，输出 `agent.message` payload 或错误
- `WorkerRunner` 是 `Runner`
- `AgentRuntimeTurnExecutor` 是当前服务端默认运行时 `TurnExecutor`
- `CommandTurnExecutor` 保留为外部进程协议适配器
- `EchoExecutor` 仅保留为测试/验证用 `TurnExecutor`
- 不再保留 `TMA_TURN_MODE`，也不再保留 `process` 模式

2026-07-06 命名再次收口：

- 不把 `LocalSystemProvider` 当作 turn executor 名称；它只表示本机能力 Provider
- 原有命令执行类型改为 `CommandTurnExecutor`，明确它是一次 turn 的适配器
- 用户侧配置曾短暂改为 `TMA_TURN_COMMAND` / `TMA_TURN_COMMAND_ARGS` / `TMA_TURN_COMMAND_TIMEOUT_MS`
- 验证脚本改为 `scripts/command_turn_echo.sh`、`scripts/verify_agent_runtime.sh`
- 验收目标改为 `make verify-agent-runtime` / `make verify-agent-runtime-full`
- 协议文档改为 `docs/command-turn-protocol.md`
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000022`，`turn_id=turn_000001`

2026-07-06 运行模式再次收口：

- 删除运行时 `TMA_TURN_MODE`
- 当时 `cmd/tma-server` 固定组装 `WorkerRunner + CommandTurnExecutor`
- HTTP server 构造函数不再默认注入 `MockRunner`，必须显式传入 Runner
- `.env.example` 改为可运行的 command turn demo 配置
- `MockRunner` / `EchoExecutor` 不再出现在真实启动文档中，只保留给测试和历史验证
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000023`，`turn_id=turn_000001`

2026-07-06 command 配置再次收口：

- 删除 `TMA_TURN_COMMAND` / `TMA_TURN_COMMAND_ARGS`
- 删除 `TMA_TURN_COMMAND_TIMEOUT_MS`，改为通用 `TMA_TURN_TIMEOUT_MS`
- `cmd/tma-server` 内部暂时固定 demo command turn：`sh scripts/command_turn_echo.sh`
- 用户侧不再需要理解 demo 脚本的启动细节
- 未来接真实 AgentRuntime 时，再以更明确的一等配置替换 demo command turn
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000024`，`turn_id=turn_000001`

2026-07-06 turn 超时默认值调整：

- `TMA_TURN_TIMEOUT_MS` 默认值先从 `30000` 调整为 `1800000`，随后调整为 `3600000`
- 该超时表示整次 turn 的兜底保护，不是单条轻量命令超时
- 真实智能体可能执行依赖安装、构建、测试、仓库检索等长任务，短超时容易误杀
- 用户主动停止应使用 interrupt，而不是依赖短超时
- 超时后当前 turn 会进入 `failed`，Session 回到 `idle`，可以继续下一条消息
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000025`，`turn_id=turn_000001`

2026-07-06 AgentRuntime 雏形接入：

- 新增 `internal/agentruntime.Runtime`
- 新增 `agentruntime.DemoRuntime`，用于替代内置 command demo 脚本作为服务端默认执行路径
- 新增 `runner.AgentRuntimeTurnExecutor`
- `cmd/tma-server` 改为组装 `WorkerRunner + AgentRuntimeTurnExecutor + agentruntime.DemoRuntime`
- `CommandTurnExecutor` 不再是默认 server path，仅保留为外部进程协议适配器
- 新增设计文档 `docs/agent-runtime.md`
- 验收脚本改为 `scripts/verify_agent_runtime.sh` / `scripts/verify_agent_runtime_full.sh`
- Make target 改为 `make verify-agent-runtime` / `make verify-agent-runtime-full`
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000026`，`turn_id=turn_000001`

2026-07-06 Runtime step 事件接入：

- 新增 runtime 事件类型：`runtime.started`、`runtime.thinking`、`runtime.tool_call`、`runtime.tool_result`、`runtime.completed`、`runtime.failed`
- 新增 `Store.AppendRuntimeEvent`
- PostgresStore 写 runtime 事件时会校验 Session 仍是 `running` 且 turn_id 是当前 turn
- 中断或完成后的旧 runtime step 不会再补写
- `DemoRuntime` 当前会写入 `runtime.started`、`runtime.thinking`、`runtime.completed`
- `AgentRuntimeTurnExecutor` 在 Runtime 报错时会尽量写入 `runtime.failed`
- `make verify-agent-runtime-full` 已校验事件链路包含 runtime step
- 完整验收通过：`session_id=sesn_000027`，`turn_id=turn_000001`

2026-07-06 LLM Client 边界接入：

- 新增 `internal/llm.Client`
- 新增 `llm.Request`、`llm.Response`、`llm.Message`、`llm.ContentPart`
- 新增 `llm.FakeClient`，不调用外部模型，只返回确定性 assistant message
- `agentruntime.DemoRuntime` 改为通过 `llm.Client.Generate` 生成回复
- 新增 runtime 事件：`runtime.llm_request`、`runtime.llm_response`
- 当前仍不引入 API key、模型厂商 SDK 或真实网络调用
- `make verify-agent-runtime-full` 已校验事件链路包含 `runtime.llm_request` / `runtime.llm_response`
- 完整验收通过：`session_id=sesn_000028`，`turn_id=turn_000001`

2026-07-06 LLM Provider 默认配置接入：

- 新增配置项 `TMA_LLM_PROVIDER`，默认值 `fake`
- 新增配置项 `TMA_LLM_MODEL`，默认值 `fake-demo`
- 新增 `llm.Provider` 和 `llm.Manager`
- `llm.Manager` 持有当前 Provider / Model，并实现 `llm.Client`
- `cmd/tma-server` 通过 `llm.Manager` 注入 `agentruntime.DemoRuntime`
- 当前只内置 `fake` Provider，不引入真实模型 SDK 或外部网络调用
- 设计目标是为未来多个 LLM Provider 和运行时热切换留入口，但本次不新增热切换 HTTP API
- 启动日志会输出 `llm_provider` 和 `llm_model`
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000029`，`turn_id=turn_000001`

2026-07-06 AgentConfigVersion 与 LLM 配置收敛：

- 将代码概念从 `AgentVersion` 收敛为 `AgentConfigVersion`
- 数据库表从 `agent_versions` 收敛为 `agent_config_versions`
- Session 字段从 `agent_version` 收敛为 `agent_config_version`
- Agent 配置版本新增 `llm_provider` / `llm_model`
- `model` 请求字段保留为兼容别名，内部统一落到 `llm_model`
- 新增 `Store.ResolveAgentRuntimeConfig(session_id)`
- `AgentRuntimeTurnExecutor` 执行 turn 前按 Session 解析 AgentConfigVersion
- `DemoRuntime` 发起 LLM 请求时带上 AgentConfigVersion 的 Provider / Model / System
- `llm.Manager` 支持每次请求指定 Provider / Model，不再只能使用全局当前配置
- 新增迁移 `000004_agent_config_versions.sql`，兼容已有本地库
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 完整验收通过：`session_id=sesn_000030`，`turn_id=turn_000001`

2026-07-07 OpenAI-compatible LLM Provider 接入：

- 新增 `llm.ProviderOpenAICompatible`
- 新增 `OpenAICompatibleProvider` / `OpenAICompatibleClient`
- 使用 Go 标准库 `net/http` 调用 `{base_url}/chat/completions`
- 新增配置项 `TMA_LLM_BASE_URL`，默认 `https://api.openai.com/v1`
- 新增配置项 `TMA_LLM_API_KEY`
- `TMA_LLM_PROVIDER=openai-compatible` 时要求配置 API Key
- 当前只支持非流式 Chat Completions 文本响应
- 暂不实现 streaming、tool calling、usage 归集、Key Vault 或 model-bank
- 单元测试使用自定义 `RoundTripper`，不依赖本地端口或外网
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 默认 fake 链路完整验收通过：`session_id=sesn_000031`，`turn_id=turn_000001`

2026-07-07 LLM 流式 delta 事件接入：

- 新增 runtime 事件类型 `runtime.llm_delta`
- 新增 `llm.Delta`
- 新增可选接口 `llm.StreamingClient`
- `llm.Manager` 实现 `GenerateStream`
- 底层 client 支持流式时走流式；不支持时自动退回 `Generate`
- `OpenAICompatibleClient` 使用 `stream: true` 调用 Chat Completions SSE
- 支持解析 `data: {...}` 和 `data: [DONE]`
- `DemoRuntime` 收到流式 delta 后写入 `runtime.llm_delta`
- 最终仍合并完整 assistant 文本并写入 `agent.message`
- 默认 `fake` Provider 不产生 delta，现有验收脚本不强制检查 delta
- `scripts/verify_agent_runtime_full.sh` 默认显式覆盖 `TMA_LLM_PROVIDER=fake`，避免本地 `.env` 中真实 Provider 配置影响基础验收
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 默认 fake 链路完整验收通过：`session_id=sesn_000032`，`turn_id=turn_000001`

2026-07-07 自定义 LLM Provider ID 接入：

- 新增配置项 `TMA_LLM_PROVIDER_TYPE`
- `TMA_LLM_PROVIDER` 允许使用业务自定义 Provider ID
- 例如 `TMA_LLM_PROVIDER=volcengine-agent-plan`
- 自定义 Provider ID 可通过 `TMA_LLM_PROVIDER_TYPE=openai` 指定底层协议
- `openai-compatible` 保留为 Provider Type 历史别名
- 如果自定义 Provider ID 没有显式设置 Provider Type，当前默认按 `openai` 注册
- `llm.Manager` 启动时会把自定义 Provider ID 注册进 Provider map
- 修正此前只接受硬编码 Provider ID 导致的 `unsupported LLM provider` 问题
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 默认 fake 链路完整验收通过：`session_id=sesn_000033`，`turn_id=turn_000001`

2026-07-07 Provider Type 命名收敛：

- `TMA_LLM_PROVIDER_TYPE` 推荐值从 `openai-compatible` 收敛为 `openai`
- `openai-compatible` 仍作为兼容别名保留
- 文档和 `.env.example` 已改为 `TMA_LLM_PROVIDER_TYPE=openai`
- 重新执行 `make fmt`、`make test`、`make build`、`make build-cli`、`make verify-agent-runtime-full`
- 默认 fake 链路完整验收通过：`session_id=sesn_000034`，`turn_id=turn_000001`

2026-07-07 真实 LLM Provider 验收命令接入：

- 新增 `scripts/verify_llm_provider.sh`
- 新增 `scripts/verify_llm_provider_full.sh`
- 新增 Make target `make verify-llm-provider`
- `verify-agent-runtime-full` 继续固定使用 `fake` Provider
- `verify-llm-provider` 读取当前 `.env` / shell 中的真实 LLM 配置
- 验收会创建 Agent / Environment / Session，发送测试消息，检查 `runtime.llm_request`、`runtime.llm_response`、`agent.message`
- 如果存在 `runtime.llm_delta`，验收输出会显示 delta 数量
- 验收输出不会打印 API Key
- 真实 Provider 验收通过：`session_id=sesn_000035`，`turn_id=turn_000001`，`delta_count=57`

配置层已从 `cmd/tma-server/main.go` 抽到 `internal/serverconfig`：

- `cmd/tma-server` 只负责组装 logger、Store、Runner 和 HTTP server
- `serverconfig.Load(".env")` 统一处理 `.env` 和 shell 环境变量
- `.env` 只补缺省值，不覆盖 shell 中已有配置
- `command` 相关配置在启动前校验，避免 server 运行后才暴露明显配置错误

2026-07-07 LLM Provider DB 配置层接入：

- 新增 `llm_providers` 表，保存 Provider ID、底层协议类型、Base URL、API Key 环境变量名和启用状态
- `cmd/tma-server` 启动时会把 `.env` / shell 中的默认 Provider upsert 到 `llm_providers`
- 老库迁移会补齐 `agent_config_versions.llm_provider` 到 `llm_providers.id` 的外键约束
- 新增配置项 `TMA_LLM_API_KEY_ENV`，默认 `TMA_LLM_API_KEY`
- 数据库只保存 `api_key_env`，真实 API Key 仍只从进程环境变量读取，不写入数据库、不写入运行时事件
- `ResolveAgentRuntimeConfig(session_id)` 现在会 JOIN `llm_providers`，按 Session 绑定的 AgentConfigVersion 解析 Provider 配置
- `AgentRuntimeTurnExecutor` 根据 `LLMAPIKeyEnv` 读取密钥，并把 Provider Type / Base URL / API Key 传给 Runtime
- `llm.Manager` 支持每次请求携带 Provider 配置，未预注册的业务 Provider ID 也可以按 `openai` 协议动态创建 client
- 修正文档中的 Volcengine Provider ID 拼写
- 已执行：`gofmt`、`GOCACHE=$PWD/.gocache make test`、`GOCACHE=$PWD/.gocache make build`、`GOCACHE=$PWD/.gocache make build-cli`
- fake 全链路验收通过：`make verify-agent-runtime-full`，`session_id=sesn_000037`，`turn_id=turn_000001`
- 真实 Provider 验收通过：`make verify-llm-provider`，`session_id=sesn_000038`，`turn_id=turn_000001`，`delta_count=43`
- 追加外键迁移后复跑：`make migrate-up`、`GOCACHE=$PWD/.gocache go test ./...`

2026-07-07 LLM Provider 管理入口接入：

- Store 新增 `UpsertLLMProvider`、`GetLLMProvider`、`ListLLMProviders`、`SetLLMProviderEnabled`
- HTTP 新增 `/v1/llm-providers` 管理接口，支持 list / create / get / update / enable / disable
- CLI 新增 `bin/tma provider list|get|create|update|enable|disable`
- Provider 管理仍只保存 `api_key_env`，不保存真实 API Key
- 创建 Agent 时会校验目标 Provider 存在且已启用，避免错误延迟到 turn 执行阶段才暴露
- `TESTING.md` 补充 Provider 管理命令
- 已执行：`gofmt`、`GOCACHE=$PWD/.gocache go test ./...`
- 手动验收通过：临时服务 `:18082`，`bin/tma provider list/create/update/disable/enable/get`
- 禁用 Provider 创建 Agent 已被拦截：返回 `400 invalid input: llm provider verify-provider-cli is disabled`
- 手动验收创建的 `verify-provider-cli` 已重新禁用，避免误用
- 完整 fake 链路复验通过：`make verify-agent-runtime-full`，`session_id=sesn_000039`，`turn_id=turn_000001`

2026-07-07 Agent 配置版本更新入口接入：

- Store 新增 `GetAgent`、`ListAgentConfigVersions`、`CreateAgentConfigVersion`
- HTTP 新增 `GET /v1/agents/{agent_id}`
- HTTP 新增 `GET /v1/agents/{agent_id}/config-versions`
- HTTP 新增 `POST /v1/agents/{agent_id}/config-versions`
- CLI 新增 `bin/tma agent get --id ...`
- CLI 新增 `bin/tma agent config list --agent ...`
- CLI 新增 `bin/tma agent config update --agent ... --llm-provider ... --llm-model ... --system ...`
- 创建新 AgentConfigVersion 时会继承未传字段，不覆盖旧版本
- 创建新 AgentConfigVersion 时会校验 Provider 存在且启用
- 新 Session 绑定 Agent 当前配置版本；旧 Session 继续绑定创建时的版本
- 单元测试覆盖：更新 Agent 配置后，旧 Session 仍绑定 v1，新 Session 绑定 v2
- 已执行：`gofmt`、`GOCACHE=$PWD/.gocache go test ./...`
- 手动验收通过：临时服务 `:18082`，`agt_000039` 从 `fake-v1` 更新到 `fake-v2`，`agent get` 返回当前版本 2，`agent config list` 返回版本 1 和 2
- 完整 fake 链路复验通过：`make verify-agent-runtime-full`，`session_id=sesn_000040`，`turn_id=turn_000001`

2026-07-07 LLM Provider 长期路线图补充：

- 新增 `docs/llm-provider-roadmap.md`
- 明确长期原则：AgentRuntime 不写厂商判断，Provider 差异下沉到 `internal/llm`
- 明确 DB 只保存 `api_key_env`，不保存真实 API Key
- 明确未来需要 `llm_models` / `abilities_json` 管理模型能力
- 明确未来 token usage 不能只放日志，必须落库审计
- 记录建议表 `llm_usage_records`，支持按 Provider / Model / Agent / Session / Turn / 时间范围统计
- 记录 usage 归一化方向：input/output/total/cached/reasoning tokens、latency、status、cost
- 记录未来统一流协议 `runtime.llm_chunk`，类型包括 text / reasoning / tool_calls / grounding / usage / stop / error
- `docs/agent-runtime.md` 和 `docs/configuration.md` 已补充路线图链接

2026-07-07 Session 多轮上下文注入 LLM：

- 新增 `managedagents.ConversationMessage`
- Store 新增 `ListConversationMessages(session_id, before_seq)`
- PostgresStore 从 `session_events` 中按 seq 读取当前 user.message 之前的 `user.message` / `agent.message`
- HTTP dispatch 将触发 turn 的 `user.message.seq` 写入 `runner.TurnRequest.UserEventSeq`
- `AgentRuntimeTurnExecutor` 执行 turn 前读取 Session 历史并传给 Runtime
- `DemoRuntime` 构造 LLM messages 的顺序变为：`system`、历史 user / assistant、当前 user
- 当前 user.message 不会从历史里重复注入，因为历史查询使用 `seq < UserEventSeq`
- 单元测试覆盖 Runtime 消息顺序，以及 Runner 适配层传递历史消息
- 已执行：`gofmt`、`GOCACHE=$PWD/.gocache go test ./...`
- 完整 fake 链路复验通过：`make verify-agent-runtime-full`，`session_id=sesn_000041`，`turn_id=turn_000001`

Store 边界也同步收窄：`CompleteSessionTurn` 不再生成 mock 回复，只负责把 Runner 产出的 `agent.message` payload 落库，并补齐 `turn_id`。

后续真实 Runner 需要接：

- Sandbox
- 模型调用
- 工具调用
- 日志流
- 中断信号传播
- 超时和失败状态

---

## 日志约定

服务端使用 Go 标准库 `slog` 输出 JSON 日志。

关键字段：

```text
session_id
turn_id
event_id
event_seq
event_type
after_seq
history_events
```

目前覆盖：

- server 启动和停止
- PostgresStore 初始化
- HTTP append events 成功后逐条 event 记录
- mock turn scheduled / completed / skipped / failed
- mock completion 写出的 agent / idle 事件
- runner start failure 写出的 failed 事件
- SSE stream opened / closed

## 2026-07-07 LLM Usage 审计基础链路

背景：

- 后续需要按 Provider / Model / Agent / Session / Turn 审计每次模型调用的 token 消耗。
- usage 不能只写日志，必须进入数据库，方便后续做账单、限额和问题追踪。

本次收口：

- `llm.Response` 增加 `Usage`，内部统一结构为 `input/output/total/cached/reasoning tokens`。
- `agentruntime.Runtime` 返回 `TurnResult`，同时带回 `agent.message` payload 和模型 usage。
- `AgentRuntimeTurnExecutor` 负责把 Runtime usage 补齐为可落库记录：
  - workspace
  - agent
  - agent_config_version
  - session
  - turn
  - provider
  - provider_type
  - model
  - latency
- `WorkerRunner` 只在 turn 成功完成后调用 `Store.RecordLLMUsage`。
- `openai-compatible` 已解析非流式 `usage`，流式请求会带 `stream_options.include_usage=true`，并解析最终 chunk usage。
- 新增 Session usage 查询：
  - HTTP: `GET /v1/sessions/{session_id}/usage`
  - CLI: `bin/tma usage list --session ...`
  - 返回 `summary` 总量和 `records` 每轮明细。
- 新增跨 Session usage 聚合：
  - HTTP: `GET /v1/llm-usage`
  - CLI: `bin/tma usage summary`
  - 支持按 `provider`、`model`、`provider_model` 分组。
  - 支持 `workspace_id`、`provider_id`、`model`、`status`、`from`、`to` 过滤。
- 新增 failed usage 基础语义：
  - 如果执行器失败且没有 usage，不写 usage。
  - 如果模型调用已经发生，执行器随错误返回 usage，`WorkerRunner` 写入 `status=failed` 和 `error_message`。
  - `AgentRuntimeTurnExecutor` 会把 Runtime 返回的部分 usage 补齐为 failed usage 记录。

重要边界：

- Runtime 不直接写数据库。
- Command / Echo executor 不是 LLM 调用，不生成 usage。
- 失败 turn 不写 completed usage；只有能证明模型调用已经发生时，才写 `status=failed` usage。

已验证：

```bash
GOCACHE=/private/tmp/tma-gocache go test ./...
```

后续建议：

- 基于 usage 聚合继续做成本看板和预算/限额策略。

---

## 2026-07-07 Context Builder 抽离

背景：

- `DemoRuntime` 里原本直接组装 `system + history + current user`。
- 后续 token budget、历史截断、summary、多模态上下文都会改这段逻辑，如果继续放在 Runtime 主流程里会越来越重。

本次收口：

- 新增 `agentruntime.ContextBuilder` 接口。
- 新增 `DefaultContextBuilder` 基础实现。
- `DemoRuntime` 改为依赖 `ContextBuilder.Build(...)` 产出 `llm.Messages`。
- 新增 `llm_models` 表，按 `provider_id + model` 保存 `context_window_tokens`。
- 默认模型总窗口为 `128000`，可通过模型配置覆盖。
- `DefaultContextBuilder` 新增 `MaxInputTokens`，运行时按 `context_window_tokens * 60%` 计算输入预算。
- 服务配置新增 `TMA_DEFAULT_CONTEXT_WINDOW_TOKENS`，作为未知模型或默认模型的总窗口兜底值。
- 新增 `session_summaries` 表，保存当前 Session summary、覆盖到的 event seq 和更新时间。
- 新增手动 summary 写入入口：
  - HTTP: `PUT /v1/sessions/{session_id}/summary`
  - CLI: `bin/tma session summary upsert --session ... --text ... --until ...`
- 写入 summary 时要求 Session 为 `idle`，并写出 `session.status_compacting -> session.status_idle` 事件。
- `runtime.llm_request` step 里补充 `history_count`、`omitted_history_count`、`estimated_token_count`、`context_truncated`、`summary_included`，方便观察上下文构造结果。

当前基础规则：

- system 非空时放第一条。
- summary 非空时放在 system 后面。
- 历史只接收 `user` / `assistant`。
- 历史空文本跳过。
- 当前 user message 总是追加到最后。
- system、summary 和当前 user message 保底保留；history 从最近到最旧尝试纳入 60% 预算。
- token 计数当前是近似估算，不是厂商 tokenizer 精确结果。

已验证：

```bash
GOCACHE=/private/tmp/tma-gocache go test ./...
```

后续建议：

- 接入真实 tokenizer 或 provider/model 对应 tokenizer。
- 增加自动 summary 生成和更新策略。
- 增加 just-in-time compaction：下一轮构建上下文时发现超预算，先压缩再继续原 turn。

---

## 2026-07-08 Tool Approval / Attach CLI 最终收口

本轮最终落点：

- `bin/tma session attach --session ...` 定为人的主 CLI 入口。
  - 同一个命令内可以发送 `user.message`、监听事件、恢复 pending approval、执行 approve/reject/skip。
  - 支持 `/say MESSAGE`、`/interrupt`、`/quit`。
  - `bin/tma event send` / `bin/tma event stream` 保留为脚本和调试入口。
- pending approval 不自动过期。
  - 系统不会后台 auto approve / auto reject / expire。
  - `session_interventions.status=pending` 会一直保留，直到用户 approve/reject。
  - `session attach` 启动时会主动查询 `status=pending` 并恢复本地审批提示，即使 `--after` 跳过了历史 `runtime.tool_intervention_required`。
- `expires_at` 已从模型和 Store 中移除。
  - `managedagents.SessionIntervention` 不再暴露 `expires_at`。
  - `PostgresStore.Save/List/DecideSessionIntervention` 不再写入、读取或 scan `expires_at`。
  - HTTP testStore / runner mock 不再构造 `ExpiresAt`。
- `000014` 迁移改为 cleanup migration。
  - 旧的 `000014_session_intervention_expires_at.sql` 被移除。
  - 新的 `000014_drop_session_intervention_expires_at.sql` 使用 `DROP INDEX IF EXISTS` 和 `DROP COLUMN IF EXISTS`，用于收敛已经跑过旧迁移的本地库。
  - `make migrate-up` 已验证在当前库上可重复执行。
- CLI 测试已跟随实现文件拆分。
  - `cmd/tma/attach.go` 对应 `cmd/tma/attach_test.go`。
  - `cmd/tma/stream_format.go` 对应 `cmd/tma/stream_format_test.go`。
  - `cmd/tma/main.go` 暂时只保留命令分发、普通 CRUD、HTTP client 和通用输出 helper。

已验证：

```bash
sh -n scripts/verify_intervention_flow.sh
GOCACHE=/private/tmp/tma-go-build-cache go test ./...
GOCACHE=/private/tmp/tma-go-build-cache make test
make db-up
make migrate-up
```

手工真实 LLM 验证结果：

- `.env` 中的 `volcengine-agent-plan / doubao-seed-2.0-pro` 能触发 `default.run_command`。
- 在 `request_approval` 下，事件停在 `runtime.tool_intervention_required`，工具不会提前执行。
- `session attach` 中发送消息、approve/reject 的交互式主流程已测通。

---

## Future: TMA Plugin 能力包机制

这里说的 Plugin 是 **TMA 项目自身的可安装能力包机制**，不是 Codex Plugin。

目标：

- 把 Agent 能力扩展标准化为可安装、可启用、可审计的包。
- 一个 TMA Plugin 可以包含：
  - skills：可注入 ContextBuilder 的任务说明、领域知识和操作规范。
  - tools：面向 LLM / UI 的工具 manifest、schema、human intervention policy 和 executor 路由。
  - MCP server / remote tools：外部系统连接器或远端能力。
  - hooks：围绕 turn、tool call、approval、文件修改、命令执行的生命周期扩展点。
  - assets：模板、示例、静态资源、UI 面板资源。
  - app / inspector panels：面向 Session Inspector 的可视化插件面板。
  - marketplace metadata：名称、版本、描述、权限、依赖、兼容版本。

第一版边界建议：

```text
tma_plugins
  id
  name
  version
  manifest_json
  enabled
  installed_at

tma_plugin_tools
  plugin_id
  tool_identifier
  api_name
  schema_json
  human_intervention
  executor_ref

tma_plugin_skills
  plugin_id
  skill_id
  content / path / metadata
```

运行时接入点：

```text
Session
  -> AgentRuntime
  -> PluginRegistry
      -> collect skills
      -> collect tools
      -> collect hooks
  -> ContextBuilder 注入 plugin skills / plugin tool manifests
  -> LLM tool call
  -> Tool Executor 路由到 plugin tool
  -> intervention policy 决定是否进入 approval
```

与现有模块的关系：

- `internal/tools` 当前是内置工具 registry / manifest / executor；未来可扩展为 builtin tools + plugin tools 的统一 registry。
- `internal/capability` 是底层能力面；plugin tool 可以包装 capability provider，也可以连接远端 MCP/tool backend。
- `ContextBuilder` 已经有 tools / skills 注入点，未来可以从 enabled plugins 收集并注入。
- `intervention_mode` 和 tool-level `human_intervention` 策略可直接复用于 plugin tools。
- `session attach` / 未来 Inspector 应能展示 plugin tool 的 pending approval、arguments preview 和执行结果。

建议顺序：

1. 先写 `docs/plugin-system.md`，固定 manifest schema 和安全边界。
2. 做只读 Plugin Manifest 解析，不执行外部代码。
3. 将 plugin tools 注册进现有 `tools.Registry`，先复用内置 executor。
4. 将 plugin skills 注入 ContextBuilder。
5. 再考虑 hooks、远端 MCP、UI panels 和 marketplace。

---

## 下一步建议

优先级建议：

1. 实现真实 Runner
   - `StartTurn(ctx, TurnRequest)`
   - `InterruptTurn(ctx, InterruptRequest)`
   - 给 `WorkerRunner` 接 Sandbox / Agent Runtime TurnExecutor
   - 真实执行失败时调用 `FailSessionTurn`

2. 增加 Postgres 集成测试
   - 用环境变量控制是否运行
   - 不影响普通 `make test`

---

## 常用命令索引

```bash
make fmt
make test
make build
make build-cli
```

```bash
make db-up
make migrate-up
make run
make db-down
```

```bash
bin/tma health
bin/tma usage list --session sesn_000001
bin/tma usage summary --group-by provider_model
bin/tma session attach --session sesn_000001 --after 0
bin/tma event list --session sesn_000001 --after 0
```

更完整的手动验收命令见：

```text
TESTING.md
```

---

## 2026-07-13: Server MCP logging 与 progress notification

本轮继续完成 Server 侧长驻 MCP host 的协议与可观测性能力，Worker 侧 MCP host 仍按当前产品边界暂缓：

- Agent MCP 配置新增 `logging.level`，支持 MCP 标准八级日志级别；initialize 后仅在 server 声明 logging capability 时发送 `logging/setLevel`，能力不匹配会明确失败。
- stdio、Streamable HTTP POST SSE 和长驻 GET SSE listener 统一识别 `notifications/progress` 与 `notifications/message`。
- progress notification 严格要求字符串或数字 `progressToken`、数字 `progress` 及可选数字 `total`；结构不合法时累计 invalid 计数。
- Host 只保存 progress/logging 数量、标准日志 level 分布和非法通知数，不保存或回显远端 token、message、logger、data、progress/total 值。
- tooling health、Prometheus 和 Workbench 已分别展示 stdio / Streamable HTTP Host 的 progress、日志和非法通知统计；Agent 编辑器可配置 transport、HTTP URL、SSE listener 与 logging level。
- Host debug 日志只记录 scope、method 和规范化 level，不记录 notification params；通用通知日志文案不再误称为 catalog changed。

本轮已执行：

```bash
go test ./internal/mcp -count=1
go test -race ./internal/mcp ./internal/execution -count=1
go test ./internal/serverconfig ./internal/tools ./internal/runner ./internal/observability ./internal/httpapi ./cmd/tma-server -count=1
npm --prefix apps/workbench run build
git diff --check
```

上述命令均通过。完整 `go test ./...`、stdio smoke、Server 重启和浏览器布局检查在本轮后续验收继续执行。

后续验收结果：

- `make verify-mcp-stdio` 通过，真实 smoke Session 为 `sesn_000221`。
- `go test ./... -count=1` 仅在并行负载下复现已知 `internal/runner.TestCommandTurnExecutorReturnsAgentPayload` 固定 5 秒超时；该用例独立 `-count=3` 连续通过，不归因于 MCP。
- Server 已重建并重启，`/health` 返回 `ok`；stdio 与 Streamable HTTP 的 notification / logging Prometheus 指标均已注册。
- 浏览器检查发现并修复 1280px 下 MCP 单行 grid 把页面撑宽的问题。修复后 1280px 页面 `scrollWidth` 等于 viewport 宽度，MCP row 无内部溢出或子控件相交；390px 手机 viewport 同样无横向滚动或重叠。
- Workbench 无保存交互验证通过：transport 切换为 Streamable HTTP 后显示服务 URL，SSE listener 解除禁用，logging level 可选择 `warning`；测试后刷新页面丢弃草稿。

---

## 2026-07-14: Remote MCP 出站安全治理

- Server 新增不可由 Agent config 覆盖的 Remote MCP egress policy，统一保护主 Streamable HTTP 请求、GET SSE listener、OAuth discovery 和 OAuth token endpoint。
- 默认 HTTPS-only，并阻止 RFC1918/ULA、loopback、link-local、云 metadata 和其他 special-use 地址；DNS 任一解析结果不安全时整次拒绝，dial 前再次解析校验并连接已验证 IP。
- host allowlist 支持精确域名与 `*.example.com` wildcard 语法；CIDR allowlist 用于最小范围放行内网。宽泛私网开关不会放开 loopback、link-local 或 metadata。
- redirect 只允许同 scheme/host/port，最多 10 次；secure transport 禁用环境 HTTP proxy，避免代理路径绕过目标 IP 校验。
- Host 配置指纹包含 egress policy；tooling health 与业务 runtime 共享同一 policy 和累计阻断数。
- tooling health、Prometheus 和 Workbench 仅暴露策略开关、allowlist 数量和 `egress_blocked_total`，不暴露内部 host、CIDR、DNS 结果或目标 URL。
- 阻断同时写入 reason-only `mcp_http_egress_blocked` 结构化安全审计日志，不记录目标或凭据数据。

验证命令与结果记录在 `TESTING.md` 的 MCP 自动测试章节；Worker MCP host 继续暂缓。

本轮验收结果：

- `go test -race ./internal/mcp ./internal/execution -count=1` 通过。
- `go test ./internal/serverconfig ./internal/tools ./internal/runner ./internal/observability ./internal/httpapi ./cmd/tma-server -count=1` 通过。
- `npm --prefix apps/workbench run build` 与 `git diff --check` 通过。
- `make verify-mcp-stdio` 通过，真实 smoke Session 为 `sesn_000223`。
- `go test ./... -count=1` 仍只复现已知 `internal/runner.TestCommandTurnExecutorReturnsAgentPayload` 并行 5 秒超时；独立 `-count=3` 连续通过。
- Server 实机使用 Agent `agt_000139` 对 metadata URL 执行 tooling health：请求被 `http_not_allowed` 阻断，响应未包含目标 IP/path，并在同一 health 响应及 Prometheus 中得到 `egress_blocked_total=1`。
- Workbench 浏览器验收显示“仅 HTTPS”“阻止私网”“Host 白名单 0”“CIDR 白名单 0”“出站阻断 1”和“运行告警”，未显示实际目标。

---

## 2026-07-14: Streamable HTTP MCP 真实 TLS 兼容性验收

- 新增 `scripts/mcp_http_fixture.py` 与 `make verify-mcp-http`，使用真实本机 TLS socket 验证 OAuth client credentials、initialize、Session/协议头、JSON/POST SSE、GET SSE/`Last-Event-ID`、tools/resources/prompts、logging/progress notification、Agent 工具执行和 shutdown `DELETE`。
- Make 入口同时纳入请求取消保留 Session、`404/410` 后重建、listener 重连和 Session 删除的定向 Go 测试，避免故障路径依赖不稳定的外部服务时序。
- 实机验收发现 macOS 上 `SSL_CERT_FILE` 不能作为可靠的 Server 私有 CA 配置，因此新增运维侧 `TMA_MCP_HTTP_CA_BUNDLE`。它把 PEM 证书追加到系统根证书池，保持 hostname/证书链校验和 TLS 1.2 下限，并统一覆盖 OAuth、POST、GET listener 与 DELETE。
- CA bundle 与 egress policy 都是 Server 全局治理配置，Agent config 无法覆盖，也不提供 insecure TLS 开关。
- `make verify-mcp-http` 已通过，最新真实 Session 为 `sesn_000235`；fixture 观测到 OAuth token、4 次 initialize、Session/协议头、POST SSE、listener 重连、目录探测、1 次工具调用和 1 次 shutdown DELETE。
- `go test -race ./internal/mcp ./internal/execution -count=1` 通过；Server config、observability、httpapi 与 `cmd/tma-server` 测试通过。并行批次仍复现已知 Runner 固定 5 秒超时，独立 `-count=3` 通过。
- 当前并行工作区的 `internal/tools.TestRegistryModelToolsUsesQualifiedFunctionNames` 独立失败：默认 Registry 已包含新增的 `agent` / `skills` / `web` API，测试仍断言旧 API 集合。本轮未修改该模块或测试，需由对应并行开发任务同步基线。
- Worker MCP host 继续暂缓。
- 产品决策：OAuth 浏览器授权、授权码回调、动态客户端注册和用户级 refresh token 托管不进入当前开发范围；企业 MCP 继续使用 `client_credentials` / 显式 `refresh_token`。仅在出现个人账号连接需求时重新启动该能力。

---

## 2026-07-14: Workspace MCP 注册表与固定版本 binding

- 新增 `internal/mcpregistry`、PostgreSQL store 和迁移 `000048_mcp_registry.sql`，支持 workspace server、不可变版本、canonical config checksum、`active` / `disabled` / `archived` 状态及当前 Agent 使用计数。
- 新增 `/v1/mcp-servers` 管理 API，覆盖列表、创建、详情、发布新版本、启停、连通性测试、版本历史和归档；写操作使用 control auth，并写入 `mcp_registry.*` operator audit。
- Agent MCP 配置新增 `bindings`。发布时 `version: 0` 固定为当时当前版本，runtime / tooling health 按固定版本解析；中央升级不会改变已有 Agent 或 Session。
- 停用是即时 kill switch，绑定解析会失败；仍有当前 Agent binding 时不能归档。旧内嵌 `mcp.servers` 保持兼容，可与中央 binding 并存。
- 中央配置拒绝 Authorization、Cookie、token、secret、password、API key 等敏感 header literal，只允许 `env_ref` / `secret_ref`。
- Workbench `设置 > MCP` 已接入注册表管理，`设置 > Agent` 可绑定中央 server，并对旧版本显示显式升级按钮。

真实 PostgreSQL 验收：

- 创建 `mcps_000001` v1，Agent `agt_000147` 使用 `version: 0` 发布后落库固定为 v1。
- 中央发布 v2 后 `usage_count=1`，Agent 仍保持 v1；Session `sesn_000231` 解析 v1 并调用 `filesystem.read_file`，返回 `tma-mcp-filesystem-ok`。
- 浏览器实测注册表连通性由“未检查”更新为 `online`；Agent 页面显示 v1 binding 和“升级到 v2”。1280px 桌面与 390px 手机均无横向溢出、控件裁切或重叠。
- `go test ./internal/mcpregistry ./internal/mcp ./internal/httpapi ./internal/runner ./internal/managedagents ./cmd/tma-server -count=1` 与 `npm --prefix apps/workbench run build` 已通过。
- 最终回归再次通过 `go test -race ./internal/mcp ./internal/mcpregistry ./internal/execution -count=1`、HTTP smoke `sesn_000232` 和 stdio smoke `sesn_000233`；完整迁移集确认执行到 `000048_mcp_registry.sql`。

范围维持不变：Worker 侧长驻 MCP host 继续暂缓，因为部署可能没有 Worker；个人账号 OAuth 浏览器授权继续按产品决策暂缓。

---

## 2026-07-14: MCP Registry PostgreSQL 强制租户隔离

- 新增 `000052_mcp_registry_rls.sql`，对 `mcp_registry_servers` 和 `mcp_registry_server_versions` 启用并强制 workspace RLS；version policy 通过父 server 校验 workspace。
- Registry PostgreSQL Store 的创建、列表、详情、更新、状态、版本和使用计数全部改为 transaction-local database scope，不再直接通过裸连接执行 Registry SQL。
- 按 ID 查询在已认证 context 中直接使用 Principal workspace；不会先做跨租户 workspace 归属查询。其他 workspace 的 server/version 统一不可见。
- 生产数据库启动自检扩展为 9 张 FORCE RLS 表和 8 条运行所需 sequence，迁移缺失、policy 缺失、runtime role 为 owner/superuser/BYPASSRLS 或权限不足都会拒绝启动。
- 真实 PostgreSQL 测试使用临时非 superuser runtime role，验证同 workspace Registry CRUD/版本读取、跨 workspace 创建拒绝、ID 探测隐藏、无 scope 零可见行和原始 SQL `WITH CHECK` 拒绝；测试通过。
- `make verify-mcp-http` 和 `make verify-mcp-stdio` 在启用 Registry FORCE RLS 后继续通过，真实 Session 分别为 `sesn_000235` 和 `sesn_000237`。

---

## 2026-07-14: MCP Registry 不可变历史版本恢复

- 新增 `POST /v1/mcp-servers/{server_id}/versions/{version}/restore`。恢复在事务内锁定 server，把历史 config/checksum 复制为新的 `current_version + 1`，不更新或删除历史版本。
- 响应返回 `source_version`、`previous_version`、`new_version` 和更新后的 server；当前/未来版本、缺失版本和归档服务恢复会被拒绝。
- 操作写入 `mcp_registry.version.restore` operator audit。中央当前版本变化不会修改已有 Agent binding、Agent config version 或 Session。
- Workbench `设置 > MCP` 增加版本历史、时间、checksum、canonical config 展开和二次确认恢复；桌面与移动布局继续复用响应式 Registry grid。
- HTTP 测试覆盖 v1 -> v2 -> 恢复 v1 为 v3、checksum 一致、Agent 固定 v1 不变、审计和非法 source 拒绝。
- 独立 `TestPostgresMCPRegistryRestoreWithRLS` 使用真实非 superuser 角色通过，覆盖恢复事务、RLS 跨 workspace 隐藏和无 scope 零可见行。
- `go test -race ./internal/mcpregistry ./internal/httpapi -count=1` 通过；Server `/health` 返回 `ok`，Registry fixture `mcps_000001` 保持 v2 和 `usage_count=1`。
- Workbench 已完成 1280x900 桌面与 390x844 手机实测：v2/v1 历史、当前标记、checksum、canonical config 展开及恢复二次确认均正常；手机端 JSON 和操作按钮完整位于单列卡片内，页面无横向溢出，浏览器控制台无 error/warn。
- 浏览器验收只执行到恢复确认后取消，没有改变 Registry 当前版本或已有 Agent binding。

---

## 2026-07-14: MCP Registry 隔离端到端验收

- 新增 `scripts/verify_mcp_registry.sh` 和 `make verify-mcp-registry`。入口使用一次性 PostgreSQL 数据库、两个 Workspace JWT 和临时非 superuser/无 `BYPASSRLS` runtime role运行真实 Server，结束或失败时自动删除数据库和角色。
- smoke 通过公开 API 完成 Registry v1 创建、Agent `version: 0` 固定为 v1、中央 v2 发布、固定 v1 Session 真实 MCP tool call、v1 恢复为 v3、checksum 校验、归档冲突、停用 kill switch、重新启用和 v3 连通性测试。
- 跨租户路径验证 Alpha 请求不能通过 body 伪造 Beta workspace，Beta JWT 按 Alpha server ID 查询返回 `404`；测试 Server 由受限 runtime role 连接，避免迁移 owner/superuser 绕过 RLS 形成假通过。
- 真实 smoke 发现 Registry 管理动作把 `workspace_id` 误传给通用审计函数的 `session_id` 参数，导致 PostgreSQL `operator_audit_log_session_id_fkey` 拒绝写入。现已新增 Workspace 级审计入口，Registry audit 正确保存 workspace、空 session 和 source/previous/new version，并补内存 HTTP 回归断言。
- `make verify-mcp-registry`、`go test -race ./internal/mcpregistry ./internal/httpapi -count=1`、脚本语法检查和 `git diff --check` 均通过；清理检查确认没有残留 `tma_verify_mcp_registry_*` 数据库或临时 runtime role。

---

## 2026-07-14: Inspector MCP Protocol 脱敏诊断

- Inspector 新增独立 `MCP Protocol` 面板，把 MCP `runtime.tool_call` / `runtime.tool_result` 按 call ID 和事件顺序配对，展示 method、request/response seq、状态、耗时、transport、protocol、capabilities、result protocol、artifact 数量和脱敏结果摘要。
- resources/prompts 桥接 API 分别映射为 `resources/list`、`resources/read`、`prompts/list`、`prompts/get`；普通工具映射为 `tools/call`。重复 call ID 使用 FIFO 配对，缺少一侧的操作保留为 pending/unpaired。
- 投影白名单不包含 arguments、endpoint、headers、error message、content/resource/prompt text 或 structured content value。单测注入了 URL、Bearer token 和敏感正文，确认序列化后的操作模型不含这些值。
- 真实 Session `sesn_000231` 浏览器验收显示 `seq 10 -> seq 11`、stdio、MCP protocol `2025-06-18`、69ms 和 `tma.mcp_result.v1`；1280px 无横向溢出，390px request/response 自动转为垂直链路且节点完整位于卡片内。
- 浏览器面板范围确认不含工具 marker、`README.md`、Authorization 或 endpoint，控制台无 error/warn。`npm --prefix apps/inspector test -- --run` 和 `npm --prefix apps/inspector run build` 通过。

---

## 2026-07-14: MCP Server 版本级生产运行保护

- 新增进程级 `RuntimeGuard`，由 Server 中的 stdio 与 Streamable HTTP MCP runtime 共享；Host Session 继续按 workspace/session/Agent/config 隔离，但并发预算和熔断按 Workspace + Registry server + 固定版本跨 Session 共享。旧内嵌 server 按 Workspace + identifier 分区。
- MCP server config 新增 `runtime.timeout_seconds`、`max_concurrency`、`failure_threshold`、`cooldown_seconds`，默认分别为 30 秒、4、5、30 秒，并限制最大值为 600 秒、64、100、3600 秒。
- 调用 deadline 包含等待并发槽；连续失败达到阈值后进入 open，冷却后只允许一个 half-open 探测，成功才恢复 closed。调用方 cancel 不增加连续失败。
- `tools/list/call`、resources 和 prompts 都经过同一保护层；没有任何自动 retry/replay，超时 `tools/call` 定向测试确认底层只调用一次。
- 失败分类限定为 canceled、timeout、authentication、rate_limited、transport、protocol、unavailable、unknown；并发等待和熔断拒绝分别使用 concurrency_wait、circuit_open。
- MCP runtime 失败转换为 `mcp_<class>` 的标准 `ExecutionError` 和固定脱敏文案，使 Inspector 保留 `runtime.tool_result`，同时排除 endpoint、Authorization、arguments 和远端正文。
- tooling health 新增顶层 `mcp_runtime_guard` 聚合快照；Prometheus 新增 `tma_mcp_runtime_guard_*` gauge/counter，失败和拒绝只使用有限分类 label，不导出 workspace、server ID、URL 或工具名。
- manifest 事件 metadata 新增 timeout、max concurrency、failure threshold 和 cooldown 四项非敏感策略事实。

验证结果：

- RuntimeGuard/config 定向测试、tools 脱敏测试、Registry 分区测试、metrics/tooling health 测试均以 `-count=3` 通过。
- `go test -race ./internal/mcp ./internal/mcpregistry ./internal/observability -count=1` 通过；tools 与 httpapi 新增用例的定向 race 通过。
- `make verify-mcp-registry` 通过，真实隔离数据库中的固定 v1 Session 成功执行 MCP tool，v2 发布、v1 恢复为 v3、停用 kill switch 和 Workspace 审计保持正常。
- 主 Server 已自动重载新二进制，日志确认 RuntimeGuard 默认值为 30 秒/4 并发/5 次失败/30 秒冷却；`/health` 返回 ok，`/metrics` 已注册 `tma_mcp_runtime_guard_*`。
- 组合包测试仍出现并行工作区已有的 artifact workspace path 和默认工具文案断言失败；与 MCP 变更无关，本轮未修改相关文件。
- Worker MCP host、个人账号 OAuth 浏览器授权、sampling/elicitation backend 和原始 JSON-RPC payload 持久化继续暂缓。

---

## 2026-07-14: 第三方 MCP Server 兼容性矩阵

- 真实官方 Server 验收发现原 stdio client 只支持 LSP 风格 `Content-Length`，而当前官方 TypeScript MCP SDK 使用 newline-delimited JSON-RPC。新增 `stdio_framing`；Workbench 新建 stdio 配置显式使用 `json_lines`，省略字段的历史配置继续按 `content_length` 解释，避免升级改变不可变版本行为。
- framing 已贯穿 canonical config、resolved client、stdio session 和 host 配置指纹；不同 framing 不会复用同一协议流。仓库 Python fixture 的 stdio、Registry、RuntimeGuard 配置全部显式固定 `content_length`。
- 新增本地 JSON Lines helper 测试和配置校验，覆盖读写、默认值、legacy 模式、非法值以及 Streamable HTTP 拒绝 stdio-only 字段。
- 新增 gated `TestExternalMCPCompatibility` 与 `make verify-mcp-compatibility`，固定 npm 包版本并真实执行 initialize、catalog 和工具调用。默认 `go test ./...` 不联网。
- 兼容测试显式断言 Server 返回完整 name/version，并确认所有固定版本 Server 与 TMA 协商 MCP `2025-06-18`；不再只用后续工具调用间接推断 initialize 兼容。
- Official Filesystem `2026.7.10` 通过，Server 自报 `secure-filesystem-server 0.2.0`，加载 14 个工具并实际执行 `read_text_file`。macOS 路径需先 canonicalize，避免 `/var` 与 `/private/var` 被目录边界判为不同路径。
- Official Filesystem initial Roots 模式通过：不传命令行目录，Server 在 initialized 后反向请求 `roots/list`，TMA 返回配置 root 后，`list_allowed_directories` 与 `read_text_file` 真实调用均通过。官方实现会在 Roots 响应后异步解析目录，首个紧邻调用可能先看到空目录，因此验收使用有界轮询确认收敛；要求首调用确定性时继续推荐命令行 root。当前明确声明 `listChanged=false`；运行期不热改 Roots，配置变更通过新 runtime/host 生效。
- Official Memory `2026.7.4` 通过，Server 自报 `memory-server 0.6.3`，加载 9 个工具并通过 `create_entities -> read_graph` 验证 host 有状态复用。
- Official Sequential Thinking `2026.7.4` 通过，Server 自报 `sequential-thinking-server 0.2.0`，实际工具名是 `sequentialthinking`，调用返回合法 structured content。
- Official Everything `2026.7.4` 的 Streamable HTTP 模式通过：测试在本机随机端口启动固定 npm 包，Server 自报 `mcp-servers/everything 2.0.0`；TMA HTTP host 在同一 Session 中加载 13 tools、7 resources、4 prompts 并完成 `echo` marker 调用，关闭后进程无残留。该本地测试不放宽生产 egress policy。
- PostgreSQL 不采用已废弃且存在历史 stacked-query 风险的官方旧包，固定活跃维护的 `@yawlabs/postgres-mcp@0.6.20`。验收自动创建随机一次性数据库和最小权限角色，显式 `ALLOW_WRITES=0`、数据库 role `default_transaction_read_only=on`，并以 `secret_ref: env:...` 在启动时解析 `DATABASE_URL`。21 tools 加载、参数化查询、两行结果截断、`pg_query` / `pg_readonly` 写入拒绝、stacked query 拒绝及最终数据不变均通过；数据库、角色和进程无残留。
- 新增 `docs/mcp-server-compatibility.md`，记录固定版本、framing、工具数、真实调用和待认证能力。Filesystem initial Roots、本地真实 Streamable HTTP 与只读 PostgreSQL 已认证；远程认证企业服务、账号型 SaaS 和其他数据仓库 MCP 继续留在后续矩阵。
- Secrets 产品边界保持不变：由环境变量或现有环境变量管理模块维护，配置只存引用，使用时按 Workspace/Session 注入；当前不开发 Vault/JIT。

---

## 2026-07-14: Workbench 单 Registry Server 熔断状态

- RuntimeGuard entry 新增结构化 Workspace/Registry server/version 分区和最近失败事实，并提供 `RegistryStates(workspaceID)` 脱敏投影；状态包含 closed、saturated、open、half_open、并发、连续失败、阈值、最近失败分类和冷却剩余秒数。
- 新增 `GET /v1/mcp-servers/runtime-status`。handler 使用 principal Workspace，并通过该 Workspace 当前 Registry server 列表二次过滤；embedded、未知、已归档和其他 Workspace 状态不返回。
- 零值 `last_failure_at` / `open_until` 不序列化。响应不包含内部 guard key、Workspace ID、identifier、URL、headers、arguments 或 MCP 内容。
- Workbench `设置 > MCP` 的 server 列表增加最严重版本状态 badge，服务详情增加逐版本运行保护行；支持图标按钮手动刷新，不主动轮询或触发 MCP 健康调用。
- 前端有限标签覆盖“未运行、正常、并发已满、已熔断、恢复探测”及八类失败原因；未知后端值不会直接呈现给用户。
- 新增前端纯函数测试，覆盖 server 分组、严重度排序和有限中文标签；Go 测试覆盖 Workspace 隔离、版本顺序、状态计算、冷却时间和 Registry 白名单过滤。

验证结果：

- `npm --prefix apps/workbench test` 79 项通过，`npm --prefix apps/workbench run build` 通过并更新内嵌 bundle。
- RuntimeGuard Registry state 与 HTTP runtime-status 测试均以 `-count=3` 通过。
- 主 Server 重启后先显示“未运行”；现有 `sesn_000231` 再次执行固定 v1 fixture 后，API 与 Workbench 显示 closed、并发 0/4、连续失败 0/5。
- 1280x900 与 390x844 浏览器检查均无 document/body 或 Registry/runtime 容器横向溢出，console 无 error/warn。

---

## 2026-07-14: MCP RuntimeGuard 真实故障注入验收

- 修复 RuntimeGuard 连续失败被跨类型成功调用错误清零的问题。`tools/list` / `resources/list` / `prompts/list` 归入 catalog 域，`tools/call` / `resources/read` / `prompts/get` 归入 operation 域；只有同域成功才清零，half-open 探测成功仍完整关闭熔断。
- 新增 `TestRuntimeGuardCatalogSuccessDoesNotResetToolCallFailures`，覆盖每个 Turn 先成功加载目录、随后工具持续失败时仍能达到阈值；RuntimeGuard 定向测试 `-count=3` 和 race 均通过。
- 扩展 `scripts/mcp_stdio_fixture.py`，通过模式文件支持 success、timeout、transport、JSON-RPC unavailable 和 protocol 五种真实故障，并把每次底层 `tools/call` 追加到调用文件，作为无 retry/replay 的独立事实。
- 新增 `scripts/verify_mcp_runtime_guard.sh` 与 `make verify-mcp-runtime-guard`。入口使用一次性数据库、Workspace JWT 和受限 runtime role，真实验证两次 timeout 后 open、open 拒绝不增加调用、冷却后 half-open、成功恢复 closed、事件脱敏、runtime-status 字段白名单和 Prometheus 指标。
- 真实验收通过：Session `sesn_000001` 的 fixture 调用序列精确为 `timeout, timeout, success`，最终 `closed`、连续失败 0，临时数据库和角色已清理。
- 新增 `scripts/verify_mcp_all.sh` 与 `make verify-mcp-all`，聚合 stdio、Streamable HTTP、Registry、RuntimeGuard、MCP 相关 race、Workbench/Inspector 测试与构建和 `git diff --check`。stdio/HTTP 共用一次性全迁移数据库，Registry/RuntimeGuard 使用各自受限数据库，避免开发库历史数据影响聚合验收。
- 四套 MCP fixture Server 显式固定 development 下的 disabled/JWT 认证并关闭 browser OIDC，避免仓库 `.env` 的登录配置污染验收。首次运行还发现开发库现有 Skills 数据使通用 `migrate-up` 在 `000042` 约束重建处失败；RuntimeGuard/Registry 使用独立数据库，不依赖或修改该开发库。
- `make verify-mcp-all` 最终完整通过：四套真实 E2E、MCP/Registry/execution/observability race、84 项 Workbench 测试、10 项 Inspector 测试、两套生产构建和 `git diff --check` 全部成功；临时数据库和角色清理检查无残留。
- Worker MCP host、个人账号 OAuth 浏览器授权、sampling/elicitation backend 和原始 JSON-RPC payload 持久化继续暂缓。

---

## 2026-07-14: MCP Client 2025-06-18 完成度审计

- 对照 MCP `2025-06-18` 协议补齐 `resources/templates/list`，支持 stdio 与 Streamable HTTP、`nextCursor` 分页、重复 cursor 防护和 `-32601` 可选能力降级。
- 新增 `completion/complete` 的 Prompt/Resource reference、当前 argument 和 context arguments 支持，并校验必填 reference、argument name 与最多 100 个 completion values；能力同时贯穿短会话 Client、长驻 HostedClient 和 RuntimeGuard。
- `initialize` 的 `completions` 声明进入 tooling health 诊断。completion 保持底层 Client/HostedClient 能力，不自动注册为模型工具，避免扩大 Agent 工具面。
- `expose.resources` 新增只读 `mcp_list_resource_templates` 桥接；tooling health 与 Workbench 增加 `resource_template_count`，与 resource/prompt catalog 一起展示。
- Inspector 把该桥接识别为 `resources/templates/list`，并对 `tma.mcp_context_result.v1` 只展示 template 数量，不展示 URI template 或远端正文。
- Official Everything `2026.7.4` 真实 Streamable HTTP 验收扩展为 13 tools、7 resources、4 prompts、2 resource templates，并分别验证 Prompt `department=Eng -> Engineering` 与 Resource `resourceId=12 -> 12` completion。
- stdio 与 Streamable HTTP 单测覆盖 template 分页、completion context、非法输入/响应、可选方法降级；tools/httpapi 测试覆盖桥接执行、capability 和健康计数。
- `resources/subscribe`、`resources/unsubscribe` 与 `notifications/resources/updated` 暂缓。当前 stdio host 没有空闲期独立 reader 和可消费的资源更新事件接口，因此不声明或实现无法可靠交付通知的半成品订阅能力。
- Secrets 边界保持不变：配置只保存 `env_ref` / `secret_ref: "env:NAME"`，使用时由 Server 进程解析注入；不开发 Vault/JIT。

验证结果：

- `go test` 与 `go test -race` 对 `internal/mcp`、`internal/tools`、`internal/httpapi` 全部通过。
- Workbench 84 项测试和生产构建通过；Inspector 测试与生产构建通过，两套内嵌静态资源均已更新。
- `make verify-mcp-compatibility` 全部通过；一次性 PostgreSQL 数据库、只读角色和全部外部 MCP Server 进程均已清理。
- 主 Server `/health` 返回 `ok`，`git diff --check` 通过。

---

## 2026-07-14: Go Core SDK 与 Server API v2

- 新增 Server/Core SDK/App Extension 分层和 Go SDK 开发文档，明确 Capability Extension、Solution Package、认证 Runner 与多 Server 边界。
- 新增 `/v2` 用户与控制面兼容层，保留 `/v1`；统一 v2 request ID 与结构化错误，并屏蔽 Worker register/heartbeat/poll/ack/result 机器协议。
- 基于现有 `session_turns` 新增一等 Run API，支持创建、列表、详情、取消、Run Event/SSE 和幂等键；`000071` 为 Turn/Event 增加幂等摘要和可索引 turn 归属。
- 新增 `sdk/tma`，提供认证、TokenSource、APIError、通用领域 Service、Session/Run/Intervention/Artifact、可恢复 SSE 与 Run Wait/Cancel/Approve/Reject。
- 新增完整 v2 OpenAPI 路由清单、Event JSON Schema、`oapi-codegen` 低层客户端和契约覆盖测试；CLI 用户与控制面请求已迁移到 SDK 和 `/v2`，Worker 消费协议继续使用 `/v1`。

---

## 2026-07-15: Core SDK 类型化服务与双 Server SSE 恢复

- Sessions、Interventions 和 Artifacts 增加类型化 create/get/archive/restore/delete、runtime settings、config upgrade、summary、usage、Event list/append/stream、审批决策和 Artifact create/list/download/delete 方法。
- CLI 的 Session、attach、Event、审批与 Artifact 命令改为直接调用类型化 SDK；新增 AST 边界测试，禁止这些命令重新通过通用 HTTP helper 拼接 Session API。
- SDK request builder 保留 escaped `RawPath`，路径参数中的保留字符不会在二次 URL 解析时退化成额外路径段。
- Run 迁移顺延为 `000071_run_api_v2.sql`，避免与新增的 `000070_llm_model_revision.sql` 冲突。
- PostgreSQL Store 测试覆盖连接断开期间由另一实例写入、按 `after_seq` 补发及一个轮询周期内无重复；HTTP 集成测试真实启动 Server A/B，共享同一数据库，由 B 写入、A 消费并断线恢复。

---

## 2026-07-15: Agents、Environments 与 LLM 强类型 SDK

- Core SDK 新增 Agents、Environments 和 LLM 类型化服务，覆盖 Agent 创建/查询/列表/更新/config version、Environment 创建、Provider 管理、Model 管理和聚合用量查询；Agent 的 tools、MCP、Skills 保持 `json.RawMessage`，部分更新使用指针区分省略与显式空值。
- SDK 新增不重试的 `DoJSONWithHeaders`。Provider 条件写和 Model 更新/删除发送带引号 revision 的 `If-Match`，Model 创建发送 `If-None-Match: *`。
- CLI 的 provider、model、agent、agent config、env 和聚合 usage 命令迁移到类型化 SDK；attach 的 Agent 版本查询不再拼 URL。`model upsert` 先按 Provider 查询：存在时保留未修改字段并按 revision 更新，不存在时执行条件创建。
- CLI AST 边界测试扩展到上述命令，禁止重新调用通用 `do/download/stream`；SDK 与 CLI 测试覆盖转义资源 ID、条件头及 Model create/update 分支。
- OpenAPI 生成器为 Agent、Environment、LLM、Session、Run、Event、Intervention 和 Artifact 核心操作补充显式请求/响应 schema，并记录 LLM 条件头；契约测试阻止核心接口退回无类型对象，`oapi-codegen` 产物已重新生成。

---

## 2026-07-15: Core SDK 控制面边界收敛

- Core SDK 新增 ObjectRefs、Traces、Workers、WorkerWork 和 Observability 类型化服务；CLI 的 Object、Trace、Observability、Worker 管理和 Work 管理动作迁移到这些服务。
- Workers 服务只暴露 list/get/archive/reap/diagnose，WorkerWork 服务只暴露 enqueue/get/cancel/requeue/reap/diagnose。register/heartbeat 与 poll/ack/heartbeat/result 继续保留在 `/v1` Worker 机器协议，不进入 Core SDK `/v2` 契约。
- WorkerWork enqueue 冲突的候选 Worker 诊断由 v2 中间件规范化到 `error.details`；SDK 在同一次响应内解码 `WorkerWorkConflict`，测试明确断言写请求不会为诊断而重放。
- CLI 边界测试禁止 Object/Trace/Observability 回退到通用 helper，并禁止 Worker/Work 文件重新出现 `/v1` 控制面路径。当前通用 CLI HTTP 调用只剩 `/health` 和明确排除的 Worker 消费协议。
- OpenAPI 为 ObjectRef、Trace、Worker/WorkerWork 控制面和 Observability 增加显式 schema，继续排除 Worker register/heartbeat/poll/ack/result；低层 Go Client 已重新生成。

---

## 2026-07-15: Workbench Templates 与 Orchestration 边界

- `GET /v1/task-templates` 保留给现有 Web App；v2 alias 显式拒绝 `/v2/task-templates`，OpenAPI 生成器和 Go 低层客户端同步排除该路径。
- Core SDK 删除 `Templates` 服务分组。`/v2/agent/task-group-templates` 和 `/v2/agent/discussion-strategies` 保留并通过类型化 `OrchestrationService` 暴露。
- Server 测试覆盖 v1 task templates 成功、v2 返回 404，以及两个 v2 Orchestration 接口继续成功；契约和 SDK 反射测试防止 legacy Templates 再次进入 v2 或 Client。
- 外部 ERP、Office、机器人和 SaaS 工具统一通过 MCP 接入，不规划 `POST /v2/tools` 或 Declarative HTTP Tool。后续只规划统一只读 Tool Catalog，用于聚合脱敏发现元数据，不承担注册、凭据保存或执行。

---

## 2026-07-15: API v2 响应与数据编码标准

- 新增 `docs/api-v2-response-and-data-standards.md`，固定成功/错误 HTTP 状态、字符串业务错误码、retryable、JavaScript 安全整数、UTC 时间、字符串枚举和 cursor 分页规则。
- v2 错误中间件补齐 405/412/413/415/422/429/502/503/504 分类；500 默认不可重试，502/503/504 和 429 默认可重试。SDK legacy error fallback 使用相同矩阵。
- 新增 `ErrRevisionConflict` 和 `ErrSessionBusy`。LLM revision/expected_version 过期返回 412 `revision_conflict`；Run 并发启动返回 409 `session_busy`；幂等冲突继续返回 409 `idempotency_conflict`。
- 缺少或格式非法的条件头返回 400；重复创建和资源占用仍返回 409。OpenAPI 所有 int64 schema 增加 `maximum: 9007199254740991`，整数版本和 attempt/limit/count 标记为 int32。

---

## 2026-07-15: Go Core SDK 真实 Server E2E

- 新增 `make test-sdk-e2e`，通过公开 Go SDK 调用真实 Server handler，不使用伪造 HTTP response。
- 验收链路覆盖 Agent、Environment、Session、Run、`waiting_approval`、RunHandle Approve、SSE Wait、Run Event 归属，以及 LocalFS Artifact 上传、列表和下载。
- Runner fixture 只承担执行侧职责：首次调度产生审批请求，批准后的 resume 完成 Turn；资源和动作仍全部通过 Server API 与 SDK。

---

## 2026-07-15: MCP、Auth、Audit 与 EnvironmentVariables 类型化

- Core SDK 将 `MCP` 和 `Auth` 从通用 `Service` 替换为 `MCPService` 与 `AuthService`，新增独立 `AuditService` 和 `EnvironmentVariablesService`。
- MCP 高层方法覆盖 Registry 生命周期、test、不可变版本 restore 和 runtime status，canonical config 使用公共 `MCPServerConfig` 与严格的 literal/`env_ref`/`secret_ref` JSON 联合类型；Audit 覆盖全局/Session operator audit、integrity key 状态和 dead-letter replay；环境变量只暴露脱敏 metadata。
- OpenAPI 为四个域的全部 operation 增加精确请求、响应、成功状态、query/path 参数和组件 schema；MCP canonical config、secret reference 联合类型、runtime state 和 health result 均进入生成契约。
- SDK mock HTTP 测试覆盖全部高层方法、资源 ID 转义和查询编码；真实 Server E2E 覆盖 Auth disabled 状态、MCP 创建/列表/版本、环境变量加密写入/脱敏读取/删除和 operator audit 查询。

---

## 2026-07-15: Skills Core SDK 类型化

- Core SDK 将 `Skills` 从通用 `Service` 替换为 `SkillsService`，覆盖核心 Skill、不可变版本、package 下载、resolve preview、Session usage、package backfill、asset retention policy 和 GC。
- 公共类型覆盖 manifest、asset bundle/SBOM、package manifest、resolved context、usage、retention policy/version、GC candidate/run/item/tombstone；动态 JSON 只保留在 inputs schema、per-use inputs 和 metadata 等扩展点。
- Skill assets 支持历史数组与新 bundle 对象联合解码；OpenAPI 使用 `oneOf` 表达兼容格式，并将 version/limit/count/attempt 标为 int32、token/bytes 标为 JavaScript-safe int64。
- OpenAPI 为全部非 Marketplace Skills operation 增加精确请求、响应、状态码、ZIP content type 和 query/path 参数；Marketplace discover/install、entry/policy 与 installed Skill enable/disable 明确保留到下一批。
- mock HTTP 测试覆盖全部 SkillsService 方法与 URL 转义；真实 Server E2E 覆盖 Skill/version、preview、Session usage、retention 和 GC，`make test-sdk-e2e` 已纳入该链路。

---

## 2026-07-15: Marketplace Core SDK 类型化

- Core SDK 将 `Marketplace` 从通用 `Service` 替换为 `MarketplaceService`，覆盖外部 discover/preview/install、内部目录 browse/preview/install、installed Skill enable/disable、目录条目审核状态流转和 Marketplace policy 版本生命周期。
- 公共类型覆盖 source/candidate、package preview、policy decision、attestation/static/binary security report、SBOM、entry 和 policy；Skill inputs 是 Marketplace 类型中唯一保留的动态 JSON 业务扩展点。
- OpenAPI 为全部 Marketplace operation 增加精确 request/response、查询参数、201 创建状态、enable/disable 的 200/201 双状态和 int32/int64 数字格式；策略版本 path 参数固定为 int32。
- Server 对空 entry/policy 列表统一返回 `[]`。SDK mock 测试覆盖全部 MarketplaceService 方法、查询编码和资源 ID 转义；真实 Server E2E 覆盖发现、预览、安装、启停、目录发布和策略版本生命周期。
- 本批未新增 Tool Catalog、工具创建或直接执行接口；Worker 消费者机器协议继续停留在 `/v1`。

---

## 2026-07-15: Skills 与 Marketplace CLI

- CLI 新增 `skill` 命令组，覆盖 Skill 生命周期、不可变版本与 package 下载、resolve、Session usage、package backfill、asset retention policy 和 GC。
- CLI 新增 `marketplace` 命令组，覆盖外部/内部 discover、preview、install、installed Skill enable/disable、entry 审核流和 policy 不可变版本。
- 所有新命令只调用 `SkillsService` 或 `MarketplaceService`；AST 架构测试禁止重新引入通用 HTTP helper 或公开 API URL 拼接。
- CLI mock 测试覆盖 JSON flag、查询编码、资源 ID 转义、多标签、package 下载和策略请求。Tool Catalog、工具创建和直接执行仍不在 CLI 范围内。

---

## 2026-07-15: API v2 通用 Schema 清理

- 为剩余 Agent 可移植性/tooling health、Session 生命周期与 Artifact multipart、Deliberation/Task Group、Trace/Span 路径补齐精确请求、响应、参数、Content-Type 和成功状态。
- Session usage record、Trace/Span 和 Observability status 的固定结构不再使用任意 object；Go 低层客户端已重新生成对应类型。
- OpenAPI 生成器遇到未登记的公开 `/v2` 路径会直接失败，不再输出空 Schema 或通用 `2XX`。
- 有意保留的事件 payload、运行输入、JSON Schema、扩展配置和 metadata 使用 `DynamicJSONValue` 或 `x-tma-dynamic-json: true`；契约测试禁止未标记的 `additionalProperties: true`。

---

## 2026-07-15: CLI OIDC Device Authorization

- Server 新增公开的 `/v1/auth/config` 与 `/v2/auth/config`，只发布认证模式、OIDC issuer/audience、CLI public client ID、scopes 和 Device Flow 能力，不返回 secret；Go Core SDK 通过类型化 `Auth.Configuration` 暴露该配置。
- CLI 新增 `auth login/status/logout`。登录使用 RFC 8628 Device Authorization Flow，不收集用户密码；access/refresh credential 按 Server base URL 存入系统 Keychain，显式 `--auth-token`/`TMA_AUTH_TOKEN` 继续具有最高优先级。
- SDK 仍只负责静态 Token 或 `TokenSource` 注入，不包含登录 UI、refresh token 持久化或 Keychain 依赖。CLI 在应用层刷新并回写凭据；logout 尝试撤销 refresh token，IdP 撤销失败也会删除本地凭据并返回 warning。
- Keycloak realm 增加 `tma-cli` public client、Device Grant、groups claim 和 `tma-api` audience mapper。`scripts/keycloak_cli_client.sh` 与 Make target 可对已有 volume 幂等 apply/verify，避免 realm import 不覆盖历史数据库。

---

## 2026-07-15: Core SDK 扩展与 Trace Cursor 分页

- `/v2/traces` 与 `/v2/spans` 改为不透明 cursor，响应固定为 `items/next_cursor/has_more`；cursor 绑定资源类型和筛选条件。`/v1` 继续返回 `traces|spans/offset/next_offset`，Server 暂时兼容 v2 `offset`，但 OpenAPI 与公共 SDK 不暴露旧参数。
- Agents 类型化服务补齐 Default、portable Import/Export、config Rollback 和 ToolingHealth；Sessions 补齐 metadata、Delete、Rerun、Compare、RuntimeConfig 和 RuntimeCapabilities；Orchestration 补齐 Deliberation、Task Group、cancel/retry 和 orphan reap；Traces 补齐 List/Get/ListSpans/GetSpan。
- SDK httptest 覆盖查询编码、资源 ID 转义、201/204、动态 JSON 和未知状态兼容；既有 Artifact 测试继续覆盖 multipart。真实 Server E2E 覆盖 Session rerun/approve/compare、Task Group 查询、Trace 投影、cursor 翻页和 Span 详情。
- CLI 只增加有运维价值的 `agent export/import`、`session compare`、`trace list` 和按 Trace ID show，没有机械暴露全部 Orchestration 控制动作。

---

## 2026-07-15: TypeScript / Node Core SDK Alpha

- 新增仓库内 `sdk/typescript` 与 `@tma/core-sdk@0.1.0-alpha.1`，最低 Node 20。OpenAPI 使用 `openapi-typescript` 生成 paths/components/operations，`client.raw` 通过 `openapi-fetch` 覆盖全部 `/v2` 用户与控制面。
- 首批高层服务覆盖 Auth、Agents、Environments、Sessions、Runs/RunHandle、Interventions、Artifacts、Traces 和 Orchestration；TokenSource、APIError、下载、multipart 和 SSE 统一由公共 transport 实现。
- SSE 使用 `eventsource-parser`，只对网络错误和 5xx 持续重连并携带 `after_seq`；401/403/404 和 Schema 错误立即返回。AbortSignal 不隐式取消远端 Run，普通写请求不自动重试。
- Vitest 覆盖 TokenSource、查询/ID 编码、201/204、APIError、multipart、未知状态、SSE 5xx 重连和 RunHandle wait。`make test-typescript-sdk-e2e` 由真实 Server handler 驱动编译后的 Node 包完成 Agent、Environment、Session、Run、SSE、Trace 和 Orchestration 链路。
- 第二批高层服务补齐 LLM、ObjectRefs、Workers/WorkerWork 控制面、MCP、Observability、Audit、EnvironmentVariables、Skills 和 Marketplace。公共类型直接引用 OpenAPI 生成 Schema，保留动态 JSON；LLM revision 使用带引号 `If-Match`，模型首次创建使用 `If-None-Match: *`。
- Worker 高层服务明确排除 register/heartbeat/poll/ack/result 机器协议；SDK 仍不包含 Tool Catalog、工具创建、直接执行和旧 `/v1/task-templates`。新增 Vitest 覆盖全部第二批 service operation、重复 tag、资源转义、下载、204、未知状态与机器协议边界。
- TypeScript 真实 Server E2E 扩展到 LLM、ObjectRef、WorkerWork、MCP、环境变量加密与脱敏、Skill resolve、Marketplace policy、operator audit 和 Observability；OpenAPI 生成物逐字节检查继续作为 E2E 前置门禁。
- Alpha 稳定化补齐全部领域 service 的包根导出；新增公共 API 编译契约，固定 service 类型并禁止 Worker register/poll 与 Templates 回流。新增无 Node 类型的浏览器编译和 npm dry-run 内容门禁，发布包只允许 dist、README 与 package metadata；`private: true` 继续防止误发布。
- Web App 启动只读 SDK 试点：本地依赖 `@tma/core-sdk`，将 Auth.me、Session list/get 切换到 `/v2` 高层服务。`core-sdk.js` 动态复用现有认证 fetch，`api.js` 保持 `{sessions}` 等 React 调用形状；Vite 增加 `/v2` 与 `/auth` proxy。写操作、事件流与 `/v1/task-templates` 均保持不变，`build-workbench-ui` 先验证 SDK。
- Web App 第二批只读迁移增加 Agent default/list/get 与 LLM provider/model list，并继续返回 `{agents}`、`{providers}`、`{models}` 兼容形状。精确 URL 测试覆盖资源 ID 和 provider query 转义，并单独固定 `/v1/task-templates` 不得迁移；Agent/LLM 写操作仍使用原 `/v1` 路径。
- Web App 第三批只读迁移增加 MCP list/runtime-status、Skills list/versions、EnvironmentVariables list 和 Observability status，总计 14 个 SDK 查询方法。适配层继续返回 `{servers}`、`{skills}`、`{versions}`、`{variables}`；环境变量仅含脱敏 metadata，相关写操作和 observability retry 继续保留 `/v1`。
- Web App 第四批只读迁移增加 Agent config versions、MCP versions、Session runtime config/capabilities，以及 Marketplace entries/policies list/get，总计 22 个 SDK 查询方法。版本列表与 Marketplace 列表继续使用原包装结构，runtime config 动态 JSON 原样保留；所有 Marketplace 状态变更动作仍使用 `/v1`。
- Web App 第五批只读迁移增加 Session events、Artifact list 和 Intervention list，总计 25 个 SDK 查询方法。适配层继续返回 `{events}`、`{artifacts}`、`{interventions}`，未知 Event 与 Artifact metadata 原样保留；Artifact 下载、审批决定、事件发送和 SSE 继续使用 `/v1`。
- Web Inspector 接入本地 `@tma/core-sdk`，将 Session Trace、Trace/Span catalog 与详情切换到 TracesService。目录分页从 offset/next_offset 改为不透明 cursor/next_cursor；现有组件继续消费 `traces/spans` 包装，加载更多时累加页内 kind/status/critical 统计。Vite 增加 `/v2` 与 `/auth` proxy，`build-inspector-ui` 先验证 SDK。
- Web Inspector 第二批增加 Session get/events、Artifact list、Intervention list 和 Observability status SDK 查询，保留原响应包装并透传共享 AbortSignal；Events 继续使用 after_seq 增量读取。Usage、Summary、Metrics、Artifact 下载、审批和事件发送保持原接口。
- TypeScript SessionsService 补充精确类型化的 summary/usage 查询与公共类型；SDK 测试覆盖资源 ID 转义和响应字段。Inspector 将 Usage、Summary 与 Artifact Preview 下载切换到 SDK，Download 直链改为 `/v2`；Metrics、审批和事件发送继续保持原接口。
- LLM provider/model diagnostic 路由补齐 `LLMDiagnosticResult` OpenAPI 契约，并进入 Go 与 TypeScript LLM 高层服务；生成物和公共类型检查覆盖两个操作。
- Web App 的 provider/model diagnostic 从手写 `/v1` fetch 迁移到 TypeScript LLM 服务与 `/v2`，保留原函数签名和页面行为；创建、更新、删除等配置写操作继续留在原接口。
- Web App 的 Artifact Preview 改用 `ArtifactsService.download()`，资源适配器按 Session/Artifact ID 调用并透传 AbortSignal；消息与预览面板的原生下载链接统一改为 `/v2`。上传仍保持 `/v1` multipart，事件发送与 SSE 未在本批迁移。
- Web App 的 Approve/Reject 改用 TypeScript `InterventionsService` 与 `/v2`，保留 `{ intervention, events }`、事件合并、pending 队列移除和 continuation 同步行为；测试覆盖 ID 转义、reason、AbortSignal 与返回事件。事件发送继续单独评审。
- Web App 的用户附件和离线 Skill package 上传改用 `ArtifactsService.upload()` 与 `/v2`，保留原 `uploadSessionArtifact` 签名、multipart 字段和 `{ object_ref, artifact, workspace_path }` 返回结构；测试确认 Fetch 自动生成 boundary、文件名/MIME、ID 转义及 AbortSignal。
- Web App 新增一批低风险读取迁移：Session Usage/Summary/Compare，以及 Orchestration Task Group Templates/List/Get，累计 31 个 SDK 只读方法；适配层继续保留 `{ task_groups }`。遗留 Trace/Span helper 仍公开 offset，待 cursor 兼容策略明确后再迁移。
- Web App 将 Marketplace external/internal discover 与 Skill retention effective/policies、GC runs/tombstones 迁移到类型化 SDK，累计 37 个 SDK 只读方法；重复 tag、动态扩展字段及 `{ policies/runs/tombstones }` 包装保持不变。Marketplace 安装与策略写操作不在本批范围。
- Web App 的 Session create/archive/restore/rerun/delete、metadata 和 runtime settings 写操作迁移到 `SessionsService` 与 `/v2`，页面现有 201/200/204、rerun response、显式 false 和状态更新逻辑不变。`config/upgrade`、消息/interrupt 事件继续独立处理。
- Inspector 的 Approve/Reject 改用 TypeScript `InterventionsService` 与 `/v2`，保持现有 reason 和 UI 刷新逻辑；测试覆盖 Session/Turn/Call ID 转义、请求体和 AbortSignal。Metrics、事件发送及其他写操作仍单独评估。
- Web App 的 Agent create/update/import/export/config rollback/tooling-health 改用 TypeScript `AgentsService` 与 `/v2`；适配层将 export 的类型化 JSON 包装为带文件名的浏览器 `Response`，现有下载交互不变。测试覆盖资源 ID 转义、动态 tools JSON、请求体和 AbortSignal；生产代码不再包含 `/v1/agents`。
- Web App 的 MCP create/update/enable/disable/test/archive/version restore 与 EnvironmentVariables put/delete 改用 TypeScript 高层服务和 `/v2`。适配层保留现有函数签名与 UI 返回形状，并新增可选 AbortSignal；测试覆盖 workspace query、资源 ID 转义、动态 MCP config、请求体和 204 删除。
- Web App 的 LLM Provider/Model create/update/enable/disable/delete 改用 `LLMService` 与 `/v2`。Provider 和 Model 的 revision 并发控制不再由 App 手写；测试固定带引号 `If-Match`、首次 Model 创建的 `If-None-Match: *`、两者互斥、资源 ID 转义、204 和 AbortSignal。
- Web App 的 Environment create、Observability retry、Skill archive、Marketplace 外部/内部 preview/install、installed Skill enable/disable、entry/policy 生命周期及 Skill retention/GC 写操作改用 TypeScript 高层服务和 `/v2`。Marketplace entry transition 收窄为契约定义的 `submit/publish/withdraw`；测试覆盖全部路径、method、动态 JSON、ID 转义和 AbortSignal。
- Web App 的 Session Trace、Trace/Span catalog/detail helper 改用 `TracesService` 与 `/v2`。目录继续返回 `{ traces }` / `{ spans }` 供调用方消费，但分页只接受不透明 cursor 并返回 `next_cursor/has_more`；测试覆盖全部筛选参数、显式 `critical=false`、ID 转义、export format 和 AbortSignal。
- TypeScript `SessionsService` 补齐 `upgradeConfig` 及生成 Schema 对应的公共请求/响应类型；SDK 完整 Alpha 门禁覆盖路径、请求和返回值。Web App 的 Session config upgrade 同步迁移到 `/v2`，保留当前 Session/Event 状态更新逻辑。
- ObjectRef 与 Skill package 原生下载链接改为 `/v2` helper，并固定 ID/版本编码。Workbench Plugin Context 落地 `tasks.list` 与 `artifacts.list` facade，由宿主的 TypeScript SDK 适配层实现；Research Projects 移除 Session/Artifact URL 拼接，Scoped HTTP 收窄为只允许 `/v2`。
- Web App Session SSE 从原生 EventSource 迁移到 `SessionsService.events`，由 SDK 负责认证、after_seq、去重、网络/5xx 重连与 AbortSignal；未知事件作为结构化 Event 消费。Session interrupt 通过 Runs list 找到唯一 `running/waiting_approval` Run 并调用 cancel。
- `RunHandle` 增加只读 `initialEvents` 与 `created`，普通消息通过 Run start 并继续向界面返回初始事件。排队消息和 `session_busy` 竞态通过新增的 TypeScript `SessionsService.appendEvents` 保留队列语义；OpenAPI 修正该 operation 的真实 201/202 成功状态，Go 与 TypeScript 生成物同步更新。生产 Web App 仅保留 `/v1/task-templates`。
- Web App 移除 Artifact CLI 路径解析对 `/v1/sessions/.../artifacts/.../download` 的历史兼容，只接受 `/v2`；Workbench Artifact Provider 测试桩和插件标准示例同步收敛到 v2/宿主 facade。
