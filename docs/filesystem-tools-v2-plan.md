# Filesystem Tools V2 下一阶段任务规划

> 状态：核心实现、指标、离线评测与本地预发布冒烟完成，待 24 小时内部灰度  
> 更新时间：2026-07-20  
> 目标周期：4～6 周  
> 模型可见工具：`read_file`、`find_files`、`search_files`、`write_file`、`edit_file`

## 1. 阶段目标

本阶段只把文件系统基础能力做扎实，不扩展 Shell、Browser、Memory、DAG 或更多多 Agent 能力。

完成后，五个文件工具应满足：

1. 文本读取有界、可分页、可定位、可检测文件变化。
2. 路径发现与内容搜索职责分离，覆盖 Glob/Grep 的核心能力。
3. 文本写入和编辑支持并发前置条件、原子替换和稳定结果。
4. 二进制文件能够被可靠识别、分流和作为 Artifact 流转，但不进入普通文本上下文。
5. LocalSystem、Onlyboxes、Worker transport 和 AgentRuntime 使用同一契约。
6. 工具失败可恢复、可审计，模型不会通过重复调用刷空上下文。

## 2. 已确定的产品决策

### 2.1 模型只看到五个工具

```text
default.read_file
default.find_files
default.search_files
default.write_file
default.edit_file
```

不单独暴露：

```text
glob
grep
file_info
read_binary_file
write_binary_file
```

原因：

- Glob 和 Grep 是实现能力，不是跨平台产品命名。
- `find_files` 表达“有哪些文件”，`search_files` 表达“哪些文件包含内容”，模型意图清楚。
- 文件类型、MIME、size 和 revision 属于每个文件工具的公共元数据，不需要单独的 `file_info`。
- 模型通常不需要直接消费或生成原始二进制；图片、PDF、Office 和未知格式应分别路由到视觉、文档 Skill 或 `execute_code`。
- 大型二进制通过 Object Store / Artifact 流转，不使用 Tool Call Base64。

### 2.2 文本和二进制边界

`read_file` 只向模型返回 UTF-8 文本。现有 byte mode 是按原始文件 byte offset 分页文本，不是读取任意二进制。

遇到二进制时返回有界元数据：

```json
{
  "path": "/workspace/report.pdf",
  "size_bytes": 1820342,
  "content_type": "application/pdf",
  "binary": true,
  "file_revision": "stat-v1:...",
  "suggested_capability": "document_skill"
}
```

推荐路由：

| 文件类型 | 路由 |
|---|---|
| PNG / JPEG / WebP | Vision capability |
| PDF | PDF / document parser |
| DOCX / XLSX / PPTX | Document Skill 或 `execute_code` |
| ZIP / TAR | `execute_code` 或受限解包能力 |
| SQLite / Parquet | `execute_code` + 格式库 |
| 未知二进制 | `execute_code` 检查文件头或专业解析器 |

现有 DOCX path-only 文本提取先保留为兼容路径，但在结果中明确 `mode=document`。新格式不继续堆进 `read_file`。

### 2.3 二进制写入边界

`write_file` 的模型 Schema 只接受文本。二进制输出使用：

```text
run_command / execute_code / 专用工具
  -> 写入 output_path
  -> Runtime 校验路径和文件
  -> 发布 Artifact / Object Ref
```

Provider 内部仍可使用 `[]byte` 完成上传同步、Skill materialization 和 Artifact restore；“Provider 能写 bytes”不等于“模型获得 write_binary_file”。

### 2.4 二进制编辑边界

`edit_file` 只做文本精确替换。二进制修改由格式感知代码生成新文件，再通过 output path 发布 Artifact，不实现通用二进制局部 patch。

## 3. 当前实现状态

### 2026-07-20 已完成

- `read_file` 已支持 auto / line / byte 三种模式。
- 默认 8 KiB page、64 KiB hard max、UTF-8 边界和 1-based 行号。
- `file_revision` 已覆盖分页前后变化检测。
- `read_file` 已返回 kind、MIME、encoding 和建议能力；图片、PDF、Office、压缩包、SQLite 与未知二进制均不返回正文。
- `find_files` 已实现 `*`、`?`、`**`、exclude、隐藏文件策略、字典序和 `after_path` 续页，不依赖 Shell。
- `search_files` 已实现 literal、Go RE2 regex、大小写选项、跨文件上限、扫描字节上限和二进制跳过统计。
- `write_file` 已支持三种 mode、expected absence/revision、SHA-256 校验、同目录 staging、fsync 和原子提交。
- `edit_file` 已支持 expected revision、expected match count、唯一匹配和稳定错误码，并复用原子写路径。
- Workspace path guard 已覆盖读、发现、单文件搜索、跨文件搜索、写和编辑，并阻止根目录及符号链接逃逸。
- LocalSystem、Onlyboxes 和 WorkerBackedProvider 已接入相同的发现与搜索契约。
- `search_file` 保留执行兼容，默认模型上下文隐藏；旧 Agent config 显式启用时仍可见。
- 文件生成已有限制、placeholder、hash、审批 continuation 和完成门禁。
- capability、tools、execution 等定向测试、核心包 race 测试和 `go test ./...` 已通过。

### 进入灰度前仍需完成

- 在预发布环境运行 Worker doctor，验证 heartbeat 已声明 `default.find_files` / `default.search_files`。
- 使用已接入的 Prometheus 指标建立真实仓库 P50/P95、扫描量、截断率和二进制误判率看板。
- 在确定性黄金轨迹评测之外，采集灰度环境真实模型的“找路径用 find、搜内容用 search”选择率。
- 灰度观察旧 Agent config 对 `default.search_file` 的显式依赖，再确定移除周期。
- 对跨进程高竞争写入补充故障注入；当前 revision 在提交前二次校验，但不是跨主机事务锁。

具体灰度步骤、指标和回滚条件见 [Filesystem Tools V2 灰度运行手册](./filesystem-tools-v2-rollout.md)。

## 4. 目标工具契约

### 4.1 `read_file`

输入保持兼容：

```json
{
  "path": "internal/tools/types.go",
  "offset_bytes": 0,
  "max_bytes": 8192,
  "start_line": null,
  "max_lines": null,
  "file_revision": null
}
```

补充结果元数据：

```text
kind
content_type
encoding
binary
suggested_capability
```

规则：

- byte mode 与 line mode 继续互斥。
- 非普通文件失败关闭。
- 二进制不返回 `Content`。
- binary detection 只读取有界前缀。
- MIME 检测不完整扫描文件。
- 同一 call signature、revision 和窗口重复返回相同结果时触发 no-progress guardrail。

### 4.2 `find_files`

```json
{
  "root": ".",
  "pattern": "**/*.go",
  "exclude": [".git/**", "vendor/**"],
  "include_hidden": false,
  "max_results": 200,
  "after_path": null
}
```

结果：

```json
{
  "root": ".",
  "files": [
    {
      "path": "internal/tools/types.go",
      "kind": "regular_file",
      "size_bytes": 18240,
      "binary": false
    }
  ],
  "truncated": false,
  "next_after_path": null
}
```

规则：

- 结果按规范化相对路径稳定排序。
- `after_path` 是词法 continuation，不承诺目录快照一致性。
- 默认跳过 `.git`、依赖缓存和隐藏路径。
- 不跟随工作区外 symlink。
- 只返回元数据，不读取正文。
- pattern 支持明确记录的 `**` 语义；实现不得依赖宿主 shell。
- 限制最大扫描目录数、文件数、耗时和结果数。

### 4.3 `search_files`

```json
{
  "query": "DefaultRegistry",
  "paths": ["internal/**/*.go"],
  "mode": "literal",
  "case_sensitive": true,
  "max_files": 1000,
  "max_results": 100
}
```

结果：

```json
{
  "matches": [
    {
      "path": "internal/tools/types.go",
      "line_number": 226,
      "offset_bytes": 6832,
      "line": "func DefaultRegistry() Registry {",
      "file_revision": "stat-v1:..."
    }
  ],
  "scanned_files": 82,
  "skipped_binary_files": 3,
  "truncated": false
}
```

规则：

- `mode` 第一版支持 `literal` 和 Go RE2 `regex`。
- `paths` 复用 `find_files` 的 pattern 语义。
- 精确单文件输入复用现有 `search_file` 流式实现。
- 跨文件搜索默认跳过 binary，并返回跳过计数。
- 限制 max files、总扫描 bytes、结果数、单行 preview 和 deadline。
- 每个 match 返回文件 revision；不把一次多文件搜索伪装成全局一致快照。
- 结果达到上限后停止扫描，并提示收窄 query/path。

### 4.4 `write_file`

```json
{
  "path": "report.md",
  "content": "...",
  "mode": "create",
  "expected_absent": true,
  "expected_revision": null,
  "content_sha256": "sha256:...",
  "create_parents": true
}
```

规则：

- `mode=create`：目标已存在则失败。
- `mode=overwrite`：目标不存在则失败。
- `mode=create_or_overwrite`：仅在策略显式允许时使用。
- `expected_absent` 与 `expected_revision` 互斥。
- 写入临时文件、flush/fsync、再次检查目标 revision、同目录原子 rename。
- 失败不破坏原文件。
- 写完返回 size、content type、SHA-256 和新 revision。
- 模型输入继续执行现有文件生成字符/token 限制。

### 4.5 `edit_file`

```json
{
  "path": "config.yaml",
  "old_string": "enabled: false",
  "new_string": "enabled: true",
  "replace_all": false,
  "expected_match_count": 1,
  "expected_revision": "stat-v1:..."
}
```

规则：

- 修改前校验 revision。
- 默认 `expected_match_count=1`。
- 0 次匹配返回 `match_not_found`。
- 多次匹配且未显式允许返回 `match_not_unique`。
- 写入使用与 `write_file` 相同的原子 helper。
- 返回 replacements、diff、lines added/deleted、新 revision 和 SHA-256。
- 保留 segmented generation 的 placeholder + segment hash 幂等路径。
- 普通编辑不通过“new string 在其他位置出现”推断已经应用。

## 5. 兼容与迁移策略

### 5.1 `search_file` 兼容

- 保留 executor 与 `FileSearchProvider.SearchFile`，避免破坏已有 Worker/Provider。
- 新增 `search_files` 后，默认模型上下文只暴露 `search_files`。
- `search_files` 精确单文件 literal 模式通过 adapter 复用现有 `SearchFile`。
- 旧 Agent config 显式启用 `default.search_file` 时继续可用，并返回 deprecated metadata。
- 完成一个发布周期和配置迁移后，再评估移除模型级 alias。

### 5.2 Provider 兼容

主 `capability.Provider` 暂不增加发现/跨文件搜索方法，避免第三方 Provider 编译中断。新增可选接口：

```go
type FileDiscoveryProvider interface {
    FindFiles(context.Context, FindFilesRequest) (FindFilesResult, error)
}

type FileTreeSearchProvider interface {
    SearchFiles(context.Context, SearchFilesRequest) (SearchFilesResult, error)
}
```

- LocalSystem、WorkspacePathGuard、Onlyboxes 和 WorkerBackedProvider 实现两者。
- 第三方 Provider 未实现时，不暴露对应 API，而不是静默使用 Shell。
- Worker register/heartbeat 必须声明 `default.find_files` / `default.search_files` capability。

### 5.3 Worker 协议

- 继续使用 `tma.work.v1`。
- 新增 API 不改变现有 invocation envelope。
- Worker capability discovery 决定模型可见面。
- Server 和 Worker 使用相同 request/result structs。
- 加入 LocalSystem、WorkerBacked、Onlyboxes 三方一致性 contract tests。

## 6. 工作分解

### Milestone 0：冻结契约和测试夹具

任务：

- `FSV2-001`：确认五个模型工具及兼容 alias。
- `FSV2-002`：定义公共 file metadata、write mode、错误码。
- `FSV2-003`：建立文本、二进制、路径和并发 fixture 集。
- `FSV2-004`：增加 manifest schema lint，所有 object 参数明确 `additionalProperties`。
- `FSV2-005`：记录当前 read/search 性能与内存基线。

退出条件：API 文档、Go structs、JSON Schema 和 fixture 完成评审，尚未修改执行语义。

### Milestone 1：公共文件内核

任务：

- `FSV2-101`：抽取统一 regular-file 检查和有界 MIME detection。
- `FSV2-102`：统一 file metadata 与 revision 返回。
- `FSV2-103`：实现 atomic write helper。
- `FSV2-104`：实现 expected revision / expected absent precondition。
- `FSV2-105`：统一 path guard 对 root、pattern 和文件目标的处理。
- `FSV2-106`：定义 no-progress call signature 与 guardrail 接口。

退出条件：read/write/edit 使用相同 revision 和文件元数据；原子写故障测试通过。

### Milestone 2：读取与二进制分流

任务：

- `FSV2-201`：为 `FileResult` 增加 kind、content type、encoding、suggested capability。
- `FSV2-202`：保持现有 auto/line/byte 行为和兼容测试。
- `FSV2-203`：完善 binary / invalid UTF-8 / unsupported file type 错误和安全文案。
- `FSV2-204`：补充图片、PDF、Office、ZIP、SQLite 和未知 bytes fixture。
- `FSV2-205`：确保 binary 不进入 `ExecutionResult.Content`、Event 或模型上下文。
- `FSV2-206`：增加重复同页读取的 warning/block 测试。

退出条件：所有二进制 fixture 均被有界识别和正确路由，没有原始 Base64 进入模型工具结果。

### Milestone 3：路径发现与跨文件搜索

任务：

- `FSV2-301`：实现 `FindFilesRequest/Result` 和 LocalSystem walker。
- `FSV2-302`：实现 pattern/exclude/hidden/limit/deadline。
- `FSV2-303`：实现 `SearchFilesRequest/Result`，复用单文件 literal engine。
- `FSV2-304`：增加 RE2 regex、case-sensitive 和跨 chunk 匹配测试。
- `FSV2-305`：实现 binary skip、scan bytes/file limits 和 stable sorting。
- `FSV2-306`：实现 PathGuard、Onlyboxes、WorkerBacked adapters。
- `FSV2-307`：将 `search_file` 设为兼容 alias，更新 Model Context 和 system role。

退出条件：不调用 Shell 即可完成仓库路径发现和内容搜索；三种 Provider 结果一致。

### Milestone 4：安全写入与编辑

任务：

- `FSV2-401`：扩展 `WriteFileRequest` 的 mode、precondition 和 checksum。
- `FSV2-402`：替换 LocalSystem/Onlyboxes 直接 `os.WriteFile` 路径。
- `FSV2-403`：扩展 `EditFileRequest` 的 expected revision/match count。
- `FSV2-404`：区分 match not found / not unique / stale revision。
- `FSV2-405`：让 edit 复用 atomic write helper。
- `FSV2-406`：返回新 revision、checksum、diff summary。
- `FSV2-407`：验证 segmented generation、审批 continuation 和 artifact 发布不回归。

退出条件：并发写和编辑不会静默覆盖；失败写不破坏原文件；文件生成门禁仍通过。

### Milestone 5：AgentRuntime、评测和发布

任务：

- `FSV2-501`：更新 tool manifests、system role、错误恢复提示。
- `FSV2-502`：更新 Worker capability negotiation 和 tooling health。
- `FSV2-503`：增加 Agent tool selection fixtures：find vs search vs read。
- `FSV2-504`：增加 partial-read 误报、binary wrong-tool 和重复调用夹具。
- `FSV2-505`：增加指标、Inspector 摘要和错误分类。
- `FSV2-506`：更新 API/SDK 文档、兼容说明和迁移指南。
- `FSV2-507`：灰度启用新工具集，保留旧 alias 观测一个发布周期。

退出条件：质量门禁、Provider parity、Worker transport、Inspector 和文档全部通过。

## 7. 代码影响范围

| 模块 | 主要修改 |
|---|---|
| `internal/capability/provider.go` | 新 request/result、公共 metadata、可选 provider interfaces |
| `internal/capability/read_file.go` | MIME、binary routing、metadata、no-progress support |
| `internal/capability/file_tree.go` | 新增路径发现、跨文件搜索与单文件 adapter |
| `internal/capability/search_file.go` | 保留兼容引擎和 alias |
| `internal/capability/file_write.go` | 新增 precondition 与 atomic write helper |
| `internal/capability/edit.go` | revision、match count、atomic edit、result metadata |
| `internal/capability/path_guard.go` | root/pattern/path scope |
| `internal/capability/onlyboxes.go` | 新 API 映射与 workspace 同步 |
| `internal/execution/worker_provider.go` | find/search transport adapter |
| `internal/workruntime/executor.go` | 新 API work execution 与 artifact 边界 |
| `internal/tools/local_system.go` | 五工具 manifest、兼容 alias、格式化结果 |
| `internal/tools/types.go` | 可选 ModelVisible/Deprecated metadata，如实施所需 |
| `internal/agentruntime` | no-progress guardrail、结果预算和 eval |
| `cmd/tma-worker` | capability declaration 与 contract tests |

## 8. 错误码

第一版固定：

```text
invalid_path
scope_violation
not_found
unsupported_file_type
binary_file
invalid_utf8
invalid_read_range
stale_file_revision
pattern_invalid
search_limit_exceeded
scan_limit_exceeded
match_not_found
match_not_unique
target_exists
target_not_found
checksum_mismatch
write_precondition_failed
atomic_write_failed
no_progress_blocked
```

要求：

- 模型可见错误包含下一步建议。
- 日志和事件不回显文件正文。
- 路径错误只返回授权范围内的 display path。
- `retryable` 由错误类型决定，不让模型自行猜测。

## 9. 测试矩阵

### 文本

- 空文件、无尾换行、CRLF。
- CJK、多字节 offset、非法 UTF-8。
- 超长单行、超大日志、page continuation。
- 读取期间 append、truncate、replace。

### 二进制

- PNG、JPEG、PDF、ZIP、DOCX、XLSX、SQLite。
- 包含 NUL 的伪文本。
- 随机 bytes 和错误扩展名。
- 大型二进制的有界探测。
- 确认模型结果、事件和日志不含 Base64 正文。

### 路径

- `..`、绝对路径、软链接逃逸、软链接交换。
- 设备文件、FIFO、socket、目录。
- hidden、exclude、深目录和循环 symlink。
- Windows path separator 与大小写差异的 provider contract fixture。

### 查找与搜索

- `*`、`**`、exclude、after path。
- literal、regex、case sensitivity。
- 跨 buffer query、超长行 preview。
- binary skip、max files、max bytes、deadline。
- 结果稳定排序和 truncation。

### 写入与编辑

- create/overwrite/precondition 组合。
- stale revision、目标被并发替换。
- 0/1/多次匹配。
- 临时文件写失败、fsync 失败、rename 失败、磁盘满。
- 原文件权限保留策略。
- segmented generation replay 和服务恢复。

## 10. 质量与性能门禁

| 指标 | 门槛 |
|---|---:|
| 非法路径实际执行率 | 0 |
| stale revision 未检测率 | 0 |
| 失败写导致原文件损坏率 | 0 |
| 二进制正文进入模型结果率 | 0 |
| 截断无 continuation 率 | 0 |
| `find_files` / `search_files` 选择正确率 | >= 95% |
| 相同只读调用无进展重复次数 | <= 2 |
| 默认 read page 内存 | O(page size) |
| 跨文件搜索内存 | O(buffer + bounded results) |

性能基线至少覆盖：

```text
10K files / 1 GB workspace
100K files / 10 GB workspace
单个 1 GB text log
单个 1 GB binary file
```

性能测试不要求把所有文件扫完；必须验证达到 deadline、file/byte/result limit 后及时停止。

## 11. 发布策略

1. 先合入公共 metadata、atomic helper 和测试，不改变模型工具面。
2. 灰度启用 `find_files` / `search_files`，保留 `search_file` alias。
3. 写/编辑 precondition 先作为可选参数发布，再在系统提示和默认策略中要求使用。
4. 观察一轮 tool selection、错误修正和旧配置调用数据。
5. 确认旧 Agent config 已迁移后，从默认 Model Context 移除 `search_file`。
6. 不在同一版本同时引入 Tool Search、Process Tool 或新的文档解析工具，避免回归面叠加。

## 12. 本阶段完成定义

只有同时满足以下条件，Filesystem Tools V2 才算完成：

- 模型默认只看到五个边界清晰的文件工具。
- 不依赖 Shell 即可完成路径发现与文本搜索。
- 文本读取和搜索有稳定 continuation、revision 与截断语义。
- 二进制被识别并路由，正文不进入模型上下文。
- 写入与编辑使用原子替换和并发前置条件。
- LocalSystem、Onlyboxes 和 WorkerBacked 行为一致。
- 旧 `search_file` 配置有明确兼容和迁移路径。
- 单元、集成、Agent eval、故障注入和性能门禁全部通过。
