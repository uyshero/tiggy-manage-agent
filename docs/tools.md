# 工具系统

## 契约

工具名使用 `<namespace>_<api>`，例如 `default_read_file`。Manifest 内部仍分别定义 identifier、
version、API、input schema、capabilities、risk、runtime 和审批元数据。模型看到的 schema、
preflight 和 execute 必须来自同一不可变 Snapshot。

输入使用 JSON Schema Draft 2020-12 校验，支持 required、enum、范围、组合约束和
`additionalProperties`，禁止外部 `$ref`。Runtime 在审批前校验，Executor 在执行边界再次
校验。错误只返回 instance/constraint path，不回显敏感参数。

工具结果分为：

- `Content`：给模型的有界内容。
- `State`：状态、分页、revision、截断和 Artifact metadata。
- `IsError`/错误码：安全、稳定、可重试语义。
- Artifact：二进制或大结果的对象引用，不直接进入模型上下文。

## 权限

权限判定顺序固定为：平台 deny、Workspace deny、Agent deny、路径/资源规则、审批策略、
默认策略。deny 始终优先，旧批准不能覆盖新的 policy snapshot。

Manifest policy 使用 `allow`、`ask`、`deny`；路径规则支持 Workspace 相对路径、具体前缀
和只读/写入区分。路径必须 canonicalize，并在打开文件时防御 symlink 和 TOCTOU。命令、
网络、浏览器提交和凭据使用应声明独立审批原因，不能仅用 read/write/exec 粗分类。

Session 可通过带 `If-Match` revision 的 runtime settings 热更新：

- `request_approval`：敏感调用暂停并等待人工决定。
- `approve_for_me`：记录自动批准事件后执行。
- `full_access`：仍执行硬性 deny 和沙箱边界，其余直接执行。

审批 Handler 只持久化决定并唤醒 Runner，不在请求协程执行工具。批准、拒绝、取消和过期
都写入 durable journal。权限审计从固化快照、审批事件和 journal 投影，按
`created_at + turn_id + call_id` 游标分页。

## 内置文件能力

稳定 API 为 `read_file`、`find_files`、`search_file`、`write_file`、`edit_file`。核心规则：

- raw byte 模式与 1-based line 模式互斥。
- 返回 size、offset/line range、next cursor、EOF 和 revision。
- UTF-8 不从 code point 中间切断；二进制返回 Artifact/metadata。
- 搜索流式处理并限制命中数、单行长度和总字节。
- 写入采用临时文件、fsync/rename 或平台等价原子流程。
- 编辑和覆盖写支持 expected revision，冲突返回可恢复错误。
- 本地、Onlyboxes 和 Worker Provider 保持同一结果语义。

字节和行数硬限制见 [configuration.md](./configuration.md)。回归入口为
`make eval-filesystem-tools` 和相关 capability/tool tests，完整命令见
[`TESTING.md`](../TESTING.md)。

## 内置图片能力

默认 Registry 暴露两个 Server 内置工具：

- `image_generate`：通过 ShuYou `gpt-image-2` 异步任务生成或编辑图片，支持带角色的参考图和可选 mask。
- `image_analyze`：分析工作区图片、base64 `data:image` URL 或无凭据的 HTTPS 图片 URL。

`image_generate` 不经过 LLM Provider Adapter：固定调用
`https://coder.shuyou.ai/v1/predictions`，固定模型 `gpt-image-2`，并只从当前沙箱托管环境变量读取
`SHUYOU_API_KEY`。Runtime 自动执行 POST 创建、GET 轮询（默认每 5 秒，最长 10 分钟）、解析
`data.output[].image`、下载所有结果并持久化为 Session artifacts。`image_analyze` 仍使用
`is_default_vision=true` 的 `text_image` 配置模型及其 Provider 凭证。

运行时对明确的图片生成/编辑请求启用 `builtin.image_generation_execution` 完成校验。若模型只
回复“正在生成”或“马上画”而本轮没有尝试 `image_generate`，该回复不会发布给用户；校验器会把
明确反馈送回同一 Agent Loop，要求模型先调用工具。能力咨询（例如“你会画图吗”“画图工具叫啥”）
不触发强制调用。工具不可用时校验器也不会制造无效重试。

生成提示词遵循本机最新 imagegen skill：调用方必须选择精确的 use-case taxonomy slug；Runtime
按 `Use case`、`Asset type`、`Primary request`、`Input images`、场景、主体、风格、构图、光线、
色彩、材质、逐字文本、约束和 Avoid 的顺序构造最终 brief。每张输入图片必须标明角色，编辑类
use case 必须声明“只改什么、保持什么不变”。具体提示已足够详细时只做结构化整理，不自动添加
角色、物件、品牌、口号、配色或叙事元素。最终提示词写入 Tool State，便于审计与复现。

生成参数支持 `size`、`resolution`（1K/2K/4K）、`quality`、`aspect_ratio`、`output_format`、
`output_compression` 和 1～10 张结果。参考图可使用工作区 `path`（读取后转换为 data URL）或
公网 HTTPS `url`，mask 同样支持工作区 PNG 或 HTTPS URL。生成结果以二进制 `ExportedFiles`
返回，由标准 Tool Artifact Recorder 写入对象存储并绑定到当前 Session。工作区输入单张上限
20 MiB，结果下载单张上限 50 MiB；缺少密钥时返回 `shuyou_api_key_not_configured`，视觉模型
未配置时返回 `vision_model_not_configured`。

## 进程插件

Worker 用 `--plugin PATH` 或 `TMA_WORKER_PLUGINS` 加载进程插件。协议流程为：

1. Worker 启动插件并读取 manifest。
2. 校验 identifier、版本、namespace、API schema 和 capability。
3. 通过 heartbeat 上报可用能力。
4. Server 仍下发标准 `tool_execution` work，不增加插件专用 work type。
5. Worker 通过 JSON request/response 调用插件，并统一处理 timeout、取消、输出预算和错误。

插件不得直接访问 TMA 数据库、伪造 Workspace 身份或绕过审批。Secret 通过环境引用或受控
配置注入，不出现在 manifest、日志或状态 API。升级必须保持 API schema 兼容，破坏性变化
使用新 major version/identifier。

最小验收包括 manifest 校验、发现/下线、成功与错误调用、取消、超时、输出截断、审批、
Workspace 隔离、Worker 重启和版本不兼容。

## Computer Use

Computer Use 是 Worker 进程插件示例，能力域为 `computer.*`。典型 API 包括 screenshot、
click、type、key、scroll、move、wait 和可选 UI tree/AX 查询。截图写入 Artifact，只把引用和
尺寸等 metadata 返回模型。

Backend 可以是 CUA、可访问性树或平台自动化实现，但必须：

- 绑定明确的 session/display，不复用其他租户状态。
- 对点击、输入、提交和凭据动作应用审批策略。
- 限制坐标、文本长度、等待时间和截图尺寸。
- 在取消或 Worker draining 时停止后续动作。
- 不将屏幕中的 secret、token 或完整表单写入日志。

Fake backend smoke 用于验证映射和 Artifact 链路，不代表真实桌面控制已通过认证。

## Tool Runtime 与 MCP

MCP 只是 Manifest 的动态来源，不是可信边界。它暴露的工具进入同一 Registry、schema、
权限、审批、预算和审计链路。MCP 专有配置与传输规则见 [mcp.md](./mcp.md)。
