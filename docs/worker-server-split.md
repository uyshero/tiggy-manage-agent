# TMA Server / Worker Split

本文档记录 `tma-server` 与 `tma-worker` 的职责边界，以及这块后续要实现的内容。

## 目标

- `tma-server` 负责控制面：Session、Event、调度、审计、SSE。
- `tma-server` 负责 Approval API 和权限决策入口。
- `tma-worker` 负责执行面：tools、sandbox、objectstore 写入。
- `tma` CLI 负责手工操作和调试，不承担常驻执行。
- 未来 SDK 只做 API 封装，不直接承担执行。
- `tma-worker` 不暴露 inbound 端口，只主动消费 `tma-server` 的 HTTP API。

## 当前状态

当前默认工具 runtime 是 `cloud_sandbox`，底层落到 `OnlyboxesProvider`。`local_system` 不再被假定天然存在：真实部署里它必须由同 workspace 的在线 `tma-worker` 提供；只有显式设置 `TMA_ALLOW_SERVER_LOCAL_SYSTEM=true` 时，才允许 server 进程在受信任开发环境里直接使用 `LocalSystemProvider`。

这里需要把 namespace 和 runtime 分开：namespace 表达能力域，runtime 表达这次工具调用实际在哪里执行。第一版显式 runtime 只保留 `auto`、`cloud_sandbox`、`local_system`；server 是默认内置承载面，不作为 runtime 暴露。详细标准见 [tool-runtime-standard.md](./tool-runtime-standard.md)。

已经开始抽的 seam：

- `internal/execution.ProviderResolver`
- `runner.AgentRuntimeTurnExecutor.ProviderResolver`
- `httpapi.Server.executionResolver`

这意味着后续可以把 provider 从 server 进程切到独立 worker，而不需要再改大部分 runtime 逻辑。

Worker registry 已开始落地：

```text
POST /v1/workers
GET  /v1/workers
POST /v1/workers/diagnose
POST /v1/workers/reap-expired
GET  /v1/workers/{worker_id}
POST /v1/workers/{worker_id}/heartbeat
POST /v1/workers/{worker_id}/archive
```

这些接口由 `tma-server` 写入数据库。`tma-worker` 只调用 HTTP API，不直连 Postgres。
配置 token 后，`GET /v1/workers*` 和 `reap-expired` 属于控制面，只接受 control token；`diagnose` / `archive` 需要 worker token 或 control token。

`cmd/tma-worker` 已有最小常驻入口：启动后注册 worker、发送 worker heartbeat、轮询 work、ack work、执行期间发送 running work heartbeat，并把结果提交回 server。`tool_execution` work 必须使用 `tma.work.v1` 标准 payload；当前 worker 支持 `default.*`，并通过 `tools.DefaultRuntime + LocalSystemProvider` 在运行 `tma-worker` 的机器上执行。AgentRuntime 在 `local_system` 且存在同 workspace 匹配 worker 时，会通过 `execution.WorkerBackedProvider` 把工具调用桥接成 `tool_execution` work 并等待 worker result；没有 worker 时默认隐藏 `local_system` 工具，只有受信任开发环境显式开启 `TMA_ALLOW_SERVER_LOCAL_SYSTEM=true` 才允许 server 进程内 fallback。`sandbox_command` 仍作为调试/兼容 work type 保留，会通过 worker 侧 `LocalSystemProvider.RunCommand` 执行。`tma-worker` 默认一次执行 1 条 work，也可以通过 `--concurrency N` / `TMA_WORKER_CONCURRENCY=N` 同时 lease/execute 多条队列 job；长任务执行期间会按 `--work-heartbeat-interval` / `TMA_WORKER_WORK_HEARTBEAT_INTERVAL` 续租当前 work。收到 SIGINT / SIGTERM 时，worker 会停止 poll，把 worker 状态 heartbeat 为 `draining`，并在 `--shutdown-timeout` / `TMA_WORKER_SHUTDOWN_TIMEOUT` 内等待 running work 完成。

Worker work 协议也已经先落了一层最小实现：

```text
POST /v1/worker-work
GET  /v1/workers/{worker_id}/work/poll
POST /v1/workers/{worker_id}/work/{work_id}/ack
POST /v1/workers/{worker_id}/work/{work_id}/heartbeat
POST /v1/workers/{worker_id}/work/{work_id}/result
```

## Work 标准

Work 的标准语义应该是一次 **tool / capability invocation**：

```json
{
  "protocol_version": "tma.work.v1",
  "namespace": "default",
  "api": "run_command",
  "capabilities": ["exec"],
  "risk": "exec",
  "runtime": "cloud_sandbox",
  "input": {
    "command": "sh",
    "args": ["-c", "printf hello"]
  }
}
```

这层标准只表达：

- 要调用哪个 namespace / API。
- 需要哪些能力。
- 输入 payload 是什么。
- 风险等级和审批需求是什么。
- result / artifact refs 如何回传。

新增能力时优先扩展 `namespace.api`，不要扩展 `work_type`。`work_type` 只表达队列 job 的生命周期语义，例如 `tool_execution`、`sandbox_command`、`artifact_sync`；业务动作如 `computer.click`、`robot.move_to`、`office.excel_export` 都应该是 `tool_execution` 的工具调用。这样 `worker_work` 保持轻量稳定，具体能力通过 worker tool registry / plugin 扩展。

它可以表达 runtime 偏好，但不应该把 runtime 当成 tool namespace：

- `namespace` / `api` 表达要做什么。
- `capabilities` / `risk` 表达能力和风险。
- `runtime` 表达 `auto` / `cloud_sandbox` / `local_system`。
- 最终由 server 根据 worker registry、server builtin tools、policy 和审批状态选择运行位置。

Worker registry 里的 `capabilities` 应该描述 worker 当前能提供的 tool / capability 集合，例如：

```json
{
  "namespaces": ["default", "browser"],
  "apis": ["default.read_file", "default.write_file", "default.run_command", "browser.screenshot"],
  "runtimes": ["local_system"],
  "capabilities": ["filesystem.read", "filesystem.write", "exec", "browser.capture"],
  "constraints": {
    "network": "disabled"
  }
}
```

`tma-worker` 不应该手写一份与实际执行逻辑脱节的 capabilities。默认 worker 会从 `workruntime.Executor.WorkerCapabilities()` 导出注册和 heartbeat 使用的能力；默认 executor 会从 `tools` manifest 推导 `default.*` 的 local_system 能力。未来 browser、artifact 或 specialized runtime 可以通过自定义 executor 显式声明 `tools.WorkerCapabilities`。

当前第一版已支持进程型工具插件：`tma-worker --plugin /path/to/plugin` 会加载插件 `manifest`，注册为 worker 本地 tool runtime，并把插件声明的 namespace / API / capabilities 通过 worker heartbeat 上报。插件执行仍走标准 `tool_execution` work，不新增 work type。协议草案见 [tool-plugin-sdk.md](./tool-plugin-sdk.md)。

Server 调度按 workspace、agent config、tool policy、worker capabilities、runtime 和审批状态匹配。当前 `POST /v1/worker-work` 在 `tool_execution` 未指定 `worker_id` 时，已经会按在线 worker 的 `namespaces` / `apis` / `runtimes` / `capabilities` 做第一版匹配并绑定 worker；找不到匹配 worker 会返回 `409`。`environment_id` 仍然作为 Session / artifact / resource 归属字段，不等同于 runtime kind。

## 需要实现的内容

### 1. Worker 身份

- 每个 worker 有稳定 `worker_id`
- worker 只归属一个 `workspace_id`
- worker 启动时注册，定期 heartbeat
- worker 失联要过期、可撤销
- registry 状态存 server 数据库，worker 不持有数据库凭证

### 2. 安全

- worker 主动连 server
- 请求要有鉴权
- 当前先支持两条 Bearer token 边界：
  - worker consumer: server-side `TMA_WORKER_AUTH_TOKEN` + worker-side `TMA_WORKER_TOKEN`
  - control plane enqueue/get: server-side `TMA_WORKER_CONTROL_AUTH_TOKEN` + CLI / 调度侧 `TMA_WORKER_CONTROL_TOKEN`
- 后续可升级为短期 token 或 mTLS
- 所有 work 下发和回传都要审计
- 默认禁止跨 workspace 调度

### 3. 调度

- server 根据 workspace、agent config、tool policy、capabilities、runtime、负载选 worker 或 server 内置实现
- ProviderResolver / 调度器接收 workspace / session / resource scope 上下文
- 第一版默认 runtime 是 `cloud_sandbox`；`local_system` 通过 worker capability 或显式开发 fallback 才存在，不急着上分布式队列
- 同一用户/同一 workspace 内多 worker 做负载分配
- 先支持静态选择，后支持健康检查和故障转移

### 4. 执行协议

- server 下发标准 work invocation
- worker 拉取 work
- worker 查本地 tool / capability registry 执行；server-only tool 不下发 worker
- worker 回传结构化 result、stdout / stderr 摘要和 artifact refs

### 5. SDK 边界

- Go Core SDK：包住 `tma-server` 的 `/v2` 用户与控制面 HTTP / SSE API，并提供 Run 高层状态机
- Worker SDK：方便实现 `tma-worker`
- SDK 不替代 worker，不直接执行命令

## 建议落地顺序

1. 先把 worker 认证和注册协议定下来。
2. 再做 tool/capability manifest 和 work invocation 标准。
3. 再做 worker registry 和 capability-based worker selection。
4. 在上述协议稳定后发布 Go Core SDK；当前 `sdk/tma` 已开始由 CLI 消费，Worker 机器协议仍保持独立。

## 边界规则

### Server 控制面

- 保存 Session / Event / Approval / Worker registry。
- 负责鉴权、RBAC、审批和审计。
- 负责选择 worker，但不直接读取 worker 本机路径。
- 只持有 object refs、artifact metadata、执行摘要和必要日志。
- 不把服务端数据库凭证下发给 worker。

### Worker 执行面

- 持有本机、sandbox、远程服务等 tool / capability 实现。
- 执行标准 work invocation。
- 将大文件、tool output、workspace snapshot 写入 objectstore。
- 通过 server API 回传 object refs / artifact refs / stdout stderr 摘要。
- 不直接写 Postgres，不绕过 server 做跨 workspace 操作。

### 不跨边界裸露

- 本机绝对路径不直接暴露给其他 workspace。
- artifact 下载必须经过 server 代理或权限校验后的 object ref。
- worker token 不进入 LLM 上下文。
- 数据库 DSN、对象存储全局密钥、LLM API key 不下发给 worker。
- tool input 和 stdout / stderr 进入事件前要走脱敏和大小限制。

## 验收清单

### Worker 可见性

- `POST /v1/workers` 能注册 worker。
- `GET /v1/workers` 能按 workspace / status 列出 worker，配置 token 后需要 control token。
- `POST /v1/workers/{id}/heartbeat` 能刷新 `last_seen_at` 和 `lease_expires_at`。
- `POST /v1/workers/{id}/archive` 能撤销 worker，配置 token 后需要 worker token 或 control token。
- `POST /v1/workers/reap-expired` 能把 lease 过期的 online worker 标记为 offline，配置 token 后需要 control token。
- `bin/tma worker register/list/heartbeat/archive/reap-expired` 可用。
- `bin/tma worker list` 能展示 worker 声明的 runtimes / APIs / capabilities。
- `POST /v1/workers/diagnose` 能按一次 tool invocation 解释每个在线 worker 是否匹配，以及缺少 runtime / API / capability / lease 等原因；配置 token 后需要 worker token 或 control token。
- `bin/tma worker diagnose --api ...` 调用 server 侧诊断接口并展示结果，不在 CLI 里复制 selector 逻辑。
- `POST /v1/worker-work` 能写入待执行 work。
- `tool_execution` 未指定 worker 时，server 能按 worker capabilities 自动选择匹配 worker；无匹配时返回带 diagnostics 的 `409`。
- `bin/tma work enqueue` 可用，能指定 workspace / worker / environment / session / turn / work_type / payload，也能通过 `--api` / `--capabilities` / `--risk` / `--runtime` / `--input` 生成标准 `tma.work.v1` invocation；server 返回 worker-selection `409` 时会展示诊断结果。
- `GET /v1/worker-work/{work_id}` 和 `bin/tma work get --work ...` 能查看 work 当前状态和 result，便于排查 worker-backed execution。
- `GET /v1/worker-work/{work_id}/diagnose` 和 `bin/tma work diagnose --work ...` 能诊断队列中单个 work/job 当前卡在 pending / leased / running / failed 的原因、assigned worker 状态、lease 状态和建议动作。
- `POST /v1/worker-work/reap-expired` 和 `bin/tma work reap-expired` 能把 lease 已过期的 leased/running work 标记为 failed；第一版不自动重新入队，避免重复执行有副作用的工具。
- `GET /v1/workers/{worker_id}/work/poll` 能返回待执行 work 或 `null`。
- `POST /v1/workers/{worker_id}/work/{work_id}/ack` 能把 work 标记为 running。
- `POST /v1/workers/{worker_id}/work/{work_id}/heartbeat` 能续租 work。
- `POST /v1/workers/{worker_id}/work/{work_id}/result` 能提交 work 结果。
- `bin/tma-worker` 能注册、worker 心跳、轮询、ack、running work 心跳和提交 result。
- `make verify-worker-work-heartbeat` 能证明真实 `tma-worker` 执行超过初始 lease 的长任务时会续租 running work，并且在 work reaper 开启时仍最终 completed。
- `bin/tma-worker` 收到 SIGINT / SIGTERM 后停止 poll，heartbeat `draining`，并在 shutdown timeout 内等待 running work 提交 result；超时后退出，由 work lease/reaper 兜底。
- `make verify-worker-shutdown-drain` 能证明真实 `tma-worker` 在 running work 期间收到 SIGTERM 后会 drain、完成 result 上报，再正常退出。
- `POST /v1/worker-work/{work_id}/cancel` 和 `bin/tma work cancel --work ...` 能从控制面取消 pending/leased/running work；worker 后续 running work heartbeat/result 会看到 `canceled`，不再覆盖成 completed。
- `make verify-worker-work-cancel` 能证明真实 `tma-worker` 在 running work 被取消后会停止本地执行、不提交 completed result，最终 work 保持 `canceled`。
- `POST /v1/worker-work/{work_id}/requeue` 和 `bin/tma work requeue --work ...` 能把 failed/canceled work 复制成新的 pending work；原 work 不改，第一版只做显式人工 requeue，不做自动 retry。
- `bin/tma-worker doctor` 能用 outbound API 检查 server health、register、heartbeat、poll、diagnose 和 archive，并展示 executor capabilities。
- `bin/tma-worker` 注册和 heartbeat 使用同一个 `workruntime.Executor` 导出的 capabilities；默认能力来自 tools manifest，自定义 executor 可以显式声明 capabilities。
- `tool_execution` work 必须通过 `tma.work.v1` 校验；坏 payload 不能入队。
- `tool_execution default.*` work 能通过 `tools.DefaultRuntime + LocalSystemProvider` 执行并返回 tool result。
- `sandbox_command` work 能通过 `LocalSystemProvider.RunCommand` 执行并返回 stdout / stderr / exit_code。
- `AgentRuntime local_system` 在存在匹配在线 worker 时会使用 worker-backed provider 入队执行 `default.*`，而不是直接在 server 进程内执行。
- `AgentRuntime local_system` 在没有匹配在线 worker 且未开启 `TMA_ALLOW_SERVER_LOCAL_SYSTEM` 时，会隐藏 `local_system` 工具，不暴露给模型。

### Tool / Capability 选择

- Work 标准表达 tool / api / capabilities / risk / input。
- `worker_work` 是第一版队列；一条 work 是一个队列 job/task。`tma-worker` 默认串行消费，一次 poll/ack/execute/result 一条 work；配置 `--concurrency N` 后同一 worker 可以同时 lease/execute 多条 work。每条 running work 在执行期间独立 heartbeat 续租，避免长任务被 reaper 误判为过期。
- Worker 注册表达自身 tools / capabilities / constraints。
- AgentRuntime 发给模型的工具集会先按 Agent 配置和当前 provider / worker 可提供能力过滤，避免模型选择当前执行面无法提供的工具。
- Server 根据 agent config、tool policy、runtime、审批状态和 worker capabilities 选择 server 内置、cloud_sandbox 或 local_system 的某个实现。
- `LocalSystemProvider` 是本机工具能力实现，不新设 `localProvider` 概念；它通常运行在 `tma-worker` 进程内，server-local fallback 只给显式开启的受信任开发环境。
- `auto`、`cloud_sandbox`、`local_system` 是第一版显式 runtime，不是 namespace；server 内置能力无需显式 runtime。

### 安全

- worker 不直连数据库。
- worker 不暴露端口，只能主动消费 server。
- worker 注册、heartbeat、poll、ack、result 在 server 配置 `TMA_WORKER_AUTH_TOKEN` 后必须带 Bearer token。
- `POST /v1/worker-work`、`GET /v1/worker-work/{work_id}`、`GET /v1/worker-work/{work_id}/diagnose`、`POST /v1/worker-work/reap-expired` 在 server 配置 `TMA_WORKER_CONTROL_AUTH_TOKEN` 后必须带独立 Bearer token。
- worker token 不进入 LLM 上下文、不进入 event payload。
- 默认禁止跨 workspace worker 调度。
- approval 决策仍在 server 控制面。

### Artifact

- worker 产生的大文件不进事件 payload。
- artifact 通过 object refs 回传。
- 下载仍走 server 权限判断。
- worker-backed `output_paths` 小文件可以通过 work result 携带内容回传，单文件内联上限为 8 MiB；更大的产物必须由 worker 主动上传到 server artifact API，work result 只回传 artifact ref。
