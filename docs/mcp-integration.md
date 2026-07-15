# MCP Integration

本文档描述 TMA 当前这版 MCP（Model Context Protocol）接入的开发约定、配置格式和测试方式。

## 当前边界

当前已落地的是 **Agent 级 MCP server 接入**：

- `AgentConfigVersion` 新增一等 `mcp` 字段，并贯穿 HTTP API、CLI、store 和运行时配置解析。
- Runtime 会在 turn 开始前读取 Agent 绑定的 MCP servers，通过 `stdio` 或 `streamable_http` 执行 `initialize` + `tools/list`，把 MCP tools 暴露成标准 TMA model tools；如果 server 不支持 `tools/list`，但显式开启了 `expose.resources` 或 `expose.prompts`，则可作为 resource-only / prompt-only 的只读上下文工具源加载。
- TMA Server 内置 session-scoped stdio host：同一 Session、Agent config version 和 MCP server 会复用一个已 initialize 的子进程，跨 Turn 保留 MCP 进程状态；不同 Session 默认隔离。
- MCP tool 调用走现有 `tools.Registry` / `tool result` 链路，结果会被整理成 TMA 统一 `ExecutionResult`。
- MCP 客户端层已支持只读 `resources/list` / `resources/templates/list` / `resources/read`、`prompts/list` / `prompts/get` 和 `completion/complete`；默认不会把 resources、prompts 或 completion 暴露成模型可调用工具，但可通过 `expose.resources` / `expose.prompts` 显式注册只读 catalog/content 桥接工具。
- Agent tooling health 会在工具加载成功后额外探测 `resources/list` / `resources/templates/list` / `prompts/list`，返回 `resource_count` / `resource_template_count` / `prompt_count` 作为可观测信息；探测失败只进入诊断，不会把已成功加载 tools 的 MCP server 判为离线。
- Workbench 的 MCP / Skills 健康检查页会展示 MCP transport、延迟、tool count、resource count、resource template count、prompt count 和 initialize capabilities，便于直接在 UI 里判断 server 暴露面。
- Workbench 的 Agent 配置编辑器提供“资源工具”和“Prompt 工具”开关，对应 `expose.resources` / `expose.prompts`。
- Workspace MCP 注册表集中维护可复用 server，并为每次配置发布保存不可变版本；Agent config 只保存固定版本 binding，不复制中央配置。
- Agent 发布配置时，binding 的 `version: 0` 会固定为当时的注册表当前版本。后续中央升级不会改变已有 Agent config 或 Session，Workbench 会显式提供版本升级操作。
- 注册表服务可以停用或归档。停用是即时 kill switch，所有固定版本在解析时都会失败；仍被当前 Agent 使用的服务不能归档。
- MCP 编辑器采用响应式 grid：超宽屏保持紧凑单行，常规桌面拆分字段与开关，移动端使用完整单列，避免 transport、URL、logging 和 SSE 控件造成页面横向溢出。
- Streamable HTTP 支持显式配置 OAuth `client_credentials` token flow，运行时会解析 `client_id` / `client_secret` 引用、向 token endpoint 换取 Bearer token，并在进程内按 `expires_in` 缓存后注入 MCP 请求的 `Authorization` header。
- Remote MCP 的主请求、SSE listener、OAuth discovery 和 token endpoint 统一经过 Server egress policy；默认只允许 HTTPS 公共地址，并防护私网/metadata SSRF、DNS 混合解析、DNS rebinding 和跨 authority redirect。
- Workbench 与 Inspector 会根据运行时事件里的 `tool_source=mcp` 显示明确的 `MCP` 标识；Inspector Timeline / Recent Events 还会展示 MCP transport、protocol version、capabilities、tool count、OAuth、SSE listener 和 context expose 等非敏感诊断 badge。
- Inspector `MCP Protocol` 会按 call ID 配对 tool call/result，展示脱敏的方法、request/response seq、状态、耗时、协议事实和结果摘要；arguments、endpoint、headers、error message 与 result body 不进入该投影。
- MCP 标准 stdio Server 使用 newline-delimited JSON-RPC（`stdio_framing=json_lines`），Workbench 新建配置会显式写入该值。为保持历史 Agent/Registry 版本不变，旧配置省略字段时仍按 `content_length` 解释。真实第三方基线见 [MCP Server 兼容性矩阵](./mcp-server-compatibility.md)。

当前还 **没有** 落地的部分：

- worker 侧托管长驻 MCP server；
- 独立 Vault / JIT token 托管服务；
- Streamable HTTP 的浏览器授权、动态客户端注册、授权码 token exchange / 用户级 refresh token 托管；当前按产品决策暂缓，仅在出现个人 GitHub、Google Drive、Notion 等账号连接需求时启动；
- sampling / elicitation 等真实 client capability；
- resource subscription（`resources/subscribe` / `resources/unsubscribe` / `notifications/resources/updated`）；stdio 需要空闲期独立读循环和可消费的更新事件接口，不能用仅在下一次请求时顺带读取通知的半实现替代；
- gRPC sidecar adapter；
- 原始 JSON-RPC payload 的持久化、回放和 Inspector 展开视图；当前只提供脱敏协议投影，避免记录 endpoint、认证信息和远端内容正文。

因此，这一版更准确的定位是：**先把 MCP 作为一等能力源接进 AgentRuntime 和工具系统**。

Secrets 继续使用现有环境变量管理和运行时注入方案：配置只保存 `env_ref` / `secret_ref`，使用时按 Workspace/Session 解析，不开发独立 Vault/JIT token 服务。

## Workspace MCP 注册表

中央注册表用于复用团队 MCP 配置，数据由 `000048_mcp_registry.sql` 持久化，并由 `000052_mcp_registry_rls.sql` 对 server/version 两张表启用 `FORCE ROW LEVEL SECURITY`。每个 workspace server 包含稳定的 `server_id` / `identifier`、状态、当前版本和使用中的 Agent 数；每次创建或更新配置都会追加不可变版本，并记录 canonical config 的 SHA-256 checksum。创建、更新、启用、停用、归档和连通性测试都会写入 operator audit。

Agent config 使用 binding 引用中央版本：

```json
{
  "bindings": [
    {
      "server_id": "mcps_000001",
      "version": 1,
      "identifier": "filesystem"
    }
  ],
  "servers": []
}
```

规则如下：

- `version: 0` 只允许作为发布请求的“使用当前版本”语义；落库前会固定为大于 0 的具体版本。
- `identifier` 可选；提供时只覆盖该 Agent 内的工具 namespace，不修改中央版本。
- Session 继续固定到自己的 `agent_config_version`，因此中央发布 v2 后，绑定 v1 的 Agent 和已有 Session 仍解析 v1。
- 恢复历史版本不会覆盖数据：服务端锁定 Registry server，把选中版本复制为新的不可变当前版本。已有 Agent binding 和 Session 仍保持原固定版本。
- 注册表 binding 和旧的内嵌 `servers` 可以并存，但解析后的 identifier 不能重复。
- 服务必须属于同一 workspace 且状态为 `active`。停用后，新的配置发布、runtime 解析和 tooling health 都会失败，以提供即时 kill switch。
- 中央配置中的敏感 header literal 会被拒绝，必须改用 `env_ref` 或 `secret_ref`；数据库和 API 只保存引用。
- Registry PostgreSQL 读写全部在事务内设置 `tma.workspace_id`。受限 runtime role 无 scope 时看不到任何 server/version，跨 workspace 按 ID 查询也不会先暴露资源归属。
- 旧 `mcp.servers` / `mcpServers` 仍完整兼容，适合 Agent 专用配置和渐进迁移。

Workbench 的 `设置 > MCP` 提供列表、新建、发布版本、启停、测试、归档和版本历史恢复；每个历史版本可查看 checksum 与 canonical config，恢复前必须二次确认。`设置 > Agent` 可绑定中央服务，并在绑定旧版本时显示显式升级按钮。UI 不会因中央发布或恢复自动重写 Agent config。

## Agent Config

`mcp` 推荐使用对象格式，支持 `servers` 或 `mcpServers`。`env` 里既可以直接写字符串，也可以写引用对象：

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/project"],
      "stdio_framing": "json_lines"
    },
    "fetch": {
      "command": "uvx",
      "args": ["mcp-server-fetch"],
      "stdio_framing": "json_lines",
      "env": {
        "FETCH_USER_AGENT": "tiggy-manage-agent",
        "API_TOKEN": {
          "env_ref": "TMA_MCP_FETCH_TOKEN"
        }
      }
    },
    "remote_search": {
      "transport": "streamable_http",
      "url": "https://mcp.example.test/mcp",
      "listen": true,
      "roots": [
        {
          "path": "/workspace/project",
          "name": "Project"
        }
      ],
      "headers": {
        "Authorization": {
          "env_ref": "TMA_MCP_REMOTE_AUTH"
        }
      }
    },
    "secure_remote": {
      "transport": "streamable_http",
      "url": "https://secure-mcp.example.test/mcp",
      "oauth": {
        "grant_type": "client_credentials",
        "token_url": "https://auth.example.test/oauth/token",
        "client_id": {
          "env_ref": "TMA_MCP_CLIENT_ID"
        },
        "client_secret": {
          "secret_ref": "env:TMA_MCP_CLIENT_SECRET"
        },
        "scopes": ["mcp.read"],
        "resource": "https://secure-mcp.example.test/mcp",
        "token_endpoint_auth_method": "client_secret_post"
      }
    }
  }
}
```

服务端会把它归一化为：

```json
{
  "servers": [
    {
      "identifier": "fetch",
      "command": "uvx",
      "args": ["mcp-server-fetch"],
      "env": {
        "FETCH_USER_AGENT": "tiggy-manage-agent",
        "API_TOKEN": {
          "env_ref": "TMA_MCP_FETCH_TOKEN"
        }
      },
      "transport": "stdio"
    },
    {
      "identifier": "filesystem",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/project"],
      "stdio_framing": "json_lines",
      "transport": "stdio"
    },
    {
      "identifier": "remote_search",
      "url": "https://mcp.example.test/mcp",
      "listen": true,
      "roots": [
        {
          "uri": "file:///workspace/project",
          "name": "Project"
        }
      ],
      "headers": {
        "Authorization": {
          "env_ref": "TMA_MCP_REMOTE_AUTH"
        }
      },
      "transport": "streamable_http"
    },
    {
      "identifier": "secure_remote",
      "url": "https://secure-mcp.example.test/mcp",
      "oauth": {
        "grant_type": "client_credentials",
        "token_url": "https://auth.example.test/oauth/token",
        "client_id": {
          "env_ref": "TMA_MCP_CLIENT_ID"
        },
        "client_secret": {
          "secret_ref": "env:TMA_MCP_CLIENT_SECRET"
        },
        "scopes": ["mcp.read"],
        "resource": "https://secure-mcp.example.test/mcp",
        "token_endpoint_auth_method": "client_secret_post"
      },
      "transport": "streamable_http"
    }
  ]
}
```

支持字段：

| 字段 | 含义 |
|---|---|
| `identifier` / `id` / `name` | MCP server 的 namespace；省略时会从 map key 推导 |
| `command` | `stdio` 启动命令，`stdio` 必填 |
| `args` | `stdio` 命令参数 |
| `env` | `stdio` 额外环境变量 |
| `cwd` | `stdio` 工作目录 |
| `stdio_framing` | `stdio` 消息 framing：官方 Server 使用 `json_lines`；历史省略值和旧 Server 使用 `content_length` |
| `url` | `streamable_http` endpoint，`streamable_http` 必填 |
| `headers` | `streamable_http` 请求头，支持凭据引用 |
| `oauth` | `streamable_http` OAuth client credentials 配置；与 `headers.Authorization` 互斥 |
| `listen` | `streamable_http` 是否启动可选 GET SSE listener |
| `roots` | `roots/list` 返回的 roots；支持 `uri` 或输入便利项 `path` |
| `sampling` | `sampling/createMessage` client capability 策略配置；当前只支持安全拒绝边界 |
| `elicitation` | `elicitation/create` client capability 策略配置；当前只支持安全拒绝边界 |
| `logging.level` | 服务端声明 logging capability 后通过 `logging/setLevel` 设置最低日志级别 |
| `runtime` | 单个 MCP Server 版本的超时、并发上限和熔断策略；省略字段时使用 Server 安全默认值 |
| `expose.resources` | 显式把 `resources/list` / `resources/read` 注册为只读 Agent tools；默认关闭 |
| `expose.prompts` | 显式把 `prompts/list` / `prompts/get` 注册为只读 Agent tools；默认关闭 |
| `title` / `description` | 覆盖默认展示文案 |
| `include_tools` | 只暴露指定 MCP tool 名称 |
| `exclude_tools` | 排除指定 MCP tool 名称 |
| `transport` | 当前支持 `stdio`、`streamable_http` |

`env` 和 `headers` 的值支持三种形式：

- 字符串：直接作为 literal 写入 MCP 子进程环境或 HTTP header。
- `{ "env_ref": "NAME" }`：从当前进程环境读取 `NAME`。
- `{ "secret_ref": "env:NAME" }`：`env_ref` 的兼容别名。

当前不引入独立 Vault/JIT 服务，“凭据引用”继续由环境变量或现有环境变量管理模块维护。Runtime 使用时按 Workspace/Session 解析并注入；`mcp` 配置、数据库和 API 响应都只保存引用，不保存真实 secret。

`oauth` 当前支持 `streamable_http` 的显式 client credentials 和 refresh token：

- `grant_type`：支持 `client_credentials` 与 `refresh_token`；省略时默认为 `client_credentials`。
- `token_url`：OAuth token endpoint，必须是 `http` 或 `https`。
- `client_id` / `client_secret`：必填。`client_id` 支持 literal、`env_ref`、`secret_ref: "env:NAME"`；`client_secret` 必须使用 `env_ref` 或 `secret_ref: "env:NAME"`，避免真实 secret 落库或随 API 响应返回。
- `refresh_token`：`grant_type=refresh_token` 时必填，且同样必须使用 `env_ref` 或 `secret_ref: "env:NAME"`。
- `scopes`：可选，会以空格拼接成 token 请求里的 `scope`。
- `audience` / `resource`：可选，会透传给 token endpoint。
- `token_endpoint_auth_method`：支持 `client_secret_post`（默认）和 `client_secret_basic`。

配置了 `oauth` 后不能再配置 `headers.Authorization`。运行时会在建立 Streamable HTTP 短会话前请求 token，并把 `Authorization: Bearer <access_token>` 注入 MCP 请求；`client_secret` 和 `refresh_token` 只保存引用，token 不会持久化、不写入数据库/API 响应，错误也不会回显 token endpoint response body，避免泄漏 `client_secret`、`refresh_token` 或 `access_token`。带 `expires_in` 的 token 会缓存在当前进程内，并在到期前留出刷新余量；没有 `expires_in` 的 token 不缓存。企业服务账号场景继续使用现有 `client_credentials` / 显式 `refresh_token`；浏览器授权、动态客户端注册、授权码回调和用户级 token 托管按产品决策暂缓，仅在出现个人账号连接需求时开发。

`sampling` 支持：

- 省略或 `{ "enabled": false }`：listener 收到 `sampling/createMessage` 时返回 `-32000` 策略错误，明确提示该 server 未启用远端采样。
- `{ "enabled": true }`：配置会被保留并传入 runtime，但当前仍不会调用主 LLM；listener 会返回 `-32000`，提示需要后续接入带审计的 sampler backend。

`elicitation` 支持：

- 省略或 `{ "enabled": false }`：listener 收到 `elicitation/create` 时返回 `-32000` 策略错误，明确提示该 server 未启用远端用户交互。
- `{ "enabled": true }`：配置会被保留并传入 runtime，但当前仍不会弹出 UI、创建审批或向用户提问；listener 会返回 `-32000`，提示需要后续接入带审计的 interaction backend。

`logging` 与 notification 支持：

- `logging.level` 接受 `debug`、`info`、`notice`、`warning`、`error`、`critical`、`alert`、`emergency`。initialize 完成后，client 只在 server 声明 logging capability 时发送 `logging/setLevel`；配置了 level 但 server 未声明 capability 会明确失败。
- stdio 响应流、HTTP POST SSE 和长驻 GET SSE listener 都会识别 `notifications/progress` 与 `notifications/message`。progress notification 必须包含字符串或数字 `progressToken` 以及数字 `progress`；可选 `total` 存在时也必须是数字，否则只累计为非法通知。
- Host 只累计 progress 数量、日志数量、规范化 level 分布和非法通知数量。远程 `data`、logger 名称、progress token、message 和进度值不会写入数据库、tool result、应用日志或 Prometheus label，避免 secret 泄漏和高基数。

`runtime` 生产保护配置：

```json
{
  "runtime": {
    "timeout_seconds": 30,
    "max_concurrency": 4,
    "failure_threshold": 5,
    "cooldown_seconds": 30
  }
}
```

- `timeout_seconds`：一次 list/read/get/call 的总 deadline，包含等待并发槽的时间；默认 `30`，范围 `1..600`。
- `max_concurrency`：同一 Workspace + Registry server + 固定版本在当前 Server 进程内允许的并发请求数；默认 `4`，范围 `1..64`。
- `failure_threshold`：连续失败达到该值后打开熔断；默认 `5`，范围 `1..100`。
- `cooldown_seconds`：熔断打开后的冷却时间；默认 `30`，范围 `1..3600`。
- Registry v1/v2 使用独立预算和熔断状态，避免故障旧版本阻断已修复的新版本；不同 Session 共享同一版本预算，不能通过新建 Session 绕过保护。旧内嵌 server 按 Workspace + identifier 隔离。
- 冷却结束后只允许一个 half-open 探测；探测成功才清零连续失败并关闭熔断，失败会重新进入冷却。
- 连续失败按 `catalog` 与 `operation` 两个失败域统计。`tools/list`、`resources/list`、`resources/templates/list`、`prompts/list` 属于 catalog；`tools/call`、`resources/read`、`prompts/get`、`completion/complete` 属于 operation。只有同域成功才清零该域失败，因此目录正常不会掩盖持续超时的工具调用；half-open 探测成功会完整关闭熔断。
- 调用方主动取消分类为 `canceled`，会被统计但不会增加连续失败；`timeout`、`authentication`、`rate_limited`、`transport`、`protocol`、`unavailable` 和 `unknown` 会计入熔断。
- `tools/call`、resources、prompts 和目录加载都只调用一次。RuntimeGuard 不自动重放任何请求，尤其不会重放可能有副作用的 `tools/call`。
- 失败的 `runtime.tool_result.error.type` 使用 `mcp_timeout`、`mcp_transport`、`mcp_circuit_open` 等稳定类型；用户可见消息是固定脱敏文案，不包含 endpoint、header、arguments 或远端响应正文。
- Agent tooling health 顶层 `mcp_runtime_guard` 返回 tracked server/version、in-flight、open circuit、调用/失败/拒绝计数及有限失败分类。Prometheus 使用 `tma_mcp_runtime_guard_*`，不把 workspace、server ID、URL 或工具名放入 label。
- `GET /v1/mcp-servers/runtime-status` 提供 Workspace 范围的逐 Registry server/version 脱敏投影。Workbench `设置 > MCP` 列表展示该 server 最严重的版本状态，详情展示每个活跃版本的并发、连续失败、最近失败分类和冷却剩余时间。
- RuntimeGuard 状态只属于当前 Server 进程，不持久化。进程重启后 Workbench 显示“未运行”；首次目录加载或工具调用后出现 `正常` / `并发已满` / `已熔断` / `恢复探测`。

保留规则：

- `default`、`artifact`、`browser`、`agent`、`skills`、`web` 这些 namespace 不能作为 MCP server identifier。
- MCP tool 名会被规范化成可暴露给模型的 `namespace.api` 形式，例如 `readFile` 会变成 `filesystem.read_file`。
- `tools` 仍然是最终暴露策略。也就是说：**先加载 MCP tools，再套用 Agent 的 tools 过滤规则**。

## CLI

更新 Agent config 时可以直接传 `--mcp`：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --mcp '{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp/project"],"stdio_framing":"json_lines"}}}'
```

远程 Streamable HTTP MCP server 示例：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --mcp '{"mcpServers":{"remote_search":{"transport":"streamable_http","url":"https://mcp.example.test/mcp","listen":true,"headers":{"Authorization":{"env_ref":"TMA_MCP_REMOTE_AUTH"}}}}}'
```

OAuth client credentials 远程 server 示例：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --mcp '{"mcpServers":{"secure_remote":{"transport":"streamable_http","url":"https://secure-mcp.example.test/mcp","oauth":{"grant_type":"client_credentials","token_url":"https://auth.example.test/oauth/token","client_id":{"env_ref":"TMA_MCP_CLIENT_ID"},"client_secret":{"secret_ref":"env:TMA_MCP_CLIENT_SECRET"},"scopes":["mcp.read"],"token_endpoint_auth_method":"client_secret_post"}}}}'
```

如果要同时限制暴露的工具面，可以配合 `--tools`：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --tools '{"tools":["filesystem"],"runtime":"auto"}' \
  --mcp '{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp/project"],"stdio_framing":"json_lines"}}}'
```

## Runtime 行为

每个 MCP server 会被适配成一个动态 `tools.Manifest`：

- manifest identifier = MCP server identifier；
- manifest api = MCP `tools/list` 返回的 tools，以及显式 `expose.resources` / `expose.prompts` 注册的只读桥接工具；
- manifest metadata 会记录 `mcp_transport`、`mcp_protocol_version`、`mcp_capabilities`、`mcp_tool_count`、`mcp_timeout_seconds`、`mcp_max_concurrency`、`mcp_failure_threshold`、`mcp_cooldown_seconds`、`mcp_oauth`、`mcp_listen`、`mcp_expose_resources`、`mcp_expose_prompts` 等非敏感诊断字段；
- implementation = `server_builtin`；
- tool result 继续复用标准 `tma.tool_result.v1` 观察链路。

事件可观测性：

- `runtime.tool_call`、`runtime.tool_result` 和审批相关事件会继承 manifest metadata 中的 `mcp_*` 字段。
- 这些字段只描述 transport、capabilities 和开关状态，不包含 URL、headers、client secret、access token 或原始 OAuth response body。
- Inspector 的 Timeline / Recent Events 会把这些字段渲染为 MCP 诊断 badge，并在 `runtime.tool_result` 上额外展示 MCP result/context 摘要卡。
- 摘要卡只展示 tool name、is_error、content item count/type、resource/prompt/message count、mime type、structured/meta 是否存在等低敏信息；不会展开 MCP content text、resource body、prompt message text、structured_content 值、URL、headers、client secret 或 access token。
- Raw Export 和原始 Recent Events JSON 仍保留后端返回的事件 payload，供受控环境下排障。

返回值整理规则：

- 文本内容优先合并到 `ExecutionResult.Content`；
- 原始结构化内容会整理进 `ExecutionResult.State`；
- 二进制 image content 当前只保留 metadata，不直接塞大 base64 到模型上下文。
- `ExecutionResult.State.protocol_version` 使用 `tma.mcp_result.v1`；显式 expose 的 resources/prompts 桥接工具使用 `tma.mcp_context_result.v1`。

Server 侧 stdio host：

- `LoadMCPRuntime` 会在加载 manifest 时解析 env/header/OAuth 配置，并把得到的 MCP client snapshot 挂到 `MCPRuntime`。
- 后续同一个 `MCPRuntime.Execute` 会复用这个已解析 client snapshot，不会在同一 runtime 生命周期内反复解析 `env_ref` / `secret_ref` 或重建 OAuth cache。
- 这保证同一 Turn / runtime 中 MCP 工具看到的是一致配置；如果宿主环境变量或 secret 发生轮换，需要重新加载 Agent config / runtime 后才会生效。
- AgentRuntime 使用 TMA Server 进程内的 `StdioHost`。host scope 包含 workspace、session、Agent、Agent config version 和 MCP server identifier；resolved command、args、env、cwd、roots 和 client policy 也会进入不可逆配置指纹，因此不同 Session、配置版本或凭据快照不会共享进程。
- 同一 host entry 只执行一次 `initialize`，后续 `tools/list`、`tools/call`、resources 和 prompts 请求复用该进程；同一进程上的请求串行执行，不同 scope 可以并行。
- 空闲 entry 默认保留 10 分钟、每分钟扫描一次，最多保留 64 个；分别由 `TMA_MCP_STDIO_HOST_IDLE_TIMEOUT_SECONDS`、`TMA_MCP_STDIO_HOST_SWEEP_INTERVAL_SECONDS`、`TMA_MCP_STDIO_HOST_MAX_SESSIONS` 配置。达到容量时优先淘汰最旧空闲 entry；全部忙碌时拒绝新 scope。TMA Server 关闭时会先停止 Turn runner，再关闭全部 MCP stdin，并在子进程不退出时兜底终止。
- stdio host 会继承调用方 `context.Context`；当工具调用或列表操作因 deadline / cancel 中断时，client 会先向 server 发送 `notifications/cancelled`（包含 in-flight `requestId` 和 context reason），再废弃对应进程，并返回标准 `context deadline exceeded` / `context canceled`。下一次请求会启动并 initialize 新进程，不会复用可能已失步的协议流。
- JSON-RPC 方法级错误不会销毁健康进程；pipe、解码、启动等 transport 错误会废弃进程。host 不自动重放失败的 `tools/call`，避免重复执行有副作用的工具。
- Agent tooling health 仍使用独立短会话探测，不进入业务 Session 的长驻进程，避免健康检查改变 Session 内 MCP 状态。
- Agent tooling health 响应的 `mcp_host` 会返回当前 entry、使用中 entry、容量、生命周期配置和累计事件；`/metrics` 暴露 `tma_mcp_stdio_host_sessions`、`tma_mcp_stdio_host_in_use_sessions`、`tma_mcp_stdio_host_max_sessions`、`tma_mcp_stdio_host_idle_timeout_seconds`、`tma_mcp_stdio_host_sweep_interval_seconds` 与按事件分类的 `tma_mcp_stdio_host_events_total`。
- stdio 在等待 client request 响应期间也会处理 server-to-client JSON-RPC request：`ping` 返回空结果，`roots/list` 返回配置 roots，sampling / elicitation 按安全策略拒绝，未知方法返回 `-32601`；server notification 仍按无需响应处理。
- 长驻 stdio host 会显式识别 `notifications/tools/list_changed`、`notifications/resources/list_changed` 和 `notifications/prompts/list_changed`，分别累计到 host 快照与 `tma_mcp_stdio_host_events_total` 的 `tools_list_changed`、`resources_list_changed`、`prompts_list_changed` 事件。目录结果不在 host 内缓存：每个 Turn 解析工具时重新执行 `tools/list`，resources/prompts 桥接列表也按调用实时读取，因此通知后的下一次列表请求会在同一 PID 上获得最新目录，无需重启 MCP 子进程。
- stdio host 同时解析 `notifications/progress` 和 `notifications/message`，将脱敏计数暴露为 `progress_notifications_total`、`log_messages_total`、`invalid_notifications_total`、`log_messages_by_level` 及对应 Prometheus 指标。
- server request 的数字或字符串 JSON-RPC `id` 会原样写回，避免跨 transport 改写 request identity。

Server 侧 Streamable HTTP host：

- AgentRuntime 使用独立的 `StreamableHTTPHost`，scope 与 stdio 一致，包含 workspace、Session、Agent、Agent config version 和 MCP identifier；URL、headers、OAuth、roots、listener 与 client policy 进入配置指纹，不同 scope 或凭据快照不会共享远程 Session。
- 同一 scope 只执行一次 `initialize`，保存服务端返回的 `Mcp-Session-Id` 和协议版本，后续 Turn 的 list/call/read/get 请求串行复用该远程 Session。不同 scope 可并行，且不会与 stdio host 共用容量。
- `listen: true` 的 GET SSE listener 绑定 host entry 生命周期，而不是单次 Turn context；会跨 Turn 保留 `Last-Event-ID`、处理 server request，并累计三类 `list_changed` notification。
- HTTP POST SSE 与 GET listener 使用同一脱敏 notification 统计边界；原始 logging `data` 和 progress message 不进入 host 快照。
- 单次 POST 因 Turn deadline/cancel 中止时不会销毁远程 Session，因为 HTTP request 边界不会让 JSON-RPC 流失步；失败的 `tools/call` 仍不会自动重放。服务端明确返回 `404` / `410` 表示 Session 失效时，host 会废弃 entry 内会话，下一请求重新 initialize。
- 空闲回收、容量淘汰和 Server shutdown 会取消 listener，并在远程服务提供 `Mcp-Session-Id` 时发送 `DELETE`；`404`、`405` 和 `204` 均按终止完成处理，其他失败累计为 `delete_errors_total`。
- 生命周期由 `TMA_MCP_HTTP_HOST_IDLE_TIMEOUT_SECONDS`、`TMA_MCP_HTTP_HOST_SWEEP_INTERVAL_SECONDS`、`TMA_MCP_HTTP_HOST_MAX_SESSIONS` 控制，默认分别为 `600`、`60`、`64`。tooling health 使用 `mcp_http_host`，Prometheus 使用 `tma_mcp_streamable_http_host_*`。
- egress policy 由 `TMA_MCP_HTTP_EGRESS_ALLOW_HTTP`、`TMA_MCP_HTTP_EGRESS_ALLOW_PRIVATE_NETWORKS`、`TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS`、`TMA_MCP_HTTP_EGRESS_ALLOWED_CIDRS` 控制。Agent config 无权覆盖；health 和 runtime 使用同一 policy 实例及阻断计数。
- 私有或企业 CA 可由 Server 运维侧配置 `TMA_MCP_HTTP_CA_BUNDLE`。PEM 证书只追加到系统信任池，不关闭证书链或 hostname 校验，并统一覆盖 OAuth、POST、GET listener 与 DELETE；Agent config 无法注入 CA 或设置 insecure TLS。
- DNS 全部解析结果必须安全，实际 dial 会再次校验并固定到已验证 IP。host allowlist 只约束域名，私网目标仍需私网开关或 CIDR；loopback、link-local、metadata 与 special-use 地址只能由精确 CIDR 明示放行。
- redirect 默认最多 10 次且不能改变 scheme、host 或 port，避免 token/body 被重放到其他 authority。
- 每次阻断会写入 reason-only 的 `mcp_http_egress_blocked` 结构化安全审计日志并累计指标；日志不包含 URL、host、IP、CIDR、header 或 OAuth 数据。

初始化能力声明：

- `initialize` 返回的 server `capabilities` 会被解析并保存在 MCP runtime 中。
- 配置非空 `roots` 时，stdio 与 `streamable_http` 的 initialize request 都会声明 `roots: { listChanged: false }`；未配置 roots 时不声明该能力。
- sampling / elicitation 在真实审计 backend 接入前不会出现在 client capabilities 中。即使 server 未遵循协商直接发起请求，client 仍会返回显式策略错误，而不会静默执行远端采样或用户交互。
- 当前会识别并展示 `tools`、`resources`、`prompts`、`completions`、`logging` 声明能力。
- 配置 `logging.level` 时会在 `notifications/initialized` 后调用 `logging/setLevel`；level 进入 host 配置指纹和 runtime 非敏感 metadata。
- Agent tooling health 会把这些声明能力作为 `capabilities` 数组返回，便于诊断 server 实际暴露面。
- Agent tooling health 在 `tools/list` 成功后会额外探测 `resources/list`、`resources/templates/list` 与 `prompts/list`，并返回 `resource_count` / `resource_template_count` / `prompt_count`。
- Workbench 会把 `capabilities` 和三类 catalog count 作为 MCP 健康检查 badge 展示；空列表或不支持的可选能力会显示为 0，不代表 tools 加载失败。

列表分页规则：

- `tools/list`、`resources/list`、`resources/templates/list`、`prompts/list` 会在响应包含 `nextCursor` 时继续请求下一页，并把多页结果合并。
- 后续分页请求会发送 `{ "cursor": "..." }`。
- 如果 server 重复返回同一个 cursor，客户端会返回错误，避免无限循环。
- `resources/list`、`resources/templates/list` 和 `prompts/list` 属于可选能力；server 返回 `-32601 method not found` 时会降级为空列表。
- `tools/list` 对默认 MCP tool runtime 仍保持严格失败；只有显式开启 `expose.resources` 或 `expose.prompts` 时，`tools/list` 返回 `-32601 method not found` 才会降级为 context-only runtime。

资源读取规则：

- `internal/mcp.Client.ListResources` 会执行 `initialize` + `resources/list`，返回 server 声明的 resource metadata。
- `internal/mcp.Client.ReadResource` 会执行 `initialize` + `resources/read`，返回文本或 blob content。
- `internal/mcp.Client.ListResourceTemplates` 会执行 `initialize` + `resources/templates/list`，分页返回 RFC 6570 URI template metadata。
- 默认资源 API 只作为底层协议能力提供，不会绕过现有工具策略自动进入模型上下文。
- 配置 `expose.resources: true` 后，runtime 会额外注册只读 `mcp_list_resources`、`mcp_list_resource_templates` 和 `mcp_read_resource` 桥接工具；调用结果通过标准 `ExecutionResult` 返回，结构化 state 使用 `tma.mcp_context_result.v1`。

Completion 规则：

- `internal/mcp.Client.Complete` 与长驻 `HostedClient.Complete` 支持 `completion/complete`，覆盖 `ref/prompt` 和 `ref/resource`、当前 argument 及可选 context arguments。
- stdio 与 Streamable HTTP 使用同一请求/结果结构；Server 返回超过协议上限 100 个 values 时客户端拒绝结果。
- `completions` 会进入 initialize capability 诊断，但当前不自动注册为模型工具；它用于 Workbench/编辑器等交互面后续实现参数建议，避免无显式产品开关地扩大 Agent 工具面。

Resource subscription 边界：

- 当前未实现 `resources/subscribe`、`resources/unsubscribe` 和 `notifications/resources/updated`。
- 长驻 stdio host 目前在请求期间读取协议流，空闲期没有独立 reader/event consumer；在提供可消费的资源更新事件接口前，不声明 subscribe capability，也不提供只能发送订阅却无法可靠接收更新的半成品实现。

Prompt 读取规则：

- `internal/mcp.Client.ListPrompts` 会执行 `initialize` + `prompts/list`，返回 server 声明的 prompt metadata 和参数定义。
- `internal/mcp.Client.GetPrompt` 会执行 `initialize` + `prompts/get`，按 name 和 arguments 返回 prompt messages。
- 默认 prompt API 只作为底层协议能力提供，不会自动注入模型上下文。
- 配置 `expose.prompts: true` 后，runtime 会额外注册只读 `mcp_list_prompts` 和 `mcp_get_prompt` 桥接工具；prompt message 只会作为该工具调用的结果返回，不会自动改写 system prompt。

`streamable_http` 当前实现：

- 业务 AgentRuntime 通过 Server host 复用长会话。直接调用 `internal/mcp.Client` 和 Agent tooling health 探测仍使用独立短会话，先 `initialize`，再发送 `notifications/initialized`，不会污染业务 Session。
- 请求使用 `POST`，`Accept` 包含 `application/json, text/event-stream`。
- 支持普通 JSON response，也支持 POST response 返回 `text/event-stream`；POST SSE 在目标 response 前夹带 server request 时，会先按同一 client policy 回应，再继续匹配原始 client request。
- initialize 之后，同一会话后续请求会带上 `Mcp-Protocol-Version`；如果服务端在 initialize response 返回 `Mcp-Session-Id`，也会持续带上该 session header。
- 配置 `listen: true` 时会启动可选 `GET` SSE listener；服务端返回 `405 Method Not Allowed` 时会静默降级。
- GET listener 会记录 SSE `id` 并在重连时发送 `Last-Event-ID`。
- GET listener 收到三类 catalog `list_changed` notification 时由 host 计数，其他 notification 无需响应；收到 `ping` 会返回空结果，收到 `roots/list` 会返回配置里的 roots；收到 `sampling/createMessage` 或 `elicitation/create` 会返回 `-32000` 策略错误；收到其他 server request 会通过 POST 返回 `-32601 method not found`。数字和字符串 request ID 都会原样回传。
- GET listener 对 progress/logging notification 做结构校验和脱敏分级计数，不返回 JSON-RPC response。
- 配置 `oauth` 时，客户端会先向 `token_url` 发送 `client_credentials` token 请求，支持 `client_secret_post` 与 `client_secret_basic`，再把返回的 Bearer token 注入 MCP 请求的 `Authorization` header；带 `expires_in` 的 token 会在同一 MCP runtime 内复用，接近过期时自动重新获取。
- 如果 Streamable HTTP 返回 `401 Unauthorized`，客户端会解析 `WWW-Authenticate` 中的 OAuth protected resource metadata，抓取 protected resource metadata 与 authorization server metadata，并在错误中给出 issuer、authorization endpoint、token endpoint 和 scopes。
- 当前支持 OAuth metadata discovery / diagnostics、显式 `client_credentials` / `refresh_token` token flow 和进程内过期缓存。浏览器授权、动态客户端注册、授权码 token exchange、用户级 token 持久化和 browser login helper 为个人账号连接专用能力，当前不开发；出现明确个人账号连接需求后再重新立项。除 `roots/list` 外，不提供 sampling/elicitation 等 client capability 的真实实现。`sampling/createMessage` 和 `elicitation/create` 已有显式安全拒绝边界，但尚未接入真实模型采样或用户交互。

## 开发说明

本次实现的主要分层：

1. `internal/mcp`
   - 负责 MCP 配置解析与 `stdio` / `streamable_http` JSON-RPC 客户端
   - 目前实现 `initialize`、`tools/list`、`tools/call`、`resources/list`、`resources/templates/list`、`resources/read`、`prompts/list`、`prompts/get`、`completion/complete`
   - `StdioHost` 负责 Server 侧 session-scoped 进程复用、请求串行、取消失效、空闲回收和 shutdown

2. `internal/tools/mcp.go`
   - 负责把 MCP server 转成 TMA `Manifest`
   - 负责把 MCP result 转成 TMA `ExecutionResult`
   - 在 `expose.resources` / `expose.prompts` 显式开启时注册只读 resources/prompts 桥接工具
   - 在 `MCPRuntime` 内保留 hosted client snapshot，使 manifest 加载和后续工具执行命中同一个 Server host entry

3. `internal/mcpregistry` 与 `internal/managedagents/postgres_mcp_registry.go`
   - 负责 Workspace MCP server、不可变版本、checksum、状态和使用计数
   - 在 Agent config 发布时固定 `version: 0`，在 runtime / tooling health 加载时解析精确版本
   - 保留内嵌 `servers` 兼容路径，并拒绝跨 workspace、停用服务和重复 identifier

4. `internal/httpapi/mcp_registry.go`
   - 提供注册表 CRUD、启停、连通性测试、版本列表和历史版本恢复 API
   - 使用 control auth 保护写操作，并把管理动作写入 operator audit

如果后续要扩展：

- 增加浏览器 OAuth、refresh token 托管、真实 client capability 或 sidecar transport，优先扩展 `internal/mcp`，并通过 `MCPRuntime` 的 client seam 接入；
- 增加 worker 托管或缓存 manifest 时，优先扩展 `execution.ResolveToolExecution` 和 worker capability 上报，并保持当前 Server host 作为无 worker 场景；
- 增加 worker 侧长驻 stdio host 时，必须保留现有 Server host 的 context cancel、Session 隔离、配置指纹和不自动重放副作用调用语义；
- 增加 UI 卡片或审计事件，优先扩展 `runner` / `httpapi inspector`。

## 测试

### 自动测试

本次接入的最小回归集：

```bash
go test ./internal/tools ./internal/execution ./internal/httpapi ./internal/managedagents ./cmd/tma
go test ./...
```

覆盖点：

- MCP config 归一化；
- MCP `env_ref` / `headers.env_ref` 引用解析；
- MCP `roots` 配置归一化与 `roots/list` client response；
- MCP stdio / Streamable HTTP initialize 的 roots client capability 声明，以及 sampling / elicitation 未实现前不虚假声明；
- MCP stdio server-to-client `ping` / `roots/list` 双向请求、sampling / elicitation 安全拒绝、未知方法 fallback 和字符串 request ID 透传；
- MCP initialize server capabilities 解析、runtime 保存和 tooling health 展示；
- MCP `sampling` 配置保留与 `sampling/createMessage` 策略拒绝；
- MCP `elicitation` 配置保留与 `elicitation/create` 策略拒绝；
- MCP `resources/list` / `resources/read` stdio 与 `streamable_http` 客户端解析；
- MCP `resources/templates/list` stdio 与 `streamable_http` 分页、可选能力降级和只读 Agent 桥接；
- MCP `prompts/list` / `prompts/get` stdio 与 `streamable_http` 客户端解析；
- MCP `completion/complete` 的 Prompt/Resource reference、context arguments、输入校验和 100 values 上限；
- MCP `expose.resources` / `expose.prompts` 显式开启后的只读 Agent 桥接工具；
- MCP resource-only / prompt-only server 在 `tools/list` 不支持时的 context-only runtime 加载；
- MCP `tools/list` / `resources/list` / `resources/templates/list` / `prompts/list` 的 `nextCursor` 分页合并与重复 cursor 防护；
- MCP `resources/list` / `resources/templates/list` / `prompts/list` 不支持时的空列表降级，以及未显式开启 context expose 时 `tools/list` 不支持继续失败；
- Agent tooling health 对 initialize capabilities、resource count、resource template count、prompt count 的 HTTP 响应；
- Workbench MCP 健康检查 badge 对 capabilities 和三类 context catalog count 的展示；
- Inspector MCP Timeline / Recent Events 诊断 badge，以及 runtime event 中非敏感 `mcp_*` metadata 透传；
- MCP `streamable_http` JSON 与 POST SSE 响应解析，以及 POST SSE 内嵌 server request 的响应；
- MCP `streamable_http` 可选 GET SSE listener、server request fallback response、字符串 request ID 和 `Last-Event-ID` 重连；
- MCP `streamable_http` 401 OAuth metadata discovery 与错误诊断；
- MCP `streamable_http` OAuth client credentials token 请求、Bearer 注入、进程内过期缓存、auth method 校验和错误脱敏；
- MCP stdio context timeout 会发送 `notifications/cancelled`，并把调用方可见错误归一为 context 错误；
- Server `StdioHost` 同一 Session 跨 runtime resolution 只启动并 initialize 一个进程，不同 Session 使用隔离进程；
- Server `StdioHost` 在取消后废弃协议流并于下一请求重建，空闲 entry 可回收，race detector 覆盖 host 与 resolver 并发路径；
- Server `StdioHost` 容量达到上限时淘汰最旧空闲 entry，全部忙碌时稳定拒绝，并把 start/stop/discard/reap/evict/reject 状态暴露到 tooling health 与 Prometheus；
- Server `StdioHost` 识别 tools/resources/prompts 三类 `list_changed` notification，累计可观测事件并在不重启进程的情况下通过下一次实时 list 请求读取新目录；
- Server `StreamableHTTPHost` 跨 Turn 复用 `Mcp-Session-Id` 和 SSE listener，按 Session 隔离、取消保活、失效重建、空闲回收、容量限制，并在回收/shutdown 时发送 `DELETE`；
- MCP `logging.level` 配置校验、capability 协商与 `logging/setLevel`，以及 stdio/POST SSE/GET SSE 的 progress/logging notification 脱敏统计；
- MCP runtime manifest 暴露；
- MCP runtime policy 默认值、范围校验、Registry server/version 分区、总调用超时和并发等待取消；
- MCP 连续失败熔断、冷却、单 half-open 探测和成功恢复；
- MCP failure class、tool result 脱敏以及超时/失败的 `tools/call` 不自动重放；
- tooling health `mcp_runtime_guard` 快照与 `tma_mcp_runtime_guard_*` Prometheus 指标；
- Workspace runtime-status API 对 Registry server 白名单、跨 Workspace 隐藏、版本排序和零值时间省略；
- Workbench 单 Server 最严重状态、逐版本运行保护详情、手动刷新以及桌面/手机无溢出布局；
- MCP tool 执行结果转换；
- Agent HTTP/API 持久化 `mcp`；
- CLI `agent config update --mcp`。

### 手工验证

仓库内已经提供一个真实 stdio MCP fixture 的端到端 smoke：

```bash
make verify-mcp-stdio
```

它会启动临时 TMA server、创建 Agent、写入 `mcp` 和 `tools` 配置、触发 fake LLM 调用 `filesystem.read_file`，并校验：

- `mcp` 已归一化落库，且 `env` 中保存的是 `env_ref` 引用；
- Runtime 暴露了 `filesystem.read_file`；
- 事件链路里出现 `runtime.tool_call`、`runtime.tool_result`、`agent.message`；
- MCP result 中包含 `tma-mcp-filesystem-ok` marker。
- fixture 启动 PID 记录只有一行，证明 `tools/list` 与 `tools/call` 复用了同一个 Server 长驻进程；短会话实现会产生两行并使 smoke 失败。

RuntimeGuard 真实故障注入与 MCP 全量验收：

```bash
make verify-mcp-runtime-guard
make verify-mcp-all
```

`verify-mcp-runtime-guard` 使用一次性 PostgreSQL 数据库、Workspace JWT 和非 owner/非 superuser runtime role 启动真实 Server。fixture 先连续两次让 `tools/call` 超过 1 秒 deadline，再验证 open 状态拒绝新请求且调用计数不增加；冷却后切换为 success，验证单 half-open 探测、工具调用和 closed 恢复。脚本同时检查 `mcp_timeout` 脱敏事件、`runtime-status`、Prometheus 指标和精确的 `timeout, timeout, success` 调用序列，结束时删除临时数据库和角色。

`scripts/mcp_stdio_fixture.py` 的故障注入只通过测试进程环境启用：

- `TMA_MCP_FAULT_MODE_FILE`：每次 `tools/call` 读取模式，支持 `success`、`timeout`、`transport`、`rpc_unavailable`、`protocol`；
- `TMA_MCP_FAULT_CALL_FILE`：每次真实进入 `tools/call` 追加当前模式，用于证明没有 retry/replay；
- `TMA_MCP_FAULT_DELAY_SECONDS`：`timeout` 模式的阻塞秒数。

`verify-mcp-all` 聚合 stdio、Streamable HTTP、Registry 与 RuntimeGuard 四套验收，并执行 MCP 相关 race、Workbench/Inspector 测试与构建以及 `git diff --check`。stdio/HTTP 共用一个自动创建并清理的全迁移临时数据库，Registry/RuntimeGuard 继续各自使用受限 runtime role 的独立数据库；入口不读取或修改开发数据库中的业务数据。四套验收 Server 都显式固定自己的认证模式并关闭 browser OIDC，不继承仓库 `.env` 的登录方式。

1. 创建或更新 Agent，写入 `mcp` 配置。

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --mcp '{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp/project"],"stdio_framing":"json_lines"}}}'
```

2. 查看 config version，确认 `mcp` 已落库。

```bash
bin/tma agent config list --agent agt_000001
```

3. 启动一个 session 并触发需要文件系统的任务，确认模型侧能看到 `filesystem.*`。

4. 检查事件流或最终结果，确认工具结果通过标准 tool result 路径返回，而不是直接泄漏原始 MCP 协议包。

### 调试建议

- 如果 `mcp` 配置写入时报 `invalid input`，先检查 `stdio.command` 或 `streamable_http.url` 是否为空，`transport` 是否是 `stdio` / `streamable_http`，identifier 是否命中保留 namespace，`env_ref` 是否指向存在的环境变量。
- 如果 Agent 没暴露 MCP tools，先检查 `tools` 配置是否把 namespace 过滤掉。
- 如果 tool call 失败，先单独验证目标 MCP server 能否在本机用同样的 `command + args + env` 正常启动。
- 私有 HTTPS MCP 报 `certificate signed by unknown authority` 时，配置 Server 级 `TMA_MCP_HTTP_CA_BUNDLE`，不要改用 HTTP 或关闭 TLS 校验。
- 运行 `make verify-mcp-http` 可启动本地真实 TLS fixture，自动检查 OAuth、egress allowlist、Session header、POST/GET SSE、catalog、notification、Agent tool call、取消/Session 重建定向测试和 shutdown DELETE。
- 运行 `make verify-mcp-registry` 会创建一次性 PostgreSQL 数据库、两组 Workspace JWT 和非 owner/非 superuser runtime role，端到端检查 Registry v1/v2/v3、固定 Agent binding、真实 MCP tool call、跨 Workspace `404`、停用 kill switch、归档冲突和 Workspace 级 operator audit；无论成功或失败都会删除临时数据库和角色。
- 运行 `make verify-mcp-runtime-guard` 可复现真实 timeout -> open -> half-open -> closed 状态机；如果失败，优先查看 `.verify-mcp-runtime-guard-server.log`，脚本退出时仍会清理一次性数据库和角色。
