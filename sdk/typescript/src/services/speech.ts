import { ServiceBase } from "./base.js";

export interface SpeechSessionStart {
  provider_id: string;
  model: string;
  session_id?: string;
  voice?: string;
  style?: string;
  audio_format?: string;
  sample_rate_hz?: number;
}

export interface SpeechEvent {
  type: "session.started" | "transcript.partial" | "transcript.final" | "audio.done" | "session.canceled" | "error";
  session_id?: string;
  mode?: "transcription" | "synthesis";
  text?: string;
  audio_format?: string;
  sample_rate_hz?: number;
  code?: string;
  message?: string;
  retryable?: boolean;
  retry_after_seconds?: number;
  limit_scope?: "global" | "workspace" | "identity" | "route";
}

export class SpeechService extends ServiceBase {
  realtimeURL(): string {
    const url = new URL(this.transport.url("/v2/speech/realtime"));
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    return url.toString();
  }

  connectRealtime(protocols?: string | string[]): WebSocket {
    return protocols === undefined ? new WebSocket(this.realtimeURL()) : new WebSocket(this.realtimeURL(), protocols);
  }
}
