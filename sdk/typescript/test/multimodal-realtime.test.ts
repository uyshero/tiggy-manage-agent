import { describe, expect, it } from "vitest";
import {
  MULTIMODAL_REALTIME_PROTOCOL,
  MultimodalMediaFlag,
  TMAClient,
  decodeMultimodalMediaFrame,
  encodeMultimodalMediaFrame,
  type MultimodalMediaFrame,
} from "../src/index.js";

class FakeWebSocket {
  readyState = 0;
  protocol = MULTIMODAL_REALTIME_PROTOCOL;
  binaryType: BinaryType = "blob";
  readonly sent: Array<string | ArrayBufferLike | Blob | ArrayBufferView> = [];
  closedCode = 0;
  private readonly listeners = new Map<string, Array<(event: any) => void>>();

  constructor(readonly url: string, readonly protocols: string | string[]) {
    queueMicrotask(() => {
      this.readyState = 1;
      this.emit("open", {});
    });
  }

  addEventListener(type: string, listener: (event: any) => void): void {
    const values = this.listeners.get(type) ?? [];
    values.push(listener);
    this.listeners.set(type, values);
  }

  send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
    this.sent.push(data);
    if (typeof data === "string" && JSON.parse(data).type === "session.start") {
      this.emitMessage(JSON.stringify({
        type: "session.started",
        protocol_version: MULTIMODAL_REALTIME_PROTOCOL,
        session_id: "session-1",
        input_flow_limits: { max_frame_bytes: 4, max_in_flight_bytes: 4, max_in_flight_frames: 1 },
        initial_input_credit: { bytes: 4, frames: 1 },
        output_flow_limits: { max_frame_bytes: 4, max_in_flight_bytes: 4, max_in_flight_frames: 1 },
        initial_output_credit: { bytes: 4, frames: 1 },
        output_tracks: [{
          id: "speaker", kind: "audio", content_type: "audio/pcm", codec: "pcm_s16le",
          delivery: "reliable", sample_rate_hz: 16000, channels: 1,
        }],
        heartbeat_ms: 15000,
      }));
    }
  }

  close(code = 1000): void {
    this.readyState = 3;
    this.closedCode = code;
    this.emit("close", { code });
  }

  emitMessage(data: unknown): void {
    queueMicrotask(() => this.emit("message", { data }));
  }

  private emit(type: string, event: unknown): void {
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

describe("MultimodalRealtimeSession", () => {
  it("negotiates the protocol and applies latest-frame credit", async () => {
    let socket: FakeWebSocket | undefined;
    const client = new TMAClient("https://tma.example.com", {
      webSocketFactory: (url, protocols) => {
        socket = new FakeWebSocket(url, protocols);
        return socket as unknown as WebSocket;
      },
    });
    const session = client.modelRuntime.connectMultimodalRealtime();
    const limits = { max_frame_bytes: 4, max_in_flight_bytes: 4, max_in_flight_frames: 1 };
    const started = await session.start({
      provider_id: "native",
      model: "realtime",
      session_id: "session-1",
      input_tracks: [{
        id: "camera", kind: "video", content_type: "video/h264", codec: "h264",
        delivery: "latest", width: 640, height: 480, max_fps: 30,
      }],
      output_modalities: ["audio"],
      output_flow_limits: limits,
      initial_output_credit: { bytes: 4, frames: 1 },
    });
    expect(started.session_id).toBe("session-1");
    expect(socket?.url).toBe("wss://tma.example.com/v2/model-runtime/multimodal/realtime");
    expect(socket?.protocols).toBe(MULTIMODAL_REALTIME_PROTOCOL);

    const first: MultimodalMediaFrame = {
      kind: "video", track_id: "camera", sequence: 1, timestamp_us: 0, payload: new Uint8Array([1, 2, 3, 4]),
    };
    expect(await session.sendMedia(first)).toBe(true);
    expect(await session.sendMedia({ ...first, sequence: 2 })).toBe(false);
    socket!.emitMessage(JSON.stringify({
      type: "flow.credit", bytes: 4, frames: 1, track_id: "camera", acknowledged_sequence: 1,
    }));
    const credit = await session.read();
    expect(credit.credit?.acknowledged_sequence).toBe(1);
    expect(await session.sendMedia({ ...first, sequence: 3 })).toBe(true);

    const binary = socket!.sent.filter((value): value is Uint8Array => value instanceof Uint8Array);
    expect(binary).toHaveLength(2);
    const third = decodeMultimodalMediaFrame(binary[1]!);
    expect(third.sequence).toBe(3);
    expect((third.flags ?? 0) & MultimodalMediaFlag.Discontinuity).toBe(MultimodalMediaFlag.Discontinuity);
  });

  it("round trips the public binary frame codec", () => {
    const encoded = encodeMultimodalMediaFrame({
      kind: "audio", track_id: "speaker", sequence: 42, timestamp_us: 1234,
      flags: MultimodalMediaFlag.KeyFrame, payload: new Uint8Array([4, 3, 2, 1]),
    });
    const decoded = decodeMultimodalMediaFrame(encoded);
    expect(decoded).toMatchObject({ kind: "audio", track_id: "speaker", sequence: 42, timestamp_us: 1234, flags: 1 });
    expect([...decoded.payload]).toEqual([4, 3, 2, 1]);
    encoded[0] = 0;
    expect(() => decodeMultimodalMediaFrame(encoded)).toThrow("header");
  });

  it("closes instead of building an unbounded slow-consumer queue", async () => {
    let socket: FakeWebSocket | undefined;
    const client = new TMAClient("https://tma.example.com", {
      webSocketFactory: (url, protocols) => {
        socket = new FakeWebSocket(url, protocols);
        return socket as unknown as WebSocket;
      },
    });
    client.modelRuntime.connectMultimodalRealtime({ maxBufferedEvents: 1 });
    socket!.emitMessage(JSON.stringify({ type: "pong" }));
    socket!.emitMessage(JSON.stringify({ type: "pong" }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(socket!.closedCode).toBe(1008);
  });
});
