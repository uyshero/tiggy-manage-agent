# Agent 完成质量离线评测

`make eval-agent-quality` 运行两套确定性、无网络、无数据库、无真实模型依赖的 Agent 质量回归。评测直接执行生产 `DemoRuntime` Tool Loop、工具 JSON Schema 预检、`TaskPlanCompletionGate`、文件工具与 path guard，以及 task-group 的 result-schema / strategy / reducer 逻辑。JSON 夹具只回放候选回答、黄金工具调用轨迹、可信任务计划和 fan-in 状态快照。

```bash
make eval-agent-quality
```

只运行文件工具回归：

```bash
make eval-filesystem-tools
```

报告为 JSON，命令退出码可直接用于 CI：全部阈值满足时为 `0`，质量阈值未满足时为 `1`，夹具或执行错误时为 `2`。

## 基线指标

- `case_pass_rate`：实际结果、阻断次数、validator 和预期错误均符合夹具的比例。
- `first_attempt_completion_rate`：无需完成门禁重试即可安全完成的用例比例，仅作趋势指标，不设最低阈值。
- `retry_correction_rate`：预期经反馈修正的用例中，确实阻断并在后续候选完成的比例。
- `false_success_rate`：应失败的候选被放行，或要求门禁阻断的用例在未达到预期阻断次数时被放行的比例。
- `evidence_compliance_rate`：`evidence_*` 用例符合预期的比例。
- `hard_fail_rate`：预期失败关闭的用例按指定阻断次数、validator 和错误结果终止的比例。
- `average_completion_attempts`：每个用例的平均候选回答次数。
- `schema_compliance_rate`：工具参数用例的结果、schema rejection 数和 executor 调用数均符合预期的比例。
- `schema_retry_correction_rate`：收到 schema 错误后修正参数并成功执行的用例比例。
- `invalid_tool_execution_rate`：预期被 schema 拒绝的调用中仍然触达 executor 的比例。
- `task_group_compliance_rate`：fan-in 用例的最终状态、schema rejection、聚合结果和 invalid-result exclusion 均符合预期的比例。
- `task_group_retry_correction_rate`：首轮结构化结果不合规、重试后产生有效结果并正确聚合的比例。
- `invalid_result_aggregation_rate`：被 result schema 拒绝的子任务结果仍进入 aggregate 的比例。
- `filesystem_compliance_rate`：文件工具选择、执行结果、错误恢复和二进制路由全部符合黄金轨迹的用例比例。
- `filesystem_selection_rate`：黄金轨迹中逐个工具位置选择正确的比例。
- `filesystem_recovery_rate`：stale revision 等可恢复错误发生后按预期重新读取或收窄操作的比例。

当前基线要求全部用例通过，false success、非法工具执行和无效结果聚合均为零；完成重试修正率、证据合规率、失败关闭率、工具 schema、task-group 与 filesystem 指标均为 100%。阈值与用例分别保存在 `testdata/agent-quality/completion-gate.json` 和 `testdata/agent-quality/filesystem-tools.json`，修改生产门禁、工具参数、文件路由或 fan-in 行为时必须同步提交有明确意图的夹具变化。

文件工具套件是确定性的生产链路回放，用于防止契约和 Tool Loop 行为回归；它不替代灰度环境中的真实模型选择率评测。

## 工具参数边界

所有注册工具的参数在审批和执行前按 manifest 的 Draft 2020-12 JSON Schema 校验。`required`、`enum`、长度、数值范围、组合约束和 `additionalProperties` 等规则统一生效；外部 `$ref` 被禁用。参数不合规时 Runtime 返回可恢复的 `invalid_tool_arguments`，反馈只包含 instance path 和 constraint path，不包含实际参数值；连续两轮不合规会触发 Tool Loop 断路。manifest schema 自身无效属于服务端配置故障并失败关闭。

`RegistryExecutor` 会再次执行同一校验，因此非 Agent Runtime 调用者也不能绕过。离线 schema 用例同时断言实际 executor 调用 ID，防止最终响应成功掩盖非法调用已经执行。

## Task-group 结果边界

`expected_result_schema` 使用同一套离线 Draft 2020-12 编译器。创建 task-group 时会先编译每个 item schema，坏 schema 或外部 `$ref` 在创建 group、子 Session 或 runner work 前失败；子任务完成后再用标准 schema 校验 `result_json`，覆盖 `enum`、`additionalProperties`、字符串/数组约束、数值范围和组合约束。校验错误只包含 instance/constraint path。

不合规的 completed item 会转为 failed，不能进入 aggregate。离线 task-group replay 直接复用生产 strategy 与 reducer，覆盖 `all_completed`、quorum、fail-fast、schema retry、`json_values` 以及 `merge_objects` 的数组去重和冲突策略。

## 证据边界

离线夹具中的 `tool_call_ids` 表示可信存储已经校验并返回的 canonical evidence refs。失败、跨 Turn 或伪造的工具引用会被持久层拒绝，因此对应快照不含 `tool_call_ids`；评测验证这种状态不能绕过完成门禁。引用与 `runtime.tool_result` 的数据库级关联约束仍由 PostgreSQL 集成测试覆盖，不在此离线套件中复制存储校验逻辑。
