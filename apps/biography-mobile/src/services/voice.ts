import type { BiographyProject, InterviewOrder } from "@/services/interview";
import { decodePCM16LE, encodePCM16LE, encodeWAVPCM16, speechTurnGraceMs, SpeechTurnDetector, StreamingPCM16Resampler } from "@/services/browser-audio";
import { noSpeechPromptTimeoutMs } from "@/domain/no-speech-policy";
import { withBiographySpeechPace } from "@/services/speech-style";
import { currentBiographyAccessToken, withBiographyAccessTokenForWebSocket } from "@/services/auth";

export type VoiceEvent =
  | { type: "partial_transcript"; text: string }
  | { type: "final_transcript"; text: string }
  | { type: "input_committed" }
  | { type: "project_loaded"; project: BiographyProject }
  | { type: "chapter_confirmation"; text: string; expression: string; chapterID: string; project: BiographyProject }
  | { type: "assistant_reply_delta"; text: string }
  | { type: "assistant_reply"; text: string; expression: string; needsRetry: boolean; speechStarted: boolean; project: BiographyProject }
  | { type: "playback_started" }
  | { type: "playback_finished" }
  | { type: "speech_detected" }
  | {
      type: "recording_ready";
      audio?: Blob;
      filePath?: string;
      segmentFilePath?: string;
      durationMs: number;
      segmentDurationMs?: number;
      sizeBytes: number;
      transcript: string;
      cumulative?: boolean;
    }
  | { type: "network_lost" }
  | { type: "network_restored" }
  | { type: "error"; message: string; code?: string };

export interface VoiceAdapter {
  readonly mode: "native" | "gateway" | "mock";
  subscribe(listener: (event: VoiceEvent) => void): () => void;
  prepare(): Promise<void>;
  startListening(options?: { manualCommit?: boolean }): Promise<void>;
  stopListening(options?: { deferInterview?: boolean }): Promise<void>;
  cancelListening(): Promise<void>;
  requestFollowup(transcript: string): Promise<void>;
  setInterviewOrder(order: InterviewOrder): Promise<void>;
  setChapterFocus(chapterID: string): Promise<void>;
  playText(text: string, expression: string): Promise<void>;
  cancelPlayback(): Promise<void>;
  finishRecordingSession(): Promise<void>;
  deleteRecordingFile(filePath: string): Promise<void>;
  dispose(): Promise<void>;
}

interface GatewayServerMessage {
  type: string;
  text?: string;
  message?: string;
  code?: string;
  expression?: string;
  needs_retry?: boolean;
	speech_started?: boolean;
	chapter_id?: string;
  project?: BiographyProject;
  resume_token?: string;
}

const clientInstanceStorageKey = "tma.biography.client_instance_id";
const resumeTokenStorageKey = "tma.biography.resume_token";
const browserBargeInGraceMs = 700;
const browserPreRollMaxBytes = 48_000;
const gatewayHeartbeatIntervalMs = 15_000;
const gatewayHeartbeatTimeoutMs = 45_000;
const gatewayReconnectMaxDelayMs = 15_000;
const gatewayReconnectMaxAttempts = 3;

type BrowserCaptureMode = "none" | "monitor" | "listening";

function readStorage(key: string): string {
  try {
    return String(uni.getStorageSync(key) || "").trim();
  } catch {
    return "";
  }
}

function writeStorage(key: string, value: string) {
  try {
    if (value) uni.setStorageSync(key, value);
    else uni.removeStorageSync(key);
  } catch {
    // A failed local cache must not stop the current interview.
  }
}

function getOrCreateClientInstanceID(): string {
  const existing = readStorage(clientInstanceStorageKey);
  if (existing) return existing;
  const created = `device-${Date.now()}-${Math.random().toString(16).slice(2)}-${Math.random().toString(16).slice(2)}`;
  writeStorage(clientInstanceStorageKey, created);
  return created;
}

class GatewayVoiceAdapter implements VoiceAdapter {
  readonly mode = "gateway" as const;
  private listeners = new Set<(event: VoiceEvent) => void>();
  private socket: WebSocket | null = null;
  private connecting: Promise<void> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectAttempt = 0;
  private lastPongAt = 0;
  private connectedBefore = false;
  private connectionFailureMessage = "";
  private timers: ReturnType<typeof setTimeout>[] = [];
  private readonly sessionID = `voice-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  private readonly clientInstanceID = getOrCreateClientInstanceID();
  private resumeToken = readStorage(resumeTokenStorageKey);
  private turn = 0;
  private disposed = false;
  private browserAudioContext: AudioContext | null = null;
  private microphoneStream: MediaStream | null = null;
  private microphoneSource: MediaStreamAudioSourceNode | null = null;
  private microphoneProcessor: AudioWorkletNode | ScriptProcessorNode | null = null;
  private microphoneSilentGain: GainNode | null = null;
  private browserCapturing = false;
  private browserCaptureStopping = false;
  private browserCaptureGeneration = 0;
  private browserCaptureMode: BrowserCaptureMode = "none";
  private browserCaptureGraceMs = speechTurnGraceMs;
  private browserCaptureStartedAt = 0;
  private browserInputSampleRate = 0;
  private browserAudioSent = false;
  private browserManualCommit = false;
  private browserDeferInterviewOnCommit = false;
  private browserInterruptionDetected = false;
  private browserTurnSpeechDetected = false;
  private browserPreRoll: ArrayBuffer[] = [];
  private browserPreRollBytes = 0;
  private browserRecordingChunks: ArrayBuffer[] = [];
  private pendingBrowserRecording: { audio: Blob; durationMs: number; sizeBytes: number } | null = null;
  private browserSpeechDetector: SpeechTurnDetector | null = null;
  private browserResampler: StreamingPCM16Resampler | null = null;
  private playbackSources = new Set<AudioBufferSourceNode>();
  private playbackScheduledTime = 0;
  private playbackServerFinished = false;

  constructor(
    private readonly url: string,
    private readonly debugTextEnabled: boolean,
  ) {}

  subscribe(listener: (event: VoiceEvent) => void) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  prepare() {
    return this.ensureConnected();
  }

  async startListening(options?: { manualCommit?: boolean }) {
    await this.ensureConnected();
    this.clearTimers();
    if (!this.debugTextEnabled) {
      if (this.browserCapturing && this.browserCaptureMode === "monitor") {
        this.promoteBrowserCapture();
      } else {
        await this.startBrowserCapture("listening", Boolean(options?.manualCommit));
      }
      return;
    }
    const samples = [
      ["那年我十九岁", "那年我十九岁，第一次一个人去上海学木工。"],
      ["师傅姓周", "师傅姓周，对我很严格，但也教会了我很多。"],
      ["我想先确认", "我想先确认一下上一章里关于父亲工作的地方。"],
    ];
    const sample = samples[this.turn % samples.length];
    this.turn += 1;
    this.timers.push(setTimeout(() => this.send({ type: "asr.debug_text", text: sample[0] }), 600));
    this.timers.push(setTimeout(() => {
      this.send({ type: "asr.debug_text", text: sample[1] });
      this.emit({ type: "input_committed" });
      this.send({ type: "input.commit" });
    }, 1800));
  }

  async stopListening(options?: { deferInterview?: boolean }) {
    this.clearTimers();
    if (!this.debugTextEnabled) {
      this.browserDeferInterviewOnCommit = Boolean(options?.deferInterview);
      await this.stopBrowserCapture(this.browserCaptureMode === "listening");
    }
  }

  async cancelListening() {
    this.clearTimers();
    if (!this.debugTextEnabled) await this.stopBrowserCapture(false);
    this.send({ type: "input.cancel" });
  }

  async requestFollowup(transcript: string) {
    await this.ensureConnected();
    this.send({ type: "interview.followup", text: transcript });
  }

  async setInterviewOrder(order: InterviewOrder) {
    await this.ensureConnected();
    this.send({ type: "interview.order.set", interview_order: order });
  }

  async setChapterFocus(chapterID: string) {
    await this.ensureConnected();
    this.send({ type: "interview.chapter.focus", chapter_id: chapterID });
  }

  async playText(text: string, expression: string) {
    await this.ensureConnected();
    if (!this.debugTextEnabled) {
      await this.ensureBrowserAudioContext();
    }
    this.send({ type: "tts.start", text, expression: withBiographySpeechPace(expression) });
  }

  async cancelPlayback() {
    this.cancelBrowserPlayback();
    if (this.socket?.readyState === WebSocket.OPEN) this.send({ type: "tts.cancel" });
  }

  async finishRecordingSession() {}

  async deleteRecordingFile(_filePath: string) {}

  async dispose() {
    this.disposed = true;
    this.clearTimers();
    this.clearConnectionTimers();
    await this.stopBrowserCapture(false);
    this.cancelBrowserPlayback();
    if (this.socket?.readyState === WebSocket.OPEN) this.send({ type: "session.finish" });
    this.socket?.close(1000, "page closed");
    this.socket = null;
    await this.browserAudioContext?.close().catch(() => undefined);
    this.browserAudioContext = null;
    this.listeners.clear();
  }

  private ensureConnected(): Promise<void> {
    if (this.socket?.readyState === WebSocket.OPEN) return Promise.resolve();
    if (this.connecting) return this.connecting;
    this.connecting = new Promise((resolve, reject) => {
      const socket = new WebSocket(withBiographyAccessTokenForWebSocket(this.url));
      socket.binaryType = "arraybuffer";
      this.socket = socket;
      socket.addEventListener("open", () => {
        this.startHeartbeat();
        this.startGatewaySession();
        this.connecting = null;
        resolve();
      });
      socket.addEventListener("message", (event) => this.handleMessage(event.data));
      socket.addEventListener("close", () => {
        if (this.socket !== socket) return;
        const canRecover = this.connectedBefore;
        const failureMessage = this.connectionFailureMessage;
        this.connectionFailureMessage = "";
        this.socket = null;
        this.connecting = null;
        this.stopHeartbeat();
        void this.stopBrowserCapture(false);
        this.cancelBrowserPlayback();
        if (!this.disposed) {
          if (failureMessage) {
            this.emit({ type: "error", message: failureMessage });
            return;
          }
          if (!canRecover) {
            this.emit({ type: "error", message: "语音连接暂时不可用，请稍后点击继续采访重试" });
            return;
          }
          this.emit({ type: "network_lost" });
          this.scheduleReconnect();
        }
      });
      socket.addEventListener("error", () => {
        this.connecting = null;
        reject(new Error("语音连接暂时不可用"));
      });
    });
    return this.connecting;
  }

  private handleMessage(payload: unknown) {
    if (payload instanceof ArrayBuffer) {
      this.enqueueBrowserPlayback(payload);
      return;
    }
    if (typeof Blob !== "undefined" && payload instanceof Blob) {
      void payload.arrayBuffer().then((audio) => this.enqueueBrowserPlayback(audio));
      return;
    }
    if (typeof payload !== "string") return;
    let message: GatewayServerMessage;
    try {
      message = JSON.parse(payload) as GatewayServerMessage;
    } catch {
      this.emit({ type: "error", message: "语音服务返回了无法识别的数据" });
      return;
    }
    switch (message.type) {
      case "session.ready":
        this.lastPongAt = Date.now();
        this.reconnectAttempt = 0;
        if (this.connectedBefore) this.emit({ type: "network_restored" });
        this.connectedBefore = true;
        break;
      case "session.pong":
        this.lastPongAt = Date.now();
        break;
      case "asr.partial":
        this.emit({ type: "partial_transcript", text: message.text || "" });
        break;
      case "asr.final":
        void this.stopBrowserCapture(false).catch(() => undefined);
        this.emit({ type: "final_transcript", text: message.text || "" });
        if (this.pendingBrowserRecording && message.text?.trim()) {
          this.emit({
            type: "recording_ready",
            ...this.pendingBrowserRecording,
            transcript: message.text.trim(),
          });
        }
        this.pendingBrowserRecording = null;
        break;
      case "interview.reply":
        if (!message.text || !message.expression || !message.project) {
          this.emit({ type: "error", message: "采访服务返回的内容不完整" });
          break;
        }
        this.emit({
          type: "assistant_reply",
          text: message.text,
          expression: message.expression,
          needsRetry: message.needs_retry === true,
          speechStarted: message.speech_started === true,
          project: message.project,
        });
        if (message.resume_token) {
          this.resumeToken = message.resume_token;
          writeStorage(resumeTokenStorageKey, this.resumeToken);
        }
        break;
      case "interview.reply.delta":
        if (message.text) this.emit({ type: "assistant_reply_delta", text: message.text });
        break;
      case "interview.reply.canceled":
        break;
      case "interview.project":
        if (message.project) this.emit({ type: "project_loaded", project: message.project });
        break;
      case "interview.project.updated":
        if (message.project) this.emit({ type: "project_loaded", project: message.project });
        if (message.resume_token) {
          this.resumeToken = message.resume_token;
          writeStorage(resumeTokenStorageKey, this.resumeToken);
        }
        break;
      case "chapter.confirmation":
        if (!message.text || !message.expression || !message.chapter_id || !message.project) {
          this.emit({ type: "error", message: "章节确认内容不完整" });
          break;
        }
        this.emit({
          type: "chapter_confirmation",
          text: message.text,
          expression: message.expression,
          chapterID: message.chapter_id,
          project: message.project,
        });
        break;
      case "tts.started":
        this.beginBrowserPlayback();
        this.emit({ type: "playback_started" });
        break;
      case "tts.finished":
        this.finishBrowserPlaybackWhenDrained();
        break;
      case "tts.canceled":
        this.cancelBrowserPlayback();
        break;
      case "error":
        void this.stopBrowserCapture(false);
        this.pendingBrowserRecording = null;
        if (message.code === "interview_busy") {
          this.connectionFailureMessage = message.message || "这本书正在另一台设备上采访，请先在那边结束今天的采访";
          this.socket?.close(4008, "interview already active");
          break;
        }
        if (message.code === "resume_invalid") {
          this.resumeToken = "";
          writeStorage(resumeTokenStorageKey, "");
          this.startGatewaySession();
        }
        this.emit({ type: "error", message: message.message || "语音服务暂时不可用", code: message.code });
        break;
    }
  }

  private send(message: Record<string, unknown>) {
    if (this.socket?.readyState !== WebSocket.OPEN) return;
    this.socket.send(JSON.stringify({ ...message, session_id: this.sessionID }));
  }

  private startGatewaySession() {
    this.send({
      type: "session.start",
      session_id: this.sessionID,
      client_instance_id: this.clientInstanceID,
      resume_token: this.resumeToken || undefined,
    });
  }

  private scheduleReconnect() {
    if (this.disposed || this.reconnectTimer || this.socket?.readyState === WebSocket.OPEN) return;
    if (this.reconnectAttempt >= gatewayReconnectMaxAttempts) {
      this.emit({ type: "error", message: "网络暂时没有恢复，请检查网络后点击继续采访" });
      return;
    }
    const baseDelay = Math.min(1_000 * 2 ** this.reconnectAttempt, gatewayReconnectMaxDelayMs);
    const delay = Math.round(baseDelay * (0.85 + Math.random() * 0.3));
    this.reconnectAttempt += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      void this.ensureConnected().catch(() => this.scheduleReconnect());
    }, delay);
  }

  private startHeartbeat() {
    this.stopHeartbeat();
    this.lastPongAt = Date.now();
    this.heartbeatTimer = setInterval(() => {
      const socket = this.socket;
      if (!socket || socket.readyState !== WebSocket.OPEN) return;
      if (Date.now() - this.lastPongAt > gatewayHeartbeatTimeoutMs) {
        socket.close(4000, "heartbeat timeout");
        return;
      }
      this.send({ type: "session.ping" });
    }, gatewayHeartbeatIntervalMs);
  }

  private stopHeartbeat() {
    if (this.heartbeatTimer) clearInterval(this.heartbeatTimer);
    this.heartbeatTimer = null;
  }

  private clearConnectionTimers() {
    this.stopHeartbeat();
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private async ensureBrowserAudioContext(): Promise<AudioContext> {
    if (!this.browserAudioContext || this.browserAudioContext.state === "closed") {
      const webkitWindow = window as typeof window & { webkitAudioContext?: typeof AudioContext };
      const AudioContextConstructor = window.AudioContext || webkitWindow.webkitAudioContext;
      if (!AudioContextConstructor) throw new Error("当前浏览器不支持语音播放");
      this.browserAudioContext = new AudioContextConstructor();
    }
    if (this.browserAudioContext.state === "suspended") await this.browserAudioContext.resume();
    return this.browserAudioContext;
  }

  private async startBrowserCapture(mode: Exclude<BrowserCaptureMode, "none">, manualCommit = false) {
    if (!navigator.mediaDevices?.getUserMedia) throw new Error("当前浏览器不支持麦克风录音");
    await this.stopBrowserCapture(false);
    const captureGeneration = ++this.browserCaptureGeneration;
    const context = await this.ensureBrowserAudioContext();
    let stream: MediaStream;
    const microphoneRequest = navigator.mediaDevices.getUserMedia({
      audio: {
        channelCount: 1,
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      },
    });
    let permissionTimeout: ReturnType<typeof setTimeout> | undefined;
    try {
      stream = await Promise.race([
        microphoneRequest,
        new Promise<never>((_, reject) => {
          permissionTimeout = setTimeout(() => reject(new Error("microphone_permission_timeout")), 30_000);
        }),
      ]);
    } catch (error) {
      if (error instanceof Error && error.message === "microphone_permission_timeout") {
        void microphoneRequest.then((lateStream) => lateStream.getTracks().forEach((track) => track.stop())).catch(() => undefined);
        throw new Error("等待麦克风授权超时，请检查浏览器权限后重试");
      }
      if (error instanceof DOMException && (error.name === "NotAllowedError" || error.name === "SecurityError")) {
        throw new Error("请允许浏览器使用麦克风后再次点击开始讲述");
      }
      throw new Error("当前浏览器无法打开麦克风");
    } finally {
      if (permissionTimeout) clearTimeout(permissionTimeout);
    }
    if (captureGeneration !== this.browserCaptureGeneration || this.disposed || this.socket?.readyState !== WebSocket.OPEN) {
      stream.getTracks().forEach((track) => track.stop());
      throw new Error("语音连接已经断开，请重新开始");
    }

    const source = context.createMediaStreamSource(stream);
    const silentGain = context.createGain();
    silentGain.gain.value = 0;
    const consume = (samples: Float32Array) => this.consumeBrowserSamples(samples);
    let processor: AudioWorkletNode | ScriptProcessorNode | null = null;
    if (context.audioWorklet && typeof AudioWorkletNode !== "undefined") {
      const processorName = `tma-pcm16-${Date.now()}-${Math.random().toString(16).slice(2)}`;
      const sourceCode = `
        class TMABiographyPCMProcessor extends AudioWorkletProcessor {
          process(inputs) {
            const channel = inputs[0] && inputs[0][0];
            if (channel) {
              const copy = new Float32Array(channel);
              this.port.postMessage(copy, [copy.buffer]);
            }
            return true;
          }
        }
        registerProcessor(${JSON.stringify(processorName)}, TMABiographyPCMProcessor);
      `;
      const moduleURL = URL.createObjectURL(new Blob([sourceCode], { type: "text/javascript" }));
      try {
        await context.audioWorklet.addModule(moduleURL);
        const worklet = new AudioWorkletNode(context, processorName, { numberOfInputs: 1, numberOfOutputs: 1, outputChannelCount: [1] });
        worklet.port.onmessage = (event: MessageEvent<Float32Array>) => consume(event.data);
        processor = worklet;
      } catch {
        // Some embedded browsers block blob-backed worklets; ScriptProcessor remains the compatibility path.
      } finally {
        URL.revokeObjectURL(moduleURL);
      }
    }
    if (!processor) {
      const scriptProcessor = context.createScriptProcessor(4_096, 1, 1);
      scriptProcessor.onaudioprocess = (event) => consume(new Float32Array(event.inputBuffer.getChannelData(0)));
      processor = scriptProcessor;
    }
    if (captureGeneration !== this.browserCaptureGeneration) {
      source.disconnect();
      processor.disconnect();
      stream.getTracks().forEach((track) => track.stop());
      throw new Error("录音已经停止");
    }

    this.microphoneStream = stream;
    this.microphoneSource = source;
    this.microphoneProcessor = processor;
    this.microphoneSilentGain = silentGain;
    this.browserResampler = new StreamingPCM16Resampler(context.sampleRate);
    this.browserSpeechDetector = mode === "monitor"
      ? new SpeechTurnDetector(context.sampleRate, 0.015, browserBargeInGraceMs, 180, 1_800)
      : new SpeechTurnDetector(context.sampleRate);
    this.browserCapturing = true;
    this.browserCaptureStopping = false;
    this.browserCaptureMode = mode;
    this.browserManualCommit = manualCommit && mode === "listening";
    this.browserDeferInterviewOnCommit = false;
    this.browserCaptureGraceMs = mode === "monitor" ? browserBargeInGraceMs : speechTurnGraceMs;
    this.browserCaptureStartedAt = performance.now();
    this.browserInputSampleRate = context.sampleRate;
    this.browserAudioSent = false;
    this.browserInterruptionDetected = false;
    this.browserTurnSpeechDetected = false;
    this.clearBrowserPreRoll();
    this.browserRecordingChunks = [];
    this.browserSpeechDetector.reset(this.browserCaptureStartedAt);
    source.connect(processor);
    processor.connect(silentGain);
    silentGain.connect(context.destination);
  }

  private consumeBrowserSamples(samples: Float32Array) {
    if (!this.browserCapturing || this.browserCaptureStopping || this.socket?.readyState !== WebSocket.OPEN) return;
    const now = performance.now();
    const activity = this.browserSpeechDetector?.push(samples, now);
    if (now - this.browserCaptureStartedAt >= this.browserCaptureGraceMs) {
      const pcm = this.browserResampler?.push(samples);
      if (pcm?.length) {
        const encoded = encodePCM16LE(pcm);
        if (this.browserCaptureMode === "listening") {
          this.socket.send(encoded);
          this.browserAudioSent = true;
          this.browserRecordingChunks.push(encoded.slice(0));
        } else if (this.browserCaptureMode === "monitor") {
          this.appendBrowserPreRoll(encoded);
        }
      }
    }
    if (activity?.speechStarted) {
      this.browserTurnSpeechDetected = true;
      if (this.browserCaptureMode === "monitor") this.browserInterruptionDetected = true;
      this.emit({ type: "speech_detected" });
    }
    const captureElapsedMs = now - this.browserCaptureStartedAt;
    const noSpeechTimedOut = !this.browserTurnSpeechDetected && captureElapsedMs >= noSpeechPromptTimeoutMs;
    if (this.browserCaptureMode === "listening" && !this.browserManualCommit && (activity?.shouldCommit || noSpeechTimedOut || captureElapsedMs >= 90_000)) {
      void this.stopBrowserCapture(true);
    }
    if (this.browserCaptureMode === "listening" && this.browserManualCommit && captureElapsedMs >= 90_000) {
      void this.stopBrowserCapture(true);
    }
  }

  private promoteBrowserCapture() {
    if (!this.browserCapturing || this.browserCaptureMode !== "monitor") return;
    this.browserCaptureMode = "listening";
    this.browserManualCommit = false;
    this.browserCaptureGraceMs = speechTurnGraceMs;
    this.browserCaptureStartedAt = performance.now();
    if (this.browserInterruptionDetected) {
      this.browserTurnSpeechDetected = true;
      this.browserPreRoll.forEach((audio) => this.socket?.send(audio));
      this.browserAudioSent = this.browserPreRollBytes > 0;
      this.browserRecordingChunks = this.browserPreRoll.map((audio) => audio.slice(0));
    } else if (this.browserInputSampleRate > 0) {
      this.browserTurnSpeechDetected = false;
      this.browserResampler = new StreamingPCM16Resampler(this.browserInputSampleRate);
      this.browserSpeechDetector = new SpeechTurnDetector(this.browserInputSampleRate);
      this.browserSpeechDetector.reset(this.browserCaptureStartedAt);
      this.browserRecordingChunks = [];
    }
    this.clearBrowserPreRoll();
  }

  private appendBrowserPreRoll(audio: ArrayBuffer) {
    this.browserPreRoll.push(audio);
    this.browserPreRollBytes += audio.byteLength;
    while (this.browserPreRollBytes > browserPreRollMaxBytes && this.browserPreRoll.length > 1) {
      const removed = this.browserPreRoll.shift();
      this.browserPreRollBytes -= removed?.byteLength || 0;
    }
  }

  private clearBrowserPreRoll() {
    this.browserPreRoll = [];
    this.browserPreRollBytes = 0;
  }

  private async stopBrowserCapture(commit: boolean) {
    this.browserCaptureGeneration += 1;
    if (this.browserCaptureStopping) return;
    if (!this.browserCapturing && !this.microphoneStream) return;
    this.browserCaptureStopping = true;
    this.browserCapturing = false;
    const wasListening = this.browserCaptureMode === "listening";
    const socketOpen = this.socket?.readyState === WebSocket.OPEN;
    const shouldCommit = commit && wasListening && this.browserAudioSent && socketOpen;
    const noSpeech = commit && wasListening && socketOpen && !this.browserAudioSent;
    if (shouldCommit && this.browserRecordingChunks.length > 0) {
      const pcmBytes = this.browserRecordingChunks.reduce((total, chunk) => total + chunk.byteLength, 0);
      const wav = encodeWAVPCM16(this.browserRecordingChunks);
      this.pendingBrowserRecording = {
        audio: new Blob([wav], { type: "audio/wav" }),
        durationMs: Math.round(pcmBytes / 32_000 * 1_000),
        sizeBytes: wav.byteLength,
      };
    }
    if (typeof AudioWorkletNode !== "undefined" && this.microphoneProcessor instanceof AudioWorkletNode) {
      this.microphoneProcessor.port.onmessage = null;
      this.microphoneProcessor.port.close();
    } else if (this.microphoneProcessor && "onaudioprocess" in this.microphoneProcessor) {
      this.microphoneProcessor.onaudioprocess = null;
    }
    this.microphoneSource?.disconnect();
    this.microphoneProcessor?.disconnect();
    this.microphoneSilentGain?.disconnect();
    this.microphoneStream?.getTracks().forEach((track) => track.stop());
    this.microphoneStream = null;
    this.microphoneSource = null;
    this.microphoneProcessor = null;
    this.microphoneSilentGain = null;
    this.browserResampler = null;
    this.browserSpeechDetector = null;
    this.browserCaptureMode = "none";
    const deferInterview = this.browserDeferInterviewOnCommit;
    this.browserManualCommit = false;
    this.browserDeferInterviewOnCommit = false;
    this.browserInputSampleRate = 0;
    this.browserAudioSent = false;
    this.browserInterruptionDetected = false;
    this.browserTurnSpeechDetected = false;
    this.clearBrowserPreRoll();
    this.browserRecordingChunks = [];
    this.browserCaptureStopping = false;
    if (shouldCommit) {
      this.emit({ type: "input_committed" });
      this.send({ type: "input.commit", defer_interview: deferInterview });
    } else if (noSpeech) {
      this.emit({ type: "error", code: "no_speech", message: "我没有听清，请按住话筒再说一次" });
    }
  }

  private beginBrowserPlayback() {
    this.cancelBrowserPlayback();
    this.playbackServerFinished = false;
    this.playbackScheduledTime = (this.browserAudioContext?.currentTime || 0) + 0.04;
    if (this.browserCaptureMode === "monitor" && this.browserInputSampleRate > 0) {
      this.browserCaptureStartedAt = performance.now();
      this.browserResampler = new StreamingPCM16Resampler(this.browserInputSampleRate);
      this.browserSpeechDetector = new SpeechTurnDetector(this.browserInputSampleRate, 0.015, browserBargeInGraceMs, 180, 1_800);
      this.browserSpeechDetector.reset(this.browserCaptureStartedAt);
      this.browserInterruptionDetected = false;
      this.browserTurnSpeechDetected = false;
      this.clearBrowserPreRoll();
    }
  }

  private enqueueBrowserPlayback(audio: ArrayBuffer) {
    const context = this.browserAudioContext;
    if (!context || audio.byteLength < 2) return;
    if (context.state === "suspended") void context.resume();
    const samples = decodePCM16LE(audio);
    const buffer = context.createBuffer(1, samples.length, 24_000);
    buffer.copyToChannel(samples, 0);
    const source = context.createBufferSource();
    source.buffer = buffer;
    source.connect(context.destination);
    const startAt = Math.max(context.currentTime + 0.02, this.playbackScheduledTime);
    this.playbackScheduledTime = startAt + buffer.duration;
    this.playbackSources.add(source);
    source.onended = () => {
      this.playbackSources.delete(source);
      source.disconnect();
      this.emitBrowserPlaybackFinishedIfReady();
    };
    source.start(startAt);
  }

  private finishBrowserPlaybackWhenDrained() {
    this.playbackServerFinished = true;
    this.emitBrowserPlaybackFinishedIfReady();
  }

  private emitBrowserPlaybackFinishedIfReady() {
    if (!this.playbackServerFinished || this.playbackSources.size > 0) return;
    this.playbackServerFinished = false;
    this.emit({ type: "playback_finished" });
  }

  private cancelBrowserPlayback() {
    this.playbackServerFinished = false;
    this.playbackSources.forEach((source) => {
      source.onended = null;
      try { source.stop(); } catch { /* The source may already have ended. */ }
      source.disconnect();
    });
    this.playbackSources.clear();
  }

  private emit(event: VoiceEvent) {
    this.listeners.forEach((listener) => listener(event));
  }

  private clearTimers() {
    this.timers.forEach((timer) => clearTimeout(timer));
    this.timers = [];
  }
}

interface NativeVoicePlugin {
  configure(options: { gatewayURL: string; shortLivedToken?: string }, callback: (result: NativeCallResult) => void): void;
  addEventListener(listener: (event: VoiceEvent) => void): void;
  removeEventListener(): void;
  startListening(options: Record<string, unknown>, callback: (result: NativeCallResult) => void): void;
  stopListening(options: Record<string, unknown>, callback: (result: NativeCallResult) => void): void;
  cancelListening(callback: (result: NativeCallResult) => void): void;
  requestFollowup(options: { text: string }, callback: (result: NativeCallResult) => void): void;
  setInterviewOrder(options: { interviewOrder: InterviewOrder }, callback: (result: NativeCallResult) => void): void;
  setChapterFocus(options: { chapterID: string }, callback: (result: NativeCallResult) => void): void;
  playText(options: { text: string; expression: string }, callback: (result: NativeCallResult) => void): void;
  cancelPlayback(callback: (result: NativeCallResult) => void): void;
  finishRecordingSession(callback: (result: NativeCallResult) => void): void;
  deleteRecording(options: { filePath: string }, callback: (result: NativeCallResult) => void): void;
  dispose(callback: (result: NativeCallResult) => void): void;
}

interface NativeCallResult {
  ok: boolean;
  message?: string;
}

export interface NativeVoiceRuntimeConfig {
  gatewayURL: string;
  shortLivedToken?: string;
}

class NativeVoiceAdapter implements VoiceAdapter {
  readonly mode = "native" as const;
  private listeners = new Set<(event: VoiceEvent) => void>();
  private readonly forward = (event: VoiceEvent) => this.listeners.forEach((listener) => listener(event));

  private configurePromise: Promise<void> | null = null;

  constructor(
    private readonly plugin: NativeVoicePlugin,
    private readonly config: NativeVoiceRuntimeConfig,
  ) {
    plugin.addEventListener(this.forward);
  }

  subscribe(listener: (event: VoiceEvent) => void) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  prepare() {
    return this.ensureConfigured();
  }

  async startListening(options?: { manualCommit?: boolean }) {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.startListening({ sampleRate: 16000, channelCount: 1, manualCommit: Boolean(options?.manualCommit) }, done));
  }

  async stopListening(options?: { deferInterview?: boolean }) {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.stopListening({ deferInterview: Boolean(options?.deferInterview) }, done));
  }

  async cancelListening() {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.cancelListening(done));
  }

  async requestFollowup(transcript: string) {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.requestFollowup({ text: transcript }, done));
  }

  async setInterviewOrder(order: InterviewOrder) {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.setInterviewOrder({ interviewOrder: order }, done));
  }

  async setChapterFocus(chapterID: string) {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.setChapterFocus({ chapterID }, done));
  }

  async playText(text: string, expression: string) {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.playText({ text, expression: withBiographySpeechPace(expression) }, done));
  }

  async cancelPlayback() {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.cancelPlayback(done));
  }

  async finishRecordingSession() {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.finishRecordingSession(done));
  }

  async deleteRecordingFile(filePath: string) {
    await this.ensureConfigured();
    await this.call((done) => this.plugin.deleteRecording({ filePath }, done));
  }

  async dispose() {
    this.plugin.removeEventListener();
    await this.call((done) => this.plugin.dispose(done));
    this.listeners.clear();
  }

  private ensureConfigured() {
    if (!this.configurePromise) {
      const attempt = this.call((done) => this.plugin.configure(this.config, done));
      this.configurePromise = attempt.catch((error) => {
        this.configurePromise = null;
        throw error;
      });
    }
    return this.configurePromise;
  }

  private call(invoke: (done: (result: NativeCallResult) => void) => void): Promise<void> {
    return new Promise((resolve, reject) => {
      invoke((result) => result.ok ? resolve() : reject(new Error(result.message || "语音服务暂时不可用")));
    });
  }
}

class MockVoiceAdapter implements VoiceAdapter {
  readonly mode = "mock" as const;
  private listeners = new Set<(event: VoiceEvent) => void>();
  private timers: ReturnType<typeof setTimeout>[] = [];
  private turn = 0;

  subscribe(listener: (event: VoiceEvent) => void) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  async prepare() {}

  async startListening(_options?: { manualCommit?: boolean }) {
    this.clearTimers();
    const samples = [
      ["那年我十九岁", "那年我十九岁，第一次一个人去上海学木工。"],
      ["师傅姓周", "师傅姓周，对我很严格，但也教会了我很多。"],
      ["我想先确认", "我想先确认一下上一章里关于父亲工作的地方。"],
    ];
    const sample = samples[this.turn % samples.length];
    this.turn += 1;
    this.timers.push(setTimeout(() => this.emit({ type: "partial_transcript", text: sample[0] }), 700));
    this.timers.push(setTimeout(() => this.emit({ type: "final_transcript", text: sample[1] }), 2100));
  }

  async stopListening(_options?: { deferInterview?: boolean }) {
    this.clearTimers();
  }

  async cancelListening() {
    this.clearTimers();
  }

  async requestFollowup(transcript: string) {
    if (!transcript.trim()) return;
    this.emit({ type: "assistant_reply_delta", text: "我听到了，正在想接下来问什么。" });
  }

  async setInterviewOrder(_order: InterviewOrder) {}

  async setChapterFocus(_chapterID: string) {}

  async playText(_text: string, _expression: string) {
    this.emit({ type: "playback_started" });
    this.timers.push(setTimeout(() => this.emit({ type: "playback_finished" }), 3600));
  }

  async cancelPlayback() {
    this.clearTimers();
  }

  async finishRecordingSession() {}

  async deleteRecordingFile(_filePath: string) {}

  async dispose() {
    this.clearTimers();
    this.listeners.clear();
  }

  private emit(event: VoiceEvent) {
    this.listeners.forEach((listener) => listener(event));
  }

  private clearTimers() {
    this.timers.forEach((timer) => clearTimeout(timer));
    this.timers = [];
  }
}

export function createVoiceAdapter(nativeConfig?: Partial<NativeVoiceRuntimeConfig>): VoiceAdapter {
  let plugin: NativeVoicePlugin | undefined;
  // #ifdef APP-PLUS
  plugin = uni.requireNativePlugin("TMA-BiographyVoice") as NativeVoicePlugin | undefined;
  // #endif
  const gatewayURL = String(import.meta.env.VITE_BIOGRAPHY_VOICE_GATEWAY_URL || "").trim();
  if (plugin) {
    return new NativeVoiceAdapter(plugin, {
      gatewayURL: String(nativeConfig?.gatewayURL || gatewayURL).trim(),
      shortLivedToken: nativeConfig?.shortLivedToken?.trim() || currentBiographyAccessToken() || undefined,
    });
  }
  if (gatewayURL) {
    return new GatewayVoiceAdapter(gatewayURL, import.meta.env.VITE_BIOGRAPHY_VOICE_DEBUG_TEXT === "true");
  }
  return new MockVoiceAdapter();
}
