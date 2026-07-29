import { describe, expect, it } from "vitest";
import { noSpeechPauseLimit, noSpeechPromptTimeoutMs, shouldPauseAfterNoSpeech } from "./no-speech-policy";

describe("no speech pause policy", () => {
  it("prompts after a short silence and pauses on the second consecutive timeout", () => {
    expect(noSpeechPromptTimeoutMs).toBe(15_000);
    expect(shouldPauseAfterNoSpeech(1)).toBe(false);
    expect(shouldPauseAfterNoSpeech(noSpeechPauseLimit)).toBe(true);
  });
});
