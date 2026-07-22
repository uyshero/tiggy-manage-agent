# TMA 架构

## 总体链路

```text
Client / Workbench / SDK
  -> HTTP API + PostgreSQL Store
  -> WorkerRunner
  -> AgentRuntimeTurnExecutor
  -> agentcore.Engine
     -> modelruntime -> llm.Manager -> Provider
     -> toolruntime -> tools.Registry -> capability.Provider
     -> agentcontrol -> Session control events
```

Server 是控制面和状态事实源；Worker 是可替换执行面；Agent Core 是唯一的
Turn 状态机。Session、Turn、事件、审批、租约和 Agent Core state 均持久化到
PostgreSQL，不能依赖单进程内存恢复任务。

## Agent Core

`internal/agentcore` 管理模型调用、工具批次、人工交互、上下文压缩、预算和终态。
每次状态写入受 revision、lease owner、attempt 和 lease expiry fencing 约束。同一
Turn 恢复时继续已有 Core state，不切换执行实现。

工具执行采用 durable journal：

1. 冻结 Registry、JSON Schema 和 Permission Policy 快照。
2. 对完整批次执行无副作用 preflight。
3. 持久化 `started` 后才执行工具。
4. 逐调用持久化 `succeeded`、`failed`、`rejected` 或 `indeterminate`。
5. 安全或带幂等键的调用可按策略恢复；未知副作用调用不盲目重放。

模型错误和普通工具错误以安全、可恢复的 Tool Result 返回模型。Registry、Schema、
权限快照或 durable contract 损坏属于 fatal error，Turn 失败关闭。统一轮次和预算
限制负责收敛，不使用“连续两次参数错误即退出”之类的局部断路规则。

## 上下文与模型

`DefaultContextBuilder` 组装 system、summary、skills、工具定义、历史消息和本轮输入。
输入预算由模型 context window 推导；大文件和工具输出在 Provider 层先有界读取，再在
模型上下文层二次裁剪。压缩是 durable model attempt，崩溃恢复后不会重复提交同一结果。

`llm.Manager` 解析 Session 固定的 `AgentConfigVersion`。当前内置 `fake` 和
`openai-compatible` Provider，统一输出 text、reasoning、tool call、usage、stop 和
error 事件。只有最终用户可见文本进入 live stream；reasoning 和工具参数分片不持久化。

每轮 usage 记录 workspace、agent、session、turn、provider 和 model。Router、failover
和更多原生 Provider 属于后续能力，不能改变上层 Turn 协议。

## Runner 与 Worker

`WorkerRunner` 负责排队、claim、lease/heartbeat、取消、中断和终态回写。
`tma-worker` 通过 HTTP 注册、轮询和提交 `tma.work.v1` 工作，不直连 PostgreSQL。

运行位置与 namespace 是两个维度：

- namespace 表达能力域，例如 `default.*`、`artifact.*`、`browser.*`、`agent.*`。
- runtime 表达执行位置，公开值为 `auto`、`cloud_sandbox`、`local_system`。
- server 内置实现是承载细节，不作为公开 runtime。

Worker 可通过 `--workspace-root`/`TMA_WORKER_WORKSPACE_ROOT` 限制本地文件和工作目录。
没有匹配 Worker 时默认隐藏 `local_system` 工具；仅受信任开发环境可启用
`TMA_ALLOW_SERVER_LOCAL_SYSTEM=true` 回退到 Server 进程。

Worker 收到 SIGINT/SIGTERM 后进入 draining，停止领取新工作，并在 shutdown timeout
内等待运行中任务。并发、heartbeat 和 reaper 配置见 [configuration.md](./configuration.md)。

## Capability Provider

`internal/capability.Provider` 是文件、命令、代码执行等低层能力接口，不负责 Agent
循环、Provider 选择或权限审批。`execution.ProviderResolver` 根据 runtime、Workspace
和 Worker capability 选择实现；`toolruntime` 在其上负责 schema、权限和 durable 编排。

文件实现必须在 Provider 层满足路径守卫、有界读取、UTF-8 边界、revision、原子写入
和 stale revision 检查。沙箱和远程 Worker 应实现同一语义，不能通过传输层改变契约。

## 控制事件与多 Agent

`user.steer` 在下一次模型调用前合并，`user.follow_up` 追加一轮输入，`user.interrupt`
进入统一取消路径。Core 使用 Session event sequence 作为持久化游标，恢复后不重复消费。

子 Agent 通过独立 Session/Turn 隔离上下文，继承但不能扩大父任务的权限。系统限制
递归深度、每轮/每 Session 子任务数、用户和 Workspace 并发与队列。父任务只回收摘要、
结构化结果、证据和 Artifact。Durable DAG、任意图编排和跨组织自治不属于当前交付范围。

## 完成与恢复保证

最终消息发布前执行 completion gate，校验任务计划、证据和结果 schema。可修正失败回到
同一循环；校验器异常、非法结果或重试耗尽均失败关闭。

生产验收覆盖普通 Turn、审批/澄清恢复、优雅重启、模型与工具阶段硬崩溃、PostgreSQL
短时中断、lease 抢占和 fencing。命令见 [`TESTING.md`](../TESTING.md)。剩余风险见
[roadmap.md](./roadmap.md)。
