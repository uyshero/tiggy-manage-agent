# TMA Configuration

本文档集中说明 TMA 当前使用的环境变量和典型配置场景。

服务端启动时会读取项目根目录下的 `.env`。如果同名环境变量已经在 shell 中设置，shell 中的值优先，`.env` 不会覆盖。

注意：`.env` 不是 shell 脚本，不支持反斜杠续行。不要写：

```env
TMA_TURN_TIMEOUT_MS=3600000 \
```

应写成单行：

```env
TMA_TURN_TIMEOUT_MS=3600000
```

## Server

### `TMA_HTTP_ADDR`

HTTP 服务监听地址。

默认：

```env
TMA_HTTP_ADDR=:8080
```

### `TMA_DATABASE_URL`

Postgres 连接字符串。服务端必需。

默认本地开发值：

```env
TMA_DATABASE_URL=postgres://tma:tma@localhost:5432/tma?sslmode=disable
```

## CLI

### `TMA_BASE_URL`

CLI 请求的 API 地址。

默认：

```env
TMA_BASE_URL=http://localhost:8080
```

示例：

```bash
TMA_BASE_URL=http://localhost:18080 bin/tma health
```

### `TMA_WORKER_CONTROL_TOKEN`

CLI 发给 server 控制面的 Bearer token。当前主要用于 worker registry 读接口、worker diagnose、worker archive 运维操作，以及 `POST /v1/worker-work`、`GET /v1/worker-work/{work_id}`、`GET /v1/worker-work/{work_id}/diagnose`、`POST /v1/worker-work/{work_id}/cancel`、`POST /v1/worker-work/{work_id}/requeue`、`POST /v1/worker-work/reap-expired` 这类 worker work 调试 / 调度接口；如果 server 配置了 `TMA_WORKER_CONTROL_AUTH_TOKEN`，CLI 应设置同一值，或在命令行上传 `--auth-token`。

示例：

```env
TMA_WORKER_CONTROL_TOKEN=change-me
```

## Worker

`tma-worker` 是常驻 worker 入口。当前版本先用于 worker 可见性和 work 轮询闭环：注册、心跳、poll、ack、running work heartbeat、result。它不直连数据库，也不对外暴露 HTTP 端口；所有交互都是 worker 主动向 `tma-server` 发起 outbound HTTP 请求。默认一次执行 1 条 work；可用 `--concurrency N` 或 `TMA_WORKER_CONCURRENCY=N` 让同一 worker 同时 lease/execute 多条队列 job。收到 SIGINT / SIGTERM 后，worker 会停止 poll，把自身标记为 `draining`，并在 shutdown timeout 内等待已 running 的 work 完成和提交 result。

启动常驻 worker 前可以先运行 doctor：

```bash
bin/tma-worker doctor --base-url http://localhost:8080 --name viito-mac
```

doctor 会展示当前 executor 导出的 runtimes / APIs / capabilities，并主动检查 server health、worker register、heartbeat、poll、diagnose 和 archive。它会临时注册一个 `<name>-doctor` worker，检查完成后归档，不进入常驻 poll loop。

### `TMA_WORKER_CONCURRENCY`

控制单个 `tma-worker` 进程的本地并发执行数。默认 `1`，保持串行消费；大于 `1` 时 worker 会补满可用 slot，连续 poll 多条 work 并并发执行。

```env
TMA_WORKER_CONCURRENCY=2
```

提高并发前应确认 worker 机器资源、工具实现和 workspace 文件写入路径能承受并行执行，避免多个 work 写同一文件或争用外部凭据。

### `TMA_WORKER_AUTH_TOKEN`

Server 侧 worker consumer API 的 Bearer token。未设置时本地开发保持开放；一旦设置，worker 注册、心跳、poll、ack、result 都必须带 `Authorization: Bearer <token>`。

示例：

```env
TMA_WORKER_AUTH_TOKEN=change-me
```

### `TMA_WORKER_CONTROL_AUTH_TOKEN`

Server 侧 worker / worker work 控制面 Bearer token。用于保护 worker registry 读接口、worker diagnose、worker archive 运维操作，以及 worker work enqueue/get/diagnose/cancel/requeue/reap-expired。它与 worker 自己消费 work 的 `TMA_WORKER_AUTH_TOKEN` 分离：`TMA_WORKER_AUTH_TOKEN` 代表“谁能注册/poll/ack/heartbeat/result”，`TMA_WORKER_CONTROL_AUTH_TOKEN` 代表“谁能查看、调度、取消、复制重入队和维护 worker/work 状态”。

示例：

```env
TMA_WORKER_CONTROL_AUTH_TOKEN=change-me-control
```

### `TMA_WORKER_TOKEN`

Worker 侧发送给 server 的 Bearer token，应与 server 的 `TMA_WORKER_AUTH_TOKEN` 匹配。

示例：

```env
TMA_WORKER_TOKEN=change-me
```

### `TMA_WORKER_WORKSPACE_ID`

Worker 所属 workspace。

默认：

```env
TMA_WORKER_WORKSPACE_ID=wksp_default
```

### `TMA_WORKER_TYPE`

Worker 类型。

默认：

```env
TMA_WORKER_TYPE=local
```

### `TMA_WORKER_REGISTERED_BY`

注册来源标识。默认使用本机 hostname。

### `TMA_WORKER_LEASE_SECONDS`

Worker 和 work 租约秒数。

默认：

```env
TMA_WORKER_LEASE_SECONDS=60
```

### `TMA_WORKER_POLL_INTERVAL`

Work 轮询间隔，使用 Go duration 格式。

默认：

```env
TMA_WORKER_POLL_INTERVAL=3s
```

### `TMA_WORKER_HEARTBEAT_INTERVAL`

Worker 心跳间隔，使用 Go duration 格式。

默认：

```env
TMA_WORKER_HEARTBEAT_INTERVAL=30s
```

### `TMA_WORKER_REAPER_ENABLED`

是否开启 server 后台 worker reaper。开启后，server 会周期性扫描 `online` 且 `lease_expires_at` 已过期的 worker，并把它们标记为 `offline`。该逻辑不删除、不 archive worker，只让 capability match / diagnose 不再把陈旧 worker 当作在线能力。

默认：

```env
TMA_WORKER_REAPER_ENABLED=true
```

### `TMA_WORKER_REAPER_INTERVAL_MS`

Server 后台 worker reaper 的扫描间隔，单位毫秒。

默认：

```env
TMA_WORKER_REAPER_INTERVAL_MS=5000
```

### `TMA_WORKER_REAPER_LIMIT`

单次 worker reaper 最多收敛多少条过期 worker。

默认：

```env
TMA_WORKER_REAPER_LIMIT=100
```

### `TMA_WORKER_WORK_HEARTBEAT_INTERVAL`

Running work 续租间隔，使用 Go duration 格式。worker 在 ack 某条 work 后、提交 result 前会周期性调用 work heartbeat，避免长任务仍在执行时被 server 侧 work reaper 当作 lease 过期标记为 failed。

默认：

```env
TMA_WORKER_WORK_HEARTBEAT_INTERVAL=15s
```

### `TMA_WORKER_SHUTDOWN_TIMEOUT`

Worker 收到 SIGINT / SIGTERM 后的 drain 等待时间，使用 Go duration 格式。shutdown 时 worker 会停止领取新 work，向 server heartbeat `draining`，并等待当前 running work 完成；超时后进程退出，未完成 work 后续会按 lease 过期由 server 侧 reaper 标记为 failed。

默认：

```env
TMA_WORKER_SHUTDOWN_TIMEOUT=30s
```

### `TMA_WORKER_WORK_REAPER_ENABLED`

是否开启 server 后台 worker work reaper。开启后，server 会周期性扫描 `leased` / `running` 且 `lease_expires_at` 已过期的 work，并把它们标记为 `failed`，错误信息写明 lease 过期。第一版不自动重新入队，避免重复执行带副作用的工具调用。

默认：

```env
TMA_WORKER_WORK_REAPER_ENABLED=true
```

### `TMA_WORKER_WORK_REAPER_INTERVAL_MS`

Server 后台 worker work reaper 的扫描间隔，单位毫秒。

默认：

```env
TMA_WORKER_WORK_REAPER_INTERVAL_MS=5000
```

### `TMA_WORKER_WORK_REAPER_LIMIT`

单次 worker work reaper 最多收敛多少条过期 work。

默认：

```env
TMA_WORKER_WORK_REAPER_LIMIT=100
```

## Trace Index Retention

### `TMA_TRACE_INDEX_RETENTION_ENABLED`

是否开启 server 后台 trace/span index retention worker。开启后，server 会周期性调用 `PruneTraceIndexes`，删除 `ended_at` 早于保留窗口的 `trace_indexes`，并通过外键级联删除对应 `trace_span_indexes`。`session_events` 仍是事实源，不会被该任务删除；后续访问缺失索引的 trace 时仍可从 events 回退投影并回填索引。

默认：

```env
TMA_TRACE_INDEX_RETENTION_ENABLED=false
```

### `TMA_TRACE_INDEX_RETENTION_DAYS`

Trace/span index 的保留天数。

默认：

```env
TMA_TRACE_INDEX_RETENTION_DAYS=30
```

### `TMA_TRACE_INDEX_RETENTION_INTERVAL_MS`

Server 后台 trace index retention worker 的扫描间隔，单位毫秒。

默认：

```env
TMA_TRACE_INDEX_RETENTION_INTERVAL_MS=3600000
```

### `TMA_TRACE_INDEX_RETENTION_LIMIT`

单次 retention 最多删除多少条 trace index。

默认：

```env
TMA_TRACE_INDEX_RETENTION_LIMIT=1000
```

## Turn

### `TMA_TURN_QUEUE_SIZE`

新 Turn 到达时的本地唤醒缓冲大小。Turn 本身持久化在 Postgres `session_turns`，该值不限制数据库中的待执行数量。

默认：

```env
TMA_TURN_QUEUE_SIZE=16
```

### `TMA_TURN_WORKER_COUNT`

每个 `tma-server` 实例并发执行 Turn 的 worker 数。不同 Session 可并行执行；同一 Session 的状态机仍只允许一个 active Turn。它控制的是 server 控制面“同时有多少个 turn 真正在跑”，不是 Session 总数，也不是数据库里待执行 turn 的总量。

```env
TMA_TURN_WORKER_COUNT=10
```

设计语义：

- 这是 **server 执行槽位上限**，用于保护 `tma-server`、Postgres、LLM Provider、artifact 写入链路，以及默认 `cloud_sandbox` 执行面。
- 每个 running turn 都可能占用一整套资源：LLM 请求、事件写库、工具调用、Docker / browser / object store I/O、summary / trace 后处理。
- 它不等价于“可承载 Session 数”。历史 Session 数主要受数据库容量影响；活跃 Session 数受 `TMA_TURN_WORKER_COUNT`、下游 worker / sandbox 能力和外部依赖限流共同约束。
- 它也不等价于 `tma-worker` 并发。后者由 `TMA_WORKER_CONCURRENCY` 控制，属于 `local_system` / plugin 执行面的本地 work 并发。

调参原则：

- 默认 `10` 作为通用起步值，适合“以 LLM + 普通工具调用为主、默认 runtime 为 `cloud_sandbox`”的中等部署。
- 如果 turn 主要是 server builtin tools、轻量 LLM 调用、少量 artifact，且机器和数据库有余量，可以逐步提高。
- 如果 turn 经常触发 `cloud_sandbox`、浏览器自动化、大文件处理、长命令或高频 artifact 上传，应降低并发，优先保证稳定性和尾延迟。
- 不要把它调成很大或“无限”。大规模吞吐应该通过横向扩容 `tma-server`、增加 `tma-worker` / sandbox 容量、做 quota 和回压，而不是只把单实例并发拉满。
- 建议按阶梯方式压测和放量，例如 `10 -> 20 -> 40`；每一步都先观察 95/99 分位延迟、错误率和队列积压，再决定是否继续上调。

压测指标：

- Turn 排队等待时间：从 `user.message` 写入到对应 turn 真正开始执行的延迟。
- Turn 完成时间：从 `user.message` 到最终 `agent.message` / `session.status_idle` 的总时长，重点看 p95 / p99。
- 数据库压力：Postgres CPU、连接数、慢 SQL、锁等待、`ClaimSessionTurns` 轮询开销。
- LLM 压力：请求并发、429/超时、平均响应时长、失败重试比例。
- 执行面压力：`cloud_sandbox` 容器创建 / `docker exec` 延迟、浏览器启动耗时、worker work backlog、artifact 上传失败率。
- 控制面稳定性：lease heartbeat 失败次数、turn fail / cancel 比例、SSE 推送延迟、trace / summary 后处理积压。

推荐口径：

- 把 `TMA_TURN_WORKER_COUNT` 当成 **单实例执行并发上限**，而不是性能目标值。
- 如果用户体感是“任务排队太久”，先看 server 槽位、worker backlog、sandbox 容器延迟、LLM 限流，找到真正瓶颈后再加并发。
- 如果用户体感是“任务经常超时、卡顿、Docker 抖动、数据库尖峰”，应先降并发或拆分执行面，而不是继续上调。

### Turn lease 配置

worker 使用数据库 lease 领取 Turn。服务启动会立即扫描无 lease 或 lease 已过期的 `running` Turn，多个 server 实例通过 `FOR UPDATE SKIP LOCKED` 避免同时领取同一任务。

```env
TMA_TURN_POLL_INTERVAL_MS=500
TMA_TURN_LEASE_DURATION_MS=10000
TMA_TURN_HEARTBEAT_INTERVAL_MS=1000
```

心跳续租失败，或另一实例通过 `user.interrupt` 把 Turn 改为 `interrupted` 后，执行 context 会被取消。心跳间隔必须小于 lease 时长。

### `TMA_TURN_TIMEOUT_MS`

单次 turn 的超时时间。它保护的是“一轮用户消息到最终 agent.message”的整体执行时间，不是单个轻量命令的超时。

默认：

```env
TMA_TURN_TIMEOUT_MS=3600000
```

默认值是 1 小时，给安装依赖、构建、运行测试、仓库检索等长任务留出空间。需要提前停止时应优先使用 interrupt。

### 大规模并发设计标准

以下标准把“承载量”拆成多个层次。设计目标不是把某一个并发参数调到极大，而是在每一层都保留明确的上限、回压和观测点。

#### 1. Server 并发

- 当前实现：由 `TMA_TURN_WORKER_COUNT` 控制单个 `tma-server` 实例同时执行多少个 turn。
- 默认值：`10`。
- 设计要求：必须是有限值；server 是控制面，不应承担无限制的执行风暴。
- 建议：先把单实例并发控制在“数据库、LLM、默认 sandbox 都能稳定承受”的范围，再通过增加 server 实例扩展总吞吐。

#### 2. Worker 并发

- 当前实现：由 `TMA_WORKER_CONCURRENCY` 控制单个 `tma-worker` 同时执行多少条 `worker_work`。
- 默认值：`1`，保持串行消费。
- 设计要求：worker 并发应按能力类型分别评估，不能统一粗暴上调。
- 建议：
  - 桌面 / `computer.*` / 人工接管类 worker：通常保持 `1`。
  - 本机命令 / 文件处理类 worker：从 `2` 起步，小步压测。
  - 纯无状态插件 worker：确认路径和凭据互不争用后再继续提高。

#### 3. Sandbox 并发

- 当前实现：`cloud_sandbox` 以 session 维度复用容器；同一 session / scope 内的命令会串行化，避免并发写同一容器状态。
- 现状边界：仓库当前没有独立的“全局 sandbox 并发阈值”配置；实际并发由 server 并发、session 分布和宿主机 Docker 资源共同决定。
- 设计要求：server 总并发不应超过宿主机可稳定支撑的 sandbox 活跃数。
- 建议：把 sandbox 看成单独容量池，重点观察容器启动耗时、活跃容器数、`docker exec` 延迟、磁盘 I/O 和网络带宽。

#### 4. Workspace Quota

- 当前实现：代码里已按 `workspace_id` 做 worker 选择和隔离，但尚未形成完整的强制并发配额。
- 建议标准：
  - 每个 workspace 都应有独立的 running quota 和 queued quota。
  - `running quota` 控制该租户同时能占多少执行槽位。
  - `queued quota` 控制该租户最多能积压多少待执行 turn / work，避免单个大客户挤爆全局队列。
  - 一般先配 `queued quota = running quota` 的 `3x ~ 10x`，再按业务等级细化。

#### 5. User Quota

- 当前实现：尚未提供完整的用户级并发 / 队列配额。
- 建议标准：
  - 单个用户默认只允许少量 active turn，避免误操作或脚本刷爆同一 workspace。
  - 交互式用户建议 active quota 较小，自动化账号或系统账号按业务等级单独放宽。
  - 用户级 queued quota 应小于 workspace 级 queued quota，防止单人长期占满租户队列。

#### 6. 队列与回压策略

- 当前实现：
  - `session_turns` 持久化待执行 turn。
  - `TMA_TURN_QUEUE_SIZE` 只是本地唤醒缓冲，不限制数据库中的积压数量。
  - `worker_work` 是独立的 worker 队列，带 lease / heartbeat / cancel / requeue。
- 建议标准：
  - 有空槽时立即执行；无空槽但未超 quota 时允许排队。
  - 超过 workspace / user queued quota 时，不应无限堆积，应返回明确错误或降级策略。
  - 需要给排队请求返回可诊断信息，例如当前运行数、当前排队数、建议重试时间。
  - 扩容优先级应是：增加 server / worker / sandbox 容量，优化工具执行面，再考虑提高单实例并发。

#### 7. 推荐放量顺序

- 先定单实例 `TMA_TURN_WORKER_COUNT`，跑稳定性压测。
- 再调 `TMA_WORKER_CONCURRENCY`，验证 `local_system` / plugin 执行面不会争用路径、凭据和桌面资源。
- 再评估 sandbox 容量和活跃容器数，确认 Docker 侧没有成为瓶颈。
- 最后补 workspace / user quota 和拒绝策略，把“排队”与“拒绝”边界说清楚。

#### 8. 当前实现与目标标准的边界

- 已实现并可直接调参：`TMA_TURN_WORKER_COUNT`、`TMA_TURN_QUEUE_SIZE`、`TMA_WORKER_CONCURRENCY`、turn / work lease 与 heartbeat。
- 已具备基础隔离但未形成完整 quota 产品面：workspace 级 worker 选择与执行隔离。
- 仍建议后续补齐：workspace quota、user quota、基于 backlog 的显式回压与错误码、sandbox 全局并发阈值。

超时后，当前 turn 会被取消并标记为 `failed`，Session 会回到 `idle`，后续仍可继续发送新的 `user.message`。

当前服务端内置 `agentruntime.DemoRuntime`。AgentRuntime 设计见 [agent-runtime.md](./agent-runtime.md)。

Session `runtime_settings` 可设置上下文预算：

- `context_input_budget_ratio_percent`：单次 LLM 请求的输入上下文预算比例，默认 60，服务端会把该值限制在 10–95 之间。
- `context_output_reserve_tokens`：显式预留输出 token。设置后输入预算会取「比例预算」和「总窗口减输出预留」里的较小值。
- `pinned_context`：不可压缩上下文，可为字符串或字符串数组；Runtime 会作为 `Pinned context` system message 注入，并在历史截断和 summary 重建时保留。
- `protected_context`：`pinned_context` 的别名。
- `tool_result_context_max_chars`：单个工具结果进入模型上下文时的最大字符数；完整结果仍保留在事件和 artifact 中。
- `compaction_prompt_max_chars`：自动 summary 的压缩 prompt 上限，默认 60000 字符。
- `compaction_summary_max_chars`：自动 summary 写回 session summary 前的上限，默认 12000 字符。

长任务通常保持默认；工具链很重或需要预留更多输出空间时可调低输入比例，或显式设置输出预留。

## Tool Runtime

工具执行的默认 runtime 由 `TMA_TOOL_RUNTIME` 和 `TMA_CLOUD_SANDBOX_*` 控制。服务端启动级配置只作为默认值；如果某个 session 的 `runtime_settings` 中显式设置了 `tool_runtime`、`cloud_sandbox_root`、`cloud_sandbox_image` 或 `cloud_sandbox_allow_network`，session 设置会覆盖启动默认值。这样可以在不重启服务的情况下热更新单个会话的工具执行面。

### `TMA_TOOL_RUNTIME`

工具 runtime 默认值。

可选值：

```text
auto
cloud_sandbox
local_system
```

默认不需要配置；未设置时等价于 `cloud_sandbox`。一般不要在 `.env` 中写 `TMA_TOOL_RUNTIME`，保持默认即可。

含义：

- `auto`：第一版在 `default.*` tools 上等价选择 `cloud_sandbox`。
- `cloud_sandbox`：使用 `OnlyboxesProvider` 执行。Session 第一次调用时通过 `docker run --pull missing -d` 创建容器，后续命令使用 `docker exec` 复用，并挂载 workspace 与 session 数据目录。默认使用 Docker 默认网络并具备外网访问能力；将 `cloud_sandbox_allow_network` 设为 `false` 后才会用 `--network none` 禁用网络。具备外网能力的 `default.run_command` / `default.execute_code` 会按当前 `intervention_mode` 进入 `network_access` 审批层；`request_approval` 会等待用户确认，`approve_for_me` 会自动批准并记录事件，`full_access` 直接执行。
- `local_system`：表示需要本机执行能力。生产语义下它必须匹配同 workspace 的在线 `tma-worker`，由 worker 在运行它的机器上执行；server 进程不会默认假装本机能力存在。

本地完整验收：

```bash
make verify-network-approval
```

该命令会用 fake LLM 触发 Python 下载脚本，覆盖 `request_approval`、`approve_for_me`、`full_access` 和 `cloud_sandbox_allow_network=false` 四条路径。

TMA 不会自动启动 Docker daemon 或 Onlyboxes Console。`cloud_sandbox` 只表示工具调用时选择沙箱执行面。一个 Session 第一次调用某类沙箱工具时创建容器，后续 Turn 通过 `docker exec` 复用；普通命令和浏览器工具使用不同 scope，避免浏览器镜像覆盖默认命令镜像。容器在空闲超时、达到最大寿命或 Server 正常退出时删除。

Session 容器生命周期配置：

```env
TMA_CLOUD_SANDBOX_CONTAINER_IDLE_TTL_SECONDS=1800
TMA_CLOUD_SANDBOX_CONTAINER_MAX_LIFETIME_SECONDS=14400
TMA_CLOUD_SANDBOX_CONTAINER_CLEANUP_INTERVAL_SECONDS=60
```

- `CONTAINER_IDLE_TTL_SECONDS`：最后一次命令完成后允许空闲的时间，默认 30 分钟。
- `CONTAINER_MAX_LIFETIME_SECONDS`：容器从创建起的最长寿命，默认 4 小时；到期后下一次调用会重建。
- `CONTAINER_CLEANUP_INTERVAL_SECONDS`：后台扫描过期容器的间隔，默认 60 秒。

容器销毁不会删除宿主机挂载的 `/workspace` 和 `/mnt/data`；容器内部未写入挂载目录的软件安装、进程和缓存会随容器一起消失。`TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS` 仍只控制 Session 数据目录的保留时间，与容器 TTL 分开。

`browser.*` 是独立能力域，不混入 `default.*` 命令工具。`local_system` 下优先由同 workspace 的本地 `tma-worker` 执行浏览器动作，适合后续扩展人工接管；`cloud_sandbox` 下会通过 Playwright headless runner 执行浏览器动作，建议使用单独的浏览器沙箱镜像。

### Browser Tools

当前内置浏览器工具：

```text
browser.open
browser.read
browser.click
browser.type
browser.screenshot
browser.takeover
browser.close
```

浏览器 runner 需要 Node.js 和 Playwright 包。`cloud_sandbox` 场景推荐单独准备轻量 headless 镜像，并通过 `TMA_BROWSER_SANDBOX_IMAGE` 指定；该变量只覆盖 `browser.*` 工具使用的镜像，不影响普通 `default.*` 命令工具：

```env
TMA_BROWSER_SANDBOX_IMAGE=your-registry/tma-browser-sandbox:playwright
```

本仓库提供了第一版浏览器沙箱镜像：

```bash
make build-browser-sandbox
```

默认会构建：

```text
tma-browser-sandbox:playwright
```

需要改镜像名时：

```bash
TMA_BROWSER_SANDBOX_IMAGE=your-registry/tma-browser-sandbox:playwright make build-browser-sandbox
```

镜像至少需要包含：

```text
node
playwright 或 playwright-core
chromium
常用字体，建议包含中文字体
```

本地 worker 场景则在运行 `tma-worker` 的机器安装 Node.js 和 Playwright。`local_system` 会按 `browser_session_id` 启动或复用本地长驻 Chromium，并通过 CDP 操作同一个真实页面；浏览器 profile 和 CDP endpoint 保存在系统临时目录下的 `tma-browser`。同一 session 的后续 `browser.read/click/type/screenshot/takeover` 会接着当前页面状态执行，适合 agent 和人工交替控制。

`cloud_sandbox` 的 Session 容器可以复用，但浏览器动作仍采用按调用启动的 headless 进程。页面状态保存在 `/mnt/data/browser`，通过 URL、storage state 和轻量动作记录在多次调用之间重建；它不持有长驻浏览器进程。

`browser.takeover` 是本地人工接管入口，只在 `local_system` runtime 暴露。它会在本地 worker 机器打开同一个长驻 headed Chromium，等待用户操作完成后返回最终页面状态；用户可以关闭浏览器窗口提前结束，也可以通过 `wait_seconds` 控制最长等待时间，默认 300 秒，最大 3600 秒。使用它时 session 或 agent tools runtime 需要配置为 `local_system`，并且本地 worker 所在机器必须有可用桌面环境：

```bash
bin/tma session runtime update --session SESSION_ID --tool-runtime local_system --intervention-mode approve_for_me
```

`browser.close` 用于关闭本地长驻 browser session，避免本机 Chromium 进程长期残留。`cloud_sandbox` 不声明 `browser.takeover` / `browser.close` capability；沙箱浏览器继续使用 headless 模式，适合自动化和截图，不承担人工接管。

本地验收：

```bash
make build-browser-sandbox
make verify-browser-tools
make verify-browser-takeover-local
```

`make verify-browser-tools` 会使用 `data:` URL 注入测试页面，在断网的 `cloud_sandbox` 中执行 `browser.open`、`browser.screenshot`、`browser.type`、`browser.click`，并校验工具事件、页面标记和截图 artifact。`make verify-browser-takeover-local` 会启动本地 worker 和 headed Chromium，需要人工关闭浏览器窗口后才会完成。

## Web Search / Crawl

`web.search` 和 `web.crawl` 是 server builtin tools，不依赖 `cloud_sandbox` 或 `local_system` provider。未配置商业搜索 key 时，默认搜索 Provider 是本地 SearXNG：

```env
TMA_WEB_SEARXNG_BASE_URL=http://localhost:8180
```

Agent 侧启用方式：

```json
{"tools":["web"],"runtime":"auto"}
```

本地开发可用 `docker compose up -d searxng` 启动内置 SearXNG 服务。没有显式设置 `TMA_WEB_SEARCH_PROVIDERS` 时，系统会自动把已经配置 key 的 Provider 排在 SearXNG 前面：

```env
TMA_WEB_TAVILY_API_KEY=...
TMA_WEB_BRAVE_API_KEY=...
TMA_WEB_EXA_API_KEY=...
TMA_WEB_BAIDU_API_KEY=...
TMA_WEB_SEARCH1API_API_KEY=...
```

自动顺序是：

```text
tavily -> brave -> exa -> baidu -> search1api -> searxng
```

需要固定顺序时仍可显式配置，前一个无结果或报错会继续尝试后一个：

```env
TMA_WEB_SEARCH_PROVIDERS=brave,tavily,searxng
```

可选 Provider endpoint 覆盖：

```env
TMA_WEB_TAVILY_BASE_URL=https://api.tavily.com/search
TMA_WEB_BRAVE_BASE_URL=https://api.search.brave.com/res/v1/web/search
TMA_WEB_EXA_BASE_URL=https://api.exa.ai/search
TMA_WEB_BAIDU_BASE_URL=https://qianfan.baidubce.com/v2/ai_search
TMA_WEB_SEARCH1API_BASE_URL=https://api.search1api.com/search
```

Baidu Provider 使用 Bearer / `X-API-Key` 方式请求，并兼容常见 JSON 搜索结果字段（如 `results`、`search_results`、`organic_results`、`references`、`data.list`）。如果你们接的是百度网关或代理层，直接用 `TMA_WEB_BAIDU_BASE_URL` 指到实际 endpoint。

抓取默认实现链：

```env
TMA_WEB_CRAWLER_IMPLS=jina,naive,search1api,browserless
TMA_WEB_CRAWLER_RETRY=1
TMA_WEB_CRAWL_CONTENT_LIMIT=25000
```

### Search fallback

`web.search` 按 `TMA_WEB_SEARCH_PROVIDERS` 顺序尝试 Provider。单个 Provider 报错会记录错误并继续尝试后续 Provider；单个 Provider 返回空结果时会按以下顺序放宽查询：

```text
query + categories + engines + time_range
query + categories + time_range
query
```

搜索结果默认最多返回 30 条，可通过 `TMA_WEB_SEARCH_ITEM_LIMIT` 调整，但仍会被工具层限制在 30 条以内。

`web.search` 的 tool state 会保留诊断信息：每次 provider / fallback attempt 的耗时、过滤条件、结果数、错误信息，以及 SearXNG 返回的 `unresponsive_engines`。模型正文仍只展示搜索结果，诊断字段主要用于 CLI / trace / 调试。

### Crawl fallback

`web.crawl` 默认尝试：

```text
jina -> naive -> search1api -> browserless
```

每个 crawler 返回正文长度至少 100 字符才算成功；内容过短、实现报错或 URL 规则不适配时会尝试下一个实现。单页内容默认最多返回 25000 字符。

`web.crawl` 的每个 page state 会保留 `attempts` 诊断信息：retry round、impl 名称、改写后的 URL、耗时、正文长度、错误类型和错误文本。模型正文仍只输出页面内容或结构化错误。

URL 特殊规则：

- GitHub `blob` URL 自动转 `raw.githubusercontent.com`，优先 `naive` / `jina`
- YouTube / Reddit 优先 `search1api`
- 小红书 / xhslink 优先 `search1api` / `jina`

当前没有自研反爬。`naive` 只做普通 HTTP 抓取和简单 HTML 文本抽取，不执行 JavaScript。`jina` 默认走公开 Jina Reader，不需要 key，但可能受限流和目标站策略影响。`browserless` 用真实浏览器执行 JavaScript 后返回 HTML，适合动态页面，但需要配置 Browserless 服务/API。

Browserless 动态渲染配置：

```env
TMA_WEB_BROWSERLESS_BASE_URL=https://chrome.browserless.io/content
TMA_WEB_BROWSERLESS_API_KEY=...
TMA_WEB_BROWSERLESS_PRIORITY=after_naive
TMA_WEB_BROWSERLESS_WAIT_SELECTOR=#app
TMA_WEB_BROWSERLESS_WAIT_TIMEOUT_MS=1200
TMA_WEB_BROWSERLESS_WAIT_SELECTOR_TIMEOUT_MS=8000
TMA_WEB_BROWSERLESS_GOTO_TIMEOUT_MS=15000
TMA_WEB_BROWSERLESS_REQUEST_TIMEOUT_MS=30000
TMA_WEB_BROWSERLESS_WAIT_UNTIL=networkidle0
TMA_WEB_BROWSERLESS_USER_AGENT=
TMA_WEB_BROWSERLESS_REJECT_RESOURCE_TYPES=image,media,font,stylesheet
TMA_WEB_BROWSERLESS_BEST_ATTEMPT=true
```

`TMA_WEB_BROWSERLESS_PRIORITY` 可选：

- `last`：保持默认顺序，Browserless 最后兜底。
- `after_naive`：推荐动态抓取配置。先用 `naive` 低成本抓静态页，失败或正文过短后立即进 Browserless，再尝试 Search1API。
- `first`：Browserless 优先，适合明确知道目标主要是 JS 动态页面的场景。

本地验收：

```bash
make verify-web-search-crawl
```

本地诊断当前搜索配置：

```bash
bin/tma web doctor --searxng-url http://localhost:8180 --query 测试 --timeout 20
```

输出会包含 search provider 顺序、各 Provider key 是否配置、crawler 顺序、SearXNG 实际命中的 engines、超时/验证码等 `unresponsive_engines`，以及 Google / DuckDuckGo / Brave / Startpage / YouTube 等禁用源是否仍被调度。

不经过 LLM / session，直接本地调试搜索和抓取：

```bash
bin/tma web search --query "中文 AI 智能体 新闻" --limit 5 --timeout 30
bin/tma web crawl --url https://example.com --timeout 45
```

`web crawl` 可强制单个实现或调整输出：

```bash
bin/tma web crawl --url https://example.com --impl browserless --timeout 60
bin/tma web crawl --url https://example.com --attempts-only
bin/tma web crawl --url https://example.com --content-only
```

容器内路径约定：

- `/workspace`：挂载 `TMA_CLOUD_SANDBOX_ROOT` 指向的源码 / 工作区，用于读写项目文件。
- `/mnt/data`：挂载 session 级持久数据目录，用于用户上传文件、加工中间产物、跨多次工具调用复用的临时数据。

通过 `POST /v1/sessions/{session_id}/artifacts/upload` 上传的文件，会在下次 sandbox 执行前同步到 `/mnt/data/uploads/{artifact_id}/{filename}`。这样同一 session 里的工具可以直接处理用户上传内容，而且不同上传不会互相覆盖。

`cloud_sandbox` 的容器本身不常驻，但同一个 session 的 `/mnt/data` 会复用同一个 host 目录；不同 session 使用不同目录，目录名来自清洗后的 TMA session id。这样可以减少重复准备数据，也避免不同用户 / session 的中间文件串在一起。

可以用本地 CLI 预检当前配置：

```bash
bin/tma sandbox doctor
```

该命令会读取当前目录 `.env`，检查 workspace root、Docker daemon 和镜像。默认会在镜像缺失时执行 `docker pull`；如果只想纯检查，使用 `--pull=false`。

### `TMA_CLOUD_SANDBOX_ROOT`

`cloud_sandbox` 的 workspace 根目录。为空时使用服务进程当前工作目录。

```env
TMA_CLOUD_SANDBOX_ROOT=.
```

### `TMA_CLOUD_SANDBOX_IMAGE`

`cloud_sandbox` 模式使用的镜像。为空时使用内置默认镜像名。

```env
TMA_CLOUD_SANDBOX_IMAGE=coolfan1024/onlyboxes-runtime:default
```

### `TMA_CLOUD_SANDBOX_ALLOW_NETWORK`

是否允许 `cloud_sandbox` 容器使用 Docker 默认网络并访问外网。

默认：

```env
TMA_CLOUD_SANDBOX_ALLOW_NETWORK=true
```

Python 脚本、`pip`、`curl`、`wget` 等在沙箱内可以直接联网下载。需要显式断网时设置为：

```env
TMA_CLOUD_SANDBOX_ALLOW_NETWORK=false
```

### `TMA_CLOUD_SANDBOX_DATA_ROOT`

`cloud_sandbox` 的 session 数据根目录。TMA 会在该目录下按 session id 创建子目录，并挂载到容器内 `/mnt/data`。

默认：

```env
TMA_CLOUD_SANDBOX_DATA_ROOT=/private/tmp/tma-cloud-sandbox-data
```

### `TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS`

`/mnt/data` 对应 host session 数据目录的过期时间，默认 3600 秒。每次同 session 工具调用都会刷新该目录 mtime；后续创建 sandbox 数据目录时，会清理超过 TTL 未使用的 session 数据目录。

```env
TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS=3600
```

本地覆盖示例。默认 runtime 已经是 `cloud_sandbox`，通常只需要在要固定 root 或镜像时配置：

```env
TMA_CLOUD_SANDBOX_ROOT=.
TMA_CLOUD_SANDBOX_IMAGE=coolfan1024/onlyboxes-runtime:default
TMA_CLOUD_SANDBOX_DATA_ROOT=/private/tmp/tma-cloud-sandbox-data
TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS=3600
```

如果要临时切到本机执行面，先确保同 workspace 有在线 `tma-worker` 注册了 `local_system` runtime，再走 session 级热更新，不改 `.env`：

```bash
bin/tma session runtime update --session <session_id> --tool-runtime local_system
```

### `TMA_ALLOW_SERVER_LOCAL_SYSTEM`

是否允许 `tma-server` 进程在没有匹配 `tma-worker` 时直接使用 `LocalSystemProvider`。

默认：

```env
TMA_ALLOW_SERVER_LOCAL_SYSTEM=false
```

这个开关只给受信任的本地开发调试使用。真实部署里保持关闭：如果 session 或 agent config 请求 `local_system`，但没有匹配在线 worker，AgentRuntime 会把相关工具从模型可见工具集中隐藏，而不是退回到 server 主机执行。

Onlyboxes 平台本身的安装、Console 配置，以及 LobeHub 对接步骤见 [产品设计架构图梳理.md](./产品设计架构图梳理.md)。

## Context

### `TMA_DEFAULT_CONTEXT_WINDOW_TOKENS`

未知模型的默认总上下文窗口。默认按 128k 处理。

```env
TMA_DEFAULT_CONTEXT_WINDOW_TOKENS=128000
```

每个模型可以通过 `llm_models.context_window_tokens` 单独指定窗口大小。Context Builder 当前固定最多使用窗口的 60% 作为输入上下文预算；超过预算时保留 system 和当前 user message，并从最近的历史消息开始尽量纳入预算。当前 token 计数是近似估算，不是模型厂商 tokenizer 的精确结果。

## Object Storage

对象存储配置用于 artifact、静态文件、workspace snapshot 和跨环境文件系统。当前服务端默认使用 `localfs` 把对象落到本地磁盘；HTTP / CLI 已经可以完成本地 upload / download 闭环，不需要先起 RustFS。

无论后端是 `localfs` 还是未来的 S3-compatible 实现，客户端下载都应通过 TMA 代理端点完成，不直接暴露底层对象存储地址。

TMA 也支持 S3-compatible 后端。`TMA_OBJECT_STORAGE_PROVIDER=s3` 时，服务端会使用 AWS SigV4 直接访问配置的 endpoint，当前覆盖 artifact 所需的 `PUT` / `GET` / `DELETE` 和内部 presign 能力，适配 RustFS / MinIO / AWS S3 风格服务。

客户端下载仍然应通过 TMA 代理端点完成，不直接暴露底层 S3 endpoint、bucket/key 或 presigned URL。`PresignGetObject` 只是 provider 内部能力，不能作为对外 API 合同。

如果你要验证真实 S3 兼容行为，需要先启动 RustFS / MinIO 或提供可访问的 S3-compatible endpoint，然后运行 `make verify-objectstore-s3`。

### `TMA_OBJECT_STORAGE_PROVIDER`

对象存储协议类型。

默认：

```env
TMA_OBJECT_STORAGE_PROVIDER=localfs
```

### `TMA_OBJECT_STORAGE_ENDPOINT`

S3 兼容 endpoint。`localfs` 后端会忽略它；`s3` 后端会用它组装 path-style 或 virtual-host-style 对象 URL。

默认：

```env
TMA_OBJECT_STORAGE_ENDPOINT=http://localhost:9000
```

### `TMA_OBJECT_STORAGE_REGION`

S3 region。部分 S3 兼容实现不会严格使用 region，但 SigV4 credential scope 需要一个值。

默认：

```env
TMA_OBJECT_STORAGE_REGION=us-east-1
```

### `TMA_OBJECT_STORAGE_BUCKET`

默认 artifact bucket。

默认：

```env
TMA_OBJECT_STORAGE_BUCKET=tma-artifacts
```

### `TMA_OBJECT_STORAGE_ACCESS_KEY_ENV`

Access Key 所在环境变量名。

默认：

```env
TMA_OBJECT_STORAGE_ACCESS_KEY_ENV=TMA_OBJECT_STORAGE_ACCESS_KEY
```

### `TMA_OBJECT_STORAGE_SECRET_KEY_ENV`

Secret Key 所在环境变量名。

默认：

```env
TMA_OBJECT_STORAGE_SECRET_KEY_ENV=TMA_OBJECT_STORAGE_SECRET_KEY
```

### `TMA_OBJECT_STORAGE_ROOT_DIR`

本地文件后端的磁盘根目录。`localfs` provider 会把 bucket 和对象 key 展开到这个目录下。

默认：

```env
TMA_OBJECT_STORAGE_ROOT_DIR=/private/tmp/tma-object-store
```

RustFS / MinIO 本地示例：

```env
TMA_OBJECT_STORAGE_PROVIDER=localfs
TMA_OBJECT_STORAGE_ROOT_DIR=/private/tmp/tma-object-store
TMA_OBJECT_STORAGE_BUCKET=tma-artifacts
```

### `TMA_OBJECT_STORAGE_USE_PATH_STYLE`

是否使用 path-style URL。RustFS / MinIO 这类本地 S3 兼容服务通常需要开启。

默认：

```env
TMA_OBJECT_STORAGE_USE_PATH_STYLE=true
```

本地示例：

```env
TMA_OBJECT_STORAGE_PROVIDER=s3
TMA_OBJECT_STORAGE_ENDPOINT=http://localhost:9000
TMA_OBJECT_STORAGE_REGION=local
TMA_OBJECT_STORAGE_BUCKET=tma-artifacts
TMA_OBJECT_STORAGE_ACCESS_KEY_ENV=TMA_OBJECT_STORAGE_ACCESS_KEY
TMA_OBJECT_STORAGE_SECRET_KEY_ENV=TMA_OBJECT_STORAGE_SECRET_KEY
TMA_OBJECT_STORAGE_ACCESS_KEY=tma
TMA_OBJECT_STORAGE_SECRET_KEY=tma-secret
TMA_OBJECT_STORAGE_USE_PATH_STYLE=true
```

## LLM

### `TMA_LLM_PROVIDER`

当前默认模型 Provider。

默认：

```env
TMA_LLM_PROVIDER=fake
```

当前内置：

```text
fake
openai-compatible
```

`fake` 不访问外部模型 API，只用于把 HTTP / Store / Runner / AgentRuntime / LLM Client 这条链路跑通。

`openai-compatible` 调用 OpenAI Chat Completions 兼容接口，适用于 OpenAI 或企业内部兼容网关。

当前 `openai-compatible` 使用 `stream: true` 读取普通文本 SSE 增量，服务端会把增量写成 `runtime.llm_delta` 事件；最终仍会写一条完整 `agent.message`。带工具 schema 的请求第一版走非流式 Chat Completions，并支持原生 `tools` / `tool_calls` 适配。

Session 级工具权限和沙箱网络可通过 `PATCH /v1/sessions/{session_id}/runtime-settings` 热更新。`intervention_mode` 当前支持 `request_approval`、`approve_for_me`、`full_access`；`cloud_sandbox_allow_network` 可控制单个 session 的沙箱是否具备外网访问能力。具备外网能力时，`default.run_command` / `default.execute_code` 和 `web.search` / `web.crawl` 会使用同一套审批策略，reason 为 `network_access`。

CLI：

```bash
bin/tma session runtime get --session sesn_000001
bin/tma session runtime update --session sesn_000001 --intervention-mode approve_for_me
bin/tma session runtime update --session sesn_000001 --cloud-sandbox-allow-network=true
```

完整回归：

```bash
make verify-network-approval
```

这个配置项先作为默认 Provider 选择入口保留。服务启动时会把它 upsert 到 `llm_providers` 表；创建 Agent 时，如果请求没有传 `llm_provider`，HTTP 层会用它补齐 AgentConfigVersion。

`TMA_LLM_PROVIDER` 可以是内置 Provider ID，也可以是业务自定义 Provider ID。例如：

```env
TMA_LLM_PROVIDER=volcengine-agent-plan
TMA_LLM_PROVIDER_TYPE=openai
```

这里 `volcengine-agent-plan` 是 TMA 内部保存和展示的 Provider ID，`openai` 是底层协议类型。自定义 Provider ID 如果没有显式设置 `TMA_LLM_PROVIDER_TYPE`，当前会默认按 `openai` 注册。

后续增加 Anthropic、本地模型或企业内部网关时，可以继续扩展 Provider Type。执行 turn 时，Runtime 会按 Session 绑定的 AgentConfigVersion 找到 Provider ID，再从 `llm_providers` 解析底层协议、Base URL 和 API Key 环境变量名。

Provider 也可以通过 HTTP / CLI 管理：

```bash
bin/tma provider list
bin/tma provider create --id volcengine-agent-plan --type openai --base-url https://ark.cn-beijing.volces.com/api/plan/v3 --api-key-env TMA_LLM_API_KEY
bin/tma provider disable --id volcengine-agent-plan
bin/tma provider enable --id volcengine-agent-plan
```

`.env` 里的 Provider 配置只用于服务启动时保证默认 Provider 存在；正式运行中，AgentConfigVersion 绑定的是 `llm_providers.id`。

Provider 长期设计、模型能力和 token usage 审计路线见 [llm-provider-roadmap.md](./llm-provider-roadmap.md)。

### `TMA_LLM_PROVIDER_TYPE`

Provider 底层协议类型。

当前支持：

```text
openai
openai-compatible
```

推荐使用 `openai`。`openai-compatible` 作为历史别名保留兼容。

内置 Provider `fake` 不需要配置 provider type。自定义 Provider ID 建议显式配置：

```env
TMA_LLM_PROVIDER=volcengine-agent-plan
TMA_LLM_PROVIDER_TYPE=openai
```

### `TMA_LLM_MODEL`

当前默认模型名。

默认：

```env
TMA_LLM_MODEL=fake-demo
```

`TMA_LLM_MODEL` 由当前 Provider 解释。对于 `fake` Provider，它只是运行时事件和调试日志里的模型标识。服务启动时，内置通用智能体 `agt_general` 会首次绑定该 Model；创建自定义 Agent 时，如果请求没有传 `llm_model` 或兼容字段 `model`，HTTP 层也会用它补齐 AgentConfigVersion。

热切换设计说明：当前服务端已经通过 `llm.Manager` 间接调用模型。未来即使增加运行时切换 API，也不需要重组 `WorkerRunner` 或 `AgentRuntimeTurnExecutor`；后续 turn 会读取 Session 绑定的 AgentConfigVersion，或读取切换后的默认配置创建新 Agent。

### `TMA_LLM_BASE_URL`

OpenAI-compatible 接口地址。

默认：

```env
TMA_LLM_BASE_URL=https://api.openai.com/v1
```

使用 OpenAI 协议 Provider 时会使用它。请求路径会拼成：

```text
{TMA_LLM_BASE_URL}/chat/completions
```

### `TMA_LLM_API_KEY_ENV`

OpenAI-compatible API Key 的环境变量名。

默认：

```env
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY
```

TMA 会把这个变量名写入 `llm_providers.api_key_env`，执行 turn 时再从进程环境变量读取真实 API Key。这样数据库只保存密钥引用，不保存真实密钥。

例如可以给不同 Provider 准备不同变量：

```env
TMA_LLM_PROVIDER=volcengine-agent-plan
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY_VOLCENGINE
TMA_LLM_API_KEY_VOLCENGINE=...
```

### `TMA_LLM_API_KEY`

OpenAI-compatible API Key。

示例：

```env
TMA_LLM_API_KEY=sk-...
```

如果使用 OpenAI 协议 Provider，该配置必填，除非你把 `TMA_LLM_API_KEY_ENV` 指向了其他环境变量。当前不会把 API Key 存入数据库，也不会返回给客户端。

### OpenAI-compatible 示例

```env
TMA_LLM_PROVIDER=volcengine-agent-plan
TMA_LLM_PROVIDER_TYPE=openai
TMA_LLM_MODEL=gpt-4o-mini
TMA_LLM_BASE_URL=https://api.openai.com/v1
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY
TMA_LLM_API_KEY=sk-...
```

创建 Agent 时也可以显式指定：

```bash
bin/tma agent create \
  --name "Code Assistant" \
  --llm-provider volcengine-agent-plan \
  --llm-model gpt-4o-mini \
  --system "You are a coding agent."
```

## Tests

### `TMA_RUN_POSTGRES_TESTS`

是否运行 Postgres 集成测试。

默认不运行。显式启用：

```bash
TMA_RUN_POSTGRES_TESTS=1 TMA_DATABASE_URL=postgres://tma:tma@localhost:5432/tma?sslmode=disable go test ./internal/managedagents -run Postgres
```

推荐使用：

```bash
make test-postgres
```

## Verification Scripts

### `make verify-llm-provider`

验证当前 `.env` 或 shell 环境中配置的真实 LLM Provider。

```bash
make verify-llm-provider
```

它会启动临时服务，创建 Agent / Environment / Session，发送一条测试消息，并检查：

```text
runtime.llm_request
runtime.llm_response
agent.message
```

如果 Provider 返回流式增量，会同时统计 `runtime.llm_delta` 数量。该命令不会打印 API Key。

### `TMA_CLI`

验收脚本使用的 CLI 路径。

默认：

```env
TMA_CLI=bin/tma
```

### `TMA_VERIFY_MESSAGE`

`scripts/verify_agent_runtime.sh` 发送的测试消息。

默认：

```env
TMA_VERIFY_MESSAGE=agent runtime verify
```

### `TMA_VERIFY_EXPECTED_TEXT`

`scripts/verify_agent_runtime.sh` 期望的 `agent.message` 文本。

默认：

```env
TMA_VERIFY_EXPECTED_TEXT=Agent runtime received: agent runtime verify
```

### `TMA_VERIFY_EXPECTED_PROTOCOL`

`scripts/verify_agent_runtime.sh` 期望的 `agent.message.payload.protocol_version`。

默认：

```env
TMA_VERIFY_EXPECTED_PROTOCOL=tma.agent_runtime.demo.v1
```

如果临时调试时需要跳过协议版本断言，可以设为空字符串：

```bash
TMA_VERIFY_EXPECTED_PROTOCOL= make verify-agent-runtime
```

### `TMA_VERIFY_WAIT_SECONDS`

等待后台 `agent.message` 的秒数。

默认：

```env
TMA_VERIFY_WAIT_SECONDS=10
```

### `TMA_VERIFY_BASE_URL`

`scripts/verify_agent_runtime_full.sh` 启动临时服务后用于验收的 API 地址。

默认：

```env
TMA_VERIFY_BASE_URL=http://localhost:18080
```

### `TMA_VERIFY_HTTP_ADDR`

`scripts/verify_agent_runtime_full.sh` 启动临时服务时使用的监听地址。

默认：

```env
TMA_VERIFY_HTTP_ADDR=:18080
```

### `TMA_SERVER_BIN`

自启动验收脚本使用的 server 二进制。

默认：

```env
TMA_SERVER_BIN=bin/tma-server
```

### `TMA_VERIFY_SERVER_LOG`

自启动验收脚本写入的 server 日志文件。

默认：

```env
TMA_VERIFY_SERVER_LOG=.verify-agent-runtime-server.log
```

### `TMA_VERIFY_SERVER_WAIT_SECONDS`

自启动验收脚本等待临时 server `/health` 成功的秒数。

默认：

```env
TMA_VERIFY_SERVER_WAIT_SECONDS=20
```

## Common Scenarios

### 本地 AgentRuntime 开发

```env
TMA_HTTP_ADDR=:8080
TMA_DATABASE_URL=postgres://tma:tma@localhost:5432/tma?sslmode=disable
TMA_TURN_QUEUE_SIZE=16
TMA_TURN_WORKER_COUNT=10
TMA_TURN_POLL_INTERVAL_MS=500
TMA_TURN_LEASE_DURATION_MS=10000
TMA_TURN_HEARTBEAT_INTERVAL_MS=1000
TMA_TURN_TIMEOUT_MS=3600000
```

启动：

```bash
make run
```

### 临时覆盖 turn 超时

```bash
TMA_TURN_TIMEOUT_MS=3600000 \
make run
```

### Postgres 集成测试

```bash
make db-up
make migrate-up
make test-postgres
```

### AgentRuntime 完整自启动验收

```bash
make verify-agent-runtime-full
```
