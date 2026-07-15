# Tool Plugin SDK

上位扩展治理、Provider 发现、兼容性、下线和人工切换规则见 [TMA Extension 与 Provider 治理标准](./extension-governance-standard.md)。设置页贡献与配置作用域见 [Extension 设置页与配置贡献标准](./extension-settings-standard.md)。

本文档定义 TMA 后续扩展 worker 能力的最小标准。目标是让第三方在不修改 `tma-server` 核心代码的情况下，把机器人、桌面控制、Office、ERP、浏览器增强、视觉解析等能力接入现有 `worker_work` 队列。

## 核心边界

`worker_work` 是调度与生命周期 envelope，不承载具体业务能力。

```text
worker_work:
  queue / lease / heartbeat / cancel / requeue / audit / result
```

具体能力放在 `tool_execution` payload 的 `namespace.api` 中：

```json
{
  "work_type": "tool_execution",
  "payload": {
    "protocol_version": "tma.work.v1",
    "namespace": "robot",
    "api": "get_state",
    "runtime": "local_system",
    "capabilities": ["robot.state"],
    "risk": "read",
    "input": {}
  }
}
```

因此第一原则是：

- 新增工具能力：优先新增 `namespace.api`。
- 新增执行生命周期语义：才考虑新增 `work_type`。
- `work_type` 应保持少量稳定，不应该为 `computer_click`、`robot_move`、`excel_export` 等业务动作新增类型。

## 当前支持状态

当前 core 已支持：

- `work_type=tool_execution`，payload 使用 `tma.work.v1`。
- worker 注册与 heartbeat 上报 `WorkerCapabilities`。
- server 按 `namespace` / `api` / `runtime` / `capabilities` 选择在线 worker。
- `tma-worker --plugin /path/to/plugin` 加载进程型工具插件。
- 自定义 namespace，例如 `robot`、`computer`、`office`。
- worker capabilities 会携带插件 manifest；server 可根据在线 worker manifest 暴露插件工具给 AgentRuntime。

当前暂未完成：

- 独立发布的多语言 SDK 包。
- 插件包安装、版本协商、签名和权限沙箱。
- 持久化 server 侧插件 manifest registry。当前 manifest 来自在线 worker heartbeat；worker 离线后相关工具不会暴露给 AgentRuntime。
- HTTP/gRPC/MCP sidecar 插件适配器。

这意味着当前已经可以开发和运行新的 worker plugin；只是还不是一个单独发版的 SDK 包。对外 SDK 的第一版应在本协议稳定后，把 manifest 校验、stdin/stdout 编解码、错误格式、artifact export 和本地调试命令封装成语言库。

## 进程型插件协议

进程型插件是第一版推荐形态。插件是一个可执行文件，必须支持两个子命令：

```bash
plugin manifest
plugin execute
```

`manifest` 输出一个 `tma.tools.manifest.v1` 兼容 JSON：

```json
{
  "identifier": "robot",
  "type": "process_plugin",
  "meta": {
    "title": "Robot",
    "description": "Robot control tools."
  },
  "system_role": "Use robot.* tools only for robot control tasks.",
  "api": [
    {
      "name": "get_state",
      "description": "Read robot state.",
      "parameters": {
        "type": "object",
        "properties": {}
      },
      "capabilities": ["robot.state"],
      "risk": "read",
      "runtime": {
        "allowed": ["local_system"],
        "preferred": "local_system"
      },
      "implementation": "worker_capability"
    }
  ]
}
```

`execute` 从 stdin 读取：

```json
{
  "protocol_version": "tma.plugin.v1",
  "call": {
    "id": "work_000001",
    "identifier": "robot",
    "api_name": "get_state",
    "arguments": {}
  },
  "context": {
    "workspace_id": "wksp_default",
    "session_id": "sesn_000001",
    "environment_id": "env_000001",
    "turn_id": "turn_000001"
  }
}
```

`execute` 向 stdout 写入：

```json
{
  "protocol_version": "tma.plugin_result.v1",
  "success": true,
  "content": "robot state: idle",
  "state": {
    "status": "idle"
  }
}
```

带 artifact 的结果：

```json
{
  "protocol_version": "tma.plugin_result.v1",
  "success": true,
  "content": "camera frame captured",
  "state": {
    "frame_id": "frame-001",
    "width": 1280,
    "height": 720
  },
  "exported_files": [
    {
      "path": "/tmp/robot-frame-001.png",
      "name": "robot-frame.png",
      "artifact_type": "asset",
      "content_type": "image/png"
    }
  ]
}
```

artifact 规则：

- 插件只返回本机文件路径和 metadata，不要把大二进制塞进 `state`。
- worker 会读取 `exported_files` 指向的文件。有 `session_id` 且 uploader 可用时，`image/*` 和超过传输阈值的文件会上传成 session artifact ref。
- 直接用 `work enqueue` 手动调试且不带 `session_id` 时，worker 无法创建 session artifact；这类测试应只检查本机路径或使用带 session 的 agent 流程。
- `content` 应是短文本摘要；结构化详情放 `state`；大内容放 artifact。

失败时：

```json
{
  "protocol_version": "tma.plugin_result.v1",
  "success": false,
  "content": "move rejected",
  "error": {
    "type": "safety_denied",
    "message": "target is outside allowed safety zone"
  }
}
```

## Worker 启动

加载一个插件：

```bash
bin/tma-worker \
  --base-url http://localhost:8080 \
  --name lab-robot-worker \
  --workspace wksp_default \
  --plugin /opt/tma/plugins/robot
```

也可以通过环境变量配置多个插件：

```bash
export TMA_WORKER_PLUGINS="/opt/tma/plugins/robot,/opt/tma/plugins/computer-cua"
bin/tma-worker --base-url http://localhost:8080 --name mixed-worker
```

worker 会把插件 manifest 合并进本地 tool registry，并在注册/heartbeat 时上报能力，例如：

```json
{
  "namespaces": ["default", "browser", "robot"],
  "apis": ["robot.get_state"],
  "runtimes": ["local_system"],
  "capabilities": ["robot.state"]
}
```

当 agent config 启用插件 namespace，且同 workspace 有匹配在线 worker 时，AgentRuntime 会把插件工具 schema 暴露给模型：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --tools '{"tools":["robot"],"runtime":"local_system"}'
```

模型调用 `robot.get_state` 后，server 会通过标准 `tool_execution` work 下发给匹配 worker；server 本身不加载插件进程。

## 新插件开发 Checklist

开发新的 work 集成时，先判断是新增工具能力还是新增生命周期：

| 需求 | 做法 |
|---|---|
| 新设备、新软件、新业务系统能力 | 新增 process plugin namespace/API |
| 已有 API 需要另一个运行位置 | 新增 worker/provider 能力或 runtime policy |
| 需要队列、租约、取消、重试、审计语义变化 | 才考虑新增 `work_type` |

推荐步骤：

1. 选择稳定 namespace，例如 `robot`、`erp`、`office`、`lab_device`。
2. 设计小而明确的 API，例如 `get_state`、`move_to`、`stop`、`capture_frame`。
3. 给每个 API 写 JSON Schema，禁止不必要的 `additionalProperties`。
4. 声明最小 capabilities，例如 `robot.state`、`robot.motion.write`、`robot.camera.read`。
5. 标注 `risk`。读取状态用 `read`；改变外部世界用 `write`；执行任意代码才用 `exec`。
6. 把长结果和二进制产物放 `exported_files`，只在 `state` 放摘要。
7. 写 direct plugin smoke：`plugin manifest` 和 `plugin execute`。
8. 写 worker smoke：启动 `tma-worker --plugin`，用 `worker diagnose` 和 `work enqueue` 验证。
9. 写 agent smoke：agent tools 启用 namespace，fake/真实 LLM 触发工具调用，校验事件和 artifact。

命名建议：

- API 用动词或动词短语：`get_state`、`read_camera`、`move_to`、`stop`。
- capability 用领域加动作：`robot.state`、`robot.motion.write`、`computer.screen.capture`。
- 不把 runtime 写进 namespace：不要设计 `local_robot.*`；用 `runtime=local_system` 表达运行位置。

## 跨平台 Worker

同一个 namespace 可以有多个 worker 实现。比如 `computer.*` 可以由 macOS CUA worker、Windows UIA worker、Linux AT-SPI worker 或远程桌面 worker 提供。

要求：

- 所有实现共享同一 API schema 和 result 基本结构。
- 平台差异放在 `state.backend`、`state.platform`、`state.coverage` 或 `state.warning` 中。
- worker heartbeat 必须准确声明 capabilities；不支持的能力不要声明。
- 调度时尽量通过 capabilities 和 runtime 匹配，而不是在模型提示里写死机器类型。

示例：

```text
robot.get_state
  local lab arm worker: capabilities robot.state, robot.motion.write
  remote sim worker: capabilities robot.state, robot.simulation
  camera-only worker: capabilities robot.state, robot.camera.read
```

## 安全和审批

插件必须假设模型可能会给出错误参数。插件自己要做边界校验，server policy 负责更高层审批。

建议：

- 对物理设备、支付、删除、外发消息、生产系统写入等动作，API 参数中提供 `dry_run` 或 `confirm_token` 之类的上层审批钩子。
- 危险动作返回可解释错误，不要静默降级执行其他动作。
- 插件日志写 stderr；stdout 只输出协议 JSON。
- 不把 worker token、数据库 DSN、对象存储密钥、设备密钥放进 `content` 或 `state`。
- `state` 中可包含 `verified:false`、`effect:"unverifiable"` 等 backend 置信度，方便上层决定是否追加读取/截图验证。

## SDK 包路线

对外 SDK 的合理分层：

| 层 | 内容 | 当前状态 |
|---|---|---|
| Protocol | `tma.plugin.v1` / `tma.plugin_result.v1` / manifest JSON | 已可用 |
| Helper SDK | manifest builder、schema helper、execute router、error/result/artifact helper | 待独立打包 |
| Adapters | HTTP/gRPC/MCP sidecar 到 process plugin 的适配 | 待实现 |
| Packaging | 插件安装、版本、签名、权限声明、marketplace | 待实现 |
| Certification | manifest lint、direct smoke、worker smoke、agent smoke | 已有脚本雏形 |

当前开发者可以直接按本文档写可执行插件；未来 SDK 只是把重复样板收进库里，不改变核心协议。

## 示例插件

仓库内置了一个最小机器人插件示例：

```bash
examples/plugins/robot-shell/robot-plugin.py manifest
echo '{"protocol_version":"tma.plugin.v1","call":{"id":"call_1","identifier":"robot","api_name":"get_state","arguments":{}},"context":{"workspace_id":"wksp_default"}}' \
  | examples/plugins/robot-shell/robot-plugin.py execute
```

接入 worker：

```bash
bin/tma-worker \
  --base-url http://localhost:8080 \
  --name robot-worker \
  --workspace wksp_default \
  --plugin examples/plugins/robot-shell/robot-plugin.py
```

检查 server 是否看见插件能力：

```bash
bin/tma worker list --workspace wksp_default --status online
bin/tma worker diagnose --namespace robot --api get_state --capabilities robot.state --runtime local_system
```

## Computer-use 插件

仓库内置了一个 `computer` 插件示例。详细 API contract、CUA backend、AX/UI tree fallback、权限边界和验收方式见 [computer-use-plugin.md](./computer-use-plugin.md)。

```bash
examples/plugins/computer-use/computer-plugin.py manifest
echo '{"protocol_version":"tma.plugin.v1","call":{"id":"call_1","identifier":"computer","api_name":"get_state","arguments":{"capture_mode":"ax"}},"context":{"workspace_id":"wksp_default"}}' \
  | TMA_COMPUTER_BACKEND=stub examples/plugins/computer-use/computer-plugin.py execute
```

它声明这些 API：

```text
computer.list_windows / computer.get_state / computer.click
computer.type_text / computer.hotkey / computer.launch_app
computer.open_url / computer.search_web
computer.bring_to_front / computer.screenshot
```

后端选择由 `TMA_COMPUTER_BACKEND=auto|cua|ax|stub` 控制。生产建议优先 CUA；AX/UI tree 是轻量 inspect fallback；stub 只用于验收。不引入 OmniParser。

接入 worker：

```bash
TMA_COMPUTER_BACKEND=auto \
bin/tma-worker \
  --base-url http://localhost:8080 \
  --name computer-worker \
  --workspace wksp_default \
  --plugin examples/plugins/computer-use/computer-plugin.py
```

启用 agent tools：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --tools '{"tools":["computer"],"runtime":"local_system"}'
```

验收：

```bash
make verify-computer-plugin-tools
```

## 机器人示例边界

机器人控制不应新增 `robot_move` 这类 `work_type`。推荐：

```text
work_type = tool_execution
namespace = robot
api = move_to / get_state / stop / read_camera
```

对于物理世界动作，插件 manifest 应显式声明更高风险和人工干预策略。当前 `risk` 只有 `read` / `write` / `exec`，后续可引入 `physical`，并在 server policy 中默认要求人工审批。

## 设计要求

插件实现必须遵守：

- 不直接写 TMA 数据库。
- 不绕过 server 上传长期产物；大文件应通过 artifact/object API 回传引用。
- 不把 worker token、数据库 DSN、对象存储密钥暴露给 LLM。
- 对物理设备、支付、删除、外发等高风险动作声明清楚风险，并支持 dry-run 或人工确认。
- stdout 只输出协议 JSON；日志写 stderr。
