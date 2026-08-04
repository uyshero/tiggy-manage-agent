export const MULTIMODAL_REALTIME_PROTOCOL = "tma.multimodal.realtime.v1";
export const MULTIMODAL_MEDIA_HEADER_BYTES = 28;
export const MULTIMODAL_MAX_TRACK_ID_BYTES = 64;
export const MULTIMODAL_MAX_FRAME_BYTES = 4 << 20;
export const MULTIMODAL_MAX_IN_FLIGHT_BYTES = 16 << 20;
export const MULTIMODAL_MAX_IN_FLIGHT_FRAMES = 8;

export type MultimodalMediaKind = "audio" | "image" | "video";
export type MultimodalDelivery = "reliable" | "latest";

export const MultimodalMediaFlag = {
  KeyFrame: 1,
  EndOfTrack: 2,
  Discontinuity: 4,
} as const;

const allowedMediaFlags = MultimodalMediaFlag.KeyFrame | MultimodalMediaFlag.EndOfTrack | MultimodalMediaFlag.Discontinuity;

export interface MultimodalTrack {
  id: string;
  kind: MultimodalMediaKind;
  content_type: string;
  codec: string;
  delivery: MultimodalDelivery;
  sample_rate_hz?: number;
  channels?: number;
  width?: number;
  height?: number;
  max_fps?: number;
}

export interface MultimodalFlowLimits {
  max_frame_bytes: number;
  max_in_flight_bytes: number;
  max_in_flight_frames: number;
}

export interface MultimodalFlowCredit {
  type?: "flow.credit";
  bytes: number;
  frames: number;
  track_id?: string;
  acknowledged_sequence?: number;
}

export interface MultimodalSessionStart {
  provider_id: string;
  model: string;
  session_id?: string;
  input_tracks: MultimodalTrack[];
  output_modalities: Array<"text" | MultimodalMediaKind>;
  output_flow_limits?: MultimodalFlowLimits;
  initial_output_credit?: MultimodalFlowCredit;
  backpressure_timeout_ms?: number;
}

export interface MultimodalSessionStarted {
  type: "session.started";
  protocol_version: string;
  session_id: string;
  output_tracks?: MultimodalTrack[];
  input_flow_limits: MultimodalFlowLimits;
  initial_input_credit: MultimodalFlowCredit;
  output_flow_limits?: MultimodalFlowLimits;
  initial_output_credit?: MultimodalFlowCredit;
  heartbeat_ms: number;
}

export interface MultimodalMediaFrame {
  kind: MultimodalMediaKind;
  flags?: number;
  sequence: number;
  timestamp_us: number;
  track_id: string;
  payload: Uint8Array;
}

export interface MultimodalObjectRefInput {
  track_id: string;
  sequence: number;
  timestamp_us: number;
  object_ref_id: string;
  content_type: string;
  size_bytes: number;
  checksum_sha256?: string;
}

export interface MultimodalRealtimeEvent {
  type: string;
  session_id?: string;
  track_id?: string;
  text?: string;
  sequence?: number;
  timestamp_us?: number;
  bytes?: number;
  frames?: number;
  acknowledged_sequence?: number;
  recommended_fps?: number;
  reason?: string;
  code?: string;
  message?: string;
  retryable?: boolean;
  retry_after_seconds?: number;
  limit_scope?: "global" | "workspace" | "identity" | "route";
  started?: MultimodalSessionStarted;
  credit?: MultimodalFlowCredit;
  media?: MultimodalMediaFrame;
}

export interface MultimodalRealtimeOptions {
  maxBufferedEvents?: number;
}

export class MultimodalRealtimeError extends Error {
  readonly code: string;
  readonly retryable: boolean;
  readonly retryAfterSeconds: number;
  readonly limitScope: string;

  constructor(event: MultimodalRealtimeEvent) {
    super(event.message?.trim() || "Multimodal realtime request failed");
    this.name = "MultimodalRealtimeError";
    this.code = event.code?.trim() ?? "";
    this.retryable = event.retryable ?? false;
    this.retryAfterSeconds = event.retry_after_seconds ?? 0;
    this.limitScope = event.limit_scope ?? "";
  }
}

interface EventWaiter {
  resolve: (event: MultimodalRealtimeEvent) => void;
  reject: (error: Error) => void;
  cleanup: () => void;
}

export class MultimodalRealtimeSession {
  private readonly socket: WebSocket;
  private readonly maxBufferedEvents: number;
  private readonly queue: MultimodalRealtimeEvent[] = [];
  private readonly waiters: EventWaiter[] = [];
  private messageChain: Promise<void> = Promise.resolve();
  private pendingMessages = 0;
  private failure: Error | undefined;
  private startAttempted = false;
  private started = false;
  private inputLimits: MultimodalFlowLimits | undefined;
  private inputBytes = 0;
  private inputFrames = 0;
  private readonly tracks = new Map<string, MultimodalTrack>();
  private readonly sequences = new Map<string, number>();
  private readonly droppedLatest = new Set<string>();
  private creditWaiters: Array<() => void> = [];

  constructor(socket: WebSocket, options: MultimodalRealtimeOptions = {}) {
    this.socket = socket;
    this.maxBufferedEvents = options.maxBufferedEvents ?? 32;
    if (!Number.isInteger(this.maxBufferedEvents) || this.maxBufferedEvents < 1 || this.maxBufferedEvents > 1024) {
      throw new RangeError("maxBufferedEvents must be between 1 and 1024");
    }
    this.socket.binaryType = "arraybuffer";
    this.socket.addEventListener("message", (event) => {
      this.pendingMessages += 1;
      if (this.pendingMessages > this.maxBufferedEvents) {
        this.pendingMessages -= 1;
        this.socket.close(1008, "multimodal pending message buffer exceeded");
        this.fail(new Error("Multimodal pending message buffer exceeded"));
        return;
      }
      this.messageChain = this.messageChain
        .then(() => this.processMessage(event.data))
        .catch((error: unknown) => this.fail(asError(error)))
        .finally(() => { this.pendingMessages -= 1; });
    });
    this.socket.addEventListener("error", () => this.fail(new Error("Multimodal WebSocket failed")));
    this.socket.addEventListener("close", (event) => {
      if (this.failure === undefined) this.fail(new Error(`Multimodal WebSocket closed (${event.code})`));
    });
  }

  async start(request: MultimodalSessionStart, signal?: AbortSignal): Promise<MultimodalSessionStarted> {
    if (this.startAttempted) throw new Error("Multimodal session start has already been attempted");
    this.startAttempted = true;
    validateSessionStart(request);
    await this.waitUntilOpen(signal);
    if (this.socket.protocol !== MULTIMODAL_REALTIME_PROTOCOL) {
      this.socket.close(1008, "multimodal subprotocol was not negotiated");
      throw new Error("Server did not negotiate the multimodal realtime subprotocol");
    }
    this.sendJSON({ type: "session.start", protocol_version: MULTIMODAL_REALTIME_PROTOCOL, ...request });
    const event = await this.nextEvent(signal);
    if (event.type === "error") throw new MultimodalRealtimeError(event);
    if (event.started === undefined) throw new Error("First multimodal server event must be session.started");
    validateSessionStarted(event.started, request);
    this.started = true;
    this.inputLimits = event.started.input_flow_limits;
    this.inputBytes = event.started.initial_input_credit.bytes;
    this.inputFrames = event.started.initial_input_credit.frames;
    for (const track of request.input_tracks) this.tracks.set(track.id, track);
    this.notifyCredit();
    return event.started;
  }

  async sendMedia(frame: MultimodalMediaFrame, signal?: AbortSignal): Promise<boolean> {
    validateMediaFrame(frame);
    const track = this.tracks.get(frame.track_id);
    if (track !== undefined && track.kind !== frame.kind) throw new TypeError("Media frame kind does not match its input track");
    const reserved = await this.reserveInput(frame.track_id, frame.sequence, frame.payload.byteLength, signal);
    if (!reserved) return false;
    const flags = (frame.flags ?? 0) | (this.droppedLatest.delete(frame.track_id) ? MultimodalMediaFlag.Discontinuity : 0);
    this.socket.send(encodeMultimodalMediaFrame({ ...frame, flags }));
    return true;
  }

  async sendObjectRef(input: MultimodalObjectRefInput, signal?: AbortSignal): Promise<boolean> {
    if (!input.object_ref_id.trim() || !positiveSafeInteger(input.size_bytes) || !nonNegativeSafeInteger(input.timestamp_us)) {
      throw new TypeError("ObjectRef input requires object_ref_id, positive size_bytes, and non-negative timestamp_us");
    }
    const reserved = await this.reserveInput(input.track_id, input.sequence, input.size_bytes, signal);
    if (!reserved) return false;
    this.droppedLatest.delete(input.track_id);
    this.sendJSON({ type: "input.object_ref", ...input });
    return true;
  }

  appendText(text: string): void {
    if (!text.trim()) throw new TypeError("Multimodal input text is required");
    this.sendJSON({ type: "input.text.append", text });
  }

  commitInput(): void {
    this.sendJSON({ type: "input.commit" });
  }

  grantOutputCredit(frame: MultimodalMediaFrame): void {
    if (!positiveSafeInteger(frame.sequence) || frame.payload.byteLength < 1 || !frame.track_id.trim()) {
      throw new TypeError("Output credit requires a consumed media frame");
    }
    this.sendJSON({
      type: "flow.credit", bytes: frame.payload.byteLength, frames: 1,
      track_id: frame.track_id, acknowledged_sequence: frame.sequence,
    });
  }

  ping(): void {
    this.sendJSON({ type: "ping" });
  }

  cancel(): void {
    this.sendJSON({ type: "session.cancel" });
  }

  read(signal?: AbortSignal): Promise<MultimodalRealtimeEvent> {
    if (!this.started) return Promise.reject(new Error("Multimodal session has not started"));
    return this.nextEvent(signal);
  }

  private nextEvent(signal?: AbortSignal): Promise<MultimodalRealtimeEvent> {
    if (this.queue.length > 0) {
      try {
        return Promise.resolve(this.consumeEvent(this.queue.shift()!));
      } catch (error) {
        return Promise.reject(asError(error));
      }
    }
    if (this.failure !== undefined) return Promise.reject(this.failure);
    return new Promise<MultimodalRealtimeEvent>((resolve, reject) => {
      let abort = () => {};
      const waiter: EventWaiter = {
        resolve, reject,
        cleanup: () => signal?.removeEventListener("abort", abort),
      };
      this.waiters.push(waiter);
      if (signal !== undefined) {
        abort = () => {
          const index = this.waiters.indexOf(waiter);
          if (index >= 0) this.waiters.splice(index, 1);
          waiter.cleanup();
          reject(signal.reason instanceof Error ? signal.reason : new DOMException("Aborted", "AbortError"));
        };
        if (signal.aborted) abort();
        else signal.addEventListener("abort", abort, { once: true });
      }
    });
  }

  close(code = 1000, reason = "multimodal session complete"): void {
    this.socket.close(code, reason);
  }

  private async processMessage(data: unknown): Promise<void> {
    if (typeof data === "string") {
      const parsed = JSON.parse(data) as MultimodalRealtimeEvent;
      if (!parsed || typeof parsed.type !== "string") throw new TypeError("Invalid multimodal control event");
      const event: MultimodalRealtimeEvent = { ...parsed };
      if (parsed.type === "session.started") event.started = parsed as unknown as MultimodalSessionStarted;
      if (parsed.type === "flow.credit") {
        const credit = parsed as unknown as MultimodalFlowCredit;
        event.credit = credit;
      }
      this.enqueue(event);
      return;
    }
    const bytes = await messageBytes(data);
    const media = decodeMultimodalMediaFrame(bytes);
    this.enqueue({
      type: "media", track_id: media.track_id, sequence: media.sequence,
      timestamp_us: media.timestamp_us, media,
    });
  }

  private enqueue(event: MultimodalRealtimeEvent): void {
    const waiter = this.waiters.shift();
    if (waiter !== undefined) {
      waiter.cleanup();
      try {
        waiter.resolve(this.consumeEvent(event));
      } catch (error) {
        waiter.reject(asError(error));
      }
      return;
    }
    if (this.queue.length >= this.maxBufferedEvents) {
      this.socket.close(1008, "multimodal event buffer exceeded");
      throw new Error("Multimodal event buffer exceeded");
    }
    this.queue.push(event);
  }

  private async reserveInput(trackID: string, sequence: number, size: number, signal?: AbortSignal): Promise<boolean> {
    for (;;) {
      if (this.failure !== undefined) throw this.failure;
      if (!this.started || this.inputLimits === undefined) throw new Error("Multimodal session has not started");
      const track = this.tracks.get(trackID);
      if (track === undefined || !positiveSafeInteger(sequence) || !positiveSafeInteger(size) || size > this.inputLimits.max_frame_bytes) {
        throw new TypeError("Invalid multimodal input frame");
      }
      if (sequence <= (this.sequences.get(trackID) ?? 0)) throw new RangeError("Multimodal sequence must increase per track");
      if (size <= this.inputBytes && this.inputFrames > 0) {
        this.inputBytes -= size;
        this.inputFrames -= 1;
        this.sequences.set(trackID, sequence);
        return true;
      }
      if (track.delivery === "latest") {
        this.droppedLatest.add(trackID);
        return false;
      }
      await this.waitForCredit(signal);
    }
  }

  private applyInputCredit(credit: MultimodalFlowCredit): void {
    if (!this.started || this.inputLimits === undefined || !positiveSafeInteger(credit.bytes) || !positiveSafeInteger(credit.frames) ||
      this.inputBytes + credit.bytes > this.inputLimits.max_in_flight_bytes || this.inputFrames + credit.frames > this.inputLimits.max_in_flight_frames) {
      throw new RangeError("Multimodal input credit exceeds negotiated limits");
    }
    this.inputBytes += credit.bytes;
    this.inputFrames += credit.frames;
    this.notifyCredit();
  }

  private waitForCredit(signal?: AbortSignal): Promise<void> {
    if (signal?.aborted) return Promise.reject(signal.reason instanceof Error ? signal.reason : new DOMException("Aborted", "AbortError"));
    return new Promise<void>((resolve, reject) => {
      const wake = () => resolve();
      this.creditWaiters.push(wake);
      signal?.addEventListener("abort", () => {
        const index = this.creditWaiters.indexOf(wake);
        if (index >= 0) this.creditWaiters.splice(index, 1);
        reject(signal.reason instanceof Error ? signal.reason : new DOMException("Aborted", "AbortError"));
      }, { once: true });
    });
  }

  private consumeEvent(event: MultimodalRealtimeEvent): MultimodalRealtimeEvent {
    if (event.credit !== undefined) this.applyInputCredit(event.credit);
    return event;
  }

  private notifyCredit(): void {
    const waiters = this.creditWaiters;
    this.creditWaiters = [];
    for (const resolve of waiters) resolve();
  }

  private sendJSON(value: unknown): void {
    if (this.socket.readyState !== 1) throw new Error("Multimodal WebSocket is not open");
    this.socket.send(JSON.stringify(value));
  }

  private waitUntilOpen(signal?: AbortSignal): Promise<void> {
    if (signal?.aborted) return Promise.reject(signal.reason instanceof Error ? signal.reason : new DOMException("Aborted", "AbortError"));
    if (this.socket.readyState === 1) return Promise.resolve();
    if (this.socket.readyState !== 0) return Promise.reject(new Error("Multimodal WebSocket is not connectable"));
    return new Promise<void>((resolve, reject) => {
      const opened = () => resolve();
      const failed = () => reject(new Error("Multimodal WebSocket failed to open"));
      this.socket.addEventListener("open", opened, { once: true });
      this.socket.addEventListener("error", failed, { once: true });
      signal?.addEventListener("abort", () => reject(signal.reason instanceof Error ? signal.reason : new DOMException("Aborted", "AbortError")), { once: true });
    });
  }

  private fail(error: Error): void {
    if (this.failure !== undefined) return;
    this.failure = error;
    for (const waiter of this.waiters.splice(0)) {
      waiter.cleanup();
      waiter.reject(error);
    }
    this.notifyCredit();
  }
}

export function encodeMultimodalMediaFrame(frame: MultimodalMediaFrame): Uint8Array {
  validateMediaFrame(frame);
  const track = new TextEncoder().encode(frame.track_id);
  const encoded = new Uint8Array(MULTIMODAL_MEDIA_HEADER_BYTES + track.byteLength + frame.payload.byteLength);
  encoded.set([0x54, 0x4d, 0x41, 0x4d, 1, mediaKindCode(frame.kind)], 0);
  const view = new DataView(encoded.buffer);
  view.setUint16(6, frame.flags ?? 0);
  view.setBigUint64(8, BigInt(frame.sequence));
  view.setBigUint64(16, BigInt(frame.timestamp_us));
  view.setUint16(24, track.byteLength);
  encoded.set(track, MULTIMODAL_MEDIA_HEADER_BYTES);
  encoded.set(frame.payload, MULTIMODAL_MEDIA_HEADER_BYTES + track.byteLength);
  return encoded;
}

export function decodeMultimodalMediaFrame(encoded: Uint8Array): MultimodalMediaFrame {
  if (encoded.byteLength < MULTIMODAL_MEDIA_HEADER_BYTES || encoded[0] !== 0x54 || encoded[1] !== 0x4d || encoded[2] !== 0x41 || encoded[3] !== 0x4d || encoded[4] !== 1 || encoded[26] !== 0 || encoded[27] !== 0) {
    throw new TypeError("Invalid multimodal media frame header");
  }
  const view = new DataView(encoded.buffer, encoded.byteOffset, encoded.byteLength);
  const trackLength = view.getUint16(24);
  if (trackLength < 1 || trackLength > MULTIMODAL_MAX_TRACK_ID_BYTES || encoded.byteLength <= MULTIMODAL_MEDIA_HEADER_BYTES + trackLength) {
    throw new TypeError("Invalid multimodal media frame track or payload");
  }
  const frame: MultimodalMediaFrame = {
    kind: mediaKindName(encoded[5]!), flags: view.getUint16(6),
    sequence: safeBigIntNumber(view.getBigUint64(8)), timestamp_us: safeBigIntNumber(view.getBigUint64(16)),
    track_id: new TextDecoder("utf-8", { fatal: true }).decode(encoded.subarray(MULTIMODAL_MEDIA_HEADER_BYTES, MULTIMODAL_MEDIA_HEADER_BYTES + trackLength)),
    payload: encoded.slice(MULTIMODAL_MEDIA_HEADER_BYTES + trackLength),
  };
  validateMediaFrame(frame);
  return frame;
}

function validateSessionStart(start: MultimodalSessionStart): void {
  if (!start.provider_id.trim() || !start.model.trim() || start.input_tracks.length < 1 || start.input_tracks.length > 8 || start.output_modalities.length < 1) {
    throw new TypeError("Multimodal session requires provider, model, input tracks, and output modalities");
  }
  const ids = new Set<string>();
  for (const track of start.input_tracks) {
    if (!track.id.trim() || ids.has(track.id) || (track.delivery !== "reliable" && track.delivery !== "latest") || (track.delivery === "latest" && track.kind !== "video")) {
      throw new TypeError("Multimodal session contains an invalid or duplicate input track");
    }
    ids.add(track.id);
  }
}

function validateSessionStarted(started: MultimodalSessionStarted, start: MultimodalSessionStart): void {
  if (started.type !== "session.started" || started.protocol_version !== MULTIMODAL_REALTIME_PROTOCOL || !started.session_id.trim() || (start.session_id !== undefined && started.session_id !== start.session_id)) {
    throw new TypeError("Invalid multimodal session.started event");
  }
  const limits = started.input_flow_limits;
  const credit = started.initial_input_credit;
  if (!validFlowLimits(limits) || !positiveSafeInteger(credit.bytes) || !positiveSafeInteger(credit.frames) || credit.bytes > limits.max_in_flight_bytes || credit.frames > limits.max_in_flight_frames) {
    throw new RangeError("Invalid multimodal input flow negotiation");
  }
}

function validFlowLimits(limits: MultimodalFlowLimits): boolean {
  return positiveSafeInteger(limits.max_frame_bytes) && limits.max_frame_bytes <= MULTIMODAL_MAX_FRAME_BYTES &&
    positiveSafeInteger(limits.max_in_flight_bytes) && limits.max_in_flight_bytes <= MULTIMODAL_MAX_IN_FLIGHT_BYTES &&
    positiveSafeInteger(limits.max_in_flight_frames) && limits.max_in_flight_frames <= MULTIMODAL_MAX_IN_FLIGHT_FRAMES;
}

function validateMediaFrame(frame: MultimodalMediaFrame): void {
  const flags = frame.flags ?? 0;
  const track = new TextEncoder().encode(frame.track_id);
  if ((frame.kind !== "audio" && frame.kind !== "image" && frame.kind !== "video") || !positiveSafeInteger(frame.sequence) || !nonNegativeSafeInteger(frame.timestamp_us) || track.byteLength < 1 || track.byteLength > MULTIMODAL_MAX_TRACK_ID_BYTES ||
    frame.payload.byteLength < 1 || frame.payload.byteLength > MULTIMODAL_MAX_FRAME_BYTES || !Number.isInteger(flags) || flags < 0 || (flags & ~allowedMediaFlags) !== 0) {
    throw new TypeError("Invalid multimodal media frame");
  }
  for (const byte of track) if (byte < 0x21 || byte > 0x7e) throw new TypeError("Multimodal track ID must be printable ASCII");
}

function mediaKindCode(kind: MultimodalMediaKind): number {
  if (kind === "audio") return 1;
  if (kind === "image") return 2;
  return 3;
}

function mediaKindName(code: number): MultimodalMediaKind {
  if (code === 1) return "audio";
  if (code === 2) return "image";
  if (code === 3) return "video";
  throw new TypeError("Invalid multimodal media kind");
}

function positiveSafeInteger(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

function nonNegativeSafeInteger(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0;
}

function safeBigIntNumber(value: bigint): number {
  if (value > BigInt(Number.MAX_SAFE_INTEGER)) throw new RangeError("Multimodal integer exceeds JavaScript safe range");
  return Number(value);
}

async function messageBytes(data: unknown): Promise<Uint8Array> {
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength).slice();
  if (typeof Blob !== "undefined" && data instanceof Blob) return new Uint8Array(await data.arrayBuffer());
  throw new TypeError("Unsupported multimodal WebSocket message type");
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}
