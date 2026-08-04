# Multimodal Realtime v1 协议

`tma.multimodal.realtime.v1` 是 Platform 计划用于音频、图片和视频实时输入输出的厂商无关协议。
当前已实现协议类型、输入校验、二进制帧编解码、credit window、TMA-native 和 OpenAI Realtime
Provider Adapter、Server→Runtime 内部 WebSocket、模型目录准入、Server ObjectRef 解析和 Invocation 审计。公共入口为
`GET /v2/model-runtime/multimodal/realtime`，必须协商本协议并通过用户鉴权或持有
`model:realtime` Scope。Go/TypeScript Realtime Core SDK 已提供帧编解码、credit 等待、`latest`
丢弃和有界事件队列。OpenAI Adapter 已完成本地契约、慢 Provider、慢客户端、双向断线和双 Server
共享 Quota Store 测试；真实账号与部署环境中的 PostgreSQL 多副本压测仍是生产开放门槛。

## 设计边界

Realtime Speech 继续使用 `/v2/speech/realtime`，保证 Biography 等现有应用不受影响。Multimodal
Realtime 使用独立协议，原因是视频需要 track、sequence、timestamp、key frame、丢帧策略和双向
流控，不能用 Speech 的无头二进制 PCM 帧表达。

职责保持如下：

```text
Application / Core SDK
  - 遵守 credit，不在 SDK 内建立无界发送队列
  - reliable 输入等待，latest 视频可丢弃尚未发送的旧帧
             |
             v
tma-server
  - Application/User 鉴权、Scope、Workspace、模型目录和 Quota
  - ObjectRef ACL、大小/Content-Type/SHA-256 校验
  - Invocation Audit 和稳定错误契约
             |
             v
tma-model-runtime
  - v1 帧校验、credit window、Provider Adapter 和有界厂商缓冲
  - Provider 消费后才归还输入 credit
             |
             v
Provider Adapter
  - TMA track/frame/event 与厂商协议互转
  - 不读取 Platform 数据库，不解析应用 Credential
```

Runtime 内部端点为 `GET /internal/v1/multimodal/realtime`，使用与其他 Runtime 请求相同的静态开发
凭证或绑定 `GET` 和该 path 的短期签名 Token。原生 Adapter 协议标识为
`tma_multimodal_websocket_v1`：兼容网关可以直接实现本协议。OpenAI Adapter 协议标识为
`openai_realtime_websocket`，只做厂商事件与 v1 之间的翻译，不改变 Core 协议。Provider API Key 只写入上游 WebSocket 的
`Authorization` Header，不进入 URL、事件或日志。

OpenAI 首版边界是：输入音频必须为 24 kHz、单声道 PCM16；图片是独立的 JPEG/PNG 帧；输出为
文本和/或 24 kHz PCM16 音频。Adapter 将音频映射为 `input_audio_buffer.append`，将文本和图片映射为
conversation item，将 `input.commit` 映射为音频 commit（存在音频时）和 response create。它不声明、
不接受连续 H264 视频；需要摄像头输入时，应用按目录和 credit 发送离散 JPEG/PNG 图片帧。

公共入口不会把 `input.object_ref` 原样传给 Runtime。Server 先校验 Workspace/Owner/Session ACL、
持久化元数据、对象存储元数据、实际大小和 SHA-256，再转换成普通二进制媒体帧。独立 Runtime 因此
不需要 Platform 数据库或对象存储权限；任何绕过 Server 到达 Runtime 的 `input.object_ref` 都会被拒绝。

## 会话状态

连接升级时协商 WebSocket subprotocol `tma.multimodal.realtime.v1`。第一条消息必须是 JSON
`session.start`；在收到 `session.started` 前发送媒体帧属于协议错误。

```text
connected -> starting -> active -> draining -> completed
                         |    |
                         |    +-> canceled
                         +------> failed
```

开始事件声明输入 track、期望输出和客户端愿意接收 Provider 输出的窗口：

```json
{
  "type": "session.start",
  "protocol_version": "tma.multimodal.realtime.v1",
  "provider_id": "realtime-provider",
  "model": "realtime-model",
  "input_tracks": [
    {
      "id": "microphone",
      "kind": "audio",
      "content_type": "audio/pcm",
      "codec": "pcm_s16le",
      "delivery": "reliable",
      "sample_rate_hz": 16000,
      "channels": 1
    },
    {
      "id": "camera",
      "kind": "video",
      "content_type": "video/h264",
      "codec": "h264",
      "delivery": "latest",
      "width": 1280,
      "height": 720,
      "max_fps": 30
    }
  ],
  "output_modalities": ["text", "audio"],
  "output_flow_limits": {
    "max_frame_bytes": 4194304,
    "max_in_flight_bytes": 16777216,
    "max_in_flight_frames": 8
  },
  "initial_output_credit": {"bytes": 4194304, "frames": 2},
  "backpressure_timeout_ms": 5000
}
```

Server 返回 `session.started`，其中 `input_flow_limits` 和 `initial_input_credit` 是客户端向 Platform
发送媒体的硬限制；`output_flow_limits` 是双方最终协商的 Platform 输出限制。默认上限为 8 个输入
track、单帧 4 MiB、在途 16 MiB 和 8 帧。Adapter 可以按 Provider 能力进一步降低，不能提高。

## Track 投递策略

- `reliable`：不得丢帧。音频和离散图片必须使用该策略；credit 不足时发送方等待，超过
  `backpressure_timeout_ms` 后以 `backpressure_timeout` 结束会话。
- `latest`：只允许视频。SDK 可以丢弃尚未 Reserve、尚未写入 WebSocket 的旧非关键帧；已经发送的
  帧不能撤回。发生丢弃后，下一帧设置 `discontinuity`，必要时等待下一个 key frame。
- sequence 在每个 track 内严格递增，但允许有间隔；重复或倒退直接返回
  `media_sequence_violation`。

## 二进制媒体帧

文本控制事件使用 WebSocket text message；音频、图片和视频使用 WebSocket binary message。固定头
为 28 字节，所有多字节整数使用 network byte order：

| Offset | 长度 | 字段 |
| --- | ---: | --- |
| 0 | 4 | Magic `TMAM` |
| 4 | 1 | Wire version，当前为 `1` |
| 5 | 1 | Kind：`1=audio`、`2=image`、`3=video` |
| 6 | 2 | Flags：`key_frame=1`、`end_of_track=2`、`discontinuity=4` |
| 8 | 8 | Track 内 sequence，必须大于 0 |
| 16 | 8 | 从会话媒体时间轴起算的非负 `timestamp_us` |
| 24 | 2 | Track ID 字节数，范围 `1..64` |
| 26 | 2 | Reserved，必须为 0 |
| 28 | 可变 | ASCII Track ID，随后是媒体 payload |

实现位于 `internal/modelruntimeprovider/multimodal_protocol.go`。Decoder 会复制 payload，调用方修改
WebSocket 输入 buffer 不会改变已经校验的帧。

## 双向背压

credit 同时包含 bytes 和 frames，发送一帧必须两者都充足：

```text
reserve(frame) -> credit.bytes -= payload bytes
               -> credit.frames -= 1

receiver consumed frame
               -> flow.credit(bytes=payload bytes, frames=1)
```

Server 只有在 Provider Adapter 已消费帧，或已经把帧复制进受限的厂商缓冲区后，才能向客户端发送
输入 `flow.credit`。仅从 WebSocket 读出数据不代表已经消费，不能提前归还。反方向上，客户端消费完
Platform 输出后发送 `flow.credit`。任一方向超出 credit、超出协商上限或 credit 溢出时，以
`flow_control_violation` 关闭连接。

`flow.slow` 只提供建议 FPS/码率，不替代 credit。实现不得通过增加无界 channel 来“解决”慢消费；
允许的最大队列始终包含在已扣除的 in-flight credit 中。

OpenAI Adapter 的每次上游写入同样受 `backpressure_timeout_ms` 限制；未配置时默认为 5000 ms。
Provider 未及时消费时返回可重试的 `backpressure_timeout`，Provider 在响应完成前断开时返回
`multimodal_provider_disconnected`。客户端断开会立即结束 Adapter 并关闭上游连接。厂商原始关闭原因
不进入公开事件或 Invocation 审计。

## ObjectRef 输入

`input.object_ref` 只用于离散图片或单个视频媒体单元，不用于连续音频，也不允许绕过 credit：

```json
{
  "type": "input.object_ref",
  "track_id": "camera",
  "sequence": 43,
  "timestamp_us": 1533000,
  "object_ref_id": "obj_123",
  "content_type": "video/h264",
  "size_bytes": 1048576,
  "checksum_sha256": "..."
}
```

`tma-server` 内部解析器已按调用者 Workspace、Owner 和 Session 访问范围读取 ObjectRef。Workspace
可见对象要求一个同 Workspace 且调用者可访问的 Session；Session 可见对象还必须已关联为该 Session
的 Artifact。解析器比较客户端声明、track、数据库元数据、对象存储元数据和实际内容的大小、
Content-Type 与 SHA-256，再由后续公共代理扣除实际字节 credit。客户端声明值不可信。
Server 只把已验证内容交给 Runtime，不下发 bucket、object key、永久 URL 或对象存储 Credential。
超过协商单帧上限的长视频应先走离线 Artifact/任务处理，不能作为实时帧发送。

## 控制事件

V1 客户端事件限定为 `session.start`、`input.text.append`、`input.object_ref`、`input.commit`、
`flow.credit`、`ping` 和 `session.cancel`。Server 事件限定为 `session.started`、
`output.text.delta`、`output.text.final`、二进制媒体帧、`flow.credit`、`flow.slow`、`pong`、
`session.completed`、`session.canceled` 和 `error`。未知事件必须报错，不能静默透传给 Provider。

## 审计与安全

- 原始媒体、Prompt、转录和 ObjectRef 内容不写日志；默认不持久化。
- `input_items/output_items` 已记录媒体帧、ObjectRef 和最终文本项；字节数使用实际 payload。
- PCM 音频时长按协商格式计算；视频记录帧数、`latest` sequence 间隙和逐 track 媒体时间跨度，不伪装成 token。
- `multimodal_realtime` Invocation recorder 已保留 Provider、Model、Application/Service Identity、
  稳定错误归因和 completed/failed/canceled 终态，并防止连接关闭竞争造成重复记录。
- 会话沿用 Workspace/Application Quota、最大时长、mTLS/Service Mesh 和逐请求内部签名凭证。
- Provider Adapter 必须声明支持的 track kind、codec、最大帧和输出 modality；目录不匹配时在连接
  Provider 前拒绝。

## 生产接入剩余门槛

Core OpenAPI、公共 WebSocket、目录准入、ObjectRef、Invocation、Go/TypeScript SDK、TMA-native
Adapter 和 OpenAI Realtime Adapter 已完成。SDK 的 `reliable` 输入只在 credit 可用时发送；`latest` 视频在 credit 不足时返回
`false`，并自动在下一次成功发送的媒体帧上设置 `discontinuity`。TypeScript 事件队列达到配置硬上限
时会关闭连接，不会继续积累内存。

剩余门槛：

1. 使用真实 OpenAI 账号验证当前模型、音频、图片和错误/限流事件契约，并形成可重复的冒烟测试。
2. 在部署环境对真实慢链路、大帧和 PostgreSQL 多副本 Quota 做端到端压测；本地可控慢链路、断线、
   重复 sequence、credit 越界和双 Server 共享配额测试已完成。

真实文本冒烟测试默认跳过，只有显式打开开关才产生外网调用和 Provider 费用：

```bash
TMA_RUN_OPENAI_REALTIME_TESTS=1 \
OPENAI_API_KEY='...' \
TMA_OPENAI_REALTIME_MODEL='gpt-realtime' \
go test ./internal/modelruntimeprovider -run TestOpenAIRealtimeLiveSmoke -count=1 -v
```

可选 `TMA_OPENAI_REALTIME_URL` 覆盖默认 `wss://api.openai.com/v1/realtime`。测试不会输出 API Key；
模型 ID 通过环境变量显式指定，便于在厂商模型版本变化时保持测试代码稳定。
