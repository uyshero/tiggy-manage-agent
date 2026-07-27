function eventPayload(event) {
  return event?.payload && typeof event.payload === "object" && !Array.isArray(event.payload)
    ? event.payload
    : {};
}

function eventData(event) {
  const payload = eventPayload(event);
  return payload.data && typeof payload.data === "object" && !Array.isArray(payload.data)
    ? payload.data
    : {};
}

function agentMessageText(event) {
  const payload = eventPayload(event);
  if (Array.isArray(payload.content)) {
    return payload.content.map((item) => item?.text || item?.content || "").join("\n").trim();
  }
  return String(payload.content || payload.message || payload.summary || payload.text || "").trim();
}

export function durableEventReplacesLiveReply(event, liveReply) {
  if (!event || !liveReply) return false;
  const payload = eventPayload(event);
  const eventTurnID = String(event.turn_id || payload.turn_id || "");
  const liveTurnID = String(liveReply.turnID || "");
  if (eventTurnID && liveTurnID && eventTurnID !== liveTurnID) return false;

  if (event.type === "runtime.failed") return true;
  if (!eventTurnID || !liveTurnID) return false;
  if (event.type === "agent.message") return Boolean(agentMessageText(event));
  if (event.type !== "runtime.progress_message") return false;

  const data = eventData(event);
  return Number(data.tool_round || 0) === Number(liveReply.toolRound || 0)
    && Boolean(String(data.text || "").trim());
}
