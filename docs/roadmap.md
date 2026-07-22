# TMA 路线图

这里只记录尚未完成且影响产品或生产边界的事项。完成项应从本文件删除，并把稳定契约更新到
对应主题文档；执行历史写入 [`DEVELOPMENT_LOG.md`](../DEVELOPMENT_LOG.md)。

## P0：生产闭环

- PostgreSQL 全量丢失的备份恢复/PITR 演练，明确 RPO/RTO。
- 为 `indeterminate` 外部副作用提供业务对账、人工确认和补偿入口。
- 在 Provider 限流、长网络分区和 retry storm 下完成容量保护与阈值校准。
- 完成多副本 Server、Worker 抢占、对象存储故障和安全审计积压的持续演练。
- 用真实 OIDC、S3、LLM 和隔离 Worker 完成发布门禁，不以 fake backend 代替生产认证。

## P1：用户工作台

- 将默认 Workbench 从 Runtime 面板收敛为任务、文件、变更、Artifact 和审批闭环。
- 完成移动端、键盘/可访问性、SSE 重连和大数据量 Session 列表体验。
- 固定 PluginContext、bundle 完整性、故障隔离和首个企业纵向插件验收。
- 把 Inspector 深链、trace、completion quality 和权限审计用于一线排障。

## P1：平台治理

- 完成 Extension Catalog、安装 revision、Provider 健康/切换和 placement fencing 的统一实现。
- 为工具权限提供 Workspace/Agent 管理 UI、effective preview 和审计检索。
- 扩展 MCP 企业认证矩阵；个人 OAuth、sampling/elicitation 只在明确需求后实现。
- 完成 Skills/Plugin 安装汇聚和外部 scanner 的隔离、超时、审计与失败策略。

## P2：模型与编排

- 增加真实 tokenizer、多模态上下文和更多 Provider 原生 tool calling adapter。
- 在统一 Provider 接口上实现显式 Router/failover，保留 usage 和错误归因。
- 基于评测决定渐进式工具暴露/Tool Search，不以固定工具数量替代数据。
- 在权限不放大的前提下改进子 Agent 结果归并、配额和可观测性。

Durable DAG、任意工作流图、跨组织自治、个人账号 MCP token 托管和无边界浏览器自动化不在
当前交付范围。进入开发前需单独定义安全模型、状态机、恢复语义和验收标准。
