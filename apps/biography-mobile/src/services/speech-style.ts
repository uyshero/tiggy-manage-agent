export const biographySpeechPaceInstruction = "整体语速约为正常对话的80%，咬字清楚，句间停顿稍长";

export function withBiographySpeechPace(expression: string): string {
  const trimmed = expression.trim();
  if (!trimmed) return biographySpeechPaceInstruction;
  if (trimmed.includes(biographySpeechPaceInstruction)) return trimmed;
  return `${trimmed}；${biographySpeechPaceInstruction}`;
}
