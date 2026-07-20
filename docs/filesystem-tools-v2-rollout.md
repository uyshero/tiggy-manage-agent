# Filesystem Tools V2 灰度运行手册

> 状态：本地预发布冒烟通过，待 24 小时内部灰度  
> 更新时间：2026-07-20  
> 范围：`read_file`、`find_files`、`search_files`、`write_file`、`edit_file`

## 1. 发布前门禁

每次变更文件工具契约、Provider、Worker transport 或 Tool Registry，至少执行：

```bash
make eval-filesystem-tools
go test ./internal/capability ./internal/tools ./internal/execution ./internal/workruntime ./internal/observability
go test -race ./internal/capability ./internal/tools ./internal/execution
go test ./...
```

硬门槛：

- 文件工具离线评测通过率 100%；
- 工具选择序列正确率 100%；
- stale revision 恢复用例通过率 100%；
- 二进制正文进入模型结果的用例数为 0；
- 全量测试、核心包 race 测试通过；
- Worker manifest 同时声明 `default.find_files` 和 `default.search_files`。

## 2. Worker 预检

构建后运行：

```bash
bin/tma-worker doctor --base-url http://localhost:8080 --name filesystem-v2-doctor
```

doctor 输出的 `apis` 必须包含：

```text
default.read_file
default.find_files
default.search_files
default.write_file
default.edit_file
```

`default.search_file` 可以继续出现在 Worker 执行能力中，但不应出现在默认模型工具列表。它只服务旧 Agent config 的显式兼容。

## 3. 本地预发布冒烟记录

2026-07-20 使用当前源码构建 `tma-server`、`tma`、`tma-worker`，在本地 Compose PostgreSQL 和真实 `local_system` Worker 上完成以下验收：

- `tma-worker doctor` 完成 health、register、heartbeat、poll、diagnose、archive 全链路，五个 V2 API 均出现在 manifest；
- 通过真实 `tma.work.v1` 队列依次执行 `write_file`、`read_file`、`edit_file`、`find_files`、`search_files`，全部进入 `completed`；
- 写入内容从 `needle=before` 精确编辑为 `needle=after`，搜索结果返回第 2 行、byte offset 6 和一致的 file revision；
- 读取 `image/png` 时返回 `kind=image`、`suggested_capability=vision`、`returned_bytes=0`，没有把二进制正文送入模型上下文；
- 真实 Agent Turn `sesn_000269/turn_000001` 只调用一次 `default.read_file` 并成功读取 4,617 bytes，Agent 正确返回文档首个 Markdown 标题；
- 全局 `/metrics` 和带 Session/Turn 参数的 `/metrics` 均出现文件系统指标；该 Turn 记录耗时 995 ms、返回 4,617 bytes、无截断、无二进制正文、无 revision conflict。

冒烟过程中发现并修复了一个 Worker transport 问题：Server 曾把 capability 内部 `meta` 一并放入模型参数 `input`，严格的 `additionalProperties:false` Schema 会在 Worker 侧拒绝调用。当前 `WorkerBackedProvider` 会在形成 `tma.work.v1.input` 前删除内部 `meta`；Session/Turn 继续由 work envelope 传输，Worker 执行时从 ExecutionContext 重建 request meta。回归测试同时校验 transport input 不含 `meta` 且满足模型可见 Schema。

本地运维注意：无进程守护时，Server 重启窗口会让 Worker 的 poll 请求失败并退出；重启 Server 后应重新启动 Worker，再确认 worker status 为 `online`。生产环境必须由进程管理器执行 Worker 自动重启。该行为不影响文件工具契约，但会形成可定位的 `tool_execution_failed` 指标样本。

## 4. 指标

无查询参数的 `/metrics` 暴露进程级累计指标，适合 Prometheus 持续抓取：

- `tma_filesystem_runtime_calls_total`
- `tma_filesystem_runtime_duration_milliseconds_bucket`
- `tma_filesystem_runtime_scanned_files_total`
- `tma_filesystem_runtime_scanned_bytes_total`
- `tma_filesystem_runtime_results_total`
- `tma_filesystem_runtime_returned_bytes_total`
- `tma_filesystem_runtime_truncated_total`
- `tma_filesystem_runtime_binary_files_total`
- `tma_filesystem_runtime_revision_conflicts_total`
- `tma_filesystem_runtime_errors_total`

进程计数器在重启后归零；Prometheus 查询应跨实例按 `api_name` 聚合。

带 `session_id` 和 `turn_id` 的 `/metrics` 还会返回 `tma_filesystem_tool_*` 指标，用于定位单次执行的扫描量、截断、错误和耗时。

P95 示例：

```promql
histogram_quantile(
  0.95,
  sum by (le, api_name) (
    rate(tma_filesystem_runtime_duration_milliseconds_bucket[10m])
  )
)
```

错误率示例：

```promql
sum by (api_name, error_code) (rate(tma_filesystem_runtime_errors_total[10m]))
/
sum by (api_name) (rate(tma_filesystem_runtime_calls_total[10m]))
```

## 5. 初始告警线

初始阈值用于发现异常，不代表最终 SLO。积累一周真实数据后再按仓库规模分层。

| 指标 | 预警 | 阻断扩量 |
|---|---:|---:|
| `read_file` P95 | > 500 ms | > 2 s |
| `find_files` P95 | > 1 s | > 5 s |
| `search_files` P95 | > 2 s | > 8 s |
| `write_file` / `edit_file` P95 | > 1 s | > 3 s |
| `error_code=other` | > 0 | > 0 |
| 文件工具总错误率 | > 2% | > 5% |
| revision conflict 比例 | > 5% | > 10% |
| `search_file` 调用占搜索调用比例 | > 5% | 暂缓移除兼容 |

截断率不能直接视为故障。`find_files` / `search_files` 截断率持续超过 20% 时，先检查模型是否使用了过宽 pattern，再决定调整上限。

## 6. 灰度步骤

1. 预发布环境启用，运行 Worker doctor 和两套 Agent quality eval。
2. 仅对内部 Agent 开启，观察至少 24 小时。
3. 扩至 10% Session，观察 P95、错误码、扫描字节和截断率。
4. 扩至 50%，重点检查 Onlyboxes 与 WorkerBackedProvider 的差异。
5. 全量启用后继续保留 `search_file` 执行兼容至少一个发布周期。

每一级至少满足：无 `other` 错误、无二进制正文泄漏、P95 未触发阻断线、质量评测保持 100%。

## 7. 回滚

出现以下任一情况立即停止扩量：

- 路径守卫逃逸或符号链接越权；
- 二进制正文进入模型上下文；
- 原子写失败后原文件损坏；
- Worker capability 与实际可执行 API 不一致；
- `other` 错误码出现且无法从事件定位原因。

回滚时保留旧 `search_file` executor，不删除数据或修改 Artifact。回退模型工具配置即可暂时停止暴露 `find_files` / `search_files`，随后从 Session/Turn 指标定位具体调用。

## 8. 兼容退出条件

只有同时满足以下条件，才删除 `default.search_file`：

- 连续两个发布周期无新 Agent config 显式引用；
- 旧配置迁移率达到 100%；
- `search_file` 调用占所有文件搜索调用低于 1%；
- `search_files` 精确单文件 literal 场景无行为回归；
- Worker 与 Server 已完成同版本升级。
