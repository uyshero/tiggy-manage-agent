# TMA 排障与修正记录

本文档记录开发和手动验收过程中遇到的典型问题、原因、修正方式和后续设计调整，方便之后回溯。

完整环境变量说明见 [configuration.md](./configuration.md)。

## 1. 已移除的 command 配置

早期版本要求配置：

```env
TMA_TURN_COMMAND=sh
TMA_TURN_COMMAND_ARGS=["scripts/command_turn_echo.sh"]
```

这两个配置后来被删除，原因是它们只是在暴露 demo 脚本的启动细节，对真实用户没有价值。

当前服务端固定使用内置 demo command turn：

```text
sh scripts/command_turn_echo.sh
```

用户侧只需要配置通用 turn 参数：

```env
TMA_TURN_QUEUE_SIZE=16
TMA_TURN_TIMEOUT_MS=3600000
```

如果仍然看到 `TMA_TURN_COMMAND is required` 或 `exec: "sh \\"`，说明正在运行旧二进制或 shell 中残留了旧环境变量。处理方式：

```bash
unset TMA_TURN_COMMAND TMA_TURN_COMMAND_ARGS
make build
make run
```

## 2. `user.message requires idle session`

现象：

```text
tma: POST /v1/sessions/sesn_000001/events returned 400 Bad Request: {"error":"invalid input: user.message requires idle session"}
```

原因：

`user.message` 只允许在 Session 为 `idle` 时发送。早期实现中，Runner 执行失败会让整个 Session 进入 `failed`，所以继续发送消息会被拒绝。

当时可临时绕过：

```bash
bin/tma session create \
  --agent agt_000001 \
  --env env_000001 \
  --title "Retry after executor fix"
```

最终修正：

已调整为：

```text
turn failed
session idle
```

现在普通 Runner 执行失败不会把整个 Session 置为 `failed`。

失败时写入：

```text
session_turns.status = failed
session_turns.error_message = <失败原因>
session.status_idle
```

`session.status_idle` payload 示例：

```json
{
  "status": "idle",
  "turn_id": "turn_000002",
  "last_turn_status": "failed",
  "reason": "command turn failed"
}
```

修正后的行为：

```bash
bin/tma event send --session sesn_000001 --text "retry after fix"
```

可以继续发送，不再需要单独做 `recover` 接口。

## 4. 为什么保留 `session.status_failed`

当前普通 Runner 失败不再使用：

```text
session.status_failed
```

保留原因：

`session.status_failed` 仍适合表示系统级 Session 故障，例如：

- Sandbox 丢失或不可恢复
- Worker 节点状态不一致
- Session 绑定的环境损坏
- 无法保证继续执行安全

普通执行失败则使用：

```text
session_turns.status = failed
session.status_idle
```

这样用户可以继续对话，事件历史里也能追踪失败 turn。

## 5. CommandTurnExecutor 输出格式错误

现象：

Runner 失败，事件中出现失败原因，例如：

```text
command turn returned invalid JSON
```

或：

```text
command turn returned empty stdout
```

原因：

外部命令必须向 stdout 输出合法 JSON，且 stdout 只能放最终 `agent.message` payload。调试日志不能写 stdout。

正确输出：

```json
{"protocol_version":"tma.command.v1","content":[{"type":"text","text":"hello from command turn"}]}
```

错误输出：

```text
debug: starting command turn
{"content":[{"type":"text","text":"hello"}]}
```

修正：

- stdout 只输出 payload JSON。
- 输出必须带 `protocol_version=tma.command.v1`。
- stderr 输出日志。
- 非可恢复错误用非 0 退出码。

协议说明：

- [command-turn-protocol.md](./command-turn-protocol.md)

## 6. 推荐验证顺序

先启动服务：

```bash
make run
```

再用 CLI 发送一条消息：

```bash
bin/tma event send --session sesn_000001 --text "hello"
```

如果出现异常，先看：

```bash
bin/tma session get --session sesn_000001
bin/tma event list --session sesn_000001 --after 0
```

再查数据库中的 turn 状态：

```bash
docker compose exec -T postgres psql -U tma -d tma \
  -c "SELECT session_id, id, status, error_message, started_at, ended_at FROM session_turns ORDER BY started_at DESC LIMIT 10;"
```
