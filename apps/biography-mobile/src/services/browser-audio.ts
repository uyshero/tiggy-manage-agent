export class StreamingPCM16Resampler {
  private buffered = new Float32Array(0);
  private position = 0;
  private readonly ratio: number;

  constructor(sourceSampleRate: number, targetSampleRate = 16_000) {
    if (sourceSampleRate <= 0 || targetSampleRate <= 0 || targetSampleRate > sourceSampleRate) {
      throw new Error("invalid audio sample rate");
    }
    this.ratio = sourceSampleRate / targetSampleRate;
  }

  push(input: Float32Array): Int16Array<ArrayBuffer> {
    if (input.length === 0) return new Int16Array(0);
    const combined = new Float32Array(this.buffered.length + input.length);
    combined.set(this.buffered);
    combined.set(input, this.buffered.length);

    const values: number[] = [];
    while (this.position + this.ratio <= combined.length) {
      const start = Math.floor(this.position);
      const end = Math.max(start + 1, Math.floor(this.position + this.ratio));
      let sum = 0;
      for (let index = start; index < end; index += 1) sum += combined[index];
      const normalized = Math.max(-1, Math.min(1, sum / (end - start)));
      values.push(normalized < 0 ? Math.round(normalized * 0x8000) : Math.round(normalized * 0x7fff));
      this.position += this.ratio;
    }

    const consumed = Math.floor(this.position);
    this.buffered = combined.slice(consumed);
    this.position -= consumed;
    return Int16Array.from(values);
  }
}

export function encodePCM16LE(samples: Int16Array): ArrayBuffer {
  const output = new ArrayBuffer(samples.length * 2);
  const view = new DataView(output);
  samples.forEach((sample, index) => view.setInt16(index * 2, sample, true));
  return output;
}

export function decodePCM16LE(input: ArrayBuffer): Float32Array<ArrayBuffer> {
  const view = new DataView(input);
  const output = new Float32Array(Math.floor(input.byteLength / 2));
  for (let index = 0; index < output.length; index += 1) {
    const sample = view.getInt16(index * 2, true);
    output[index] = sample < 0 ? sample / 0x8000 : sample / 0x7fff;
  }
  return output;
}

export function encodeWAVPCM16(chunks: ArrayBuffer[], sampleRate = 16_000): ArrayBuffer {
  if (sampleRate <= 0) throw new Error("invalid WAV sample rate");
  const dataLength = chunks.reduce((total, chunk) => total + chunk.byteLength, 0);
  if (dataLength % 2 !== 0) throw new Error("PCM16 data must contain complete samples");

  const output = new ArrayBuffer(44 + dataLength);
  const view = new DataView(output);
  const writeASCII = (offset: number, value: string) => {
    for (let index = 0; index < value.length; index += 1) view.setUint8(offset + index, value.charCodeAt(index));
  };

  writeASCII(0, "RIFF");
  view.setUint32(4, 36 + dataLength, true);
  writeASCII(8, "WAVE");
  writeASCII(12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeASCII(36, "data");
  view.setUint32(40, dataLength, true);

  const bytes = new Uint8Array(output, 44);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(new Uint8Array(chunk), offset);
    offset += chunk.byteLength;
  }
  return output;
}

export function rootMeanSquare(samples: Float32Array): number {
  if (samples.length === 0) return 0;
  let energy = 0;
  for (const sample of samples) energy += sample * sample;
  return Math.sqrt(energy / samples.length);
}

export interface SpeechTurnActivity {
  speechStarted: boolean;
  shouldCommit: boolean;
}

export const speechTurnGraceMs = 450;
export const speechTurnSilenceMs = 800;

export class SpeechTurnDetector {
  private candidateSpeechMs = 0;
  private speechDetected = false;
  private lastSpeechAt = 0;
  private startedAt = 0;

  constructor(
    private readonly sampleRate: number,
    private readonly threshold = 0.01,
    private readonly graceMs = speechTurnGraceMs,
    private readonly minimumSpeechMs = 120,
    private readonly silenceMs = speechTurnSilenceMs,
  ) {
    if (sampleRate <= 0) throw new Error("invalid speech detector sample rate");
  }

  reset(now: number) {
    this.candidateSpeechMs = 0;
    this.speechDetected = false;
    this.lastSpeechAt = now;
    this.startedAt = now;
  }

  push(samples: Float32Array, now: number): SpeechTurnActivity {
    if (now - this.startedAt < this.graceMs) {
      this.candidateSpeechMs = 0;
      return { speechStarted: false, shouldCommit: false };
    }

    let speechStarted = false;
    if (rootMeanSquare(samples) >= this.threshold) {
      this.candidateSpeechMs += samples.length / this.sampleRate * 1_000;
      this.lastSpeechAt = now;
      if (!this.speechDetected && this.candidateSpeechMs >= this.minimumSpeechMs) {
        this.speechDetected = true;
        speechStarted = true;
      }
    } else if (!this.speechDetected) {
      this.candidateSpeechMs = 0;
    }

    return {
      speechStarted,
      shouldCommit: this.speechDetected && now - this.lastSpeechAt >= this.silenceMs,
    };
  }
}
