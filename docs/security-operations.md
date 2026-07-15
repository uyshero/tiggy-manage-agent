# TMA Security Operations

本文档说明统一身份授权审计如何进入企业日志与告警系统。

## 数据路径

每个受保护请求都会先写本地 JSON 结构化日志。配置 `TMA_SECURITY_AUDIT_OTLP_ENDPOINT` 后，同一授权决策默认同步写入 PostgreSQL durable outbox，由后台 worker 使用租约批量发送到 OTLP/HTTP Logs `/v1/logs`。

```text
OIDC/JWT/Gateway
  -> TMA identity and resource authorization
     -> stdout JSON (local source)
     -> PostgreSQL outbox -> leased worker -> OTLP/HTTP Logs -> Collector -> SIEM
     -> Prometheus counters -> Alertmanager
```

远端出口失败不会改变 HTTP 授权结果。事件保留在 outbox 并按指数退避重试；达到最大次数或完整性校验失败后进入 dead letter。数据库插入失败时本地 JSON 日志仍然存在；必须对 `persistence_failed`、dead letter 和积压年龄告警。

## Keycloak Realm 安全基线

`deploy/keycloak/tma-realm.json` 为自管 Keycloak Realm 定义以下最低基线：外部访问强制 TLS、12 位复杂密码与 5 次密码历史、连续 5 次失败后的递增等待、最长 15 分钟临时锁定，以及登录事件和管理员事件审计。开发 Compose 仍使用 `start-dev`，只用于本机联调；生产环境必须使用反向代理或 Keycloak 原生 HTTPS，并以 `start` 模式运行。

首次导入 Realm 会自动应用该基线。已有数据卷不会因修改导入 JSON 而自动更新，应显式同步并验证：

```bash
make keycloak-security-apply
make verify-keycloak-security
```

工具通过容器内 `KC_BOOTSTRAP_ADMIN_PASSWORD` 登录 Admin API，不读取或输出明文密码。生产环境应从 Secret 管理系统注入 bootstrap 管理员、初始管理员和 SMTP 凭据；完成首次引导后，应禁用常驻 bootstrap 管理员并改用受审计的独立管理账号。

## OTLP 记录

Instrumentation scope 为 `tma.security.authorization`，resource attribute 包含 `service.name=tiggy-manage-agent`。常用 log attributes：

- `event.name=authorization_decision`
- `event.id`，稳定的 `saud_*` 去重键
- `auth.outcome`、`auth.reason`、`auth.type`、`auth.required_role`
- `enduser.id`
- `tma.organization.id`、`tma.workspace.id`、`tma.owner.id`
- `auth.roles`、`auth.authorization_sources`
- `http.request.method`、`url.path`

Token、Cookie、query string 和未匹配的原始 Claim 不进入 payload。Collector token 应使用独立、最小权限凭据，并由 Secret 管理系统注入 `TMA_SECURITY_AUDIT_OTLP_TOKEN`。

生产环境还必须从 Secret 管理系统注入至少 32 字节的完整性密钥。单密钥兼容配置为 `TMA_SECURITY_AUDIT_INTEGRITY_KEY`；生产推荐配置 `TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID` 与 `TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON` keyring。TMA 在写入 outbox 前用活动 key 计算 HMAC-SHA256 并持久化 key ID，claim 后按该 ID 精确选择验证 key；未知 ID 或校验失败的记录直接进入 dead letter，不发送 payload。

轮换完整性密钥时按以下顺序操作：

1. 在 keyring 中同时部署旧 key 和新 key，活动 ID 仍指向旧 key。
2. 所有实例加载同一 keyring 后，把活动 ID 切换到新 key 并滚动重启。
3. 确认旧 key ID 不再有 `pending`、`delivering` 或 `dead_letter` 记录，再从所有实例的 keyring 移除旧 key。

优先使用服务端 readiness 判定，避免人工漏算迁移前空 ID 行：

```bash
bin/tma --auth-token "$TMA_OPERATOR_TOKEN" observability integrity-keys
```

也可以调用 `GET /v1/observability/security-audit/integrity-keys`。只有目标旧 key 的 `safe_to_remove=true` 时才可移除；活动 key、未配置但数据库仍引用的 key，以及存在历史空 ID 阻塞行时都返回 `false`。响应仅包含 key ID 和计数，不包含密钥值。

```sql
SELECT integrity_key_id, status, COUNT(*)
FROM security_audit_outbox
GROUP BY integrity_key_id, status
ORDER BY integrity_key_id, status;
```

迁移前创建的 HMAC 行其 `integrity_key_id` 为空，会依次尝试当前受信 keyring。这个兼容路径只适用于空 ID；非空但未知的 ID 不会降级尝试其他 key。

## Collector 路由

Collector 应启用 OTLP HTTP receiver，并按 instrumentation scope 把授权日志路由到安全日志后端。示例骨架：

```yaml
receivers:
  otlp:
    protocols:
      http: {}

exporters:
  otlphttp/security_siem:
    endpoint: https://siem-gateway.example.com/otlp
    headers:
      Authorization: Bearer ${env:SIEM_EXPORT_TOKEN}

service:
  pipelines:
    logs/security:
      receivers: [otlp]
      exporters: [otlphttp/security_siem]
```

生产环境应在 Collector 增加 scope filter、持久队列和 retry，并限制 TMA 使用的接收凭据只能写日志。

## PostgreSQL 租户隔离

`agents`、`agent_config_versions`、`environments`、`managed_environment_variables`、`mcp_registry_servers`、`mcp_registry_server_versions`、`object_refs`、`session_artifacts` 和 `sessions` 已启用 `FORCE ROW LEVEL SECURITY`。workspace 策略只允许访问与事务局部 `tma.workspace_id` 相同的行；Session 还会应用 `tma.owner_id`，Operator 使用空 owner 获得 workspace 范围。HTTP 层从已认证 Principal 派生 scope 并放入请求 context，PostgreSQL Store 在事务开始后设置局部值。请求参数中的 workspace/owner 不能覆盖 context scope。

生产数据库必须使用两个账号：

- migration owner：执行 schema migration 和 `GRANT`，不运行服务。
- runtime role：非 superuser、无 `BYPASSRLS`、不是表 owner，只具有运行所需 DML 权限。

`TMA_ENV=production` 会在监听 HTTP 前验证 runtime role、9 张受保护表的 FORCE RLS/policy、DML 权限和所需 sequence 权限；错误使用 owner/superuser、遗漏 `000045` 到 `000052` 的迁移或缺少权限时启动失败。账号创建与授权 SQL 见 [configuration.md](./configuration.md#tma_database_url)。

MCP Registry 的 server 与不可变 version 使用父子 policy；所有 Registry Store 操作都在同一事务设置 workspace，受限角色不会通过按 ID 查询先探测其他 workspace 的资源归属。workers 等尚未进入上述清单的表仍由应用层 `AccessScope` 查询保护，后续启用 RLS 前仍需先完成关联查询和后台 runtime 的事务 scope 迁移。

## Prometheus 告警

加载 [tma-security-alerts.yml](../deploy/prometheus/tma-security-alerts.yml)。默认规则覆盖：

- 五分钟认证失败激增。
- 已认证身份跨 Workspace/Owner scope 访问。
- 非 Operator 身份重复访问控制面。
- OTLP 发送失败或队列丢弃。
- outbox 持久化失败、dead letter 和最老积压超过五分钟。
- 完整性状态查询失败、未配置 key ID 引用、历史空 ID 长期阻塞和旧 key 一小时仍不可移除。
- 非 durable 模式下内存队列持续超过 80%。

默认阈值是安全基线。上线后应按正常请求量、机器人流量和 IdP 刷新行为校准；不要把 subject、Workspace 或路径增加为 Prometheus 标签。

## 调查顺序

1. 从告警的 `reason` 和 `auth_type` 判断认证、RBAC 或资源 scope 问题。
2. 在 SIEM 中按 `enduser.id`、`tma.workspace.id` 和时间窗口查找对应授权事件。
3. 使用 `auth.authorization_sources` 确认是直接 Claim、外部角色还是 Group mapping 授权。
4. 若 SIEM 缺事件，检查 persistence/export failure、outbox pending/dead-letter、最老积压和本地 stdout JSON。
5. 若完整性 key 告警触发，运行 `bin/tma observability integrity-keys`，按 `configured`、`blocking` 和 `safe_to_remove` 定位轮换状态。
6. 不在工单、告警注释或聊天中粘贴 Bearer token、Cookie 或完整 JWT。

Dead letter 原因确认并修复后，Operator 可执行：

```text
POST /v1/observability/security-audit/replay?limit=100
```

投递语义是至少一次。Collector 接收成功但 TMA 写 delivered 状态失败时，租约到期后事件会以相同 `event.id` 再次发送；SIEM 必须按该字段幂等去重。
