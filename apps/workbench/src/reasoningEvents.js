function payload(event) {
  return event?.payload && typeof event.payload === "object" ? event.payload : {};
}

function data(event) {
  const value = payload(event).data;
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

export function isReasoningChunk(event) {
  return event?.type === "runtime.llm_chunk" && data(event).type === "reasoning";
}

function reasoningGroupKey(event) {
  const eventPayload = payload(event);
  const eventData = data(event);
  return `${String(eventPayload.turn_id || event.turn_id || "")}:${Number(eventData.tool_round || 0)}`;
}

export function mergeReasoningChunks(events) {
  const merged = [];
  for (const event of events || []) {
    const previous = merged[merged.length - 1];
    if (!isReasoningChunk(event) || !isReasoningChunk(previous) || reasoningGroupKey(event) !== reasoningGroupKey(previous)) {
      merged.push(event);
      continue;
    }
    const previousPayload = payload(previous);
    const previousData = data(previous);
    const currentData = data(event);
    merged[merged.length - 1] = {
      ...previous,
      payload: {
        ...previousPayload,
        data: {
          ...previousData,
          text: `${String(previousData.text || "")}${String(currentData.text || "")}`,
          end_index: currentData.index ?? previousData.end_index ?? previousData.index
        }
      }
    };
  }
  return merged;
}
