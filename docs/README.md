# TMA 文档

本文档目录只保留长期有效的产品和工程契约。HTTP 字段以
[`api/v2/openapi.yaml`](../api/v2/openapi.yaml) 为准，测试命令以
[`TESTING.md`](../TESTING.md) 和 `make help` 为准，历史决策以
[`DEVELOPMENT_LOG.md`](../DEVELOPMENT_LOG.md) 为准。

| 文档 | 内容 |
| --- | --- |
| [architecture.md](./architecture.md) | Agent Core、Runner、Worker、Provider、LLM 与多 Agent 边界 |
| [platform-guide.md](./platform-guide.md) | Platform 公共能力、Retrieval/Speech 接入、现有应用差异和未来扩展方向 |
| [platform-boundary.md](./platform-boundary.md) | Platform 核心职责、应用所有权、依赖规则和新功能准入标准 |
| [platform-remediation-plan.md](./platform-remediation-plan.md) | Platform 最终项目划分、整改阶段、数据切换和验收门槛 |
| [repository-split.md](./repository-split.md) | Platform、Knowledge、Biography、R语言生存分析工作台的拆分边界与迁移顺序 |
| [repository-cutover-runbook.md](./repository-cutover-runbook.md) | 四仓数据迁移、Gateway 切流、验证、回滚和兼容层删除门槛 |
| [sdk-release-policy.md](./sdk-release-policy.md) | Core SDK 统一版本、兼容窗口和应用升级流程 |
| [model-runtime.md](./model-runtime.md) | 独立模型数据面职责、内部协议、安全和部署模式 |
| [tools.md](./tools.md) | 工具契约、权限、文件能力、进程插件和 Computer Use |
| [mcp.md](./mcp.md) | MCP 注册、配置、传输、安全和兼容性 |
| [mcp-gitlab.md](./mcp-gitlab.md) | GitLab Docker MCP 的只读模板、Token、安全边界和验证 |
| [extensions.md](./extensions.md) | Extension/Provider 治理、设置贡献和版本规则 |
| [workbench.md](./workbench.md) | 对话工作台、Inspector、UI 扩展与 R语言生存分析工作台边界 |
| [configuration.md](./configuration.md) | Server、Worker、Runtime 和外部服务配置 |
| [deployment.md](./deployment.md) | Docker/Kubernetes、数据库迁移、对象存储和 Onlyboxes |
| [operations.md](./operations.md) | 可观测性、安全、告警和排障 |
| [api.md](./api.md) | API 入口、认证、错误、分页和 SSE 约定 |
| [sdk.md](./sdk.md) | Go/TypeScript SDK 和应用扩展边界 |
| [roadmap.md](./roadmap.md) | 尚未完成的产品与生产事项 |

文档维护规则：

- 同一事实只在一份主题文档中定义，其他位置只链接。
- 不在 `docs` 保存执行日志、周报、文章稿或已完成的阶段计划。
- 配置项必须能在代码、`--help` 或部署模板中找到对应实现。
- API 字段不手工复制完整 OpenAPI，只记录跨接口约定和常用入口。
- 验收结果不写日期化流水账，只保留可重复执行的命令。
