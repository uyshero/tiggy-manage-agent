export function toolCallID(event) {
  const data = event?.payload?.data;
  if (!data || typeof data !== "object" || Array.isArray(data)) return "";
  return String(data.id || data.call_id || "").trim();
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

export function terminalToolLifecycleEvent(lifecycle) {
  return latestEvent(lifecycle?.decision, lifecycle?.result) || null;
}
