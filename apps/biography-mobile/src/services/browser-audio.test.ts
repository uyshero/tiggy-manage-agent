import { describe, expect, it } from "vitest";
import { decodePCM16LE, encodePCM16LE, encodeWAVPCM16, rootMeanSquare, speechTurnSilenceMs, SpeechTurnDetector, StreamingPCM16Resampler } from "./browser-audio";

describe("browser audio conversion", () => {
  it("resamples continuous 48kHz chunks to 16kHz without losing duration", () => {
    const resampler = new StreamingPCM16Resampler(48_000);
    const first = resampler.push(new Float32Array(2_401).fill(0.5));
    const second = resampler.push(new Float32Array(2_399).fill(0.5));

    expect(first.length + second.length).toBe(1_600);
    expect(first[0]).toBeCloseTo(16_384, -1);
    expect(second[second.length - 1]).toBeCloseTo(16_384, -1);
  });

  it("keeps fractional source frames across 44.1kHz chunks", () => {
    const resampler = new StreamingPCM16Resampler(44_100);
    let outputSamples = 0;
    for (let index = 0; index < 10; index += 1) {
      outputSamples += resampler.push(new Float32Array(4_410).fill(-0.25)).length;
    }

    expect(outputSamples).toBe(16_000);
  });

  it("encodes and decodes signed little-endian PCM16", () => {
    const encoded = encodePCM16LE(Int16Array.from([-32_768, -1, 0, 1, 32_767]));
    const view = new DataView(encoded);
    expect(view.getUint8(0)).toBe(0);
    expect(view.getUint8(1)).toBe(128);

    const decoded = decodePCM16LE(encoded);
    expect(decoded[0]).toBe(-1);
    expect(decoded[2]).toBe(0);
    expect(decoded[4]).toBe(1);
  });

  it("calculates signal energy", () => {
    expect(rootMeanSquare(Float32Array.from([1, -1, 1, -1]))).toBe(1);
    expect(rootMeanSquare(new Float32Array(0))).toBe(0);
  });

  it("wraps captured PCM16 chunks in a playable mono WAV", () => {
    const first = encodePCM16LE(Int16Array.from([1, -1]));
    const second = encodePCM16LE(Int16Array.from([32_767, -32_768]));
    const wav = encodeWAVPCM16([first, second]);
    const view = new DataView(wav);

    expect(new TextDecoder().decode(wav.slice(0, 4))).toBe("RIFF");
    expect(new TextDecoder().decode(wav.slice(8, 12))).toBe("WAVE");
    expect(view.getUint32(24, true)).toBe(16_000);
    expect(view.getUint32(40, true)).toBe(8);
    expect(view.getInt16(44, true)).toBe(1);
    expect(view.getInt16(50, true)).toBe(-32_768);
  });

  it("ignores opening-audio tails and requires sustained speech", () => {
    const detector = new SpeechTurnDetector(48_000);
    detector.reset(0);

    expect(detector.push(new Float32Array(4_800).fill(0.03), 300).speechStarted).toBe(false);
    expect(detector.push(new Float32Array(4_800).fill(0.012), 600).speechStarted).toBe(false);
    expect(detector.push(new Float32Array(2_400).fill(0.012), 650).speechStarted).toBe(true);
  });

  it("allows a brief pause and then commits without a long dead period", () => {
    const detector = new SpeechTurnDetector(48_000);
    detector.reset(0);
    detector.push(new Float32Array(7_200).fill(0.02), 600);

    expect(speechTurnSilenceMs).toBe(800);
    expect(detector.push(new Float32Array(4_800), 600 + speechTurnSilenceMs - 100).shouldCommit).toBe(false);
    expect(detector.push(new Float32Array(4_800), 600 + speechTurnSilenceMs + 100).shouldCommit).toBe(true);
  });
});
