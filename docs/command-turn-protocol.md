# TMA Command Turn Protocol

本文档定义 `CommandTurnExecutor` 调用外部命令时使用的 JSON stdin/stdout 协议。

该协议用于把 TMA 的 `WorkerRunner` 接到外部 Agent Runtime、远端 worker、Python/Node/Go 脚本或其他独立执行进程。

当前 `cmd/server` 默认路径已经改为 `AgentRuntimeTurnExecutor + agentruntime.DemoRuntime`。`CommandTurnExecutor` 暂时保留为外部进程适配器和协议验证入口。

完整环境变量说明见 [configuration.md](./configuration.md)。

## 当前状态

用户侧不再配置外部命令和参数。服务端当前默认走 AgentRuntime。通用 turn 超时配置仍然适用于 AgentRuntime 和 CommandTurnExecutor：

```env
TMA_TURN_TIMEOUT_MS=3600000
```

该超时保护的是整次 turn，默认 1 小时。真实智能体执行依赖安装、构建或测试时可能较慢；用户主动停止应走 `user.interrupt`。

超时后，命令 context 会被取消，当前 turn 进入失败路径：`session_turns.status=failed`，Session 回到 `idle`，不会补写 `agent.message`。

## 输入协议

`CommandTurnExecutor` 会把一段 JSON 写入外部命令的 stdin。

字段：

```json
{
  "protocol_version": "tma.command.v1",
  "session_id": "sesn_000001",
  "turn_id": "turn_000001",
  "user_payload": {
    "content": [
      {
        "type": "text",
        "text": "hello"
      }
    ],
    "turn_id": "turn_000001"
  }
}
```

字段说明：

- `protocol_version`：当前协议版本，固定为 `tma.command.v1`。
- `session_id`：当前 Session ID。
- `turn_id`：当前执行 turn ID。
- `user_payload`：原始 `user.message` payload。Store 已经补入同一个 `turn_id`。

外部命令可以读取 `user_payload.content` 里的文本，也可以直接转发完整 payload 给模型或 Sandbox。

## 输出协议

外部命令必须向 stdout 输出一段合法 JSON，作为 `agent.message` 的 payload。

最小示例：

```json
{
  "protocol_version": "tma.command.v1",
  "content": [
    {
      "type": "text",
      "text": "Command turn received your message."
    }
  ]
}
```

注意：

- 输出 JSON 不需要自己加 `turn_id`。
- Store 会在 `CompleteSessionTurn` 中统一补齐 `payload.turn_id`。
- stdout 只能输出 payload JSON。日志和调试信息请写 stderr。
- 输出必须带 `protocol_version=tma.command.v1`。

## 成功行为

外部命令满足以下条件时，TMA 认为 turn 成功：

- 命令退出码为 `0`
- stdout 非空
- stdout 是合法 JSON
- stdout 的 `protocol_version` 必须是 `tma.command.v1`

成功后：

```text
agent.message
session.status_idle
```

这两条事件会使用同一个 `payload.turn_id`。

## 失败行为

以下情况会触发 `FailSessionTurn`：

- 命令退出码非 `0`
- 命令超时
- stdout 为空
- stdout 不是合法 JSON
- stdout 缺少 `protocol_version`
- stdout 中的 `protocol_version` 不是 `tma.command.v1`
- TMA 无法启动命令

失败后：

```text
session.status_idle
```

同时：

- Session 状态回到 `idle`
- `session_turns.status` 变为 `failed`
- `session_turns.error_message` 保存失败原因
- `session.status_idle` payload 会包含 `last_turn_status=failed`
- `session.status_idle` payload 的 `reason` 保存失败原因

这表示“本次 turn 失败，但 Session 仍可继续接收下一条 user.message”。

## 中断行为

用户发送 `user.interrupt` 后：

```text
user.interrupt
session.status_interrupting
session.status_idle
```

`WorkerRunner` 会 cancel 当前 turn 的 context。

对于 `CommandTurnExecutor`：

- TMA 通过 `capability.LocalSystemProvider.RunCommand` 启动命令
- context 被 cancel 后，外部命令会被终止
- 被中断的命令不会再补 `agent.message`

外部命令应尽量支持被系统信号快速终止，避免长时间阻塞。

## 示例执行器

仓库内置一个 shell 示例：

```bash
scripts/command_turn_echo.sh
```

它从 stdin 读取输入 JSON，并输出固定的 `agent.message` payload。

手工测试：

```bash
printf '{"protocol_version":"tma.command.v1","session_id":"sesn_000001","turn_id":"turn_000001","user_payload":{"content":[{"type":"text","text":"hello"}]}}' \
  | sh scripts/command_turn_echo.sh
```

预期输出：

```json
{"protocol_version":"tma.command.v1","content":[{"type":"text","text":"Command turn received your message."}]}
```

## 接入建议

外部命令推荐遵循这些约定：

- stdout 只输出最终 `agent.message` payload JSON。
- 输出 payload 带上 `protocol_version=tma.command.v1`。
- stderr 输出诊断日志。
- 非可恢复错误用非 `0` 退出码。
- 长任务要能响应进程终止。
- 输出 payload 时保持 `content` 数组结构，方便未来扩展 tool call、日志片段和多模态内容。
