import { describe, expect, it } from "vitest";
import { encodeWAVPCM16 } from "./browser-audio";
import { createRecordingTitle, formatRecordingDuration, recordingAudioBlob, recordingFilePaths, type StoredRecording } from "./recordings";

describe("recording metadata", () => {
  it("numbers recordings within a chapter", () => {
    expect(createRecordingTitle("童年往事", 0)).toBe("童年往事 · 第 1 次采访");
    expect(createRecordingTitle("", 2)).toBe("未分类 · 第 3 次采访");
  });

  it("formats recording duration for older-user friendly scanning", () => {
    expect(formatRecordingDuration(0)).toBe("00:00");
    expect(formatRecordingDuration(65_100)).toBe("01:05");
  });

  it("combines browser turns into one playable WAV", async () => {
    const firstPCM = new Uint8Array([1, 0, 2, 0]).buffer;
    const secondPCM = new Uint8Array([3, 0, 4, 0]).buffer;
    const recording = {
      audio: new Blob([encodeWAVPCM16([firstPCM])], { type: "audio/wav" }),
      segments: [
        { transcript: "第一句", durationMs: 1, audio: new Blob([encodeWAVPCM16([firstPCM])]), sizeBytes: 48 },
        { transcript: "第二句", durationMs: 1, audio: new Blob([encodeWAVPCM16([secondPCM])]), sizeBytes: 48 },
      ],
    } as StoredRecording;

    const combined = await recordingAudioBlob(recording);
    const bytes = new Uint8Array(await combined!.arrayBuffer());
    expect([...bytes.slice(44)]).toEqual([1, 0, 2, 0, 3, 0, 4, 0]);
    expect(new DataView(bytes.buffer).getUint32(40, true)).toBe(8);
  });

  it("deduplicates a cumulative native interview file", () => {
    const recording = {
      filePath: "file:///interview.wav",
      segments: [
        { transcript: "第一句", durationMs: 1, filePath: "file:///interview.wav", sizeBytes: 48 },
        { transcript: "第二句", durationMs: 2, filePath: "file:///interview.wav", sizeBytes: 52 },
      ],
    } as StoredRecording;
    expect(recordingFilePaths(recording)).toEqual(["file:///interview.wav"]);
  });
});
