# Tool / Runtime Standard

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
| `open` | `browser.open` | `read` | `cloud_sandbox` | 待实现 |
| `navigate` | `browser.navigate` | `read` | `cloud_sandbox` | 待实现 |
| `read` | `browser.read` | `read` | `cloud_sandbox` | 待实现 |
| `click` | `browser.read`, `browser.interact` | `write` | `cloud_sandbox` | 待实现 |
| `type` | `browser.read`, `browser.interact` | `write` | `cloud_sandbox` | 待实现 |
| `screenshot` | `browser.read`, `browser.capture` | `read` | `cloud_sandbox` | 待实现 |
| `download` | `browser.download`, `artifact.write` | `write` | `cloud_sandbox` | 待实现 |
| `upload_file` | `browser.upload`, `filesystem.read` | `write` | `cloud_sandbox` | 待实现 |
| `network_log` | `browser.network` | `read` | `cloud_sandbox` | 待实现 |

### `agent.*`

Agent 能力域属于 server 控制面，默认不下发 worker。

| API | capabilities | risk | runtime 默认 | 第一版实现 |
|---|---|---|---|---|
| `create_session` | `agent.session.write` | `write` | server 内置 | 已有 server API |
| `get_session` | `agent.session.read` | `read` | server 内置 | 已有 server API |
| `send_message` | `agent.message.write` | `write` | server 内置 | 已有 events API |
| `list_events` | `agent.event.read` | `read` | server 内置 | 已有 server API |
| `stream_events` | `agent.event.stream` | `read` | server 内置 | 已有 SSE |
| `approve_tool` | `agent.approval.write` | `write` | server 内置 | 已有 server API |
| `reject_tool` | `agent.approval.write` | `write` | server 内置 | 已有 server API |
| `archive_session` | `agent.session.write` | `write` | server 内置 | 已有 server API |

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
