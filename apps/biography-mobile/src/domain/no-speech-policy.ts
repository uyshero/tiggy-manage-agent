export const noSpeechPromptTimeoutMs = 15_000;
export const noSpeechPauseLimit = 2;

export function shouldPauseAfterNoSpeech(consecutiveCount: number): boolean {
  return consecutiveCount >= noSpeechPauseLimit;
}
