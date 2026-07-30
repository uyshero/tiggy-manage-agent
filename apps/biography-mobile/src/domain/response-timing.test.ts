import { describe, expect, it } from "vitest";
import { DEFAULT_FOLLOWUP_DELAY_MS, resolveFollowupDelayMs } from "./response-timing";

describe("response timing", () => {
  it("uses a short default response window", () => {
    expect(resolveFollowupDelayMs(undefined)).toBe(DEFAULT_FOLLOWUP_DELAY_MS);
    expect(resolveFollowupDelayMs("")).toBe(DEFAULT_FOLLOWUP_DELAY_MS);
    expect(resolveFollowupDelayMs("not-a-number")).toBe(DEFAULT_FOLLOWUP_DELAY_MS);
  });

  it("accepts an explicit response window within safe bounds", () => {
    expect(resolveFollowupDelayMs("800")).toBe(800);
    expect(resolveFollowupDelayMs(100.4)).toBe(250);
    expect(resolveFollowupDelayMs(8_000)).toBe(3_000);
  });
});
