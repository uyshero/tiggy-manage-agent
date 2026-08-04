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

- 将对话工作台从 Runtime 面板收敛为任务、文件、变更、Artifact 和审批闭环。
- 完成移动端、键盘/可访问性、SSE 重连和大数据量 Session 列表体验。
- 收紧对话工作台 UI Extension 边界、bundle 完整性和故障隔离；完整专业产品必须独立部署。
- 完成 R语言生存分析工作台独立数据库、API、GitLab 和 R Runtime 的流量切换并下线 Platform 兼容代理。
- 完成 Knowledge Service/Share/Question 的数据复制与网关切流，下线 Platform `/v2/knowledge/*` 兼容实现和嵌入 Web。
- 把 Inspector 深链、trace、completion quality 和权限审计用于一线排障。

## P1：平台治理

- 完成 Extension Catalog、安装 revision、Provider 健康/切换和 placement fencing 的统一实现。
- 为工具权限提供 Workspace/Agent 管理 UI、effective preview 和审计检索。
- 扩展 MCP 企业认证矩阵；个人 OAuth、sampling/elicitation 只在明确需求后实现。
- 完成 Skills/Plugin 安装汇聚和外部 scanner 的隔离、超时、审计与失败策略。

## P2：模型与编排

- Model Runtime 已具备原生 mTLS/Service Mesh、逐请求短期凭证、流式背压指标、Workspace/Application
  Quota Policy 和预算告警；后续增加 Provider Router/failover。
- 图片多模态 Generate、Multimodal Realtime v1 协议基线、TMA-native/OpenAI Realtime Adapter、Runtime 内部链路、模型目录准入、Server ObjectRef 治理、公共治理路由、Invocation 审计和 Realtime SDK 已完成；
  豆包 Agent Plan 的真实文本/图片 Generate、Usage 和 Invocation Audit 已通过全新数据库与 Go Core SDK 验证，本地慢 Provider/慢客户端、双向断线和双 Server 共享配额验证已完成；后续完成 OpenAI Realtime 真实账号和部署环境 PostgreSQL 多副本压测，再评估连续视频 Provider，同时增加真实 tokenizer 与更多 Provider 原生 tool calling adapter。
- 在统一 Provider 接口上实现显式 Router/failover，保留 usage 和错误归因。
- 评测渐进式工具暴露效果，并继续探索按需 Tool Search；不以固定工具数量替代数据。
- 在权限不放大的前提下改进子 Agent 结果归并、配额和可观测性。
- 后期实现统一 `ResourceSnapshot` / 内容缓存，减少同一文件或网页在连续提问中的重复读取。文件以规范化路径、mtime、size 和内容哈希识别版本；网页以规范化 URL、租户/认证范围、ETag、Last-Modified、TTL 和正文提取器版本识别快照。历史问题可复用已验证快照，涉及“当前/最新”或执行副作用时必须重新验证；同时覆盖 Edit/Write 后失效、上下文压缩后的按需恢复、并发请求合并、缓存大小/淘汰策略和命中率观测。该项不是当前 Agent Loop 正确性与权限闭环的阻塞项。

Durable DAG、任意工作流图、跨组织自治、个人账号 MCP token 托管和无边界浏览器自动化不在
当前交付范围。进入开发前需单独定义安全模型、状态机、恢复语义和验收标准。
