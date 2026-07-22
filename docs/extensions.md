# Extension 与 Provider

## 对象与边界

Extension 是可安装、可版本化、可治理的能力包；Provider 是某项 capability 的具体实现；
Worker 是 Provider 的运行载体；Tool Manifest 是模型调用契约。四者不能混用。

扩展类型包括 Tool/Runtime Provider、Workbench Plugin、Skill package 和集成连接器。
Core 只定义稳定协议、Registry、权限、审计和生命周期，不把企业业务代码编译进 Server。

Descriptor 至少包含：

- 稳定 identifier、display name、semantic version 和 publisher。
- extension type、entrypoints、platform/architecture compatibility。
- capabilities、tool manifests、settings contributions 和 Workbench contributions。
- required roles、permissions、secret refs 和数据作用域。
- checksum/signature、minimum TMA version 和升级策略。

## 发现与选择

Extension Catalog 记录可安装版本；Workspace installation 记录启用版本与配置；Worker
heartbeat 上报实际可执行 capability。有效候选必须同时满足安装启用、版本兼容、Worker
在线、配置完整、权限允许和健康检查通过。

选择顺序为显式 Workspace/Agent 绑定、管理员默认、确定性优先级。多个 Worker 提供同一
能力时使用稳定 placement 和 epoch fencing，禁止因心跳抖动在一次 Turn 内切换 Provider。
人工切换必须预览影响、写审计并创建新配置 revision。

Worker 下线后：新调用不再分配；运行中调用按 lease 和幂等性恢复；无法证明结果的外部
副作用标为 `indeterminate`。不得悄悄回退到权限更大的 Server 本机实现。

## 设置贡献

Settings Contribution 使用版本化 JSON Schema 和标准 UI renderer。支持 global、organization、
workspace、agent 和 user/session 作用域，但每个字段必须声明 authority、继承和 override 规则。

标准控件覆盖 string、secret、number、boolean、enum、multiselect、object/list 和 diagnostic
action。未知 renderer 或更高 schema version 必须只读降级，不能猜测编辑。

保存流程：

1. 读取当前 revision 和 effective config。
2. 客户端按 schema 校验，Server 重复校验。
3. Secret 写入 secret store，只保存引用。
4. 使用 `If-Match` 更新并生成新 revision。
5. 重新计算 availability/diagnostics，并写审计事件。

Provider 离线时设置页仍可读，但会显示配置来源、最后健康状态和不可用原因；只有明确允许
离线编辑的字段可保存。Diagnostic action 必须声明 role、risk、approval policy/reason、
timeout 和输出脱敏规则。

## 版本、冲突与组合

- Identifier 和已发布版本不可原地修改。
- 新字段必须有默认/缺省语义；破坏性 schema/API 使用新 major version。
- 同一 extension 的前后端贡献按同一安装 revision 激活。
- namespace 冲突默认拒绝，除非 Descriptor 明确声明替代关系。
- 组合包不能扩大子扩展权限，卸载时检查反向依赖。
- Secret、租户数据和 Worker 凭据不能进入 bundle 或 Catalog metadata。

## 验收

新扩展至少验证安装/升级/回滚、checksum/signature、兼容性拒绝、配置 revision、secret
脱敏、发现/下线、权限、审计、多 Workspace 隔离、Worker 重启和 UI 离线降级。工具插件
细节见 [tools.md](./tools.md)，Workbench 贡献见 [workbench.md](./workbench.md)。
