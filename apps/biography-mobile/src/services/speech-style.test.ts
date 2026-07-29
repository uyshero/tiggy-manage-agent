import { describe, expect, it } from "vitest";
import { biographySpeechPaceInstruction, withBiographySpeechPace } from "./speech-style";

describe("biography speech style", () => {
  it("adds the slower global pace after the emotional direction", () => {
    expect(withBiographySpeechPace("温和、关切")).toBe(`温和、关切；${biographySpeechPaceInstruction}`);
  });

  it("does not duplicate the global pace instruction", () => {
    expect(withBiographySpeechPace(biographySpeechPaceInstruction)).toBe(biographySpeechPaceInstruction);
  });
});
