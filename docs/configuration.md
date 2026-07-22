# TMA 配置

配置优先级由具体入口决定：CLI flag 通常覆盖环境变量，Session runtime settings 覆盖允许
热更新的运行参数。Secret 只通过环境引用或 Secret Manager 注入，不写入数据库、Agent 配置、
命令行历史或文档示例。完整默认值以 `bin/tma-server --help`、`bin/tma-worker --help` 和代码为准。

## 最小开发配置

```env
TMA_ENV=development
TMA_HTTP_ADDR=:8080
TMA_DATABASE_URL=postgres://tma:tma@localhost:5432/tma?sslmode=disable
TMA_AUTH_MODE=disabled
TMA_LLM_PROVIDER=fake
TMA_OBJECT_STORAGE_PROVIDER=filesystem
TMA_OBJECT_STORAGE_ROOT_DIR=.tma/objects
```

`TMA_AUTH_MODE=disabled` 只能用于本机开发。生产环境会对认证、Worker token、数据库角色、
对象存储和加密配置执行 fail-closed 校验。

## Server 与认证

| 变量 | 用途 |
| --- | --- |
| `TMA_ENV` | `development`/`test`/`production` 行为门禁 |
| `TMA_HTTP_ADDR` | HTTP 监听地址 |
| `TMA_DATABASE_URL` | Server runtime PostgreSQL URL |
| `TMA_AUTH_MODE` | `disabled`、`jwt`、`oidc` 或 `trusted_gateway` |
| `TMA_AUTH_TOKEN` | 本地/兼容控制 token；生产优先 OIDC |
| `TMA_AUTH_JWT_SECRET/ISSUER/AUDIENCE` | HS256 JWT 兼容模式 |
| `TMA_AUTH_OIDC_ISSUER/AUDIENCE/JWKS_URL` | OIDC/JWKS 校验 |
| `TMA_AUTH_OIDC_SIGNING_ALGS` | 允许的签名算法列表 |
| `TMA_AUTH_OIDC_CLAIM_MAPPING_JSON` | subject、workspace、group/role claim 映射 |
| `TMA_AUTH_OIDC_WEB_*` | Web login client、redirect、logout 和 session secret |
| `TMA_AUTH_GATEWAY_TOKEN/TRUSTED_CIDRS` | 可信网关身份边界 |
| `TMA_AUTH_COOKIE_TRUSTED_ORIGINS` | Cookie 写操作允许的 Origin |

生产必须显式配置 issuer、audience、算法和 Workspace 映射；不能从未验证 header 直接接受
身份。Web session secret、JWT secret 和 gateway token 必须使用独立高熵值。

## Turn、Agent Core 与子 Agent

| 变量族 | 用途 |
| --- | --- |
| `TMA_TURN_WORKER_COUNT/QUEUE_SIZE` | Server 内 Turn 调度并发和队列 |
| `TMA_TURN_POLL_INTERVAL_MS` | 领取待执行 Turn 的间隔 |
| `TMA_TURN_TIMEOUT_MS` | 单 Turn 上限 |
| `TMA_TURN_LEASE_DURATION_MS` | durable claim 租约 |
| `TMA_TURN_HEARTBEAT_INTERVAL_MS` | 租约续期周期，必须显著小于 lease |
| `TMA_DEFAULT_CONTEXT_WINDOW_TOKENS` | 模型未登记窗口时的保守默认值 |
| `TMA_SUBAGENT_MAX_DEPTH` | 子 Agent 最大递归深度 |
| `TMA_SUBAGENT_MAX_CHILDREN_PER_TURN/SESSION` | 数量上限 |
| `TMA_SUBAGENT_USER_*`、`TMA_SUBAGENT_WORKSPACE_*` | 并发和排队配额 |

Session `runtime_settings` 可热更新 intervention mode、completion gate、runtime 选择和允许的
沙箱网络等字段。更新必须携带 `If-Match` revision；硬性安全边界不能由 Session 覆盖。

## Worker

| 变量 | 用途 |
| --- | --- |
| `TMA_BASE_URL` | Server 地址 |
| `TMA_WORKER_TOKEN` | Worker 身份 token |
| `TMA_WORKER_CONTROL_TOKEN` | 控制面 token，不与 Worker token 共用 |
| `TMA_WORKER_WORKSPACE_ID/TYPE` | Worker 归属和类型 |
| `TMA_WORKER_WORKSPACE_ROOT` | 本地文件/工作目录根 |
| `TMA_WORKER_CONCURRENCY` | 同时执行的 work 数 |
| `TMA_WORKER_POLL_INTERVAL` | 拉取间隔 |
| `TMA_WORKER_HEARTBEAT_INTERVAL` | Worker 状态 heartbeat |
| `TMA_WORKER_WORK_HEARTBEAT_INTERVAL` | work lease heartbeat |
| `TMA_WORKER_SHUTDOWN_TIMEOUT` | draining 等待上限 |
| `TMA_WORKER_PLUGINS` | 进程插件路径列表 |
| `TMA_WORKER_REAPER_*`、`TMA_WORKER_WORK_REAPER_*` | 失联 Worker/work 回收 |

`TMA_ALLOW_SERVER_LOCAL_SYSTEM=true` 只用于受信任开发机。生产 local-system 能力应运行在
绑定 Workspace、受路径守卫和独立凭据保护的 Worker。

## LLM

```env
TMA_LLM_PROVIDER=openai-main
TMA_LLM_PROVIDER_TYPE=openai
TMA_LLM_MODEL=gpt-4o-mini
TMA_LLM_BASE_URL=https://api.openai.com/v1
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY
TMA_LLM_API_KEY=replace-in-secret-manager
TMA_LLM_MAX_ATTEMPTS=3
TMA_LLM_RETRY_BASE_DELAY_MS=250
```

Provider/Model 也可通过 API 管理。数据库只保存 API key 的环境变量名。认证、invalid request
等不可重试；rate limit、timeout 和 server error 使用有界退避并尊重 `Retry-After`。响应流
开始后不自动重放。

## 对象存储与沙箱

对象存储变量为 `TMA_OBJECT_STORAGE_PROVIDER`、`ENDPOINT`、`REGION`、`BUCKET`、
`USE_PATH_STYLE`、`ACCESS_KEY_ENV`、`SECRET_KEY_ENV` 和开发用 `ROOT_DIR`。生产使用 S3
兼容存储、TLS、独立 Bucket、加密、版本化和最小权限；Server/Worker 不持有 Bucket 管理权限。

Cloud sandbox 使用 `TMA_CLOUD_SANDBOX_ROOT/DATA_ROOT/IMAGE`，网络由
`TMA_CLOUD_SANDBOX_ALLOW_NETWORK` 和 Session policy 共同决定。Container/data 的 idle TTL、
max lifetime 和 cleanup interval 使用同名前缀变量。生产不要挂载 Docker socket 给普通 Server。

## 文件工具

| 变量 | 约束 |
| --- | --- |
| `TMA_READ_FILE_SMALL_FILE_BYTES` | 小文件一次读取阈值 |
| `TMA_READ_FILE_DEFAULT_MAX_BYTES` | 默认页大小 |
| `TMA_READ_FILE_HARD_MAX_BYTES` | 单次硬上限 |
| `TMA_READ_FILE_MAX_LINES` | 行模式硬上限 |

Byte 值必须在 `256..1048576`，并满足 `small <= default <= hard`；行数必须在
`1..5000`。无效值阻止 Server/Worker 启动。

## Web 与 Browser

Web search 通过 `TMA_WEB_SEARCH_PROVIDERS` 选择 Baidu、Brave、Exa、Search1API、SearXNG
或 Tavily；各 Provider 使用对应 `*_BASE_URL` 和 `*_API_KEY`。`TMA_WEB_SEARCH_ITEM_LIMIT`
限制结果数。

Crawl 使用 `TMA_WEB_CRAWLER_IMPLS`、`TMA_WEB_CRAWLER_RETRY` 和
`TMA_WEB_CRAWL_CONTENT_LIMIT`。Browserless 的 URL、key、user agent、wait strategy、资源
过滤和 timeout 使用 `TMA_WEB_BROWSERLESS_*`。浏览器网关使用 `TMA_BROWSER_GATEWAY_URL`、
service secret、Workspace allowlist、idle TTL 和每 Workspace session 上限。

任何外网能力同时受工具权限、审批和沙箱网络策略约束；配置 API key 不等于自动授权访问。

## MCP

`TMA_MCP_STDIO_HOST_*` 和 `TMA_MCP_HTTP_HOST_*` 控制实例上限、idle timeout 和 sweep。
HTTP egress 使用 `TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS/CIDRS`、`ALLOW_HTTP`、
`ALLOW_PRIVATE_NETWORKS`；私有 CA 使用 `TMA_MCP_HTTP_CA_BUNDLE`。安全规则见
[mcp.md](./mcp.md)。

## Skills

Skills 安装策略使用 `TMA_SKILLS_ALLOWED_LICENSES/DENIED_LICENSES`、GitHub owner/repository
allowlist、固定 commit 要求、attestation keys、license/attestation 强制开关、静态扫描阈值
和 binary scanner provider。GitHub token 只从环境注入。

资产保留和 GC 使用 `TMA_SKILLS_ASSET_RETENTION_*`、`TMA_SKILLS_ASSET_GC_*`。生产默认
使用不联网的 builtin scanner；启用外部 scanner 前必须定义 timeout、隔离和失败关闭策略。

## 可观测性与安全审计

Trace index retention 使用 `TMA_TRACE_INDEX_RETENTION_*`。自动导出使用
`TMA_PERFETTO`、`TMA_OTEL_EXPORTER_OTLP_ENDPOINT`/标准 OTel 变量和采样率。

授权审计使用 `TMA_SECURITY_AUDIT_OTLP_ENDPOINT/TOKEN`、durable outbox、batch/flush、lease、
retry、retention 和 queue 配置。生产必须配置 HMAC integrity key ring 和 active key ID；轮换前
通过状态 API 确认旧 key 没有 pending/delivering 记录。详见 [operations.md](./operations.md)。

## 验证

```bash
go test ./internal/serverconfig/...
make verify-agent-runtime
make verify-sql-baseline
```

生产配置还应执行部署检查和隔离环境的故障恢复演练，见 [deployment.md](./deployment.md) 与
[`TESTING.md`](../TESTING.md)。
