# 运维、可观测性与排障

## 事实源与视图

`session_events` 是 Turn 行为事实源；Agent Core state 是恢复事实源；trace index、span index、
Prometheus 和导出文件都是可重建视图。保留策略只能清理派生索引，不能误删 Session 事实。

模型使用经过预算和脱敏的事件投影，人类使用完整授权后的 timeline/trace。两种投影不能共用
“把所有日志塞进上下文”的实现。

## 指标与 Trace

`GET /metrics` 提供低基数 Prometheus 指标，覆盖 HTTP/授权、Worker、LLM usage、tool、审批、
completion validation、exporter、Agent Core 和 lease。固定进程级指标包括：

- `tma_agent_core_events_total{event,idempotency}`：压缩恢复、工具重放、indeterminate、预算耗尽。
- `tma_worker_lease_events_total{event}`：lease lost、inactive renewal 和 renewal failure。
- completion validation 与 security audit outbox 的计数、积压和 dead letter。

标签不能包含 subject、workspace、session、turn、路径、工具参数或其他高基数/敏感值。逐身份
调查使用结构化审计日志。

Session trace API/CLI 可导出 JSON、Perfetto 和 OTel。自动导出支持 Perfetto 文件和
OTLP/HTTP，按 `session_id + turn_id` 确定性采样。状态 API 只显示 endpoint、启用状态和最近
运行，不返回 token。

## 安全审计

授权决策先写 PostgreSQL durable outbox，再由 lease worker 批量发送 OTLP/HTTP Logs。事件用
稳定 ID 去重，并用 HMAC-SHA256 integrity key 签名。达到最大重试次数进入 dead letter，必须
支持显式 replay 和审计。

Key rotation 流程：添加新 key、切换 active key ID、等待旧 key 的 pending/delivering 归零、
通过 integrity-keys API 确认 `safe_to_remove`，最后删除旧 key。不得仅按时间猜测安全窗口。

数据库租户隔离依赖 Workspace RLS、runtime role 最小权限和生产启动校验。OIDC Realm 应限制
redirect URI、签名算法、token lifetime 和管理角色。Collector 只接收 TLS 流量并按 tenant/
scope 路由，不能把 exporter token 写入 payload。

## 必备告警

`deploy/prometheus/tma-security-alerts.yml` 应覆盖：

- 认证失败/跨 scope/Operator 拒绝异常升高。
- completion validation fail 或 retry rate 异常。
- Agent Core budget exhausted、indeterminate tool、compaction recovery 异常。
- Worker lease lost/renewal failure。
- audit persistence/export failure、dead letter、积压和 key rotation blocker。
- PostgreSQL、对象存储、LLM Provider 和 MCP/Web egress 失败率。

告警阈值必须经过 staging 压测校准；不要用包含 session/用户 ID 的 Prometheus label 提升定位能力。

## 调查顺序

1. 确认影响的 Workspace、Session、Turn 和时间范围。
2. 查看 Session 状态、pending intervention 和 Core phase。
3. 对照 Worker lease/heartbeat，确认是否发生 claim 转移或 fencing。
4. 查看 trace critical path、LLM usage、tool journal 和 completion validation。
5. 检查 PostgreSQL、对象存储、Provider、MCP/Web 和 exporter 状态。
6. 对外部副作用为 `indeterminate` 的调用做业务对账，不直接重试。
7. 记录根因、恢复动作和可重复回归命令。

## 常见问题

### Session 不是 idle

`user.message requires idle session` 通常表示 Turn 仍在 running、waiting approval/clarification
或取消尚未收敛。先查询 Session、events 和 interventions；不要直接改数据库状态。等待人工输入
时完成对应决定，失联 Worker 由 lease/reaper 恢复。

### 工具调用失败

按错误码区分 invalid arguments、policy denied、approval rejected、provider unavailable、timeout
和 fatal contract error。参数/普通执行错误可由模型修正；schema/policy snapshot 损坏应修复
部署或配置，不能无限重试。

### Worker 看不到本地工具

检查 Workspace ID、worker type/capability、heartbeat、workspace root、插件 manifest 和 runtime
选择。生产不会自动回退到 Server 本机执行。

### SSE 断开

事件流用 event sequence/`Last-Event-ID` 续传。live text 是临时流，断开后以最终
`agent.message` 和持久化事件为准。客户端需指数退避并重新读取 Session 状态。

### CommandTurnExecutor 输出错误

CommandTurnExecutor 是低层外部进程适配器，不是默认 Server 路径。若单独使用，输入输出必须
遵守 JSON envelope、timeout、取消和非零退出码规则；默认 Agent Turn 由 Agent Core 执行。

## 验证入口

```bash
make verify-agent-runtime
make eval-agent-quality
make verify-worker-work-cancel
make verify-sql-baseline
go test ./...
```

故障注入、MCP、Computer Use、数据库和浏览器测试的前置条件见
[`TESTING.md`](../TESTING.md)。危险的 `SIGKILL`、`SIGSTOP`、数据库中断和竞争实例演练只在
隔离 staging 环境运行。
