# MCP 集成

## 边界

AgentConfigVersion 可绑定 Workspace MCP Registry 中的 stdio 或 Streamable HTTP Server。
Runtime 执行 `initialize` 和分页 `tools/list`，把 MCP tool 归一化成普通
`namespace.api` 工具。resources、prompts 和 completions 是可选能力，不默认进入模型上下文。

MCP 配置随 AgentConfigVersion 固定，Session 回放不追随 Registry 的后续修改。Server
按 Session/Agent config 隔离实例，并按 idle TTL 回收。Secret 只保存 `env_ref` 或
`secret_ref: env:NAME`，数据库和 API 不保存真实值。

## 配置形状

```json
{
  "servers": [
    {
      "id": "filesystem",
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
      "stdio_framing": "json_lines",
      "roots": [{"uri": "file:///workspace", "name": "workspace"}],
      "expose": {"resources": false, "prompts": false}
    }
  ]
}
```

新 stdio 配置使用 newline-delimited JSON-RPC，即 `json_lines`。历史配置省略 framing 时
仍按 legacy `content_length` 解释。命令和参数必须通过管理员允许列表，不能让普通 Agent
提交任意本地进程。

HTTP 配置支持 endpoint、headers 引用、可选 GET SSE listener 和 OAuth client credentials。
OAuth token 仅缓存在进程内并在过期前刷新，不持久化或返回 API。`Authorization` header
与 OAuth 配置互斥。

## 运行时能力

支持：

- `tools/list`/`tools/call`，含 cursor 分页。
- `resources/list`、`resources/templates/list`、`resources/read`。
- `prompts/list`、`prompts/get`。
- `completion/complete` 作为客户端参数补全能力。
- Streamable HTTP JSON response、POST SSE、session header 和 protocol version。
- 可选 GET SSE listener、`Last-Event-ID` 重连和 `roots/list` 回应。

设置 `expose.resources`/`expose.prompts` 后，资源和 Prompt 通过只读桥接工具暴露。未设置时
只用于 tooling health。Server 不支持相应 list 方法时降级为空；显式暴露资源或 Prompt 的
resource-only/prompt-only Server 可以没有 `tools/list`。

当前不支持真实 sampling、elicitation、resource subscription、浏览器授权码流程、动态客户
端注册或用户级 token 托管。收到 `sampling/createMessage`/`elicitation/create` 时返回策略错误。

## 网络与安全

- HTTP 默认要求 TLS；私有 CA 只能由运维设置 `TMA_MCP_HTTP_CA_BUNDLE`。
- egress 按 allowed hosts/CIDRs、HTTP/private-network 开关过滤，重定向后再次校验。
- 防止 DNS rebinding、SSRF、localhost/cloud metadata 和凭据透传。
- 输入 schema、输出预算、timeout、并发和审批使用统一工具边界。
- MCP annotations 只作提示，不能提高权限。
- 日志不记录 header、OAuth token、env secret 或完整工具参数。

## 兼容基线

自动矩阵至少验证 Filesystem、Everything、本地 Streamable HTTP、GitHub 和 PostgreSQL
Server 的 initialize、tools list/call、schema、framing、分页和清理。Filesystem 还验证
`roots/list` 和目录边界；PostgreSQL 验证参数化只读查询和 rejected write 后数据不变。

默认 `go test ./...` 跳过联网和数据库矩阵。完整入口及当前固定版本见
[`TESTING.md`](../TESTING.md) 中的 MCP 章节。新增第三方 Server 必须先进入矩阵，不因
“协议兼容”直接标记为企业认证。
