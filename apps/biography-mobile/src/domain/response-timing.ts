export const DEFAULT_FOLLOWUP_DELAY_MS = 650;

const MIN_FOLLOWUP_DELAY_MS = 250;
const MAX_FOLLOWUP_DELAY_MS = 3_000;

export function resolveFollowupDelayMs(value: unknown): number {
  const parsed = typeof value === "number" ? value : Number(String(value ?? "").trim());
  if (!Number.isFinite(parsed) || parsed <= 0) return DEFAULT_FOLLOWUP_DELAY_MS;
  return Math.min(MAX_FOLLOWUP_DELAY_MS, Math.max(MIN_FOLLOWUP_DELAY_MS, Math.round(parsed)));
}
