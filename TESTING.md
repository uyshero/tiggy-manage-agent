# TMA 测试命令

本文档记录当前 Go 项目的手动验收命令。

当前 server 使用 `PostgresStore`，数据会持久化，ID 以数据库已有数据为准。

server 启动时会自动读取项目根目录下的 `.env`。如果 shell 里已经 `export` 了同名变量，shell 里的值优先。

环境变量总览见 [docs/configuration.md](./docs/configuration.md)。

---

## 1. 基础验证

```bash
cd "/Users/viito/Desktop/Harness 企业级定制化实现/tiggy-manage-agent"
```

```bash
make fmt
make test
make build
make build-cli
```

Postgres 集成测试默认跳过。启动数据库并迁移后，可以显式运行：

```bash
make db-up
make migrate-up
make test-postgres
```

当前覆盖：

- `user.message -> CompleteSessionTurn`：turn `completed`，Session 回到 `idle`
- `user.message -> user.interrupt -> late CompleteSessionTurn`：turn `interrupted`，不补 `agent.message`
- `user.message -> FailSessionTurn`：turn `failed`，Session 回到 `idle`，可继续发送下一条消息
- 原生 `tools` / `tool_calls` 与 `tma.tool_call.v1` fallback
- Session 级 `intervention_mode`
- CLI `event stream` 对审批事件的可读展示
- Trace / span 投影、持久 span index 写入、`/v1/spans` 索引查询和 trace index retention prune

---

## 2. 启动数据库和服务

终端 A：

```bash
cd "/Users/viito/Desktop/Harness 企业级定制化实现/tiggy-manage-agent"
make db-up
make migrate-up
make run
```

默认监听：

```text
http://localhost:8080
```

启动服务。当前服务端使用内置 `agentruntime.DemoRuntime`：

```bash
make run
```

此模式下 `agent.message` 通常返回：

```text
Agent runtime received: <你的文本>
```

AgentRuntime 设计见 [docs/agent-runtime.md](./docs/agent-runtime.md)。

常见误配置和修正记录见 [docs/troubleshooting.md](./docs/troubleshooting.md)。

服务启动后，可在另一个终端跑自动验收：

```bash
make verify-agent-runtime
```

也可以使用自启动完整验收，它会自动启动数据库、迁移、启动临时服务、验收、停止服务：

```bash
make verify-agent-runtime-full
```

`verify-agent-runtime-full` 会显式使用 `fake` Provider，适合检查基础 HTTP / Store / Runner / Runtime 链路，不受本地真实模型配置影响。

如果要手动验证 Context Builder 历史截断，可以在启动服务前设置：

```bash
export TMA_DEFAULT_CONTEXT_WINDOW_TOKENS=128
make run
```

然后连续发送多轮消息，查看 `runtime.llm_request` 事件中的 `history_count`、`omitted_history_count`、`estimated_token_count` 和 `context_truncated`。Context Builder 当前会使用总窗口的 60%，所以上例实际预算约为 76 token。

手动写入 Session summary：

```bash
bin/tma session summary upsert \
  --session sesn_000001 \
  --text "Earlier conversation established repo layout and provider settings." \
  --until 12
```

查看 summary：

```bash
bin/tma session summary get --session sesn_000001
```

写入 summary 时会产生 `session.status_compacting` 和 `session.status_idle` 事件。

Session 级 runtime settings：

```bash
bin/tma session runtime get --session sesn_000001
bin/tma session runtime update --session sesn_000001 --intervention-mode request_approval
bin/tma session runtime update --session sesn_000001 --intervention-mode approve_for_me
bin/tma session runtime update --session sesn_000001 --intervention-mode full_access
```

Tool intervention 决策：

```bash
bin/tma session attach --session sesn_000001 --after 0

bin/tma session intervention list --session sesn_000001 --status pending
bin/tma session intervention approve --session sesn_000001 --turn turn_000003 --call call_edit --reason "looks safe"
bin/tma session intervention reject --session sesn_000001 --turn turn_000003 --call call_edit --reason "not this time"
```

## 3. LLM Provider 管理命令

Provider 管理只保存 API Key 的环境变量名，不保存真实 API Key。

查看 Provider：

```bash
bin/tma provider list
bin/tma provider get --id fake
```

创建或更新一个 OpenAI 协议 Provider：

```bash
bin/tma provider create \
  --id volcengine-agent-plan \
  --type openai \
  --base-url https://ark.cn-beijing.volces.com/api/plan/v3 \
  --api-key-env TMA_LLM_API_KEY
```

```bash
bin/tma provider update \
  --id volcengine-agent-plan \
  --base-url https://ark.cn-beijing.volces.com/api/plan/v3 \
  --api-key-env TMA_LLM_API_KEY
```

启用 / 禁用：

```bash
bin/tma provider disable --id volcengine-agent-plan
bin/tma provider enable --id volcengine-agent-plan
```

被禁用的 Provider 不能再用于创建新的 Agent；已经绑定该 Provider 的 Session 在执行 turn 时也会失败并回到 `idle`。

## 4. LLM Model 管理命令

模型配置保存总上下文窗口，不保存输入上下文预算。Context Builder 固定使用总窗口的 60%。

```bash
bin/tma model list
bin/tma model list --provider fake
```

新增或更新模型窗口：

```bash
bin/tma model upsert \
  --provider volcengine-agent-plan \
  --model doubao-seed-2.0-pro \
  --context-window 128000
```

没有显式配置的模型会按默认 `128000` 处理。

## 5. Agent 配置版本命令

查看 Agent 当前配置：

```bash
bin/tma agent get --id agt_000001
```

查看 Agent 历史配置版本：

```bash
bin/tma agent config list --agent agt_000001
```

创建新的 AgentConfigVersion：

```bash
bin/tma agent config update \
  --agent agt_000001 \
  --llm-provider volcengine-agent-plan \
  --llm-model doubao-seed-2.0-pro \
  --system "You are a concise coding agent."
```

更新后，新建 Session 会绑定新的 `agent_config_version`；已经存在的 Session 继续绑定创建时的旧版本。

如果确实要让某个已有 idle Session 后续使用 Agent 当前最新配置，需要显式升级：

```bash
bin/tma session config upgrade --session sesn_000001 --to-current
```

升级会写入 `session.config_updated` 事件；running / waiting approval 中的 Session 不允许升级。

## 6. LLM Usage 查询命令

查看某个 Session 的 token usage 总量和每轮明细：

```bash
bin/tma usage list --session sesn_000001
```

返回结构包含：

```json
{
  "session_id": "sesn_000001",
  "summary": {
    "record_count": 1,
    "input_tokens": 10,
    "output_tokens": 5,
    "total_tokens": 15,
    "cached_input_tokens": 0,
    "reasoning_tokens": 0,
    "latency_ms": 120
  },
  "records": []
}
```

`summary` 是该 Session 下所有 usage 明细的合计；`records` 是每个 turn 的 usage 明细。

查看跨 Session 的 usage 聚合：

```bash
bin/tma usage summary
```

默认按 `provider_id + model` 分组。也可以指定分组和过滤条件：

```bash
bin/tma usage summary --group-by provider
bin/tma usage summary --group-by model
bin/tma usage summary --provider volcengine-agent-plan
bin/tma usage summary --model doubao-seed-2.0-pro
bin/tma usage summary --status failed
bin/tma usage summary --from 2026-07-07T00:00:00+08:00 --to 2026-07-08T00:00:00+08:00
```

## 7. 自动验收

它会自动创建 Agent / Environment / Session，发送一条消息，并检查事件中包含：

- `session.status_running`
- `user.message`
- `runtime.started`
- `runtime.thinking`
- `runtime.llm_request`
- `runtime.llm_response`
- `runtime.completed`

如果要验证当前 `.env` 中配置的真实 LLM Provider，使用：

```bash
make verify-llm-provider
```

它会读取当前 `.env` / shell 环境中的：

```text
TMA_LLM_PROVIDER
TMA_LLM_PROVIDER_TYPE
TMA_LLM_MODEL
TMA_LLM_BASE_URL
TMA_LLM_API_KEY_ENV
TMA_LLM_API_KEY
```

启动服务时会把默认 Provider 写入 `llm_providers`，数据库只保存 `TMA_LLM_API_KEY_ENV` 指向的变量名，不保存真实 API Key。然后脚本创建 Agent / Environment / Session，发送一条消息，并检查真实模型链路返回了非空 `agent.message`。如果 Provider 支持流式输出，结果会显示 `runtime.llm_delta` 数量。

如果要验证当前 `.env` 中配置的真实 LLM Provider 的 tool approval 流程，先启动服务，再运行：

```bash
scripts/verify_intervention_flow.sh
```

默认模式会自动创建 Agent / Environment / Session，设置 `intervention_mode=request_approval`，发送 `APPROVAL_TEST_RUN_COMMAND`，等待 pending approval，然后打印 `session attach`、直接 approve、直接 reject 三组命令供手动测试。

也可以自动走 approve 或 reject 分支：

```bash
TMA_APPROVAL_TEST_DECISION=approve scripts/verify_intervention_flow.sh
TMA_APPROVAL_TEST_DECISION=reject scripts/verify_intervention_flow.sh
```

可用环境变量：

```text
TMA_BASE_URL
TMA_CLI
TMA_DOTENV_PATH
TMA_APPROVAL_TEST_WAIT_SECONDS
TMA_APPROVAL_TEST_MESSAGE
TMA_APPROVAL_TEST_DECISION=manual|approve|reject
```

### Web Search / Crawl 验收

验证 `web.search` / `web.crawl` 工具注入、执行、结果回传：

```bash
make verify-web-search-crawl
```

该目标会自动完成：

- 构建 `bin/tma-server` 和 `bin/tma`
- 启动 Postgres 并执行迁移
- 启动 Docker Compose 中的 SearXNG，并探测 `/healthz` 和 `format=json`
- 启动本地 HTML fixture，验证 Agent 调用 `web.crawl`
- 启动本地 SearXNG-compatible mock，验证 Agent 调用 `web.search`
- 检查事件中出现 `runtime.tool_call`、`runtime.tool_result`、`agent.message` 和 `session.status_idle`

通过时会输出类似：

```text
Web search/crawl verification passed
session_id=sesn_000055
crawl_turn_id=turn_000001
search_turn_id=turn_000002
```

本地服务默认端口：

```text
TMA server:           http://localhost:18083
HTML fixture:         http://127.0.0.1:18084
SearXNG mock:         http://127.0.0.1:18085
Docker SearXNG:       http://localhost:8180
```

如果端口被占用，可覆盖：

```bash
TMA_VERIFY_WEB_HTTP_ADDR=:19083 \
TMA_VERIFY_WEB_BASE_URL=http://localhost:19083 \
TMA_VERIFY_WEB_FIXTURE_ADDR=127.0.0.1:19084 \
TMA_VERIFY_WEB_MOCK_SEARXNG_ADDR=127.0.0.1:19085 \
make verify-web-search-crawl
```

- `agent.message`
- `session.status_idle`

同时校验 `agent.message` 文本为：

```text
Agent runtime received: agent runtime verify
```

并校验 `agent.message` payload 中包含当前协议版本：

```text
protocol_version = tma.agent_runtime.demo.v1
```

---

## 3. CLI 健康检查

终端 B：

```bash
cd "/Users/viito/Desktop/Harness 企业级定制化实现/tiggy-manage-agent"
bin/tma health
```

预期包含：

```json
{
  "service": "tiggy-manage-agent",
  "status": "ok"
}
```

---

## 4. 创建 Agent

```bash
bin/tma agent create \
  --name "Code Assistant" \
  --model gpt-4o \
  --system "You are a coding agent."
```

空数据库首次运行时，通常返回：

```text
id = agt_000001
```

---

## 5. 创建 Environment

```bash
bin/tma env create --name default-cloud
```

空数据库首次运行时，通常返回：

```text
id = env_000001
```

自定义 config 示例：

```bash
bin/tma env create \
  --name github-cloud \
  --config '{"type":"cloud","networking":{"type":"limited","allowed_hosts":["api.github.com"]}}'
```

---

## 6. 创建 Session

```bash
bin/tma session create \
  --agent agt_000001 \
  --env env_000001 \
  --title "First TMA task"
```

空数据库首次运行时，通常返回：

```text
id = sesn_000001
status = idle
```

查询 Session：

```bash
bin/tma session get --session sesn_000001
```

---

## 7. 查看历史 Events

```bash
bin/tma event list --session sesn_000001 --after 0
```

创建 Session 后至少应看到两条状态事件：

```text
seq=1 session.status_provisioning
seq=2 session.status_idle
```

只看 `seq > 1` 的事件：

```bash
bin/tma event list --session sesn_000001 --after 1
```

---

## 8. 发送消息

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "hello from cli"
```

当前异步 WorkerRunner 会先返回两类事件：

```text
session.status_running
user.message
```

两条事件的 `payload.turn_id` 应相同，例如：

```text
turn_id = turn_000001
```

如果这是创建 Session 后的第一条消息，seq 通常是：

```text
seq=3 session.status_running
seq=4 user.message
```

后台 AgentRuntime 完成后会追加：

```text
seq=5 runtime.started
seq=6 runtime.thinking
seq=7 runtime.llm_request
seq=8 runtime.llm_response
seq=9 runtime.completed
seq=10 agent.message
seq=11 session.status_idle
```

`agent.message` 和后续 `session.status_idle` 也应带同一个 `payload.turn_id`。

可选：查看 `session_turns` 中的生命周期状态：

```bash
docker compose exec -T postgres psql -U tma -d tma \
  -c "SELECT session_id, id, status, started_at, ended_at FROM session_turns ORDER BY started_at DESC LIMIT 5;"
```

完成的 turn 应显示：

```text
status = completed
```

Runner 启动或执行失败时会追加：

```text
session.status_idle
```

该事件 payload 会带 `last_turn_status=failed` 和 `reason`。同时 `session_turns.status` 标为 `failed`，`error_message` 保存失败原因，Session 回到 `idle`，可以继续发送下一条消息。

再次查看历史：

```bash
bin/tma event list --session sesn_000001 --after 2
```

---

## 9. 测试 SSE 历史续传

`event stream` 是阻塞命令，会一直监听 SSE。用 `Ctrl+C` 退出。

```bash
bin/tma event stream --session sesn_000001 --after 0
```

预期立即补发历史事件。已支持可读格式的事件会显示为短文本，例如：

```text
status: provisioning

status: idle

: stream ready
```

尚未专门格式化的事件仍会按原始 SSE 输出，便于调试新事件类型。

测试从 `seq=2` 后续传：

```bash
bin/tma event stream --session sesn_000001 --after 2
```

预期不会再补发 `seq=1` 和 `seq=2`，只返回 `seq > 2` 的历史事件，然后等待后续新事件。

---

## 10. 测试 SSE 实时推送

终端 B 先启动监听：

```bash
bin/tma event stream --session sesn_000001 --after 2
```

终端 C 发送事件：

```bash
cd "/Users/viito/Desktop/Harness 企业级定制化实现/tiggy-manage-agent"
bin/tma event send \
  --session sesn_000001 \
  --text "second message"
```

终端 B 应实时看到新事件：

```text
status: running (turn turn_000003)

user> second message
```

如果当前 Session 已设置：

```bash
bin/tma session runtime update --session sesn_000001 --intervention-mode request_approval
```

`bin/tma event stream --session sesn_000001 --after 0` 当前会把常见事件格式化为更易读的提示，而不是原始 SSE JSON。常见输出包括：

```text
status: running (turn turn_000003)

user> edit README

delta> working

agent> done
```

当模型命中敏感工具时，审批事件会显示为：

```text
approval required
  seq: 12
  turn: turn_000003
  call: call_edit
  tool: default.edit_file
  mode: request_approval
  message: Tool call requires approval before execution.
  policy: optional
```

当前这一步只验证“可见性”和“拦截”：

- `request_approval`：敏感工具不执行，事件流中出现 `approval required`
- `approve_for_me`：会出现 `runtime.tool_intervention_approved`，随后继续执行
- `full_access`：直接执行，不出现审批提示

交互式会话入口：

```bash
bin/tma session attach --session sesn_000001 --after 0
```

查看交互式输入说明：

```bash
bin/tma session attach --help
```

`session attach` 会持续监听同一个 session 的 SSE，也可以直接在当前终端输入消息发送 `user.message`。
启动后会打印简短提示：

```text
attached to sesn_000001
type a message, /say MESSAGE, /interrupt, or /quit
approval: a=approve, r REASON=reject, s=skip
```

启动时会先查询当前 `status=pending` 的审批记录；即使使用了较新的 `--after` 跳过历史 `runtime.tool_intervention_required` 事件，只要 Store 里仍有 pending approval，也会恢复成本地 prompt：

```text
pending approval recovered: default.edit_file call=call_edit
approval action: a=approve, r [reason]=reject, s=skip
```

常用输入：

```text
hello agent
/say hello agent
/interrupt
/quit
```

遇到 `approval required` 时，会在当前命令中提示：

```text
approval action: a=approve, r [reason]=reject, s=skip
```

- `a` / `approve`：调用 approve API，工具执行后 event stream 继续输出后续事件
- `r not safe` / `reject not safe`：带可选拒绝原因 reject，随后 fail 当前 turn 并回到 idle
- `s` / `skip`：本次不处理，本地 prompt 消失，pending 记录继续保留
- `/say MESSAGE`：在本地已有 pending prompt 时仍强制发送一条新 user message；服务端会按 waiting approval 规则返回提醒并重新发送审批事件

发送成功后会显示：

```text
message sent
```

如果发送消息、interrupt 或审批 API 返回错误，`session attach` 会在终端打印 `... failed: ...`，但不会退出整个交互式会话；可以继续输入下一条消息或下一次审批决策。SSE 连接本身断开时，命令仍会退出并返回错误。

查看 pending 审批记录：

```bash
bin/tma session intervention list \
  --session sesn_000001 \
  --status pending
```

批准或拒绝某个 pending call：

```bash
bin/tma session intervention approve \
  --session sesn_000001 \
  --turn turn_000003 \
  --call call_edit \
  --reason "looks safe"

bin/tma session intervention reject \
  --session sesn_000001 \
  --turn turn_000003 \
  --call call_edit \
  --reason "not this time"
```

批准后应写出 `runtime.tool_intervention_approved` 事件，并消费保存的 tool call 追加 `runtime.tool_result`。拒绝后应写出 `runtime.tool_intervention_rejected` 事件，不执行工具。

在 `bin/tma event stream` 中，批准、拒绝和工具结果会分别显示为 `approval approved`、`approval rejected`、`tool result`。如果工具结果带有 artifact，`tool result` 下会显示 artifact id、名称、类型和 TMA 代理下载路径；如果 artifact 记录失败，会显示 `artifact error`。

细粒度 approve/reject 命令仍保留给脚本和调试使用：`request_approval` 会把 turn 标记为 `waiting_approval`，不会生成 pending 后的临时 `agent.message`。approve 会执行被批准的工具并回写结果事件；如果 pending 记录中带有 continuation messages，还会把 tool result 送回 LLM，并继续最多 4 轮 tool loop。续跑中再次遇到敏感工具会再次 pending / waiting approval；模型返回普通文本时生成最终 `agent.message`，并让 session 回到 `idle`。reject 会写 rejected 事件并 fail turn，不会把拒绝原因喂回模型继续推理。长期未审批时记录保持 `pending` / `waiting_approval`，不会自动 approve / reject / expire。

如果 session 正在 `waiting_approval`，用户又发送新的 `user.message`，服务端不会开启新 turn；它会返回 `202 Accepted`，写一条提醒 `agent.message`，并重新写出 pending 的 `runtime.tool_intervention_required`，方便 CLI/UI 再次展示审批信息。

approve 后的 continuation LLM 调用会进入 usage 记录，可用下面命令确认：

```bash
bin/tma usage list --session sesn_000001
```

这两条事件的 `data` 中应包含相同的 `payload.turn_id`。

稍后继续看到：

```text
event: agent.message
event: session.status_idle
```

后两条事件也应保持同一个 `payload.turn_id`。

---

## 11. Object Ref / Session Artifact

这一节覆盖对象引用、Session artifact、TMA 代理下载和安全删除。文件字节由 objectstore 后端保存，Postgres 只保存 metadata。

如果只想验证 metadata 流，可以先创建一个 object ref，表示对象存储中已经存在或即将存在的对象：

```bash
bin/tma object create \
  --bucket tma-artifacts \
  --key wksp_default/sesn_000001/output.txt \
  --content-type text/plain \
  --size 42 \
  --sha256 abc123 \
  --metadata '{"source":"manual-test"}'
```

查看 object ref：

```bash
bin/tma object get --id obj_000001
```

把 object ref 挂到 Session artifact：

```bash
bin/tma session artifact create \
  --session sesn_000001 \
  --object obj_000001 \
  --turn turn_000001 \
  --call call_write \
  --name output.txt \
  --type file \
  --metadata '{"preview":"hello"}'
```

列出 Session artifacts：

```bash
bin/tma session artifact list --session sesn_000001
bin/tma session artifact list --session sesn_000001 --json
```

预期：

- `object_ref` 中只有 bucket / key / checksum / size / metadata 等引用信息。
- `session artifact` 引用 `object_ref_id`，并记录 session / turn / call 关系。
- 默认 `session artifact list` 输出人类可读摘要；加 `--json` 输出原始 API 响应，方便脚本处理。
- Postgres 不保存文件二进制。

上传并通过 TMA 代理下载真实内容：

```bash
printf 'hello artifact\n' >/tmp/tma-artifact.txt

curl -sS -X POST \
  -F file=@/tmp/tma-artifact.txt \
  -F turn_id=turn_000001 \
  -F tool_call_id=call_manual \
  -F artifact_type=file \
  http://localhost:8080/v1/sessions/sesn_000001/artifacts/upload

bin/tma session artifact list --session sesn_000001
bin/tma session artifact download --session sesn_000001 --artifact art_000001 --output /tmp/tma-artifact.downloaded.txt
```

通过工具执行产生的输出 JSON 也会尽力记录为 Session artifact。可在 `runtime.tool_result` 的 JSON 中查看 `artifacts` 数组；下载路径仍是 `/v1/sessions/{session_id}/artifacts/{artifact_id}/download`，不暴露底层 objectstore 地址。

验证 Inspector 页面是否包含 artifact 下载和复制命令入口：

```bash
make verify-inspector-ui
```

该验收会启动真实 TMA server，读取 `/inspector` HTML，并校验页面包含 `Download`、`Copy CLI`、`data-copy`、`bin/tma session artifact download --session ...` 等关键内容。它不依赖浏览器插件，适合作为 Inspector UI 基础交互的轻量回归。

浏览器 smoke 可以在静态验收通过后手动跑：

```bash
make verify-inspector-browser-smoke
```

该验收会启动真实 TMA server 和 headless Chrome，自动造数并实际点击：

- Recent Traces `Load more`
- Session filter 后的 Span Search `Load more`
- trace card 加载 Timeline / Artifacts
- Timeline 的 tool result 截断提示
- 大文本 artifact `Preview` 的 10KB 截断提示
- 页面 console error / unhandled rejection 计数

也可以手动启动页面点检：

```bash
TMA_HTTP_ADDR=:18089 \
TMA_DATABASE_URL='postgres://tma:tma@localhost:5432/tma?sslmode=disable' \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
bin/tma-server
```

然后打开：

```text
http://localhost:18089/inspector
```

最小点检路径：

- 页面标题为 `TMA Inspector`，脚本资源来自 `/inspector/assets/api.js`、`utils.js`、`app.js`
- `Recent Traces` 有数据时，点击 trace 卡片后应填充 Trace ID / Session / Turn，并渲染 Waterfall、Spans、Timeline 和 Raw JSON
- `Recent Traces` / `Span Search` 返回超过第一页时应显示 `Load more`，点击后追加下一页而不是覆盖当前列表
- 在 `Session` 输入框填入 session id 后，点 `Filter by Session` 或按 Enter，`Recent Traces` 和 `Span Search` 应只显示该 session 的结果；点 `Clear` 恢复全局 catalog
- `Span Search` 有数据时，点击 span 卡片后应定位到对应 span，URL hash 包含 `trace` 和 `span`
- 点击文本/JSON artifact 的 `Preview` 时，页面只 inline 展示前 10KB 左右内容；完整内容用 `Download` 或 `Copy CLI` 获取
- 大工具结果对应的 `runtime.tool_result` data 应包含 `context.content_truncated` / `context.state_truncated` 标记，而不是把完整长文本和大 state 直接塞进事件
- 浏览器 console 不应出现 error

Trace / span index 的 focused 回归：

```bash
go test ./internal/managedagents ./internal/observability ./internal/httpapi -count=1
```

其中 `TestGetSessionTraceProjectsTurnTimeline` 会覆盖：

- `GET /v1/sessions/{id}/trace` 仍从 `session_events` 投影完整 trace
- 生成 trace 后写入 `trace_indexes` / `trace_span_indexes`
- `/v1/traces` 和 `/v1/spans` 可从索引返回 catalog / span search，并包含 `limit`、`offset`、`next_offset`、`has_more`
- `runtime.span_started` / `runtime.span_event` / `runtime.span_ended` 会被投影为原生 lifecycle span
- `PruneTraceIndexes(before, limit)` 会删除过期 trace index，并级联删除 span index

手动验证 Postgres 索引表：

```bash
bin/tma trace show --session sesn_000001 --turn turn_000001

docker compose exec -T postgres psql -U tma -d tma \
  -c "SELECT trace_id, session_id, turn_id, span_count, updated_at FROM trace_indexes ORDER BY updated_at DESC LIMIT 5;"

docker compose exec -T postgres psql -U tma -d tma \
  -c "SELECT trace_id, span_id, kind, status, duration_ms, critical FROM trace_span_indexes ORDER BY start_time DESC LIMIT 10;"
```

注意：`session_events` 仍是事实源；索引用于 Inspector / span search / retention，索引缺失时 HTTP 查询会回退投影并回填。

---

## 12. 使用 curl 验证 SSE

```bash
curl -N "http://localhost:8080/v1/sessions/sesn_000001/events/stream?after_seq=0"
```

另一个终端发送事件：

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "hello from curl stream"
```

---

## 13. 测试 Interrupt

当前版本使用异步 WorkerRunner。发送 `user.message` 后，Session 会停留在 `running`，这时可以中断。

终端 B 发送一条消息：

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "interrupt me"
```

立刻查看 Session：

```bash
bin/tma session get --session sesn_000001
```

通常可以看到：

```text
status = running
```

马上发送中断：

```bash
bin/tma event interrupt --session sesn_000001
```

预期返回：

```text
user.interrupt
session.status_interrupting
session.status_idle
```

这三条事件的 `payload.turn_id` 应与刚才的 `user.message` 一致。

可选：再次查询 `session_turns`，中断的 turn 应显示：

```text
status = interrupted
```

如果中断太晚，Session 可能已经被后台 AgentRuntime 带回 `idle`，这时会返回：

```text
user.interrupt requires running session
```

---

## 14. Archive Session

```bash
bin/tma session archive --session sesn_000001
```

预期：

```text
status = terminated
```

再次查看历史事件：

```bash
bin/tma event list --session sesn_000001 --after 0
```

应新增一条终止状态事件：

```text
session.status_terminated
```

被 archive 的 Session 不应再接受新事件：

```bash
bin/tma event send \
  --session sesn_000001 \
  --text "should fail"
```

预期返回冲突错误。

---

## 15. Delete Session

```bash
bin/tma session delete --session sesn_000001
```

预期：

```text
deleted session sesn_000001
```

删除后查询：

```bash
bin/tma session get --session sesn_000001
```

预期返回 `not found`。

---

## 16. 数据库验证

数据库模式不需要本机安装 `psql`。迁移命令会使用 Postgres 容器里的 `psql`。

启动 Postgres：

```bash
make db-up
```

应用初始 schema：

```bash
make migrate-up
```

启动服务：

```bash
make run
```

也可以显式指定数据库地址：

```bash
TMA_DATABASE_URL="postgres://tma:tma@localhost:5432/tma?sslmode=disable" make run
```

看到以下日志表示已进入 Postgres 模式：

```text
using postgres store
```

然后重复第 3 到第 14 节的 CLI 命令。

注意：Postgres 会保留历史数据，所以返回的 ID 不一定是 `agt_000001`。以后续命令实际返回的 ID 为准。

查看数据库日志：

```bash
make db-logs
```

停止数据库：

```bash
make db-down
```

---

## 17. Onlyboxes 验证

### 17.1 Sandbox doctor

先检查本地 cloud_sandbox 前置条件：

```bash
bin/tma sandbox doctor
```

`sandbox doctor` 会读取当前目录 `.env` 中的 `TMA_CLOUD_SANDBOX_ROOT` 和 `TMA_CLOUD_SANDBOX_IMAGE`，并检查：

- runtime 是否会落到 `cloud_sandbox`。
- workspace root 是否存在且是目录。
- `docker` 命令是否可找到。
- Docker daemon 是否可连接。
- sandbox 镜像是否已存在于本地；缺失时默认自动 `docker pull`。

它不会自动启动 Docker。若只是想验证 Docker 链路，可以临时指定已有镜像：

```bash
bin/tma sandbox doctor --image busybox:latest
```

如果只想做无 pull 的纯检查：

```bash
bin/tma sandbox doctor --pull=false
```

### 17.2 Provider 级验证

这一节只验证 `OnlyboxesProvider` 的真实 Docker 执行边界，不需要启动 TMA server，也不依赖 LLM。

默认使用项目内置镜像名：

```bash
make verify-onlyboxes
```

如果本地还没有 `coolfan1024/onlyboxes-runtime:default` 镜像，可以先用一个已有的 shell 镜像验证挂载和 workdir 链路：

```bash
TMA_ONLYBOXES_TEST_IMAGE=busybox:latest make verify-onlyboxes
```

预期测试会：

- 创建临时 workspace。
- 在 host 写入 `marker.txt`。
- 用 Docker 把 workspace 挂载到容器 `/workspace`。
- 如果配置了 session data root，还会把 session 数据目录挂到 `/mnt/data`。
- 在容器内读取 `marker.txt` 并写出 `out.txt`。
- 回到 host 检查 `out.txt` 内容。

通过表示 `cloud_sandbox` provider 的基本命令执行、workspace 挂载、session 数据挂载和输出回写链路可用。

### 17.3 Session 级验证

这一节验证完整链路：

```text
TMA server
  -> fake LLM 触发 default.run_command
  -> AgentRuntime tool loop
  -> SessionProviderResolver 默认选择 cloud_sandbox
  -> OnlyboxesProvider
  -> runtime.tool_call / runtime.tool_result / agent.message
```

默认使用项目内置镜像名：

```bash
make verify-onlyboxes-session
```

如果本地还没有 `coolfan1024/onlyboxes-runtime:default` 镜像，可以先用 `busybox` 验证完整 session 链路：

```bash
TMA_ONLYBOXES_TEST_IMAGE=busybox:latest make verify-onlyboxes-session
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 用 `TMA_TOOL_RUNTIME=cloud_sandbox` 启动临时 TMA server。
- 创建 agent / environment / session。
- 把 session `intervention_mode` 设置为 `approve_for_me`，避免工具审批挂起。
- 发送 `tma.verify_tool_call` 消息，让 fake LLM 发起工具调用。
- 校验事件历史中出现 `runtime.tool_call`、`runtime.tool_result`、`agent.message`，且 tool result 同时包含 `/workspace` 和 `tma-session-tool-ok`。

通过表示 session 级工具执行已经真实走到 Onlyboxes provider。

### 17.4 cloud_sandbox 外网审批验证

这一节验证 `cloud_sandbox_allow_network` 和 `intervention_mode` 的组合行为：

```bash
make verify-network-approval
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 用 `TMA_TOOL_RUNTIME=cloud_sandbox` 启动临时 TMA server。
- 让 fake LLM 通过 `tma.verify_network_download` 触发 `default.execute_code`，执行 Python `urllib.request.urlopen(...)` 下载。
- 验证 `request_approval + cloud_sandbox_allow_network=true` 会产生 pending intervention，reason 为 `network_access`，批准后继续执行。
- 验证 `approve_for_me + cloud_sandbox_allow_network=true` 会写出 auto approval 事件并执行成功。
- 验证 `full_access + cloud_sandbox_allow_network=true` 不写审批事件并直接执行成功。
- 验证 `full_access + cloud_sandbox_allow_network=false` 不写 `network_access` 审批事件，且 Python 下载不会成功。

通过表示“沙箱具备外网能力”和“访问外网进入审批策略”两层语义已经在真实 AgentRuntime 链路里闭环。

### 17.5 cloud_sandbox 上传数据验证

这一节验证用户上传文件进入 session sandbox 数据目录，并且同一 session 的 `/mnt/data` 能跨多次工具调用保留中间文件：

```bash
make verify-onlyboxes-upload-data
```

如果本地还没有默认镜像，也可以用 `busybox` 先验证链路：

```bash
TMA_ONLYBOXES_TEST_IMAGE=busybox:latest make verify-onlyboxes-upload-data
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 启动临时 TMA server。
- 创建 agent / environment / session。
- 通过 `POST /v1/sessions/{session_id}/artifacts/upload` 上传一个 `file` artifact。
- 发送 `tma.verify_uploaded_file_seed`，让 fake LLM 触发 sandbox 命令读取 `/mnt/data/uploads/{artifact_id}/input.txt`，并写入 `/mnt/data/state.txt`。
- 再发送 `tma.verify_uploaded_file_read`，确认下一次工具调用仍能读取上传文件和上一轮写入的 `/mnt/data/state.txt`。

通过表示上传接口、对象存储、session artifact、`OnlyboxesProvider` 同步逻辑和 session 级 `/mnt/data` 持久目录已经形成最小闭环。

### 17.6 cloud_sandbox 输出回收验证

这一节验证 `default.run_command` / `default.execute_code` 的 `output_paths` 能把 `/mnt/data` 里生成的文件回收到 session artifact：

```bash
make verify-onlyboxes-export-artifact
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 启动临时 TMA server。
- 创建 agent / environment / session。
- 通过 `POST /v1/sessions/{session_id}/artifacts/upload` 上传一个输入文件。
- 发送 `tma.verify_uploaded_file_export`，让 fake LLM 触发 sandbox 命令在 `/mnt/data/outputs/export.txt` 生成产物，并通过 `output_paths` 请求保存。
- 从 `runtime.tool_result` 的 `artifacts` 中取出生成的 `file` artifact。
- 通过 `bin/tma session artifact download` 下载该 artifact，校验内容同时包含上传文件标记和生成文件标记。

通过表示上传文件 -> `/mnt/data` 加工 -> `output_paths` 导出 -> session artifact 下载 已经形成最小闭环。

### 17.7 cloud_sandbox Browser Tools 验证

这一节验证 `browser.*` 能在带 Playwright 的 headless 沙箱镜像中执行，并把截图回收到 session artifact。

先构建浏览器沙箱镜像：

```bash
make build-browser-sandbox
```

默认镜像名：

```text
tma-browser-sandbox:playwright
```

运行验收：

```bash
make verify-browser-tools
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 用 `TMA_TOOL_RUNTIME=cloud_sandbox` 和 `TMA_CLOUD_SANDBOX_ALLOW_NETWORK=false` 启动临时 TMA server。
- 用 `data:` URL 注入本地测试页面，不依赖外网。
- 发送 `tma.verify_browser_flow`，让 fake LLM 触发 `browser.open`、`browser.screenshot`、`browser.type`、`browser.click`。
- 校验事件历史中出现 browser tool call / result、页面标记 `tma-browser-flow-ok`，且截图结果包含 artifact ref。

通过表示 `browser.*` 工具、Playwright runner、browser sandbox 镜像、`/mnt/data` 截图输出和 session artifact 回收链路已经形成最小闭环。

需要改镜像名时：

```bash
TMA_BROWSER_SANDBOX_IMAGE=your-registry/tma-browser-sandbox:playwright make build-browser-sandbox
TMA_BROWSER_SANDBOX_IMAGE=your-registry/tma-browser-sandbox:playwright make verify-browser-tools
```

人工接管浏览器需要本地桌面环境，不放进 cloud_sandbox smoke。手动点检可以运行：

```bash
make verify-browser-takeover-local
```

脚本会启动临时 server 和本地 `tma-worker`，把 agent/session tools runtime 配成 `local_system`，发送 `tma.verify_browser_takeover`，让 fake LLM 触发 `browser.takeover`。预期行为：本机弹出 headed Chromium；用户操作或关闭窗口后，工具返回最终页面标题、URL、正文片段和可交互元素。脚本会校验事件历史、agent message、worker work payload，以及结果中包含 `tma-browser-takeover-ok`。后续同一 `browser_session_id` 的 `browser.read/click/type/screenshot` 会通过 CDP 复用同一个本地长驻浏览器；脚本最后会发送 `tma.verify_browser_close` 并校验 `browser.close` 释放本地浏览器进程。

### 17.8 S3-compatible 对象存储验证

这一节验证 `TMA_OBJECT_STORAGE_PROVIDER=s3` 的真实对象存储闭环。运行前需要先启动 RustFS / MinIO / S3-compatible 服务，并确保 bucket 已存在或服务端允许自动写入该 bucket。

默认使用：

```env
TMA_OBJECT_STORAGE_ENDPOINT=http://localhost:9000
TMA_OBJECT_STORAGE_REGION=local
TMA_OBJECT_STORAGE_BUCKET=tma-artifacts
TMA_OBJECT_STORAGE_ACCESS_KEY=tma
TMA_OBJECT_STORAGE_SECRET_KEY=tma-secret
TMA_OBJECT_STORAGE_USE_PATH_STYLE=true
```

执行：

```bash
make verify-objectstore-s3
```

也可以覆盖 endpoint / bucket / credentials：

```bash
TMA_OBJECT_STORAGE_ENDPOINT=http://localhost:9000 \
TMA_OBJECT_STORAGE_BUCKET=tma-artifacts \
TMA_OBJECT_STORAGE_ACCESS_KEY=tma \
TMA_OBJECT_STORAGE_SECRET_KEY=tma-secret \
make verify-objectstore-s3
```

脚本会自动：

- build `bin/tma-server` 和 `bin/tma`。
- 启动 Postgres 并执行迁移。
- 用 `TMA_OBJECT_STORAGE_PROVIDER=s3` 启动临时 TMA server。
- 创建 agent / environment / session。
- 通过 `POST /v1/sessions/{session_id}/artifacts/upload` 上传一个文件 artifact。
- 通过 `bin/tma session artifact download` 从 TMA 代理下载 artifact。
- 比对上传和下载文件内容一致。
- 删除验证产生的 session artifact 和 object ref metadata。

通过表示 HTTP 上传、真实 S3-compatible PutObject、TMA 代理下载、真实 GetObject 和 metadata 管道已经形成最小闭环。当前删除步骤只清理 TMA metadata；底层对象物理回收后续需要单独设计 GC / retention 策略。

### 17.9 Worker-backed local_system 验证

这一节验证完整链路：

```text
TMA server
  -> fake LLM 触发 default.run_command
  -> AgentRuntime tool loop
  -> local_system worker capability match
  -> WorkerBackedProvider 入队 tma.work.v1 tool_execution
  -> tma-worker outbound poll / ack / result
  -> runtime.tool_result / agent.message
```

运行：

```bash
make verify-worker-backed-local-system
```

脚本会自动：

- build `bin/tma-server`、`bin/tma` 和 `bin/tma-worker`。
- 启动 Postgres 并执行迁移。
- 用 fake LLM 启动临时 TMA server，并配置 `TMA_WORKER_AUTH_TOKEN`。
- 启动一个本地 `tma-worker`，worker 只主动消费 server API，不暴露端口。
- 创建 agent / environment / session。
- 将 agent tools 配置为 `{"tools":["default"],"runtime":"local_system"}`。
- 将 session `intervention_mode` 设置为 `approve_for_me`。
- 发送 `tma.verify_tool_call`，让 fake LLM 发起 `default.run_command`。
- 校验事件历史中出现 `runtime.tool_call`、`runtime.tool_result`、`agent.message`。
- 从 worker 日志中取出 completed `work_id`，再用 `bin/tma work get --work ...` 校验 work 状态和 tool result。

通过表示 `local_system` 工具执行已经真实经过 `tma-worker`，而不是落在 server 进程内 fallback。

### 17.9.1 Worker process plugin 验证

这一节验证 worker 进程型插件可以进入完整 AgentRuntime 链路：

```text
tma-worker --plugin
  -> heartbeat capabilities.manifests
  -> AgentRuntime 暴露 robot.get_state
  -> fake LLM 调用 robot.get_state
  -> WorkerBackedProvider 下发 tma.work.v1 tool_execution
  -> 插件 execute 返回 tool result
```

运行：

```bash
make verify-worker-plugin-tools
```

脚本会自动创建一个临时 `robot` 插件，插件只实现 `manifest` 和 `execute` 两个命令。通过条件：

- `bin/tma worker list --json` 能看到在线 worker 的 `robot.get_state` API 和 `robot` manifest。
- agent tools 配置为 `{"tools":["robot"],"runtime":"local_system"}` 后，fake LLM 能触发 `robot.get_state`。
- 事件历史包含 `runtime.tool_call`、`runtime.tool_result`、`agent.message`，且结果包含 `tma-worker-plugin-ok`。
- `worker_work.payload` 是标准 `tma.work.v1`，`namespace=robot`、`api=get_state`、`capabilities=["robot.state"]`、`risk=read`。

### 17.9.2 Computer-use process plugin 验证

这一节验证 `computer` 插件可以作为 worker 后端能力挂入现有 `tool_execution` 链路。该 smoke 使用 fake CUA backend，不控制真实电脑，但会覆盖 CUA tool 映射、截图落 PNG 文件、以及带 session 的 screenshot artifact ref；真实运行时可切到 `auto` / `cua` / `ax`。详细标准见 [docs/computer-use-plugin.md](./docs/computer-use-plugin.md)。

```bash
make verify-computer-plugin-tools
```

脚本会自动：

- 启动真实 TMA server。
- 启动真实 `tma-worker --plugin examples/plugins/computer-use/computer-plugin.py`。
- 校验 worker heartbeat 发布 `computer` manifest、`computer.get_state` API 和 `computer.ax.read` capability。
- 创建启用 `{"tools":["computer"],"runtime":"local_system"}` 的 agent。
- 发送 `tma.verify_computer_plugin_tool`，让 fake LLM 调用 `computer.get_state {"capture_mode":"ax"}`。
- 发送 `tma.verify_computer_plugin_screenshot`，让 fake LLM 调用 `computer.screenshot`。
- 校验事件历史包含 `runtime.tool_call`、`runtime.tool_result`、`agent.message`，且 `get_state` 结果包含 CUA `get_accessibility_tree` 映射和 `ui_tree`。
- 校验 screenshot 结果包含 CUA `get_desktop_state` 映射、`has_screenshot=true`、无 `screenshot_png_b64`，并且有 session artifact ref。
- 校验 `worker_work.payload` 是标准 `tma.work.v1`，`namespace=computer`、`api=get_state`、`capabilities=["computer.state.read","computer.ax.read"]`、`risk=read`、`runtime=local_system`。

真实 CUA 后端手动点检：

```bash
TMA_COMPUTER_BACKEND=cua \
TMA_COMPUTER_CUA_CMD=cua-driver \
bin/tma-worker --base-url http://localhost:8080 --name computer-worker --plugin examples/plugins/computer-use/computer-plugin.py

bin/tma worker diagnose --namespace computer --api get_state --capabilities computer.state.read,computer.ax.read --runtime local_system
```

CUA CLI 调用格式如有差异，用 `TMA_COMPUTER_CUA_TEMPLATE='...'` 适配。当前不部署 OmniParser。

已验证的真实 CUA 动作包括 `computer.get_state`、`computer.list_windows`、`computer.launch_app`、`computer.bring_to_front`、`computer.type_text`、`computer.hotkey`、`computer.open_url`、`computer.search_web` 和 `computer.screenshot`。`type_text` / `hotkey` / `click` 的 `pid` 可省略，插件会尝试解析指定 app 或当前前台窗口；按键别名如 `Command` / `Return` 也会规范化。`type_text` / `hotkey` 可能返回 `verified:false`、`effect:"unverifiable"`；这表示 CUA 已发送键盘事件但无法证明目标 UI 状态变化，不表示 TMA 调用失败。`computer.screenshot` 依赖 macOS Screen Recording 权限；若返回 `screencapture failed for main display`，需要给启动 worker 的终端/CUA 授权后重启 worker。CUA screenshot 的 `screenshot_png_b64` 会在插件内落成 PNG 文件，`state.result` 只保留摘要；有 session 的 worker 路径会把 `image/*` export 上传成 artifact，避免截图 base64 进入 tool result。

### 17.10 Worker-backed local export 验证

这一节验证 `worker-backed local_system` 上的 `output_paths` 也能把 worker 机器生成的文件回收到 session artifact：

```bash
make verify-worker-backed-local-export
```

脚本会自动：

- build `bin/tma-server`、`bin/tma` 和 `bin/tma-worker`。
- 启动 Postgres 并执行迁移。
- 用 fake LLM 启动临时 TMA server，并配置 `TMA_WORKER_AUTH_TOKEN`。
- 启动一个本地 `tma-worker`。
- 创建 agent / environment / session，并将 agent tools 配置为 `{"tools":["default"],"runtime":"local_system"}`。
- 发送 `tma.verify_worker_export`，让 fake LLM 触发 `default.run_command` 在 worker workspace 里写 `worker-export.txt`，并通过 `output_paths` 请求保存。
- 从 `runtime.tool_result.artifacts` 中取出生成的 `file` artifact。
- 通过 `bin/tma session artifact download` 下载该 artifact，校验内容包含 `tma-worker-export-ok`。

注意：这一节不仅是在验证 recorder / objectstore，还在验证 `output_paths` 已经从工具入参透传到 worker work payload。若未来回归成“tool result 里只有结构化 JSON artifact、没有 file artifact”，优先检查 `capability.RunCommandRequest` / `ExecuteCodeRequest` 是否仍携带 `output_paths`。

通过表示 worker 机器生成文件 -> work result 回传 -> server objectstore 落盘 -> session artifact 下载 已经形成最小闭环。

大文件路径额外验证 worker 不再把文件内容内联进 `worker_work.result`，而是通过 server artifact upload API 上传后只回传 artifact ref：

```bash
make verify-worker-backed-large-local-export
```

该脚本会触发 fake LLM 生成一个超过 8 MiB 的 worker 本地文件，下载 artifact 并校验内容包含 `tma-worker-large-export-ok`。通过表示大文件已经走 worker -> server artifact upload，而不是 base64 塞进 work result。

### 17.11 Worker / worker work 过期收敛

第一版 worker work 不做自动重试。若 worker 掉线或长时间没有给 leased/running work 续租，后台 reaper 会把过期 work 标记为 failed，方便排障和释放卡住状态。若 worker 自身长时间没有 heartbeat，后台 worker reaper 会把过期 online worker 标记为 offline，避免 capability match / diagnose 继续把陈旧 worker 当作在线能力。

正常 `tma-worker` 执行长任务时会在 ack 后、result 前按 `TMA_WORKER_WORK_HEARTBEAT_INTERVAL` 周期性调用 work heartbeat；这里的过期回收脚本故意不续租，用来验证 reaper 兜底路径。

真实验收：

```bash
make verify-worker-work-reap-expired
```

该脚本会启动真实 TMA server，打开后台 worker reaper 与 worker work reaper，注册一个 worker，投递 work，用短 lease poll 并 ack 成 running，等待 work lease 过期后由 server 自动回收，再用 `work get` 确认该 work 已变为 `failed` 且带 `worker work lease expired` 错误信息。随后脚本还会注册一个 1 秒 lease 的 worker，不发送 heartbeat，确认 server 自动把它标记为 `offline`。

反向验证真实 `tma-worker` 的 running work heartbeat：

```bash
make verify-worker-work-heartbeat
```

该脚本会启动真实 TMA server、后台 worker work reaper 和一个真实 `tma-worker`，用 3 秒 work lease 执行一个 6 秒 `sandbox_command`。worker 每 1 秒续租 running work；最终 `work get` 必须显示 `completed`，worker 日志里也必须至少出现两条 `worker work heartbeat`。通过表示长任务不会因为执行时间超过初始 lease 而被 reaper 误标为 failed。

相关配置：

```env
TMA_WORKER_REAPER_ENABLED=true
TMA_WORKER_REAPER_INTERVAL_MS=30000
TMA_WORKER_REAPER_LIMIT=100
TMA_WORKER_WORK_REAPER_ENABLED=true
TMA_WORKER_WORK_REAPER_INTERVAL_MS=30000
TMA_WORKER_WORK_REAPER_LIMIT=100
```

脚本也会在 running 与 failed 两个阶段调用：

```bash
bin/tma work diagnose --work WORK_ID
```

`work diagnose` 面向队列中的单个 work/job，展示当前状态、assigned worker、lease、原因和建议动作；它不同于 `worker diagnose`，后者诊断的是某次 tool invocation 能否匹配到 worker。

`tma-worker` 默认串行消费队列；需要让同一个 worker 同时处理多条 work 时，可以设置：

```bash
bin/tma-worker --base-url http://localhost:8080 --name viito-mac --concurrency 2
```

或：

```env
TMA_WORKER_CONCURRENCY=2
```

并发只改变 worker 本地 poll/execute slot 数量，不改变 server 队列协议。提高并发前要确认多个 work 不会写同一文件或争用同一外部资源。

worker 收到 SIGINT / SIGTERM 后会进入 drain：停止继续 poll，heartbeat `draining`，并在 `TMA_WORKER_SHUTDOWN_TIMEOUT` 内等待已 running 的 work 完成并提交 result。单元测试 `TestRunWorkerDrainsRunningWorkOnShutdown` 覆盖该行为；如果超过 timeout，剩余 work 仍由 lease + reaper 兜底转为 failed。

真实验收：

```bash
make verify-worker-shutdown-drain
```

该脚本会启动真实 TMA server 和真实 `tma-worker`，在 work running 时向 worker 发送 SIGTERM；worker 必须 heartbeat `draining`、等待 long-running work completed、再正常退出。通过表示发布 / 重启 worker 时不会立即丢掉已经 ack 的 running work。

反向控制面取消验收：

```bash
make verify-worker-work-cancel
```

该脚本会启动真实 TMA server 和真实 `tma-worker`，投递一个长时间运行的 work，等它进入 `running` 后调用 `bin/tma work cancel --work ... --reason ...`。worker 下一次 running work heartbeat 必须观察到 `canceled`，取消本地执行，不提交 completed result；最终 `work get` 仍保持 `canceled`，错误信息为取消原因。

手动命令：

```bash
bin/tma worker reap-expired --limit 100
bin/tma work cancel --work WORK_ID --reason "user stopped it"
bin/tma work requeue --work WORK_ID --clear-worker
bin/tma work reap-expired --limit 100
```

等价 HTTP：

```bash
curl -X POST "$TMA_BASE_URL/v1/workers/reap-expired" \
  -H "Authorization: Bearer $TMA_WORKER_CONTROL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"limit":100}'

curl -X POST "$TMA_BASE_URL/v1/worker-work/reap-expired" \
  -H "Authorization: Bearer $TMA_WORKER_CONTROL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"limit":100}'

curl -X POST "$TMA_BASE_URL/v1/worker-work/WORK_ID/cancel" \
  -H "Authorization: Bearer $TMA_WORKER_CONTROL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason":"user stopped it"}'

curl -X POST "$TMA_BASE_URL/v1/worker-work/WORK_ID/requeue" \
  -H "Authorization: Bearer $TMA_WORKER_CONTROL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"clear_worker":true}'
```

通过表示 `lease_expires_at` 已过期的 `leased` / `running` work 会变为 `failed`，并带 `worker work lease expired ...` 错误信息。这里故意不自动重新入队，避免命令、文件写入、外部 API 调用被重复执行；需要重试时由控制面显式 `work requeue` 复制出新的 pending work，原 failed / canceled 记录保留用于审计。

### 17.12 Worker doctor

`tma-worker doctor` 用于在启动常驻 worker 前检查 outbound API、token 和 executor capabilities：

```bash
bin/tma-worker doctor --base-url http://localhost:8080 --name viito-mac
```

doctor 会：

- 打印当前 executor 导出的 runtimes / APIs / capabilities。
- 检查 `/health`。
- 临时注册 `<name>-doctor` worker。
- 发送 heartbeat。
- 调用 poll endpoint 验证 worker consumer API 权限。
- 调用 server-side worker diagnose，确认刚注册的 worker 能被控制面看见。
- 归档临时 worker。

### 17.13 cloud_sandbox 默认配置

新 session 默认已经走 `cloud_sandbox`，`.env` 不需要写 `TMA_TOOL_RUNTIME=cloud_sandbox`。如果要固定 workspace root 或镜像，只配置覆盖项：

```env
TMA_CLOUD_SANDBOX_ROOT=.
TMA_CLOUD_SANDBOX_IMAGE=coolfan1024/onlyboxes-runtime:default
TMA_CLOUD_SANDBOX_DATA_ROOT=/private/tmp/tma-cloud-sandbox-data
TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS=3600
```

如果要临时切到本机执行面，先启动同 workspace 的 `tma-worker`，再通过 `bin/tma session runtime update --session <session_id> --tool-runtime local_system` 对单个 session 热更新，不改 `.env`。没有匹配在线 worker 时，默认会隐藏 `local_system` 工具；只有受信任开发环境显式设置 `TMA_ALLOW_SERVER_LOCAL_SYSTEM=true` 才允许 server 进程本机 fallback。

---

## 18. 常见问题

### 18.1 `psql: command not found`

不要直接运行：

```bash
psql "postgres://tma:tma@localhost:5432/tma?sslmode=disable" -f sql/migrations/000001_init.sql
```

请运行：

```bash
make migrate-up
```

这个命令会进入 Docker 里的 Postgres 容器执行 `psql`。

### 18.2 连接失败

确认 server 正在运行：

```bash
bin/tma health
```

如果服务不是跑在 `localhost:8080`，指定地址：

```bash
bin/tma --base-url http://localhost:9090 health
```

或使用环境变量：

```bash
export TMA_BASE_URL=http://localhost:9090
bin/tma health
```

### 18.3 ID 不对

Postgres 会保留历史数据，ID 会继续递增。以实际命令返回的 ID 为准。

### 18.4 `event stream` 没退出

这是正常行为。SSE 是持续连接，用：

```text
Ctrl+C
```

退出监听。

### 18.5 Postgres 模式下 SSE 的边界

历史续传读取 `session_events`，所以 `--after` 可以跨重启使用。

实时推送目前使用当前 server 进程内的订阅中心。如果未来部署多个 API 进程，需要补 Postgres `LISTEN/NOTIFY` 或独立消息队列，才能让所有进程共享实时 fanout。
