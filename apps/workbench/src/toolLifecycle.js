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
          approval_source: item.approval_source,
          disposition: item.disposition,
          execution_mode: item.execution_mode,
          permission: objectValue(item.permission),
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

export function toolApprovalPresentation(event, lifecycle) {
  const data = objectValue(event?.payload?.data);
  const decision = lifecycle?.decision;
  const decisionData = objectValue(decision?.payload?.data);
  const requiredData = objectValue(lifecycle?.required?.payload?.data);
  const permission = objectValue(data.permission);
  const state = String(data.approval_state || "").trim().toLowerCase();
  const source = approvalSourceLabel(decisionData.approval_source || data.approval_source);
  const reason = approvalReason(decisionData.decision_reason || requiredData.reason || permission.reason);
  const detail = {
    source: source || undefined,
    reason: reason || undefined,
    risk: permission.risk || undefined,
    policy_mode: permission.mode || undefined,
    approval_policy: permission.approval_policy || undefined
  };

  if (decision?.type === "runtime.tool_intervention_rejected") {
    return { status: "rejected", label: source ? `已拒绝（${source}）` : "已拒绝", kind: "error", detail };
  }
  if (decision?.type === "runtime.tool_intervention_approved") {
    return { status: "approved", label: source ? `已批准（${source}）` : "已批准", kind: "approved", detail };
  }
  if (lifecycle?.required || state === "pending" || data.pending_intervention === true) {
    return { status: "pending", label: source ? `待${source}审批` : "待审批", kind: "pending", detail };
  }
  if (["approved", "approve"].includes(state)) {
    return { status: "approved", label: source ? `已批准（${source}）` : "已批准", kind: "approved", detail };
  }
  if (["rejected", "reject", "denied"].includes(state)) {
    return { status: "rejected", label: source ? `已拒绝（${source}）` : "已拒绝", kind: "error", detail };
  }
  if (["not_required", "not-required", "none"].includes(state) || permission.required === false) {
    return { status: "not_required", label: "无需审批", kind: "none", detail };
  }
  return { status: "not_triggered", label: "未触发审批", kind: "none", detail };
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
      case "tool.call_started":
        lifecycle.started = latestEvent(lifecycle.started, event);
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

export function toolResultFailurePresentation(event) {
  const data = objectValue(event?.payload?.data);
  const error = objectValue(data.error);
  const status = String(data.status || "").trim().toLowerCase();
  const failed = data.success === false || Object.keys(error).length > 0 || [
    "failed",
    "error",
    "rejected",
    "canceled",
    "cancelled"
  ].includes(status);
  if (!failed) return null;

  const contentResult = parseToolResultContent(data.content);
  const contentError = objectValue(contentResult.error);
  const message = firstText(
    error.message,
    contentError.message,
    typeof data.error === "string" ? data.error : "",
    data.message,
    contentResult.message,
    data.content,
    event?.payload?.message,
    "工具执行失败。"
  );
  return {
    message,
    type: firstText(error.type, error.code, contentError.type, contentError.code, data.error_type, data.reason),
    retryable: typeof data.retryable === "boolean" ? data.retryable : undefined
  };
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

function parseToolResultContent(content) {
  if (typeof content !== "string" || !content.trim().startsWith("{")) return {};
  try {
    return objectValue(JSON.parse(content));
  } catch {
    return {};
  }
}

function firstText(...values) {
  for (const value of values) {
    const text = typeof value === "string" ? value.trim() : "";
    if (text) return text;
  }
  return "";
}

function approvalSourceLabel(value) {
  const source = String(value || "").trim().toLowerCase();
  if (source === "user") return "用户";
  if (source === "human") return "人工";
  if (source === "policy") return "策略";
  if (source === "system") return "系统";
  return source;
}

function approvalReason(value) {
  const reason = String(value || "").trim();
  if (["approved from app", "approved from inspector"].includes(reason.toLowerCase())) return "";
  return reason;
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

export function toolLifecycleIsRunning(lifecycle, executionActive = true) {
  if (!executionActive) return false;
  if (!lifecycle?.started || lifecycle?.result) return false;
  if (lifecycle?.decision?.type === "runtime.tool_intervention_rejected") return false;
  return true;
}

export function shouldSynthesizeThinking(internalEvents) {
  const events = Array.isArray(internalEvents) ? internalEvents : [];
  return events.length > 0 && !events.every((event) => String(event?.type || "").startsWith("tool."));
}

export function liveToolProgressAfterEvent(current, event) {
  if (!current) return null;
  if (["runtime.failed", "runtime.completed"].includes(event?.type)) return null;
  if (event?.type !== "tool.call_result") return current;
  const completedCallID = toolCallID(event);
  return !completedCallID || completedCallID === current.callID ? null : current;
}
