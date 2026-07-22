export function toolCallID(event) {
  const data = event?.payload?.data;
  if (!data || typeof data !== "object" || Array.isArray(data)) return "";
  return String(data.id || data.call_id || "").trim();
}

export function normalizeToolTimelineEvents(events) {
  const source = Array.isArray(events) ? events : [];
  const plannedCalls = new Map();
  const nativeCalls = new Set(source.filter((event) => event?.type === "runtime.tool_call").map(toolCallID).filter(Boolean));
  const nativeResults = new Set(source.filter((event) => event?.type === "runtime.tool_result").map(toolCallID).filter(Boolean));

  for (const event of source) {
    if (event?.type !== "tool.batch_planned") continue;
    for (const item of arrayValue(event?.payload?.data?.calls)) {
      const call = objectValue(item?.call);
      const callID = String(call.id || "").trim();
      if (callID) plannedCalls.set(callID, { call, item });
    }
  }

  const normalized = [];
  for (const event of source) {
    normalized.push(event);
    if (event?.type === "tool.batch_planned") {
      const calls = arrayValue(event?.payload?.data?.calls);
      calls.forEach((item, index) => {
        const call = objectValue(item?.call);
        const callID = String(call.id || "").trim();
        if (!callID || nativeCalls.has(callID)) return;
        normalized.push(runtimeToolEvent(event, "runtime.tool_call", callID, {
          identifier: call.name,
          arguments: objectValue(call.arguments),
          approval_state: item.approval_state,
          disposition: item.disposition,
          execution_mode: item.execution_mode,
          side_effect: item.side_effect
        }, index, calls.length));
      });
      continue;
    }
    if (event?.type === "tool.call_started") {
      const data = objectValue(event?.payload?.data);
      const callID = String(data.call_id || "").trim();
      if (!callID || nativeCalls.has(callID) || plannedCalls.has(callID)) continue;
      normalized.push(runtimeToolEvent(event, "runtime.tool_call", callID, {
        identifier: data.name,
        arguments: {},
        attempt: data.attempt
      }));
      continue;
    }
    if (event?.type === "tool.call_result") {
      const data = objectValue(event?.payload?.data);
      const result = objectValue(data.result);
      const callID = String(data.call_id || result.call_id || "").trim();
      if (!callID || nativeResults.has(callID)) continue;
      const planned = plannedCalls.get(callID)?.call || {};
      normalized.push(runtimeToolEvent(event, "runtime.tool_result", callID, {
        identifier: data.name || result.name || planned.name,
        arguments: objectValue(planned.arguments),
        success: toolResultSucceeded(data.status, result),
        content: toolResultContent(result.content),
        state: objectValue(result.state),
        artifacts: arrayValue(result.artifacts),
        error: objectValue(result.error),
        duration_ms: durationMillis(data.started_at, data.completed_at)
      }));
    }
  }
  return normalized;
}

export function buildToolCallLifecycles(events) {
  const lifecycles = new Map();
  for (const event of events || []) {
    const callID = toolCallID(event);
    if (!callID) continue;
    const lifecycle = lifecycles.get(callID) || {};
    switch (event.type) {
      case "runtime.tool_call":
        lifecycle.call = latestEvent(lifecycle.call, event);
        break;
      case "runtime.tool_intervention_required":
      case "runtime.human_input_required":
        lifecycle.required = latestEvent(lifecycle.required, event);
        break;
      case "runtime.tool_intervention_approved":
        lifecycle.approved = latestEvent(lifecycle.approved, event);
        lifecycle.decision = latestEvent(lifecycle.decision, event);
        break;
      case "runtime.tool_intervention_rejected":
      case "runtime.human_input_skipped":
      case "runtime.human_input_canceled":
        lifecycle.rejected = latestEvent(lifecycle.rejected, event);
        lifecycle.decision = latestEvent(lifecycle.decision, event);
        break;
      case "runtime.human_input_submitted":
        lifecycle.approved = latestEvent(lifecycle.approved, event);
        lifecycle.decision = latestEvent(lifecycle.decision, event);
        break;
      case "runtime.tool_result":
        lifecycle.result = latestEvent(lifecycle.result, event);
        break;
      default:
        continue;
    }
    lifecycles.set(callID, lifecycle);
  }
  return lifecycles;
}

function latestEvent(left, right) {
  if (!left) return right;
  return Number(right?.seq || 0) > Number(left.seq || 0) ? right : left;
}

function runtimeToolEvent(source, type, callID, data, index = 0, count = 1) {
  const baseSeq = Number(source?.seq || 0);
  return {
    ...source,
    type,
    seq: baseSeq + ((index + 1) / (Math.max(count, 1) + 1)),
    payload: {
      ...objectValue(source?.payload),
      data: { id: callID, call_id: callID, ...data }
    }
  };
}

function toolResultSucceeded(status, result) {
  const normalized = String(status || "").trim().toLowerCase();
  if (["failed", "error", "rejected", "canceled", "cancelled"].includes(normalized)) return false;
  if (Object.keys(objectValue(result.error)).length) return false;
  return true;
}

function toolResultContent(content) {
  if (typeof content === "string") return content;
  return arrayValue(content).map((item) => {
    if (typeof item === "string") return item;
    const part = objectValue(item);
    return String(part.text || part.content || "");
  }).filter(Boolean).join("\n");
}

function durationMillis(startedAt, completedAt) {
  const started = new Date(startedAt || "").getTime();
  const completed = new Date(completedAt || "").getTime();
  return Number.isFinite(started) && Number.isFinite(completed) && completed > started ? completed - started : 0;
}

function objectValue(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function arrayValue(value) {
  return Array.isArray(value) ? value : [];
}

export function terminalToolLifecycleEvent(lifecycle) {
  return latestEvent(lifecycle?.decision, lifecycle?.result) || null;
}
