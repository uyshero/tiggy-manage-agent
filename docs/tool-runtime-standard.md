# Tool / Runtime Standard

上位扩展治理、Provider 发现、兼容性、下线和人工切换规则见 [TMA Extension 与 Provider 治理标准](./extension-governance-standard.md)。设置页贡献与配置作用域见 [Extension 设置页与配置贡献标准](./extension-settings-standard.md)。

本文档定义 TMA 第一版内置工具 namespace、runtime 选择规则、work invocation 标准，以及哪些能力在哪些位置实现。

## 核心结论

TMA 第一版先把概念收窄：

- **namespace 表达能力域**：例如 `default.*`、`artifact.*`、`browser.*`、`agent.*`。
- **runtime 表达显式运行位置 / 优先级策略**：第一版只暴露 `cloud_sandbox`、`local_system`、`auto`。
- **server 是默认内置承载面**：控制面、metadata、权限、轻量 API 默认由 server 实现，不需要在 runtime 里显式写 `server`。

正确模型：

```text
agent config
  -> tool namespace / api
  -> capabilities / policy
  -> runtime preference: auto | cloud_sandbox | local_system
  -> server 选择内置实现或 worker/provider 实现
  -> work invocation 执行并回传 result / artifact refs
```

## Runtime

第一版显式 runtime 只保留三类：

| runtime | 含义 | 说明 |
|---|---|---|
| `auto` | 自动选择 | 默认策略。server 先判断是否内置可执行；否则按策略选择 cloud_sandbox，再考虑 local_system |
| `cloud_sandbox` | 云沙箱 | 大部分命令、代码、文件写入、浏览器自动化的默认兜底执行面。假设稳定存在 |
| `local_system` | 本地系统 | 运行在提供该能力的本机 worker / 进程上。只用于明确允许本机能力的场景 |

`server` 不作为第一版 runtime 暴露。server 内置能力由工具 manifest 的 implementation 标记，例如：

```json
{
  "namespace": "artifact",
  "api": "create",
  "implementation": "server_builtin"
}
```

`remote` 也不作为第一版 runtime 暴露。后续如果出现专用远程实现，可以在 worker/provider registry 里作为 implementation metadata 扩展，不提前进入用户配置面。

默认选择建议：

```text
server_builtin tool:
  server 内置直接执行

runtime = auto:
  server_builtin -> cloud_sandbox -> local_system

runtime = cloud_sandbox:
  只选择 cloud_sandbox 能力实现；不存在则失败或走审批

runtime = local_system:
  只选择 local_system 能力实现；不存在则失败或走审批
```

## Work Invocation

Work 是一次工具调用，不是一次“环境调用”。

标准形态：

```json
{
  "protocol_version": "tma.work.v1",
  "namespace": "browser",
  "api": "screenshot",
  "capabilities": ["browser.read", "browser.capture"],
  "risk": "read",
  "runtime": "auto",
  "input": {
    "url": "https://example.com"
  }
}
```

字段含义：

| 字段 | 含义 |
|---|---|
| `namespace` | 能力域，例如 `default`、`artifact`、`browser`、`agent` |
| `api` | namespace 下的 API，例如 `screenshot` |
| `capabilities` | 此次调用需要的能力 |
| `risk` | 风险等级，用于审批和策略 |
| `runtime` | `auto` / `cloud_sandbox` / `local_system` |
| `input` | API 输入 |

`runtime` 是偏好和约束，不是 namespace。最终执行位置仍由 server 根据 manifest、agent config、policy、worker registry 和审批状态决定。

## 第一版 Namespace

第一版只收敛这些能力域：

| namespace | 能力域 | 默认实现位置 |
|---|---|---|
| `default.*` | 通用文件、命令、代码、网络 fetch 等默认能力 | `auto`，优先 server 内置或 cloud_sandbox |
| `artifact.*` | object ref、artifact metadata、上传下载、转换、索引 | metadata/下载在 server；重型处理在 cloud_sandbox |
| `browser.*` | 浏览器导航、读取、交互、截图、上传下载 | cloud_sandbox 优先；必要时 local_system |
| `agent.*` | session、message、event、approval、多 agent 协作 | server 内置 |
| `skills.*` | workspace skill 查询、检查、安装、升级与 Agent 绑定 | server 内置 |
| `computer.*` | 桌面窗口、AX/UI tree、鼠标键盘、屏幕截图 | process plugin；local_system worker |

暂不把 `local_system.*`、`cloud_sandbox.*` 作为第一版用户侧 namespace。它们是 runtime / provider 实现面。后续如果确实需要“明确本机”或“明确云沙箱”的高级工具，可以再暴露实现型 namespace。

## API 草案

### `default.*`

默认能力域用于表达最常见的通用操作。它不是“模糊别名”，而是第一版稳定 API 面；runtime 决定具体实现。

| API | capabilities | risk | runtime 默认 | 第一版实现 |
|---|---|---|---|---|
| `read_file` | `filesystem.read` | `read` | `auto` | cloud_sandbox / local_system |
| `write_file` | `filesystem.write` | `write` | `auto` | cloud_sandbox / local_system |
| `edit_file` | `filesystem.read`, `filesystem.write` | `write` | `auto` | cloud_sandbox / local_system |
| `list_files` | `filesystem.read` | `read` | `auto` | cloud_sandbox / local_system |
| `search_files` | `filesystem.read` | `read` | `auto` | cloud_sandbox / local_system |
| `run_command` | `exec` | `exec` | `cloud_sandbox` | cloud_sandbox，local_system 需显式允许 |
| `execute_code` | `code.execute` | `exec` | `cloud_sandbox` | cloud_sandbox，local_system 需显式允许 |
| `fetch_url` | `network.http` | `read` | `cloud_sandbox` | cloud_sandbox |

#### Large file generation

`default.write_file` remains the one-shot path for small files. Generated `content` is recommended to stay at or below 6,000 estimated serialized tokens and is rejected above 8,000 tokens.

Larger files use the existing `write_file` + `edit_file` protocol; no append API is required:

1. Write a compact file skeleton containing unique numbered placeholders such as `__TMA_PLACEHOLDER_REPORT_001__`.
2. Replace one placeholder per `edit_file` call with `replace_all=false`.
3. Keep each `new_string` at or below 6,000 estimated tokens and always below 8,000 tokens.
4. Issue only one `write_file` or `edit_file` mutation per model response and wait for its result before generating the next segment. Runtime rejects every file mutation in a multi-mutation response before any file is changed.
5. Split at complete semantic boundaries: functions, classes, modules, chapters, or complete data structures.
6. Runtime records `placeholder -> SHA-256(new_string)` for each successful segment. A retry is `already_applied` only when both values match recorded evidence; matching text elsewhere in the file is not sufficient.
7. Before completion, call `read_file` and confirm that no `__TMA_PLACEHOLDER_...__` marker remains, then run the syntax check or test appropriate for the generated file. Runtime reads every tracked file itself and blocks the final agent message until all placeholders are gone and a successful path-referencing check/test has run after the last segment.

Malformed or oversized file mutations are rejected before intervention policy evaluation. The model receives a structured recoverable error with this protocol and must not retry the unchanged payload. Two consecutive invalid or oversized argument rounds retain the existing circuit breaker and fail the turn.

Segmented generation state is serialized in the intervention continuation envelope, so approval and service-resume paths retain hashes, remaining placeholders, validation state, and the approved target path. In `request_approval` mode, approving the skeleton approves subsequent registered placeholder edits for that file; an arbitrary command still requires its normal approval. Intermediate skeleton/edit artifacts are deferred, and the validated file is published once at completion. Persisted events redact `content` and `new_string`, retaining only character count, estimated tokens, and SHA-256.

Prometheus session metrics expose `tma_file_generation_oversized_calls_total`, `tma_file_generation_segments_total`, `tma_file_generation_idempotent_replays_total`, `tma_file_generation_remaining_placeholders`, and `tma_file_generation_duration_milliseconds`.

### `artifact.*`

Artifact 能力域负责文件和产物的 metadata、代理下载、上传、转换和索引。

| API | capabilities | risk | runtime 默认 | 第一版实现 |
|---|---|---|---|---|
| `create` | `artifact.metadata.write` | `write` | server 内置 | 已有 server API |
| `list` | `artifact.metadata.read` | `read` | server 内置 | 已有 server API |
| `get` | `artifact.metadata.read` | `read` | server 内置 | 待补标准 API |
| `delete` | `artifact.metadata.write` | `write` | server 内置 | 已有 server API |
| `upload` | `artifact.write`, `object.write` | `write` | server 内置 | 已有 server multipart API |
| `download` | `artifact.read` | `read` | server 内置 | 已有 server 代理下载 |
| `convert` | `artifact.read`, `artifact.write`, `cpu.medium` | `write` | `cloud_sandbox` | 待实现 |
| `preview` | `artifact.read`, `artifact.write`, `cpu.medium` | `write` | `cloud_sandbox` | 待实现 |
| `index` | `artifact.read`, `search.index.write` | `write` | `cloud_sandbox` | 待实现 |

### `browser.*`

Browser 能力域默认优先云沙箱。只有明确允许本机浏览器时，才走 `local_system`。

| API | capabilities | risk | runtime 默认 | 第一版实现 |
|---|---|---|---|---|
| `open` | `browser.open`, `browser.read` | `read` | `cloud_sandbox` | 已有 Playwright headless runner，支持 cloud_sandbox / local_system worker |
| `navigate` | `browser.navigate` | `read` | `cloud_sandbox` | 暂并入 `open` / `read` 的 `url` 参数 |
| `read` | `browser.read` | `read` | `cloud_sandbox` | 已有 Playwright headless runner，返回正文和可交互元素 |
| `click` | `browser.read`, `browser.interact` | `write` | `cloud_sandbox` | 已有 Playwright headless runner |
| `type` | `browser.read`, `browser.interact` | `write` | `cloud_sandbox` | 已有 Playwright headless runner |
| `screenshot` | `browser.read`, `browser.capture` | `read` | `cloud_sandbox` | 已有 Playwright headless runner，输出 PNG artifact |
| `takeover` | `browser.read`, `browser.interact`, `browser.takeover` | `write` | `local_system` | 已有本地长驻 headed Chromium/CDP runner，用于人工接管后返回页面状态 |
| `close` | `browser.close` | `write` | `local_system` | 已有本地 browser session 关闭入口 |
| `download` | `browser.download`, `artifact.write` | `write` | `cloud_sandbox` | 待实现 |
| `upload_file` | `browser.upload`, `filesystem.read` | `write` | `cloud_sandbox` | 待实现 |
| `network_log` | `browser.network` | `read` | `cloud_sandbox` | 待实现 |

### `agent.*`

Agent 能力域属于 server 控制面，默认不下发 worker。

| API | capabilities | risk | runtime 默认 | 第一版实现 |
|---|---|---|---|---|
| `spawn` | `agent.session.write`, `agent.message.write` | `write` | server 内置 | 已实现；创建子 session 并可立即发首条消息 |
| `create_session` | `agent.session.write` | `write` | server 内置 | 已实现；只创建子 session，不自动发消息 |
| `send_message` | `agent.message.write` | `write` | server 内置 | 已实现；向已有子 session 发送消息 |
| `get_session` | `agent.session.read` | `read` | server 内置 | 已有 server API |
| `wait` | `agent.session.read`, `agent.event.read` | `read` | server 内置 | 已实现；等待子 session 到 idle / terminated / waiting_approval |
| `collect_result` | `agent.session.read`, `agent.event.read`, `artifact.metadata.read` | `read` | server 内置 | 已实现；拉取最新 agent.message、状态与 artifact |
| `list_events` | `agent.event.read` | `read` | server 内置 | 已有 server API |
| `stream_events` | `agent.event.stream` | `read` | server 内置 | 已有 SSE |
| `approve_tool` | `agent.approval.write` | `write` | server 内置 | 已有 server API |
| `reject_tool` | `agent.approval.write` | `write` | server 内置 | 已有 server API |
| `archive_session` | `agent.session.write` | `write` | server 内置 | 已实现；归档不再需要的子 session |
| `cancel_start` | `agent.session.write` | `write` | server 内置 | 已实现；取消尚未晋升的持久化启动请求 |
| `run_group` / `get_group` / `wait_group` / `collect_group` | `agent.session.write`, `agent.session.read` | `write` / `read` | server 内置 | 已实现；持久化 fan-out / fan-in 与结构化聚合 |
| `cancel_group` / `retry_group_item` / `retry_group` | `agent.session.write` | `write` | server 内置 | 已实现；task group 恢复与取消 |
| `start_discussion` | `agent.session.write`, `agent.message.write` | `write` | server 内置 | 已实现；创建 2–8 个动态角色并启动固定两轮讨论 |
| `list_discussion_strategies` / `get_discussion` / `wait_discussion` / `collect_discussion` | `agent.session.read`, `agent.event.read` | `read` | server 内置 | 已实现；策略发现、恢复推进和结果收集 |
| `cancel_discussion` / `retry_discussion_participant` | `agent.session.write` | `write` | server 内置 | 已实现；级联取消和当前轮单参与者重试 |

当前 `agent.*` 的推荐使用模式是：

```text
agent.spawn
  -> agent.wait
  -> agent.collect_result
```

这条链路对应第一版 subagent 编排语义：父 agent 不直接共享上下文给子 agent，而是显式创建子 session、等待子 session 完成，再回收结果。这样能复用现有 Session / Event / Approval / Artifact / Audit 机制，也更容易做后续的 depth limit、quota 和多 agent 编排治理。

需要多角色讨论时，父 Agent 先按 `list_discussion_strategies` 返回的 `team_plan_schema` 生成目标、策略、预算和动态角色，然后调用 `start_discussion`。服务端把讨论实现为持久化有界状态机：第一轮独立观点、主持人争议归纳与问题分配、第二轮回应、最终主持人共识；`get_discussion` / `wait_discussion` 会幂等推进状态，服务重启不要求父 Agent 重新创建团队。

如果子 session 进入 `waiting_approval`，推荐闭环是：

```text
agent.get_session
  -> agent.stream_events 或 agent.list_events
  -> agent.approve_tool / agent.reject_tool
  -> agent.wait
  -> agent.collect_result
```

这里 `agent.stream_events` 当前实现为带超时的长轮询工具，而不是无限流式连接。它更适合放在 LLM tool loop 里消费“子任务刚刚产生了什么新事件”，而不是替代前端 SSE。

当前实现默认启用第一版 subagent 治理阈值：

- 最大递归深度：`3`
- 单个父 turn 最多创建子 session：`5`
- 单个父 session 累计最多创建子 session：`20`

此外，当前还支持两类 active quota：

- workspace 活跃 subagent 上限：默认 `50`
- user 活跃 subagent 上限：默认 `10`

这些阈值现在已经进入 server 配置面，可通过 `TMA_SUBAGENT_*` 环境变量调整。目标是优先避免无限递归 spawn、同一轮 fan-out 失控，以及单个 workspace / user 长时间吞掉全部编排容量。

## MCP

当前 MCP 作为 **动态工具来源** 接入，而不是新增 runtime：

- Agent config 里的 `mcp` 负责声明 stdio MCP servers；
- Runtime 先把 MCP `tools/list` 结果适配成标准 `tools.Manifest`；
- 最终仍然复用同一套 `tools.Registry`、tool filtering、tool result 和上下文预算逻辑。

因此，MCP server 暴露出来的工具在 TMA 内部看起来和普通 `namespace.api` 工具一致，只是 manifest 来源不同。当前实现细节、配置格式和测试方法见 [mcp-integration.md](./mcp-integration.md)。

结构型治理拒绝会尽力在父 turn 写入 `runtime.subagent_spawn_rejected`，active admission 拒绝写入 `runtime.subagent_start_rejected`。事件保留命中的 scope、policy、limit 和当前计数，因此可直接用于审计日志、配额拒绝率指标和容量调优；工具调用本身同时返回结构化 quota 状态，供父 agent 当场降级或稍后重试。

Store 通过 `CreateSubagentSession` 原子执行结构型 quota admission 和 session 插入，通过 `StartSubagentTurn` 原子执行 active admission 和 turn 启动。Postgres 实现使用 workspace 级事务 advisory lock，保证多个 server 实例并发 spawn / start 时配额不会穿透。通用 `CreateSession` / `AppendEvents` 不承担 agent 工具的专用治理语义。

active admission 满时，`EnqueueSubagentStart` 把消息持久化到有限队列；WorkerRunner 在 claim turn 前调用 `PromoteSubagentStarts`，按 priority / FIFO 尝试晋升。晋升成功后产生正常 `session_turn`，后续继续复用既有 lease、heartbeat、重启恢复和失败处理。

pending 请求可通过 `agent.cancel_start` 显式取消；archive 父/子 session 会自动取消关联请求。超时扫描将请求标记为 `expired`。取消和超时都保留 session event，因此既能审计，也会自动进入现有 event-type 指标。

### `skills.*`

Skills 能力域属于 server 控制面，workspace 由当前 Session 决定。安装与启用属于写操作，遵循 Session 的 intervention policy。

| API | capabilities | risk | runtime 默认 | 第一版实现 |
|---|---|---|---|---|
| `search` | 无 provider capability 依赖 | `read` | server 内置 | 已实现；搜索当前 workspace 已安装 registry |
| `inspect` | 无 provider capability 依赖 | `read` | server 内置 | 已实现；读取精确版本或最新版本 |
| `discover` | 无 provider capability 依赖 | `read` | server 内置 | 已实现；GitHub code/repository search 与精确仓库验证 |
| `preview` | 无 provider capability 依赖 | `read` | server 内置 | 已实现；安装前返回 provenance、license、asset index、attestation、静态/二进制扫描、SBOM、policy decision 和版本 diff，不写 registry/object store |
| `read_asset` | 无 provider capability 依赖 | `read` | server 内置 | 已实现；按需读取 package 文本资产；二进制拒绝内联返回，脚本不自动执行 |
| `install` | 无 provider capability 依赖 | `write` | server 内置 | 已实现；安装 inline 或受限 GitHub `SKILL.md`，或显式发布升级版本；GitHub source 重新验证 attestation、静态扫描和不联网的内置二进制扫描，通过后转存 object store；外部 Scanner/ClamAV 当前未开放 |
| `enable` | 无 provider capability 依赖 | `write` | server 内置 | 已实现；创建 Agent config version，当前 Session 保持 pinned |

Workbench Skills 管理页通过 control auth 管理 API 复用同一 Skills service：发现和 Preview 保持只读，安装会重新抓取并重新执行 Policy、Attestation、静态与内置二进制扫描；启用会创建新的 Agent config version。外部 Scanner/ClamAV 保留开发代码但不进入当前生产工厂。二进制 asset 只通过 object ref 受控下载，不进入模型上下文、不由 `skills.read_asset` 返回，也不会自动执行。

### `computer.*`

Computer-use 能力域通过 worker process plugin 暴露，详细 contract 见 [computer-use-plugin.md](./computer-use-plugin.md)。

| API | capabilities | risk | runtime 默认 | 第一版实现 |
|---|---|---|---|---|
| `list_windows` | `computer.window.read` | `read` | `local_system` | process plugin；CUA / AX fallback |
| `get_state` | `computer.state.read`, `computer.ax.read` | `read` | `local_system` | process plugin；CUA / AX fallback |
| `click` | `computer.input.mouse` | `write` | `local_system` | process plugin；CUA 优先 |
| `type_text` | `computer.input.keyboard` | `write` | `local_system` | process plugin；CUA 优先 |
| `hotkey` | `computer.input.keyboard` | `write` | `local_system` | process plugin；CUA 优先 |
| `launch_app` | `computer.app.launch` | `write` | `local_system` | process plugin；CUA / OS fallback |
| `bring_to_front` | `computer.window.focus` | `write` | `local_system` | process plugin；CUA 优先 |
| `screenshot` | `computer.screen.capture` | `read` | `local_system` | process plugin；artifact export |

`computer.*` 不新增 `work_type`。模型调用后仍下发 `tool_execution` / `tma.work.v1`。

## Manifest 要求

每个 tool API 至少声明：

```json
{
  "namespace": "browser",
  "api": "screenshot",
  "capabilities": ["browser.read", "browser.capture"],
  "risk": "read",
  "runtime": {
    "allowed": ["auto", "cloud_sandbox", "local_system"],
    "preferred": "cloud_sandbox"
  },
  "implementation": "worker_capability"
}
```

字段规则：

- `namespace/api` 唯一标识工具 API。
- `capabilities` 用于 worker 匹配、审批策略和运行时选择。
- `risk` 用于默认审批。
- `runtime.allowed` 表示用户/agent policy 可指定的 runtime。
- `runtime.preferred` 表示 `auto` 下的默认倾向。
- `implementation` 可为 `server_builtin` 或 `worker_capability`。

## Server / Worker 分工

Server 负责：

- 保存 manifest、agent config、policy、worker registry。
- 处理 server builtin tools。
- 在发给模型前，先按 agent config、runtime policy 和当前 provider/worker capabilities 过滤本轮可见工具。
- 当前第一版只有 `local_system` 会额外参考同 workspace 在线 worker registry 来收窄模型可见工具；`cloud_sandbox` 不被本机 worker registry 过滤。
- 根据 work invocation、policy、runtime 和 worker capabilities 选择执行实现。
- 维护审计、审批、事件、artifact metadata。

Worker 负责：

- 从 `workruntime.Executor.WorkerCapabilities()` 导出并注册自己支持的 namespace / API / capabilities / runtime。
- 主动 poll work。
- 执行自己能处理的 work invocation。
- 回传 result、artifact refs、stdout / stderr 摘要。

Worker 不负责：

- 暴露 inbound 端口。
- 直连 Postgres。
- 绕过 server 做权限判断。
