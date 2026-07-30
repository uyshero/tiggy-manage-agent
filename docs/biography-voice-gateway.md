# 自传语音网关

自传 App 使用独立 WebSocket 网关承载长连接和音频事件，不把豆包凭据下发到
UniApp，也不把实时音频放入普通 TMA Turn。

## 本地验证

启动模拟网关：

```bash
go run ./cmd/tma-biography-voice-gateway
```

启动接入网关的 H5 App：

```bash
cd apps/biography-mobile
VITE_BIOGRAPHY_VOICE_GATEWAY_URL=ws://127.0.0.1:8091/v1/voice/session \
VITE_BIOGRAPHY_VOICE_DEBUG_TEXT=false \
npm run dev:h5
```

H5 会申请浏览器麦克风权限，将输入连续转换为 16 kHz PCM16，并播放网关返回的
24 kHz PCM16。部署时页面必须使用 HTTPS，网关必须使用 `wss://`。没有麦克风时可将
`VITE_BIOGRAPHY_VOICE_DEBUG_TEXT` 临时设为 `true`，使用示例文字验证控制协议。
生产 App 由 Kotlin/Swift 原生插件采集音频，任何长期 Token 都不能写入 `VITE_*` 变量。
启用 `TMA_BIOGRAPHY_AUTH_MODE=oidc` 后，移动端会先读取 `/v1/auth/config`；若未配置
`VITE_BIOGRAPHY_AUTH_LOGIN_URL`，H5 默认通过 OIDC discovery 发起 Authorization Code + PKCE
登录，回跳后换取 token，再调用 `/v1/auth/me` 确认身份。若接入的是 TMA 统一登录层，也可以
配置 `VITE_BIOGRAPHY_AUTH_LOGIN_URL`，由登录层在回跳地址中返回 `oidc_token` / `access_token`
或标准 `code + state`。

当 TMA 使用 Keycloak 时，自传 App 使用独立的 `tma-biography-mobile` public client，而不是
复用 `tma-web`。它强制 Authorization Code + PKCE，并把 `tma-api` 写入 access token 的
audience，网关和 TMA 主服务因此可以复用同一个 issuer/JWKS 校验边界。H5 本地回跳地址为
`http://127.0.0.1:5175/`；当前生产 H5 回跳地址为 `https://story.tiggy.cloud/*`，正式 App
应在 IdP 中登记发布时的自定义 scheme 或 universal link，不要只保留开发地址。

## 配置

| 环境变量 | 默认值/用途 |
| --- | --- |
| `TMA_BIOGRAPHY_VOICE_HTTP_ADDR` | `:8091` |
| `TMA_BIOGRAPHY_VOICE_PROVIDER` | `mock`；生产使用 `doubao` |
| `TMA_BIOGRAPHY_VOICE_CLIENT_TOKEN` | 可选的网关 Bearer Token |
| `TMA_BIOGRAPHY_VOICE_ALLOWED_ORIGINS` | `localhost:*,127.0.0.1:*`；浏览器来源 host 白名单，同时用于 WebSocket 和 H5 REST CORS，例如 `app.example.com,localhost:*` |
| `TMA_BIOGRAPHY_AUTH_MODE` | `disabled`；生产使用 `oidc` |
| `TMA_BIOGRAPHY_AUTH_OIDC_ISSUER` | OIDC issuer；`oidc` 模式必填 |
| `TMA_BIOGRAPHY_AUTH_OIDC_AUDIENCE` | 网关校验的 token audience；`oidc` 模式必填 |
| `TMA_BIOGRAPHY_AUTH_OIDC_JWKS_URL` | 可选显式 JWKS 地址；不填则走 OIDC discovery |
| `TMA_BIOGRAPHY_AUTH_OIDC_CLIENT_ID` | 暴露给移动端的公开 OIDC client id |
| `TMA_BIOGRAPHY_AUTH_OIDC_SCOPES` | `openid,profile,email` |
| `TMA_BIOGRAPHY_DATA_DIR` | `.tma-biography`；保存用户隔离后的项目进度 |
| `TMA_LLM_API_KEY_ENV` | TMA 豆包 Provider 的 Key 变量名，默认 `TMA_LLM_API_KEY`；语音网关直接复用 |
| `TMA_BIOGRAPHY_VOICE_DOUBAO_API_KEY_ENV` | 可选覆盖，仅用于特殊部署；常规部署不配置 |
| `TMA_BIOGRAPHY_VOICE_DOUBAO_ASR_URL` | `wss://openspeech.bytedance.com/api/v3/plan/sauc/bigmodel_async`，Agent Plan ASR 2.0 双流地址；实时采访不使用统一返回结果的 `bigmodel_nostream` |
| `TMA_BIOGRAPHY_VOICE_DOUBAO_ASR_RESOURCE_ID` | `volc.seedasr.sauc.duration` |
| `TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_URL` | `wss://openspeech.bytedance.com/api/v3/plan/tts/bidirection`，Plan 双向流式 TTS 地址 |
| `TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_RESOURCE_ID` | `seed-tts-2.0` |
| `TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_MODEL` | 内置 Seed-TTS 2.0 音色留空；仅自定义/复刻模型按控制台要求配置 |
| `TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_SPEAKER` | `zh_female_kefunvsheng_uranus_bigtts`（暖阳女声 2.0，支持语气指令） |
| `TMA_BIOGRAPHY_INTERVIEW_PROVIDER` | `mock`；生产使用 `tma` |
| `TMA_BIOGRAPHY_TMA_BASE_URL` | `http://127.0.0.1:8080` |
| `TMA_BIOGRAPHY_TMA_TOKEN_ENV` | TMA Bearer Token 变量名，默认 `TMA_AUTH_TOKEN` |
| `TMA_BIOGRAPHY_TMA_AGENT_ID` | `tma` 模式必填，实时自传采访 Agent ID |
| `TMA_BIOGRAPHY_TMA_ORGANIZER_AGENT_ID` | 后台章节整理 Agent ID；未配置时兼容性回退到采访 Agent |
| `TMA_BIOGRAPHY_TMA_ENVIRONMENT_ID` | 可选；不填时使用 Agent 绑定的 Environment |
| `TMA_BIOGRAPHY_TMA_WORKSPACE_ID` | 可选，多租户部署时明确指定 |
| `TMA_BIOGRAPHY_TMA_OWNER_ID` | 可选，生产由登录用户映射 |
| `TMA_BIOGRAPHY_TMA_INTERVIEW_THINKING` | `disabled`；实时采访关闭深度思考以降低首字延迟 |
| `TMA_BIOGRAPHY_TMA_INTERVIEW_COMPACTION_THRESHOLD_TOKENS` | `8000`；采访上下文达到该估算值后摘要压缩 |
| `TMA_BIOGRAPHY_TMA_INTERVIEW_COMPACTION_SUMMARY_MAX_CHARS` | `4000`；采访摘要最大字符数 |
| `TMA_BIOGRAPHY_INTERVIEW_FIRST_RESPONSE_TIMEOUT` | `6s`；超过后取消慢请求并返回可朗读兜底问题 |
| `TMA_BIOGRAPHY_INTERVIEW_TIMEOUT` | `45s`；单轮采访总超时 |
| `TMA_BIOGRAPHY_RESUME_SIGNING_KEY` | `tma` 模式必填，至少 32 字节的服务端恢复密钥 |

## 初始化采访 Agent

TMA 主服务启动后，执行一次：

```bash
go run ./cmd/tma-biography-agent-bootstrap
```

命令默认复用 `.env` 中的 `TMA_LLM_PROVIDER` 和 `TMA_LLM_MODEL`，分别维护“自传采访者”和“自传章节整理者”。
可用 `TMA_BIOGRAPHY_TMA_INTERVIEW_MODEL` 和 `TMA_BIOGRAPHY_TMA_ORGANIZER_MODEL` 为两个职责选择不同模型。
实时采访完整注入采访 Skill，后台整理完整注入章节整理与事实核验 Skills，
两个 Agent 都显式关闭不需要的平台默认工具，避免把工具定义加入每轮模型上下文，
同时把仓库 `skills/` 下的专业采访、事实核验和章节梳理 Skill 幂等发布到 TMA Skill Registry，
并将明确版本绑定到 Agent。Skill 内容未变化时不会重复发布版本。已存在时更新必要的配置，
不存在时才创建工作区所属的 Custom Agent。输出中的 `next_config`
写明网关需要的 Agent ID 和 Environment ID。未显式配置 Environment 时，命令优先使用同工作区
的“通用 Sandbox”；同名旧 Agent 尚未绑定时会补上该绑定。无法唯一判断默认 Environment 时会
要求显式设置 `TMA_BIOGRAPHY_TMA_ENVIRONMENT_ID`。该命令不会修改 `.env`。

启用真实采访时配置：

```env
TMA_BIOGRAPHY_INTERVIEW_PROVIDER=tma
TMA_BIOGRAPHY_TMA_AGENT_ID=agt_xxx
TMA_BIOGRAPHY_TMA_ORGANIZER_AGENT_ID=agt_xxx
TMA_BIOGRAPHY_TMA_ENVIRONMENT_ID=env_xxx
```

生产环境必须使用 `wss://` 豆包地址、OIDC 用户凭据、明确的 Origin allowlist 和
独立网关鉴权。登录由 OIDC Provider / TMA 登录层完成，不自建短信验证码；网关只校验
Bearer token，并用 `iss + sub` 生成内部用户 ID，隔离自传项目、采访 session、录音、
转写文本、章节整理结果、最近问题和待确认内容。前端不得传入或伪造 `userId`。
豆包 API Key 只允许从服务端 Secret/环境变量解析。ASR/TTS 与 TMA 对话
模型共用这一个 Key，但各自的 Resource ID、WebSocket 地址、TTS 模型和音色仍分开配置。
网关与 TMA 主服务一样会先加载当前目录的 `.env`，shell 中已显式设置的变量优先。

TMA 采访采用实时、后台两条通道。实时通道只生成下一句追问和 TTS 情感指令，写出
`interview.reply` 后即可开始合成语音，不等待章节整理。每个 WebSocket 连接另有一个有序的
后台整理队列，使用独立 TMA Session 逐条处理已经确认的口述，完成后推送
`interview.project.updated`。网关会校验章节必填字段及 `0-100` 进度范围；整理失败只记录
服务端日志，不打断正在进行的采访，也不会用无效输出覆盖 App 中的项目状态。

项目中的 `bookGoal` 记录目标读者、希望读者最终记住或理解的内容，以及该目标是否经过用户
明确确认。每章的 `narrative` 分别跟踪事件、场景、感受、关系、选择、影响和今日回望，
`nextFocus` 记录下一次最值得补充的一个方向。旧项目可以暂时没有这些字段；新的后台整理结果
必须完整返回它们。章节进度按未来成书可用的叙事材料评估，不按已经提到多少年份或事件评估。
连接建立时，`mock` 模式下发演示项目；`TMA` 模式下发空白自传项目，不会把演示人物和
故事带入真实用户的 TMA Session。

真实采访每轮成功后会签发 AES-GCM 加密的 `resume_token`，内部包含 TMA Session、
客户端实例、最新章节快照和过期时间。实时回复与后台章节更新都会携带当时最新的令牌，
App 始终保存后收到的版本，不会收到明文 TMA Session ID。恢复时网关会校验客户端绑定、
凭据有效期和 Session 归属的采访 Agent；新令牌优先恢复其中的异步章节快照，旧版令牌仍可
从最近一条包含完整项目的有效 `agent.message` 恢复。

## App 协议

客户端文本事件：

- `session.start`：包含 `client_instance_id`，可选包含 `resume_token`
- `input.commit`
- `tts.start`
- `tts.cancel`
- `session.finish`

客户端二进制帧用于上传音频。`asr.debug_text` 仅存在于模拟 Provider。

服务端文本事件：

- `session.ready`
- `asr.partial` / `asr.final`
- `interview.project`：当前自传项目初始状态
- `interview.reply.delta`：TMA 生成中的采访话术预览，供页面提前显示
- `interview.reply`：立即返回的采访话术、情感指令和当前章节快照
- `interview.project.updated`：后台整理完成后的最新章节进度和恢复令牌
- `tts.started` / `tts.finished` / `tts.canceled`
- `session.finished`
- `error`

`interview.project.updated` 与 TTS 事件相互独立，客户端不能假定两者的到达顺序。真实
Provider 会使用服务端二进制帧返回 TTS 音频。每个连接只允许主会话循环写 WebSocket；
后台整理结果通过内部通道交给主循环。取消事件必须清空尚未播放的音频，并保留已经播放到的文本边界。
H5 WebSocket 无法设置自定义 `Authorization` header，因此 `oidc` 模式下可把当前 OIDC token
放在 `access_token` 查询参数；原生 App 插件应优先使用 `Authorization: Bearer <token>`。
App 本地的“上次采访”和录音列表同样按当前 OIDC 用户 ID 分区；退出登录会清空本机 token 和
`resume_token`，但不会删除已保存的采访进度或录音文件。
