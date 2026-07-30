import { beforeEach, describe, expect, it, vi } from "vitest";
import { uploadRecordingBackup } from "./recording-backup";
import type { StoredRecording } from "./recordings";

const storage = new Map<string, unknown>();

function installUniStorage() {
  vi.stubGlobal("uni", {
    getStorageSync: vi.fn((key: string) => storage.get(key) || ""),
    setStorageSync: vi.fn((key: string, value: unknown) => storage.set(key, value)),
    removeStorageSync: vi.fn((key: string) => storage.delete(key)),
  });
}

function recording(): StoredRecording {
  return {
    id: "recording-12345678",
    ownerID: "usr_a",
    projectID: "book-a",
    chapterID: "childhood",
    chapterTitle: "童年往事",
    transcript: "那年夏天，我第一次离开家。",
    durationMs: 1_200,
    title: "童年往事 · 第 1 次采访",
    createdAt: 1_735_000_000_000,
    sizeBytes: 4,
    audio: new Blob([new Uint8Array([1, 2, 3, 4])], { type: "audio/wav" }),
  };
}

describe("recording backup", () => {
  beforeEach(() => {
    storage.clear();
    storage.set("tma.biography.auth.oidc_token", "header.payload.signature");
    installUniStorage();
    vi.stubEnv("VITE_BIOGRAPHY_AUTH_BASE_URL", "https://bio.example");
  });

  it("uploads the combined recording with the current OIDC token", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      expect(init?.method).toBe("PUT");
      expect(new Headers(init?.headers).get("authorization")).toBe("Bearer header.payload.signature");
      if (url === "https://bio.example/v1/recordings/recording-12345678") {
        expect(new Headers(init?.headers).get("content-type")).toBe("application/json");
        expect(JSON.parse(String(init?.body))).toMatchObject({ projectID: "book-a", transcript: "那年夏天，我第一次离开家。" });
        return new Response("", { status: 201 });
      }
      expect(url).toBe("https://bio.example/v1/recordings/recording-12345678/segments/recording-12345678-legacy/audio");
      const body = init?.body as FormData;
      expect(JSON.parse(String(body.get("metadata")))).toMatchObject({ transcript: "那年夏天，我第一次离开家。", transcriptionStatus: "ready" });
      expect(body.get("audio")).toBeInstanceOf(Blob);
      return new Response("", { status: 201 });
    });
    vi.stubGlobal("fetch", fetchMock);

    await uploadRecordingBackup(recording());

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("keeps the local recording when the login has expired", async () => {
    storage.delete("tma.biography.auth.oidc_token");
    await expect(uploadRecordingBackup(recording())).rejects.toThrow("登录已过期");
  });
});
