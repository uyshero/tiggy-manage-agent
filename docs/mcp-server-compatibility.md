# MCP Server 兼容性矩阵

本文档记录 TMA 与真实第三方 MCP Server 的可重复兼容性结果。矩阵只认固定包版本和实际 `initialize`、`tools/list`、`tools/call` 结果，不把“进程能启动”视为通过。

当前基线由 TMA 请求并由所有矩阵 Server 确认协商 MCP `2025-06-18`；Server 返回其他版本或缺少 name/version 时测试失败。

## 自动验收

```bash
make verify-mcp-compatibility
```

该命令需要可访问 npm registry 的网络、`npx`、Docker Compose 和本地 PostgreSQL 容器。测试默认不随 `go test ./...` 联网或创建数据库；只有设置 `TMA_RUN_MCP_COMPATIBILITY=1` 或运行上述 Make 入口才启用。npm 包使用精确版本，文件测试数据只写入 `t.TempDir()`；PostgreSQL 场景自动创建随机一次性数据库和最小权限角色，结束时终止连接并删除数据库/角色。MCP 进程由 Server 侧 Host 关闭。

## 2026-07-14 基线

| Server | 固定包版本 | Server 自报版本 | Transport / framing | Catalog | 真实调用 | 结果 |
| --- | --- | --- | --- | --- | --- | --- |
| Official Filesystem | `@modelcontextprotocol/server-filesystem@2026.7.10` | `secure-filesystem-server 0.2.0` | stdio / `json_lines` | 14 tools，包含 `read_text_file`、`list_allowed_directories` | 从 canonical 临时根目录读取 marker | 通过 |
| Official Filesystem / initial Roots | `@modelcontextprotocol/server-filesystem@2026.7.10` | `secure-filesystem-server 0.2.0` | stdio / `json_lines` / `roots/list` | 14 tools | 不传命令行目录；Server 初始化后请求 `roots/list`，等待允许目录收敛后读取 marker | 通过（异步收敛） |
| Official Memory | `@modelcontextprotocol/server-memory@2026.7.4` | `memory-server 0.6.3` | stdio / `json_lines` | 9 tools，包含 `create_entities`、`read_graph` | 创建实体后从同一 host 进程读取图谱 marker | 通过 |
| Official Sequential Thinking | `@modelcontextprotocol/server-sequential-thinking@2026.7.4` | `sequential-thinking-server 0.2.0` | stdio / `json_lines` | 1 tool：`sequentialthinking` | 执行单步 thought 并解析 `structuredContent.thoughtNumber=1` | 通过 |
| Official Everything | `@modelcontextprotocol/server-everything@2026.7.4` | `mcp-servers/everything 2.0.0` | Streamable HTTP / Session | 13 tools、7 resources、4 prompts、2 resource templates | 本机随机端口启动真实 HTTP Server；同一 Session 完成 catalog、Prompt/Resource completion 与 `echo` marker 调用 | 通过 |
| YawLabs PostgreSQL | `@yawlabs/postgres-mcp@0.6.20` | `@yawlabs/postgres-mcp 0.6.20` | stdio / `json_lines` | 21 tools，包含 `pg_readonly`、`pg_query` 和 schema introspection | `secret_ref` 注入一次性只读数据库；参数化读取、2 行截断通过，`pg_query`/`pg_readonly` 写入及 stacked query 被拒绝，数据未变化 | 通过 |
| TMA legacy fixture | 仓库内 `scripts/mcp_stdio_fixture.py` | `tma-mcp-fixture 1.0.0` | stdio / `content_length` | 1 tool | `readFile` marker | 通过，兼容旧 framing |

## 已确认的兼容规则

- MCP 标准 stdio Server 使用 newline-delimited JSON-RPC，必须配置 `stdio_framing=json_lines`；Workbench 新建 stdio Server 会显式写入该值。
- 为保证升级不改变历史 Agent/Registry 版本，持久化配置省略 `stdio_framing` 时继续按 legacy `content_length` 解释。旧 TMA fixture 或使用 LSP 风格 header 的自建 Server 也可显式配置该值。
- framing 会进入 Server host 配置指纹；同一 Session 中 framing 改变会使用不同 host entry，不复用旧协议流。
- macOS 的 `/var` 通常映射到 `/private/var`。Filesystem 根目录和调用参数必须使用同一 canonical path，否则官方 Server 会按目录越界拒绝。
- Sequential Thinking 当前实际 tool name 是 `sequentialthinking`，不要根据 README 小节标题推断为 `sequential_thinking`。
- Memory 的写入与读取必须命中同一 host scope，矩阵用 `create_entities -> read_graph` 验证跨调用进程状态确实保留。
- Official Filesystem 可以不传命令行目录。配置非空 `roots` 后，TMA 在 initialize 中声明 `roots: { listChanged: false }`，并响应 Server 初始化后的 `roots/list`；真实目录授权和文件读取已通过。Roots 在当前 runtime 内保持不变，Agent 配置变更会创建新 client snapshot 和 host，不发送 `notifications/roots/list_changed`。
- Official Filesystem `2026.7.10` 在收到 Roots 响应后异步执行目录解析与校验；紧邻 `tools/list` 的第一次工具调用可能早于状态更新。矩阵通过 `list_allowed_directories` 有界轮询确认收敛后再读取文件。要求首个工具调用确定性可用时，仍应传命令行目录；不能把 `tools/list` 成功当作 Roots 已生效的同步屏障。
- Official Everything 的 Streamable HTTP 模式会签发 `Mcp-Session-Id`；TMA Server 侧 HTTP host 在 tools/resources/resource templates/prompts、completion 和 `tools/call` 间复用该 Session，并在关闭时发送 DELETE。兼容测试在本机随机端口直连，不修改生产 egress policy；生产配置仍默认 HTTPS-only 且阻止 loopback/private network。
- PostgreSQL 基线不使用已废弃的 `@modelcontextprotocol/server-postgres`，而固定活跃维护的 YawLabs `0.6.20`。MCP 层显式 `ALLOW_WRITES=0`，数据库层使用随机最小权限角色并设置 `default_transaction_read_only=on`；`DATABASE_URL` 只通过 `secret_ref: env:...` 在启动时解析。`POSTGRES_MAX_ROWS=2` 的截断标记、参数化查询、extended protocol stacked-query 拒绝和最终行数不变均为通过条件。

## 尚未认证

| 能力 | 状态 | 原因 / 下一步 |
| --- | --- | --- |
| Official Filesystem 运行期 Roots 变更 | 不宣称支持 | 初始 `roots/list` 已认证；TMA 明确声明 `listChanged: false`，配置变更通过新 Agent config/runtime/host 生效 |
| 远程认证 Streamable HTTP 企业服务 | 待凭据 | 本地官方 Everything 已认证；确定企业服务后再验证 TLS、OAuth/服务账号与环境变量引用注入 |
| GitHub、GitLab、Notion、Slack | 待业务账号 | 涉及个人/企业授权范围；当前不开发浏览器 OAuth，优先使用服务账号 token 的环境变量引用 |
| 其他数据仓库 MCP | 待选型 | PostgreSQL 已认证；Snowflake、BigQuery、ClickHouse 等需确定企业产品与最小权限测试账号后分别验证 |

## Secrets 边界

兼容性矩阵不引入 Vault/JIT。Secret 继续由环境变量或现有环境变量管理模块维护，MCP 配置只保存 `env_ref` / `secret_ref: "env:NAME"`；Runtime 在启动进程、发送 HTTP 请求或交换 OAuth token 前解析并注入。测试和文档不得保存真实 token，事件、日志、Inspector 和矩阵结果也不得输出 secret。
