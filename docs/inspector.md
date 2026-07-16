# TMA Inspector 用户使用手册

本文档面向使用 TMA Inspector 排查 Agent 执行过程的开发、测试和运维人员。Inspector 是 TMA Server 内置的观测与调试页面，用于查看 Session、Turn、Trace、Span、上下文、工具调用、审批、制品和导出器状态。

> Inspector 当前是专家调试页面，不是面向最终用户的聊天界面。页面包含原始事件、工具参数和运行时元数据，不应直接暴露到公网。

## 1. 功能概览

Inspector 支持以下操作：

- 按 Session、Turn 或 Trace ID 定位一次执行；
- 搜索近期 Trace 和跨 Session Span；
- 查看 Trace 概览、关键路径、Span 瀑布图和时间线；
- 查看 Session 摘要、Plan 历史、Token 用量和上下文预算；
- 区分 MCP、Worker Plugin 和 Builtin 工具事件；
- 审批或拒绝等待中的工具调用；
- 预览、下载 Session 制品并复制 CLI 下载命令；
- 查看 Prometheus 指标、Exporter 状态和原始导出数据；
- 导出 Trace JSON、Perfetto JSON 或 OTel JSON；
- 通过 URL Hash 分享 Session、Turn、Trace 和 Span 深链。

### 1.1 Session、Turn、Trace 与 Span

这四个概念描述的是不同层级，不是同一对象的不同名称：

```text
Session：一项持续任务或一段连续会话
├── Turn 1：一次用户输入到 Agent 本轮结束
│   └── Trace 1：Turn 1 的完整执行轨迹
│       ├── Span：本轮整体执行
│       ├── Span：LLM 请求
│       ├── Span：工具调用
│       └── Span：审批等待
├── Turn 2
│   └── Trace 2
└── Session 级摘要、制品、用量和有序事件
```

#### Session

`Session` 是持续时间最长的任务容器，可理解为“一项任务”或“一段连续对话”。它绑定 Agent、Environment 和运行设置，并保存跨 Turn 共用的数据：

- 有序事件历史；
- Session 摘要；
- Plan/Todo 当前状态和历史版本；
- LLM 用量记录；
- 制品；
- 等待中的人工审批；
- Session 状态和运行配置。

同一个 Session 可以先后执行多个 Turn。Inspector 中的 Session ID 通常以 `sesn_` 开头。

#### Turn

`Turn` 是 Session 中的一轮执行：通常从一次用户输入开始，到 Agent 给出本轮响应、失败或进入等待状态结束。工具调用、LLM 多轮推理和审批后的 continuation 都可以发生在同一个 Turn 内。

Turn 有独立的 ID、状态、开始/结束时间和用量。切换 Turn 可以比较同一任务不同轮次的行为，但不会切换到另一个 Session。

#### Trace

`Trace` 是一个 Turn 的完整可观测执行轨迹。它把本轮的事件投影成便于分析的时间线、统计、调用图和 Span 树。

在 TMA 中，`session_events` 是持久化事实源，Trace 是从这些事件生成的观测视图。因此 Trace 不是另一次执行，也不会改变 Agent 状态。Trace 可以导出为 TMA JSON、Perfetto JSON 或 OTel JSON。

通常一个 Turn 对应一条 Trace。使用 Trace ID 可以直接反查所属 Session 和 Turn。

#### Span

`Span` 是 Trace 中一个有起止时间的执行单元，例如一次 LLM 请求、一次工具调用、一次审批等待或一次上下文压缩。Span 可以包含子 Span，从而形成调用树。

常用字段：

- `kind`：操作类别，例如 `llm`、`tool`、`approval`；
- `status`：`ok`、`error`、`open` 等状态；
- `duration_ms`：Span 总耗时，包含子 Span；
- `self_duration_ms`：扣除子 Span 后自身占用的时间；
- `parent_span_id` / `child_span_ids`：父子关系；
- `critical`：是否位于决定 Turn 总耗时的关键路径；
- `events` / `attributes`：Span 内事件和诊断属性。

关键路径 Span 不一定失败，它表示该 Span 的耗时会直接影响整条 Trace 的完成时间。

#### Event 与 Timeline Step

`Event` 是 Session 中按 `seq` 单调排序的持久化事实，例如用户消息、LLM 请求、工具结果和运行完成。Inspector 的 `Recent Events` 展示原始事件。

`Timeline Step` 是 Trace 为人类阅读而生成的事件投影，可能提取消息、工具、结果和制品摘要。一个 Step 不等于一个 Span：Step 侧重“发生了什么”，Span 侧重“这段操作持续多久、与其他操作是什么关系”。

## 2. 启动与访问

### 2.1 前置条件

本地使用需要：

- Go；
- Docker 和 Docker Compose；
- 可用的 Postgres；
- 仅在重新构建 Inspector 前端时需要 Node.js 和 npm。

### 2.2 启动 TMA Server

在项目根目录执行：

```bash
make db-up
make migrate-up
make run
```

默认服务地址为 `http://localhost:8080`。先检查服务健康状态：

```bash
curl http://localhost:8080/health
```

预期返回：

```json
{"status":"ok","service":"tiggy-manage-agent"}
```

也可以后台启动：

```bash
make server-start
make server-status
```

停止后台服务：

```bash
make server-stop
```

### 2.3 打开 Inspector

浏览器访问：

```text
http://localhost:8080/inspector
```

Inspector 静态资源嵌入在 TMA Server 中，不需要单独启动前端服务。

如修改了 `apps/inspector/src` 下的前端源码，需重新构建并重启 Server：

```bash
make build-inspector-ui
make server-restart
```

## 3. 界面结构

页面分为左侧检索区和右侧详情区。

### 3.1 左侧检索区

| 区域 | 用途 |
| --- | --- |
| Query | 输入 Session、Trace ID，选择 Turn 和导出格式 |
| Recent Traces | 浏览近期 Trace，可按当前 Session 和 Turn 筛选 |
| Span Search | 跨 Trace 搜索 Span |
| Turns | 查看当前 Session 下的 Turn 并切换 |

### 3.2 右侧详情区

| 区域 | 用途 |
| --- | --- |
| Overview | 显示 Turn、耗时、Step、Span、工具、MCP 和错误数量 |
| Session | Session 状态、Agent、Environment、创建信息和运行设置 |
| Trace Summary | Trace 状态、步骤统计及执行摘要 |
| Context Summary | 已持久化的 Session 上下文摘要 |
| Usage | LLM 调用、Token 和延迟统计 |
| Plan History | 按最新优先查看 Plan 状态、步骤进度和完成证据 |
| Context Coverage | 摘要已覆盖和尚未汇总的事件范围 |
| Context Budget | 上下文窗口、输入、工具 Schema、固定上下文和输出预留预算 |
| Tool Sources | 按 MCP、Worker Plugin、Builtin 过滤工具事件 |
| Waterfall | Span 的起止时间、层级、耗时和关键路径 |
| Spans | 当前 Trace 内 Span 的筛选、详情、事件和属性 |
| Timeline | 按事件序号显示执行步骤 |
| Pending Approvals | 审批或拒绝待执行工具调用 |
| Artifacts | 预览、下载制品或复制 CLI 命令 |
| Recent Events | 查看最近 18 条 Session 原始事件 |
| Metrics | 当前 Session/Turn 的 Prometheus 文本指标 |
| Exporters | Perfetto、OTLP、采样和重试状态 |
| Raw Export | 当前 Trace 或所选格式的原始 JSON |

## 4. 快速定位一次执行

### 4.1 使用 Session ID

1. 在 `Session` 输入框填入 Session ID，例如 `sesn_000001`。
2. 单击 `Load`。
3. 默认加载该 Session 最新 Turn。
4. 在 `Turn` 下拉框或左侧 `Turns` 列表中切换历史 Turn。

注意：在 Session 输入框按回车只会执行 `Filter by Session`，刷新左侧 Trace/Span 目录，不会加载右侧详情。要加载完整详情，请单击 `Load`。

Session ID 可从 Workbench、CLI 输出、API 响应或服务端日志中获得。

### 4.2 使用 Trace ID

1. 在 `Trace ID` 输入框填入 Trace ID。
2. 单击 `Load Trace`，或在输入框按回车。
3. Inspector 会自动回填对应的 Session 和 Turn，并加载完整详情。

也可以直接单击 `Recent Traces` 中的记录。

### 4.3 使用 Recent Traces

- 初次打开页面会加载最近 20 条 Trace；
- `Filter by Session` 使用 Query 中的 Session/Turn 筛选目录；
- `Clear` 清空当前定位并恢复近期目录；
- 出现 `Load more` 时可继续加载下一页，每页 20 条；
- 每条记录显示 Turn 状态、Session 标题、耗时、Span 数量和摘要。

分页使用 Server 返回的不透明 cursor。Inspector 不根据当前条数计算 offset；修改 Session、Turn 或筛选条件后会重新从第一页开始查询。

### 4.4 自动刷新

勾选 `Auto refresh every 5s` 后，只要已填写 Session ID，Inspector 每 5 秒重新加载当前 Session/Turn 的详情。上一轮请求尚未完成时不会启动重叠请求；页面进入后台时暂停，重新回到页面后立即补一次刷新；Turn 进入 `completed`、`failed`、`canceled` 或 `terminated` 终态后自动关闭。

自动刷新期间，Events 使用最后收到的 `seq` 作为 `after_seq` 增量读取，并在浏览器中按序号去重合并。手动加载或切换 Session 时仍会执行一次完整读取，确保本地事件状态正确。

Inspector 的 Session、Trace/Span、Events、Usage、Summary、Plan History、Artifacts、Interventions 和 Observability 只读请求通过 TypeScript Core SDK 访问 `/v2`，并共享同一个取消信号。Artifact Preview 使用 SDK 下载，普通 Download 链接指向同一 `/v2` 资源。Approve/Reject 也通过 SDK 的 Interventions 服务提交到 `/v2`；Metrics 继续使用 Prometheus 接口。

适用场景：

- 观察运行中的 Turn；
- 等待工具调用完成；
- 等待审批后的 continuation；
- 查看新产生的事件或制品。

排查完成后建议关闭自动刷新，减少数据库和 Trace 投影请求。

## 5. Trace 与 Span 分析

### 5.1 先看 Overview 和 Trace Summary

建议先确认：

- Turn 状态是否为 `completed`、`failed`、`running` 或等待状态；
- `Duration` 是否异常；
- `Errors` 是否大于 0；
- `Tools` 和 approval waits 是否符合预期；
- Trace Summary 是否已经指出失败步骤或关键工具。

### 5.2 使用 Waterfall 定位慢步骤

Waterfall 以整次 Turn 时长为横轴显示 Span：

- 横条越长，Span 耗时越长；
- 缩进表示父子层级；
- `critical` 样式表示 Span 位于关键路径；
- error/failed/rejected 状态会突出显示；
- 单击一行会同步选中下方 Span 详情。

优先检查关键路径上耗时最长或状态异常的 Span，再结合 Timeline 和 Attributes 确认原因。

### 5.3 筛选当前 Trace 内的 Span

`Spans` 面板提供两个过滤条件：

- `Filter`：匹配 Span 名称、ID、父 Span ID、Kind、Status、Attribute 和 Event；
- `Kind`：只保留当前 Trace 中指定类型的 Span。

单击 `Clear` 清除这两个过滤条件。单击表格行可查看该 Span 的：

- 状态、耗时和事件序号范围；
- 父 Span 和子 Span；
- Span Events；
- Attributes。

Span 详情中的子 Span ID 可直接单击跳转。

### 5.4 跨 Trace 搜索 Span

左侧 `Span Search` 用于跨 Session/Trace 排查同类问题，支持：

- 关键字：名称、ID 或 Attribute；
- Kind：`interaction`、`llm`、`tool`、`approval`、`context`、`event`；
- Status：例如 `ok`、`error`、`open`；
- Critical：是否位于关键路径；
- Min Duration：最小耗时，单位毫秒。

单击 `Search Spans` 执行搜索。结果顶部显示当前已加载页面的 Kind、Status 和 Critical 聚合数量；继续加载时会累加新页统计。单击结果会加载对应 Trace 并定位 Span。

### 5.5 阅读 Timeline

Timeline 按 Session Event 的 `seq` 顺序展示执行过程，常见类型包括：

- `runtime.llm_request` / `runtime.llm_response`：模型请求与响应；
- `runtime.tool_call` / `runtime.tool_result`：工具调用与结果；
- `runtime.tool_intervention_required`：等待人工审批；
- `runtime.context_compacting` / `runtime.context_compacted`：上下文压缩；
- `runtime.completed` / `runtime.failed`：Turn 完成或失败。

工具结果过大时，Timeline 会显示截断提示，包括原始字符数、可见字符数或被省略的状态字节数。截断仅影响页面预览；完整内容应通过关联 Artifact 下载。

## 6. 工具来源筛选

`Tool Sources` 按事件中的 `tool_source` 统计工具来源：

- `MCP`：Agent 绑定的 MCP Server 工具；
- `Worker Plugin`：由 Worker 进程插件提供的工具；
- `Builtin`：TMA Server 内置工具；
- `other`：存在来源但不属于上述类型的事件。

单击 `All`、`MCP`、`Worker Plugin` 或 `Builtin` 可过滤 `Timeline` 和 `Recent Events`。该筛选不会改变 Waterfall、Span 表或 Usage。

当工具来源为 `MCP` 时，Timeline 和 Recent Events 会额外显示 MCP 诊断 badge，包括 transport、MCP protocol version、server capabilities、tool count、OAuth、SSE listener、resources/prompts expose 等非敏感信息。这些信息来自 `runtime.tool_call` / `runtime.tool_result` 事件里的 `mcp_*` metadata，不包含 MCP endpoint URL、headers、client secret、access token 或原始 OAuth response body。

`MCP Protocol` 面板会把同一 call ID 的 `runtime.tool_call` 与 `runtime.tool_result` 配对为一次协议操作，并展示：

- `tools/call`、`resources/list`、`resources/templates/list`、`resources/read`、`prompts/list` 或 `prompts/get` 方法；
- request / response event seq、时间、状态和执行耗时；
- transport、protocol version、capabilities、tool count、OAuth / SSE / expose 等诊断事实；
- result protocol、artifact 数量、截断状态、错误类型和脱敏后的 MCP result 摘要。

重复 call ID 会按事件顺序逐个配对；只有 request 或只有 response 时会保留 pending / unpaired 操作，避免静默丢失不完整链路。面板不会读取或显示 arguments、endpoint、headers、error message、content text、resource body、prompt text 或 structured content value。

对 `runtime.tool_result`，Inspector 还会读取 `state.protocol_version` 并展示 MCP 结果摘要：

- `tma.mcp_result.v1`：显示 tool name、`is_error`、content item 数量、content type、structured content 是否存在和 meta key 数量。
- `tma.mcp_context_result.v1`：按桥接类型显示 resource、resource template、resource content、prompt 或 prompt message 的脱敏数量与有限元数据。
- `tma.mcp_context_result.v1`：显示 resources/prompts 桥接工具名称、resource/prompt/message 数量、mime type、text/blob item 数量和 prompt roles。

摘要卡不会展开 MCP content text、resource body、prompt message text 或 structured_content 值；如需完整 payload，应在受控环境中查看 Recent Events 原始 JSON、Raw Export 或关联制品。

## 7. 上下文、计划与用量

### 7.1 Context Summary

显示当前 Session 已持久化的摘要。摘要通常用于后续 Turn 的上下文续接，不等同于完整事件历史。

### 7.2 Plan History

`Plan History` 通过 Session 的持久化快照展示全部 Plan，按创建时间最新优先排列。顶部汇总 `active`、`completed`、`canceled` 和 `superseded` 数量，可按状态筛选。

展开一条 Plan 可以查看 handling mode、创建/更新时间、关联 Turn，以及每个步骤的状态。完成步骤的 `Evidence` 是 Agent 标记完成时提交的验证依据；没有 evidence 的 pending、in_progress 或 blocked 步骤不会伪装成已完成。

该面板是只读观测面，不用于批准计划，也不会修改 Plan 状态。计划批准应使用独立的 `plan_approval` 流程，不能用工具审批代替。

### 7.3 Context Coverage

重点字段：

- `source_until_seq`：摘要已处理到的事件序号；
- `latest event seq`：当前加载事件中的最大序号；
- `covered events`：已被摘要覆盖的事件数；
- `unsummarized events`：尚未进入摘要的事件数。

面板最多展示最近 8 条未汇总事件。运行中的 Turn 存在少量未汇总事件通常是正常现象；Turn 完成很久后仍持续增加，需要检查 summary 刷新流程。

### 7.4 Context Budget

面板从最新携带 `context_budget` 的运行步骤中读取预算，常见字段包括：

- `context_window_tokens`：模型上下文窗口；
- `max_input_tokens`：允许的最大输入；
- `message_tokens`：消息占用；
- `tool_schema_tokens`：工具 Schema 占用；
- `pinned_context_tokens`：固定上下文占用；
- `reserved_output_tokens`：为模型输出预留的 Token。

同时检查 `context_truncated`、`summary_included` 和 `pinned_context_included`。若没有携带上下文预算的事件，页面会显示 `No context budget loaded.`，不代表 Turn 一定异常。

### 7.5 Usage

Usage 显示调用记录数、总 Token 和累计延迟，并保留后端返回的完整 JSON。排查成本或配额问题时，应结合 Provider、Model、输入/输出 Token 和具体 Turn 查看。

## 8. 审批工具调用

当 Agent 使用 `request_approval` 干预模式且某个工具需要人工确认时，调用会出现在 `Pending Approvals`。

审批前检查：

1. 工具标识和 API 名称；
2. `arguments` 中的命令、路径、网络目标和写入内容；
3. Session 和 Turn 是否为预期任务；
4. 操作是否可能删除数据、覆盖文件或调用外部系统。

操作含义：

- `Approve`：立即以固定原因 `approved from inspector` 批准调用，并继续执行；
- `Reject`：弹出原因输入框，确认后拒绝调用，并让运行时按拒绝结果继续处理；
- 取消 Reject 弹窗：不提交任何决定。

> `Approve` 是有副作用的运行控制操作，不提供二次确认。不要在无法确认参数和执行环境时批准。

提交后 Inspector 会重新加载当前 Session。若调用仍显示 pending，先关闭自动刷新并手动 `Load`；仍无变化时查看页面顶部状态、Recent Events 和服务端日志。

## 9. 制品预览与下载

`Artifacts` 面板列出当前 Session 的制品。每个制品提供：

- `Preview`：在 `Artifact Preview` 面板内预览；
- `Download`：在新窗口打开下载地址；
- `Copy CLI`：复制等价的命令行下载命令。

命令格式：

```bash
bin/tma session artifact download \
  --session SESSION_ID \
  --artifact ARTIFACT_ID \
  --output OUTPUT_PATH
```

内联预览规则：

- 图片：直接显示；
- JSON、文本、XML、YAML：以文本显示，JSON 会格式化；
- 文本超过 10,240 字符：只显示前 10,240 字符并标记截断；
- 其他二进制格式：只显示 MIME、大小和地址，需下载查看。

Timeline 中关联的制品也提供相同的 Preview、Download 和 Copy CLI 操作。

## 10. 导出 Trace

在 Query 中选择 `Export Format`：

- `Trace JSON`：TMA 原生 Trace 投影，适合程序分析和问题归档；
- `Perfetto JSON`：可导入 Perfetto 查看时间轴；
- `OTel JSON`：OpenTelemetry 风格数据，适合 Collector 或观测系统接入。

操作：

- `Preview Export`：将所选格式加载到 `Raw Export`；
- `Download`：下载 JSON 文件，文件名为 `<session>-<turn>-<format>.json`。

Perfetto 使用方式：

1. 下载 `perfetto` 格式；
2. 打开 `https://ui.perfetto.dev`；
3. 选择 `Open trace file` 并导入下载的 JSON。

也可以使用 CLI：

```bash
bin/tma trace show --session SESSION_ID --turn TURN_ID

bin/tma trace export \
  --session SESSION_ID \
  --turn TURN_ID \
  --format perfetto \
  --output trace.json
```

## 11. Metrics 与 Exporters

### 11.1 Metrics

`Metrics` 面板展示 `/metrics?session_id=...&turn_id=...` 返回的 Prometheus 文本。可用于确认：

- Trace、Span、工具和审批数量；
- LLM Usage；
- Exporter 是否启用及最近尝试状态；
- 当前服务进程可见的 Worker 与运行时指标。

### 11.2 Exporters

`Exporters` 面板显示：

- Sampling 是否启用及采样率；
- 自动重试是否启用、最大次数和近期待重试数；
- Perfetto 目的地和最近成功/失败/尝试；
- OTLP HTTP 目的地、Token 是否已配置和最近状态；
- 最近 5 条持久化 Exporter Run。

`Retry due exporters` 只重试已到期且符合重试条件的失败任务，不会无条件重放所有导出。

如果 Server 配置了 `TMA_WORKER_CONTROL_AUTH_TOKEN`，该重试 API 需要 Bearer Token，而当前 Inspector 页面不会附加控制面 Token。此时请使用 CLI：

```bash
bin/tma \
  --base-url http://localhost:8080 \
  --auth-token "$TMA_WORKER_CONTROL_AUTH_TOKEN" \
  observability retry
```

只读的 Exporter 状态仍可在 Inspector 中查看。

## 12. 深链与分享

Inspector 会把当前定位同步到 URL Hash：

```text
http://localhost:8080/inspector#session=SESSION_ID&turn=TURN_ID&trace=TRACE_ID&span=SPAN_ID
```

支持参数：

- `session`：Session ID；
- `turn`：Turn ID；
- `trace`：Trace ID；
- `span`：Span ID。

打开深链后，Inspector 优先按 Trace ID 加载；只有 Session 时加载指定或最新 Turn。单击 Waterfall/Span 会更新 `span` 参数，便于分享精确位置。

分享前注意：

- URL 本身不包含页面数据，但 ID 可能属于内部运行标识；
- 接收者必须能访问同一个 TMA Server；
- 不要把包含生产 Session ID 的链接发到公开渠道；
- 页面没有冻结快照，后端数据变化后同一链接的展示也可能变化。

## 13. 常见排查流程

### 13.1 Turn 失败

1. 加载 Session 和失败 Turn；
2. 查看 Overview 的 Errors 和 Trace Summary；
3. 在 Waterfall 中定位 error/failed Span；
4. 查看 Span Events 和 Attributes；
5. 在 Timeline 中找到对应 `runtime.tool_result` 或 `runtime.failed`；
6. 查看 Recent Events 的完整 payload；
7. 需要归档时下载 Trace JSON。

### 13.2 Turn 一直运行

1. 开启 5 秒自动刷新；
2. 查看 Pending Approvals 是否有待处理调用；
3. 查看 Timeline 最后一条事件；
4. 检查关键路径上的 open Span；
5. 查看 Context 是否正在 compacting；
6. 检查 Worker、Server 日志和相关 Exporter/工具状态。

### 13.3 工具调用很慢

1. 在 Span Search 中选择 `tool` 并设置最小耗时；
2. 打开命中结果；
3. 对照 Waterfall 的关键路径；
4. 查看工具来源是 MCP、Worker Plugin 还是 Builtin；
5. 检查 Span Attributes、工具结果和外部依赖耗时。

### 13.4 Token 使用异常

1. 查看 Usage 的输入/输出 Token；
2. 查看 Context Budget 的 message、tool schema 和 pinned context 分账；
3. 检查 `context_truncated`；
4. 查看 Context Coverage 是否有大量未汇总事件；
5. 对照 Timeline 中上下文压缩事件。

## 14. 常见问题

### 页面无法打开

检查：

```bash
curl http://localhost:8080/health
curl -I http://localhost:8080/inspector
```

若服务未运行，执行 `make run` 或 `make server-start`。若修改过监听地址，使用实际的 `TMA_HTTP_ADDR` 对应端口。

### 页面打开但没有 Recent Traces

可能原因：

- 数据库中还没有产生 Turn 事件；
- 当前 Session/Turn 筛选条件没有命中；
- 数据库迁移未完成；
- Server 连接了另一套数据库。

先点 `Clear`，再确认 `.env` 或 shell 中的 `TMA_DATABASE_URL`。

### Load 后显示错误文本

页面顶部状态会显示后端错误。常见原因包括 Session ID 不存在、Trace 尚未生成、数据库连接失败或接口返回非 2xx。结合浏览器 Network 面板和 `.tma-server.log` 排查。

### 数据没有实时更新

Inspector 使用定时重新加载，不是 SSE 实时流。确认已勾选自动刷新且 Session ID 非空，或手动单击 `Load`。

### Artifact 不能内联预览

二进制格式不支持内联预览；文本大于 10,240 字符会截断。使用 `Download` 或 `Copy CLI` 获取完整文件。

### Retry due exporters 返回 401

Server 启用了控制面鉴权。当前 Inspector 不发送 Bearer Token，请使用带 `--auth-token` 的 `bin/tma observability retry`。

### 某些面板显示 No data

Inspector 对 Usage、Summary、Artifacts、Events、Interventions、Metrics 和 Exporter Status 使用独立请求。某个面板无数据不一定代表整个 Trace 加载失败，也可能是该 Turn 没有产生对应记录或相关能力未启用。

## 15. 安全与使用限制

- Inspector 当前没有独立登录页或完整 RBAC；生产环境应由企业网关、VPN 或反向代理限制访问。
- 页面可能展示工具参数、文件路径、提示词片段、事件 payload 和制品内容，应按内部敏感数据处理。
- Approve/Reject 和 Exporter Retry 是写操作；共享屏幕或录屏时避免暴露敏感参数。
- Inspector 的 Trace/Span 目录基于近期数据投影和索引，不应替代长期审计与告警系统。
- Raw Export 和下载制品在离开 TMA 后不再受 TMA 访问边界保护，应存放在受控位置。

## 16. 验证 Inspector

静态页面与接口验收：

```bash
make verify-inspector-ui
```

浏览器 Smoke Test：

```bash
make verify-inspector-browser-smoke
```

第二个命令需要本机可用的 Chrome/Chromium 调试环境。两项验证都会构建 Inspector，并启动临时测试服务；详细失败原因可查看脚本输出和对应 `.verify-inspector-*.log`。

更多接口和观测设计说明参见：

- [Observability 设计与实现](./observability.md)
- [API Reference](./api-reference.md)
- [TMA Configuration](./configuration.md)
