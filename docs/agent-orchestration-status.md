# Agent Orchestration Status and Roadmap

本文档是 TMA 多 Agent 编排的当前能力边界与收尾清单。当前产品方向以可靠的 subagent 与 task group 为主；durable workflow / DAG 不属于当前交付范围。

## 当前结论

多 Agent 主链路已经闭环：父 Agent 可以创建子 Session、发送任务、持久化排队、等待或流式读取事件、收集结构化结果，并通过 task group 执行 fan-out / fan-in。父 Agent 也可以按 JSON Schema 动态组建 2–8 个角色，由主持 Agent 运行可恢复的固定两轮讨论。服务重启、active quota 满、子 Agent 失联、部分任务失败和人工运维都有明确状态与恢复路径。

当前适合的任务模型：

```text
parent agent
  -> spawn / run_group
  -> persistent queue and admission
  -> parallel subagents
  -> wait / retry / cancel
  -> validate and reduce results
  -> parent continues
```

## 已完成能力

### Subagent 生命周期

- 独立 Session、parent session / turn lineage 和递归深度限制。
- workspace / owner active quota，以及每个 parent turn / session 的 child 上限。
- priority + FIFO 的持久化启动队列、queued quota、排队超时和显式取消。
- 服务重启后恢复 pending 请求，active 槽位释放后自动晋升。
- parent 删除或终止后的 orphan subagent 回收。

### Task group

- fan-out / fan-in、`all_completed`、`any_completed` 和 quorum。
- fail-fast、group cancel、item retry、group retry 和级联 nested group 操作。
- 多种 text / JSON reducers、aggregate schema 和 item 结果 JSON Schema 校验。
- nested group lineage、递归结果收集与跨 Session 状态查询。
- 内置 task-group templates，用于固定的并行拆分和聚合模式。

### Multi-Agent Deliberation

- `team_plan` JSON Schema 校验目标、2–8 个动态角色、固定两轮和 token / wall-clock budget。
- brainstorm + critique、structured debate、red team review 和 expert panel 四种策略。
- 第一轮独立贡献、主持人 agreements / disagreements / missing evidence 与按角色提问、第二轮回应和最终共识。
- discussion、participant、round 和 contribution 持久化；查询时幂等 reconciliation 支持服务重启恢复。
- discussion cancel、当前轮单参与者 retry、幂等创建和结构化最终结果。

### 运维与可观测

- queued、running、rejected、wait seconds 等 metrics 和 runtime events。
- Inspector 跨 Session execution tree、template metadata、schema error 和 aggregate 展示。
- Inspector 展示 discussion phase / strategy / budget、动态角色、两轮观点、主持争议与问题和最终共识，并支持 cancel / participant retry。
- Inspector 支持 cancel、retry item、retry group、orphan reap 和独立 2 秒刷新。
- 控制动作使用 control bearer auth，并写入持久化 `operator_audit_log`。
- 审计保存 principal 指纹、operator label、角色、动作、资源、成功/失败和详情。

## 术语边界

当前的 `AgentTaskGroupTemplate` 是 task group 参数模板：它设置 strategy、reducer、quorum、fail-fast 和 item schema，然后展开成一个 task group。

它不是 durable workflow，当前没有以下对象或语义：

- workflow instance / node / edge 持久化。
- DAG 依赖解锁和条件分支调度。
- approval、timer、wait-event、compensation 节点。
- workflow template 版本、checkpoint 和 workflow-level replay。

文档、API 和 UI 应使用 `task-group template`，不要把它描述为完整 workflow engine。

## 当前已知限制

- task-group templates 是代码内置 registry，没有数据库 CRUD、租户自定义和版本管理。
- control auth 当前兼容单 token，principal 角色固定为 `admin`；`X-TMA-Operator` 只是审计显示标签，不是可信身份凭据。
- lineage Inspector 每个 parent Session 最多读取 100 个直接 children；当前默认治理上限远低于该值。
- global orphan reap audit 没有 Session 归属，通过 `/v1/operator-audit` 查询，不进入单个 Session lineage audit。
- Postgres integration tests 需要显式设置 `TMA_RUN_POSTGRES_TESTS=1` 和 `TMA_DATABASE_URL`。
- 当前没有声明跨版本 deterministic replay，也没有完成生产容量基线。
- Deliberation 当前固定两轮，不支持运行中增删角色、开放式无限讨论或讨论级 deterministic replay。

## 上线前收尾清单

### 必须完成

1. 应用包括 `000031_operator_audit_log.sql`、`000032_skills.sql` 和 `000033_multi_agent_deliberations.sql` 在内的全部 migrations，并验证回滚与备份流程。
2. 配置非空 `TMA_CONTROL_AUTH_TOKEN`，确认 Inspector 不在共享终端或录屏中暴露 token。
3. 按生产容量设置 active / queued quota、queue timeout、turn lease 和 worker concurrency。
4. 运行 Postgres integration tests，以及 queue recovery、orphan reap、cancel/retry 的故障演练。
5. 建立 queued、rejected、wait seconds、orphan reap 和 operator action failed 告警。
6. 验证多 server 实例下 admission lock、turn lease、队列晋升和重复请求幂等。

### 建议完成

1. 对典型 fan-out 规模执行容量测试，记录 P50/P95 排队与完成时间。
2. 为高风险 control action 制定 operator label 规范和审计保留周期。
3. 准备 queue 堵塞、stuck turn、orphan 激增和 audit 写入失败的 runbook。
4. 固定一组 task-group 回归样例，覆盖 reducer、schema validation、nested group 和 fail-fast。

## 后续路线

### P1：生产治理

- 多 principal RBAC：`viewer`、`operator`、`admin`，由服务端可信配置绑定 token / identity 与角色。
- workspace-scoped control permission 和 audit 查询权限。
- token、费用、wall-clock 和 fan-out budget，以及超预算降级策略。
- stuck detector、dead-letter inspection 和自动修复建议。

### P2：调度与质量

- 基于 capability、负载、成本和历史成功率的 agent selection。
- task-group replay、离线 eval、schema 合规率和 retry 收益评测。
- 结构化 context / artifact handoff，减少父子 Agent 重复传输。
- task-group template 的数据库 registry、版本和 workspace override。

### P3：按需求触发的未来能力

只有出现多阶段长期任务、条件分支、跨天审批或复杂依赖的明确产品需求时，再评估 durable workflow / DAG。届时复用现有 queue、task group、Session lineage、Inspector 和 audit，不提前固化 workflow schema。

## 验收口径

当前多 Agent 阶段可以在以下条件下视为收尾：

- fan-out / fan-in 场景无需人工重试即可稳定完成或给出可恢复失败状态。
- quota 满时请求不会丢失，重启后 queued work 可继续晋升。
- orphan、cancel、retry 和 operator action 都可查询、可审计。
- Inspector 能定位 Session、group、item、queue wait、schema error 和 aggregate。
- 生产配置、容量基线、告警和故障 runbook 已由部署环境验证。
