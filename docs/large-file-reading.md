# 大文件分页读取设计与调研

本文记录 `default.read_file` 企业级分页读取的参考实现、取舍和 TMA 协议。外部项目仅作为设计证据；实现继续沿用 TMA 的 Registry、Capability Provider、AgentRuntime、Onlyboxes 和 Tool Result 边界。

## 参考实现

| 项目 | 参考文件或文档 | 观察 | TMA 决策 |
| --- | --- | --- | --- |
| Hermes Agent | [`file_operations.py`](https://github.com/NousResearch/hermes-agent/blob/205ed71ba0e55d1b34083e9db52fee732aa7038e/tools/file_operations.py#L567), [`file_tools.py`](https://github.com/NousResearch/hermes-agent/blob/205ed71ba0e55d1b34083e9db52fee732aa7038e/tools/file_tools.py#L692) | 1-based 行 `offset/limit`、硬行数/字符数限制、重复 page 去重、`search_files` 定位和继续读取提示；shell backend 用 `sed`/`wc`，可跨 local/docker/SSH。 | 采用明确 continuation、重复页提示和安全搜索；拒绝依赖 shell 文本工具、每页 `wc -l` 全文件扫描和进程内去重状态。 |
| OpenHands SDK | [`gemini/read_file/definition.py`](https://github.com/OpenHands/software-agent-sdk/blob/51c102b9c0348bbdd4e6a84b1ac4199e0d77f827/openhands-tools/openhands/tools/gemini/read_file/definition.py), [`impl.py`](https://github.com/OpenHands/software-agent-sdk/blob/51c102b9c0348bbdd4e6a84b1ac4199e0d77f827/openhands-tools/openhands/tools/gemini/read_file/impl.py), [`constants.py`](https://github.com/OpenHands/software-agent-sdk/blob/51c102b9c0348bbdd4e6a84b1ac4199e0d77f827/openhands-tools/openhands/tools/file_editor/utils/constants.py) | 行 offset/limit 与结构化 `is_truncated/lines_shown/total_lines`，并提示先 grep；当前实现仍使用 `readlines()` 读取全部内容。File editor 对过长输出加 `<response clipped>`。 | 采用结构化截断状态和 grep-first 提示；拒绝 `readlines()` 和只在模型输出阶段 clipping。 |
| OpenCode | [`read.ts`](https://github.com/anomalyco/opencode/blob/08fb47373509ba64b13441061314eeacf4264f51/packages/opencode/src/tool/read.ts), [`read-filesystem.ts`](https://github.com/anomalyco/opencode/blob/08fb47373509ba64b13441061314eeacf4264f51/packages/core/src/tool/read-filesystem.ts) | 流式读取，最多 2000 行、50 KiB 和单行 2000 字符；`TextDecoder` 流式处理 UTF-8，保留无结尾换行；提示用 grep 定位。行 offset 仍需从文件开头扫描。 | 采用同时限制字节、行数、单行预览和保留 CRLF/无尾换行；TMA 增加 byte mode，避免随机访问必须扫描前缀。 |
| Codex | [`file_read.rs`](https://github.com/openai/codex/blob/315195492c80fdade38e917c18f9584efd599304/codex-rs/exec-server/src/file_read.rs), [`remote_file_stream.rs`](https://github.com/openai/codex/blob/315195492c80fdade38e917c18f9584efd599304/codex-rs/exec-server/src/remote_file_stream.rs), [`tool_output.rs`](https://github.com/openai/codex/blob/315195492c80fdade38e917c18f9584efd599304/codex-rs/tools/src/tool_output.rs) | 远程文件系统使用有硬上限的 `read_at(offset,len)` 块协议；打开的 handle 在路径被替换后仍读同一个 inode；日志 preview 在 UTF-8 字符边界截断。 | 采用 ReaderAt 风格 byte offset、服务端硬上限和 UTF-8 边界；TMA Worker work 是无状态调用，因此用 stat revision 代替跨调用打开句柄。 |
| Aider | [`io.py`](https://github.com/Aider-AI/aider/blob/5dc9490bb35f9729ef2c95d00a19ccd30c26339c/aider/io.py#L453), [`repomap.py`](https://github.com/Aider-AI/aider/blob/5dc9490bb35f9729ef2c95d00a19ccd30c26339c/aider/repomap.py), [`base_coder.py`](https://github.com/Aider-AI/aider/blob/5dc9490bb35f9729ef2c95d00a19ccd30c26339c/aider/coders/base_coder.py#L598) | chat files 仍整文件读取；大型仓库依靠 token-bounded repo map、符号定位和只加入必要文件降低上下文。 | 不采用其整文件 chat 注入；采用“先定位再读取必要窗口”的思路，源代码优先搜索符号或关键词。 |
| Claude Code | [公开仓库](https://github.com/anthropics/claude-code) | 产品公开接口提供行 offset/limit 类 Read 工具，但执行内核不是开源代码，无法核验内存、revision 或远程一致性。 | 仅把公开交互习惯作为模型可用性参考，不推断或复制内部实现。 |

## 协议

Byte mode 是跨 Provider 的主协议，offset 始终是原始文件 byte offset：

```json
{
  "path": "logs/app.log",
  "offset_bytes": 0,
  "max_bytes": 32768,
  "file_revision": "stat-v1:..."
}
```

Line mode 是面向源代码和人工定位的便利层，行号为 1-based：

```json
{
  "path": "src/main.go",
  "start_line": 200,
  "max_lines": 120,
  "file_revision": "stat-v1:..."
}
```

互斥规则：`offset_bytes/max_bytes` 中任一字段出现即为 byte mode，`start_line/max_lines` 中任一字段出现即为 line mode，两组不能同时出现。`path` 单独调用为 auto mode：不超过 `read_file_small_file_bytes` 时完整返回，否则返回受 `read_file_default_max_bytes` 限制的首块。

成功结果的 `ExecutionResult.Content` 只包含本页文本。`ExecutionResult.State` 保留以下 metadata，不复制页内容：

```json
{
  "path": "logs/app.log",
  "size_bytes": 123456,
  "offset_bytes": 0,
  "returned_bytes": 8192,
  "start_line": 1,
  "end_line": 104,
  "next_offset_bytes": 8192,
  "eof": false,
  "truncated": true,
  "file_revision": "stat-v1:...",
  "mode": "byte"
}
```

非首个 byte page 无法在 O(page size) I/O 内得出绝对行号，`start_line/end_line` 固定为 `0`（unknown）；不要为填这两个字段从头扫描超大文件。Line mode 和 offset 0 的 byte mode返回可确定行号。若 offset 落在 UTF-8 continuation byte，实际 offset 向前进到下一个 rune 边界，并通过 `requested_offset_bytes` 保留请求值。

## 一致性与错误

`file_revision` 是 stat fingerprint：版本、size、mtime、mode 和平台可用的 device/inode 或 file index，经 SHA-256 压缩；不会读取或哈希完整文件。每次读取在开始和结束时检查打开文件的 revision。继续分页时传回旧 revision，文件被追加、截断、原地修改或替换会返回：

```json
{
  "error": {
    "code": "stale_file_revision",
    "message": "file changed between paginated reads; restart from the first page using the new file_revision",
    "metadata": {
      "expected_file_revision": "...",
      "actual_file_revision": "..."
    }
  }
}
```

其他可恢复错误包括 `invalid_read_range`、`read_limit_exceeded`、`offset_out_of_range`、`line_out_of_range`、`unsupported_read_pagination` 和 `invalid_utf8`。Provider 不静默 clamp 模型请求；硬上限超限必须显式失败。context cancellation 和 `RequestMeta.deadline` 会在打开前、流式 skip/read/search 循环和特殊格式有界读取循环中检查。

选择 stat revision 而不是 cursor 的原因：TMA 的 local、Onlyboxes 和 Worker-backed 调用都是同一 `tma.work.v1` 无状态 work，持久 cursor 会引入 server/worker affinity、句柄回收和断线恢复状态。stat revision 能在三种执行面维持同一 JSON 合约。未来若 Worker transport 提供有生命周期的文件 handle，可在协议新版本中增加 cursor，但不能替代当前无状态字段。

## UTF-8 与文件类型

- Byte offset 绝不是 rune index。页首向下一个 UTF-8 边界对齐；页尾回退到上一个完整 rune，中文和 emoji 不产生损坏文本。
- 原始 CRLF、LF 和无结尾换行均原样保留。
- Line mode 同时受 `max_lines` 和默认 byte page 限制。超大单行可在 rune 边界停止，`line_truncated=true`，后续用 `next_offset_bytes` 切换到 byte mode。
- 首部样本和当前页继续执行 UTF-8/NUL 二进制检测。二进制结果只返回安全提示和 metadata。
- DOCX 需要 ZIP 中央目录才能提取文本，不能按原始 byte page 得到有意义文本。为保持兼容，path-only 仍执行有 64 MiB package 上限的文本提取；显式 byte/line mode 返回 `unsupported_read_pagination`。
- 超大 JSON/JSONL、CSV、日志优先 `search_file`、格式感知解析器或有界命令统计；源代码优先符号/关键词定位；不要把顺序遍历数百页当作“理解文件”。

## 搜索与上下文预算

`default.search_file` 是 `filesystem.read`、`risk=read` 的单文件字面量搜索，不经过 exec 审批。它流式扫描、最多返回 100 个匹配行，单行 preview 最多 4096 bytes，并给出 1-based 行号、原始 match byte offset 和同一 `file_revision`。典型流程是：

1. `search_file(path, query)` 定位。
2. 带 search 返回的 `file_revision` 调用 line window，或从 match `offset_bytes` 附近调用 byte window。
3. 只按 `next_offset_bytes` 继续必要页，不重复相同 page。
4. 最终说明检查过的范围。

默认 8192-byte page 小于默认 `tool_result_context_max_chars=12000`，为 JSON envelope 和 metadata 留出空间。文件尚未读完由 State 的 `truncated=true/eof=false` 表示；若 Session 把上下文预算设得更小导致二次裁剪，模型可见 State 会额外出现 `model_context_truncated=true`，并保留 original/visible char 数。两种截断不能混为一谈。

Worker capability transport 为重建 `capability.FileResult` 会在内部 work result State 携带当前普通文本页；server-facing Runtime 立即转换为 metadata-only State。普通文本链路只传当前有界页，不为绕过上下文预算复制完整大文本文件或创建额外 artifact。DOCX path-only 提取是保留兼容性的例外，传输受 64 MiB package 上限保护；DOCX 不支持分页。

## 配置

Server 与 `tma-worker` 都读取并校验：

- `TMA_READ_FILE_DEFAULT_MAX_BYTES=8192`
- `TMA_READ_FILE_HARD_MAX_BYTES=65536`
- `TMA_READ_FILE_SMALL_FILE_BYTES=8192`
- `TMA_READ_FILE_MAX_LINES=400`

Byte 配置范围为 256 bytes 到 1 MiB，且 `small <= default <= hard`；行上限为 1 到 5000。Session runtime settings 不允许覆盖这些部署资源边界。Server 和 Worker 会分别拒绝无效启动配置；即使 schema 或 transport 被绕过，最终 Provider 仍再次校验请求。

## Provider 语义

- `LocalSystemProvider` 使用 `os.Open`、`ReadAt` 和 `bufio.Reader`，普通文本读取内存为 O(page size)。
- `WorkspacePathGuardProvider` 在调用 read/search 前沿用 workspace root、符号链接和 existing-prefix 校验，不扩大可读路径。
- `OnlyboxesProvider` 沿用 Session 文件同步和 `/workspace`、`/mnt/data` host 映射，成功结果与结构化错误都映射回 sandbox 路径。
- `WorkerBackedProvider` 通过 Registry manifest 和 `tma.work.v1` 传递同一请求/结果；Worker 端再次执行相同 limits、revision 和 UTF-8 校验。
