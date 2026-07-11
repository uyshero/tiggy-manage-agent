# Computer-use Plugin Standard

本文档定义 TMA 第一版 `computer.*` worker 插件标准。目标是让桌面控制能力通过 `tool_execution + namespace.api` 接入，而不是新增 `work_type`。

当前实现参考：

```text
examples/plugins/computer-use/computer-plugin.py
```

该插件面向本地 worker：

```text
tma-worker --plugin examples/plugins/computer-use/computer-plugin.py
```

OmniParser 不属于当前标准，不自动部署，也不是插件依赖。

## 范围

`computer.*` 表达“操作整台电脑或桌面环境”的能力，区别于 `browser.*`：

| namespace | 用途 |
|---|---|
| `browser.*` | 浏览器页面导航、DOM/Playwright 交互、网页截图 |
| `computer.*` | OS 桌面、应用窗口、鼠标键盘、屏幕截图、AX/UI tree |

如果任务只需要网页交互，优先用 `browser.*`。只有需要控制原生应用、系统窗口、跨应用流程、远程桌面或本机桌面时才启用 `computer.*`。

`computer.*` 分两层：

- 高层意图 API，例如 `computer.open_url`、`computer.search_web`，用于把常见桌面动作收敛成少量稳定调用。
- 低层原语 API，例如 `computer.hotkey`、`computer.type_text`、`computer.click`，用于精确控制和调试。底层 backend 需要的 `pid`、快捷键别名等细节由插件尽量自动解析，不要求用户或模型在自然语言里手写。

## Manifest Contract

插件 manifest 必须满足：

```json
{
  "identifier": "computer",
  "type": "process_plugin",
  "api": [
    {
      "name": "get_state",
      "namespace": "computer",
      "api": "get_state",
      "runtime": {
        "allowed": ["local_system"],
        "preferred": "local_system"
      },
      "implementation": "worker_capability"
    }
  ]
}
```

要求：

- `identifier` 固定为 `computer`。
- 所有 API 的 `namespace` 固定为 `computer`。
- `runtime.allowed` 第一版只声明 `local_system`。
- `implementation` 必须是 `worker_capability`。
- 插件必须通过 `tma-worker --plugin` 加载，由 worker heartbeat 上报 manifest。
- server 不加载 CUA、AX、桌面自动化库；server 只负责调度、审计、事件和 artifact。

## API Contract

### `computer.list_windows`

读取当前桌面可见窗口或可操作应用。

```json
{}
```

Capabilities:

```text
computer.window.read
```

Risk:

```text
read
```

返回 state 建议：

```json
{
  "backend": "cua",
  "windows": [
    {
      "id": "window-1",
      "app": "Finder",
      "title": "Downloads",
      "bounds": {"x": 0, "y": 0, "width": 1200, "height": 800}
    }
  ]
}
```

### `computer.get_state`

读取当前电脑状态。默认优先返回结构化 UI tree；必要时可带截图、窗口信息或 backend 原始摘要。

参数：

```json
{
  "capture_mode": "ax",
  "window_id": "optional-window-id",
  "app": "optional-app-name"
}
```

`capture_mode` 允许值：

```text
ax | screenshot | vision | som
```

当前标准建议：

- 默认使用 `ax`，因为它结构化、低算力、可解释。
- `screenshot` 可用于确认视觉状态。
- `vision` / `som` 只作为 CUA backend 可能支持的模式传递，不要求 TMA core 理解。
- 不使用 OmniParser。

Capabilities:

```text
computer.state.read
computer.ax.read
```

Risk:

```text
read
```

返回 state 建议：

```json
{
  "backend": "ax",
  "platform": "darwin",
  "capture_mode": "ax",
  "ui_tree": {
    "role": "application",
    "name": "Finder",
    "children": [
      {
        "role": "window",
        "name": "Downloads",
        "children": [
          {
            "id": "ax-1",
            "role": "button",
            "name": "Back"
          }
        ]
      }
    ]
  }
}
```

### `computer.click`

点击坐标、窗口内元素或 backend 元素引用。`pid` 可选；省略时插件会尝试解析指定 `app` / `name`，否则使用当前前台窗口。

参数：

```json
{
  "pid": 4506,
  "app": "Google Chrome",
  "x": 120,
  "y": 300,
  "button": "left",
  "window_id": "optional-window-id",
  "element_id": "optional-element-id"
}
```

Capabilities:

```text
computer.input.mouse
```

Risk:

```text
write
```

策略建议：

- 模型应先调用 `computer.get_state`，再调用 `computer.click`。
- 如果同时提供 `element_id` 和坐标，backend 可优先使用 `element_id`。
- 物理设备、支付、删除、发送外部消息等场景应由上层审批策略拦截。

### `computer.type_text`

向目标应用输入文字。`pid` 可选；省略时插件会尝试解析指定 `app` / `name`，否则使用当前前台窗口。CUA backend 仍可能在内部需要 `pid`，但这是插件责任，不应暴露给用户。

参数：

```json
{
  "pid": 4506,
  "app": "Google Chrome",
  "text": "hello",
  "delivery_mode": "foreground"
}
```

Capabilities:

```text
computer.input.keyboard
```

Risk:

```text
write
```

返回语义：

- `status=completed` 表示 TMA worker 已成功调用 backend。
- CUA 可能返回 `verified:false` / `effect:"unverifiable"`。这不是 TMA 失败，而是 backend 只能确认键盘事件已发送，不能从目标 UI 反向证明文字确实落入了输入框。
- 上层如果需要强确认，应在输入后追加 `computer.get_state`、`computer.screenshot` 或应用级读取工具做验证。

### `computer.hotkey`

向目标应用发送按键组合。`pid` 可选；省略时插件会尝试解析指定 `app` / `name`，否则使用当前前台窗口。快捷键是否生效取决于目标应用是否可接收事件、窗口是否被系统允许激活、当前 Space/焦点状态，以及系统权限。

参数：

```json
{
  "pid": 4506,
  "app": "Google Chrome",
  "keys": ["cmd", "l"]
}
```

插件会规范常见按键别名，例如 `Command` / `command` / `⌘` -> `cmd`，`Return` -> `enter`，`Esc` -> `escape`。

Capabilities:

```text
computer.input.keyboard
```

Risk:

```text
write
```

返回语义同 `computer.type_text`：`verified:false` / `effect:"unverifiable"` 表示按键事件已发出但效果不可由 backend 自动确认。上层需要通过窗口状态、AX tree、截图或应用自身状态继续验证。

### `computer.launch_app`

启动本地应用。

参数：

```json
{
  "app": "Calculator"
}
```

Capabilities:

```text
computer.app.launch
```

Risk:

```text
write
```

### `computer.open_url`

在桌面浏览器里打开 URL。它是高层意图 API；模型应优先使用它，而不是手动串 `launch_app`、`hotkey`、`type_text`。

参数：

```json
{
  "url": "https://www.baidu.com/s?wd=%E6%9D%8E%E5%9B%BD%E5%BA%86",
  "browser": "Google Chrome"
}
```

`browser` / `app` 可选；默认由 `TMA_COMPUTER_DEFAULT_BROWSER` 控制，未设置时使用 `Google Chrome`。

Capabilities:

```text
computer.app.launch
computer.input.keyboard
```

Risk:

```text
write
```

### `computer.search_web`

在桌面浏览器里打开搜索结果页。它用于验证“真实电脑被操作”，不是替代 `web.search` 的网页检索 API。

参数：

```json
{
  "query": "李国庆",
  "engine": "baidu",
  "browser": "Google Chrome"
}
```

`engine` 支持 `baidu`、`google`、`bing`；默认由 `TMA_COMPUTER_SEARCH_ENGINE` 控制，未设置时使用 `baidu`。

Capabilities:

```text
computer.app.launch
computer.input.keyboard
```

Risk:

```text
write
```

### `computer.bring_to_front`

把一个运行中的应用拉到前台。CUA backend 要求 `pid`；TMA 插件也允许传 `app` / `name` / `bundle_id`，由插件先通过 CUA `launch_app` 获取 pid，再调用 CUA `bring_to_front`。

参数：

```json
{
  "pid": 4506,
  "app": "Calculator",
  "name": "Calculator",
  "bundle_id": "com.apple.calculator"
}
```

Capabilities:

```text
computer.window.focus
```

Risk:

```text
write
```

返回 state 建议：

```json
{
  "backend": "cua",
  "cua_tool": "bring_to_front",
  "result": {
    "activated": true,
    "pid": 4506
  }
}
```

### `computer.screenshot`

截取当前桌面屏幕，并通过 plugin result 的 `exported_files` 回传 artifact。

参数：

```json
{}
```

Capabilities:

```text
computer.screen.capture
```

Risk:

```text
read
```

返回 result 建议：

```json
{
  "protocol_version": "tma.plugin_result.v1",
  "success": true,
  "content": "computer.screenshot completed",
  "state": {
    "backend": "cua",
    "screenshot_path": "/tmp/tma-computer-screenshot.png"
  },
  "exported_files": [
    {
      "path": "/tmp/tma-computer-screenshot.png",
      "name": "computer-screenshot.png",
      "artifact_type": "asset",
      "content_type": "image/png"
    }
  ]
}
```

CUA backend 的 `get_desktop_state` 可能直接返回 `screenshot_png_b64`。示例插件会把该字段解码成临时 PNG 文件，并从 `state.result` 中移除大 base64，只保留 `has_screenshot`、`screenshot_path`、屏幕尺寸和 MIME type 等摘要。worker 在有 `session_id` 且 artifact uploader 可用时，会把 `image/*` export 上传成 session artifact；直接用 `work enqueue` 手动调试且不带 session 时，结果中可能只有 worker 本机路径和 artifact error。

## Backend Contract

当前示例插件支持四种 backend：

| backend | 用途 |
|---|---|
| `auto` | 默认；优先 CUA，部分 read/action API 失败后回退 AX |
| `cua` | 只走 CUA CLI/template |
| `ax` | 只走本机 AX/UI tree fallback |
| `stub` | 验收和 CI 用，不控制真实电脑 |

### CUA Backend

推荐用 CUA 作为主要执行后端，尤其是 Windows/Linux/远程桌面/跨平台真实控制。

启动：

```bash
TMA_COMPUTER_BACKEND=cua \
TMA_COMPUTER_CUA_CMD=cua-driver \
bin/tma-worker \
  --base-url http://localhost:8080 \
  --name computer-worker \
  --plugin examples/plugins/computer-use/computer-plugin.py
```

如果 CUA CLI 版本的调用格式不同，用模板适配：

```bash
TMA_COMPUTER_BACKEND=cua \
TMA_COMPUTER_CUA_TEMPLATE='cua-driver call {tool} {args_json}' \
bin/tma-worker --base-url http://localhost:8080 --name computer-worker --plugin examples/plugins/computer-use/computer-plugin.py
```

模板变量：

| 变量 | 含义 |
|---|---|
| `{tool}` | API 名，例如 `get_state` |
| `{args_json}` | JSON 参数，例如 `{"capture_mode":"ax"}` |

CUA backend 返回 stdout 若为 JSON，会进入 `state.result`；否则进入 `state.result.stdout`。

真实 CUA 0.7.x CLI 的工具名和 TMA API 名不完全一致，当前示例插件已做映射：

| TMA API | CUA tool |
|---|---|
| `computer.get_state` | `get_accessibility_tree` |
| `computer.screenshot` | `get_desktop_state` |
| `computer.launch_app` | `launch_app` |
| `computer.open_url` | `launch_app` + `bring_to_front` + `hotkey` + `type_text` + `hotkey` |
| `computer.search_web` | `open_url` flow with engine URL |
| `computer.bring_to_front` | `bring_to_front` |
| `computer.type_text` | `type_text` |
| `computer.hotkey` | `hotkey` |

macOS 上 `computer.screenshot` 还依赖系统 Screen Recording 权限。若 CUA 返回 `screencapture failed for main display`，通常需要给启动 `tma-worker` 的 Terminal/iTerm/Codex 进程授予“录屏与系统录音”权限，并重启对应进程和 worker。

### AX/UI Tree Backend

AX backend 是轻量 fallback，不是完整跨平台桌面自动化框架。

当前覆盖：

| 系统 | 当前 fallback |
|---|---|
| macOS | `System Events` 读取前台应用、窗口、顶层 AX 元素；可用 `open`、`screencapture`、部分 keystroke |
| Linux | `wmctrl -l` 读取窗口列表；完整 UI tree 建议走 CUA |
| Windows | PowerShell 列出有主窗口的进程；完整 UI tree 建议走 CUA |

macOS 权限要求：

- 运行 `tma-worker` 的 Terminal / app 需要 Accessibility 权限。
- 截图需要 Screen Recording 权限。
- 自动输入和快捷键需要允许 System Events 控制电脑。

### Stub Backend

Stub backend 用于验证 TMA 集成链路，不控制真实电脑。

```bash
TMA_COMPUTER_BACKEND=stub examples/plugins/computer-use/computer-plugin.py execute
```

验收脚本使用 fake CUA backend 做端到端 smoke，会覆盖 `get_accessibility_tree`、`get_desktop_state`、截图落 PNG 文件，以及带 session 的 screenshot artifact ref：

```bash
make verify-computer-plugin-tools
```

## Agent 配置

启用 `computer` namespace：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --tools '{"tools":["computer"],"runtime":"local_system"}'
```

要求：

- 同 workspace 有在线 worker。
- worker heartbeat 里包含 `computer` manifest。
- worker capabilities 包含调用需要的 capability，例如 `computer.ax.read`。

诊断：

```bash
bin/tma worker list --workspace wksp_default --status online
bin/tma worker diagnose --namespace computer --api get_state --capabilities computer.state.read,computer.ax.read --runtime local_system
```

## Work Payload

模型调用 `computer.get_state` 后，server 下发的 work 仍然是标准 `tool_execution`：

```json
{
  "work_type": "tool_execution",
  "payload": {
    "protocol_version": "tma.work.v1",
    "namespace": "computer",
    "api": "get_state",
    "capabilities": ["computer.state.read", "computer.ax.read"],
    "risk": "read",
    "runtime": "local_system",
    "input": {
      "capture_mode": "ax"
    }
  }
}
```

不新增：

```text
work_type = computer_click
work_type = computer_get_state
work_type = cua_action
```

## Safety

第一版 `risk` 仍使用通用枚举：

```text
read | write | exec
```

建议策略：

- `list_windows` / `get_state` / `screenshot` 是 `read`。
- `click` / `type_text` / `hotkey` / `launch_app` / `bring_to_front` 是 `write`。
- 涉及 shell、安装软件、执行脚本时不要塞进 `computer.*`，应走 `default.run_command` 或专门 API，并按 `exec` 处理。
- 对支付、删除、发布、发送消息、物理设备动作等高风险任务，上层 policy 应要求人工审批。
- 插件 stdout 只能输出协议 JSON；日志写 stderr。
- 插件不得直接访问 TMA 数据库或绕过 server 上传长期产物。

## Verification

基础验证：

```bash
examples/plugins/computer-use/computer-plugin.py manifest

echo '{"protocol_version":"tma.plugin.v1","call":{"id":"call_1","identifier":"computer","api_name":"get_state","arguments":{"capture_mode":"ax"}},"context":{"workspace_id":"wksp_default"}}' \
  | TMA_COMPUTER_BACKEND=stub examples/plugins/computer-use/computer-plugin.py execute
```

端到端验证：

```bash
make verify-computer-plugin-tools
```

该验收会验证：

- CUA template backend 适配层能被调用。
- worker heartbeat 发布 `computer` manifest。
- AgentRuntime 暴露 `computer.get_state`。
- worker 执行 `computer.get_state` 并返回 `ui_tree`。
- `worker_work.payload` 保持标准 `tma.work.v1`。

真实桌面验收需要单独在目标机器执行：

```bash
TMA_COMPUTER_BACKEND=cua \
TMA_COMPUTER_CUA_CMD=cua-driver \
bin/tma-worker --base-url http://localhost:8080 --name computer-worker --plugin examples/plugins/computer-use/computer-plugin.py
```

然后人工点检：

```text
computer.get_state capture_mode=ax
computer.list_windows
computer.screenshot
computer.bring_to_front
computer.click
computer.type_text
computer.hotkey
computer.open_url
computer.search_web
```

真实桌面验收不应默认进入 CI，因为它依赖桌面权限、窗口状态和本机安装的 CUA/AX 工具。
