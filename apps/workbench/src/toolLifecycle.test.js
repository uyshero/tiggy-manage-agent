import assert from "node:assert/strict";
import test from "node:test";
import { buildToolCallLifecycles, normalizeToolTimelineEvents, terminalToolLifecycleEvent, toolApprovalPresentation, toolCallID, toolResultFailurePresentation } from "./toolLifecycle.js";

function event(seq, type, id, data = {}) {
  return { seq, type, created_at: `2026-07-14T16:19:0${seq}Z`, payload: { data: { id, ...data } } };
}

test("groups approval and result events by tool call id", () => {
  const events = [
    event(1, "runtime.tool_call", "call-1"),
    event(2, "runtime.tool_intervention_required", "call-1"),
    event(3, "runtime.tool_intervention_approved", "call-1"),
    event(4, "runtime.tool_result", "call-1", { success: true })
  ];
  const lifecycle = buildToolCallLifecycles(events).get("call-1");

  assert.equal(toolCallID(events[0]), "call-1");
  assert.equal(lifecycle.required.seq, 2);
  assert.equal(lifecycle.approved.seq, 3);
  assert.equal(lifecycle.result.seq, 4);
  assert.equal(terminalToolLifecycleEvent(lifecycle).seq, 4);
});

test("uses rejection as terminal state when execution never starts", () => {
  const lifecycle = buildToolCallLifecycles([
    event(1, "runtime.tool_call", "call-2"),
    event(2, "runtime.tool_intervention_required", "call-2"),
    event(3, "runtime.tool_intervention_rejected", "call-2")
  ]).get("call-2");

  assert.equal(terminalToolLifecycleEvent(lifecycle).type, "runtime.tool_intervention_rejected");
  assert.equal(lifecycle.result, undefined);
});

test("uses the newest decision and result even when events arrive out of order", () => {
  const lifecycle = buildToolCallLifecycles([
    event(7, "runtime.tool_result", "call-3", { success: true }),
    event(3, "runtime.tool_intervention_rejected", "call-3"),
    event(5, "runtime.tool_intervention_approved", "call-3"),
    event(2, "runtime.tool_intervention_required", "call-3"),
    event(1, "runtime.tool_call", "call-3")
  ]).get("call-3");

  assert.equal(lifecycle.decision.type, "runtime.tool_intervention_approved");
  assert.equal(terminalToolLifecycleEvent(lifecycle).type, "runtime.tool_result");
});

test("normalizes Agent Core tool events into visible runtime tool lifecycles", () => {
  const events = normalizeToolTimelineEvents([
    {
      seq: 10,
      type: "tool.batch_planned",
      payload: { data: { calls: [{
        call: { id: "call-core-1", name: "web_search", arguments: { query: "农业新闻", limit: 10 } },
        approval_state: "not_required",
        disposition: "execute"
      }] } }
    },
    {
      seq: 11,
      type: "tool.call_started",
      payload: { data: { call_id: "call-core-1", name: "web_search", status: "running" } }
    },
    {
      seq: 12,
      type: "tool.call_result",
      payload: { data: {
        call_id: "call-core-1",
        name: "web_search",
        status: "succeeded",
        started_at: "2026-07-22T06:16:27Z",
        completed_at: "2026-07-22T06:16:47Z",
        result: { content: [{ text: "10 results" }], state: { query: "农业新闻" } }
      } }
    }
  ]);

  const call = events.find((item) => item.type === "runtime.tool_call");
  const result = events.find((item) => item.type === "runtime.tool_result");
  const lifecycle = buildToolCallLifecycles(events).get("call-core-1");

  assert.deepEqual(call.payload.data.arguments, { query: "农业新闻", limit: 10 });
  assert.equal(call.payload.data.identifier, "web_search");
  assert.equal(result.payload.data.success, true);
  assert.equal(result.payload.data.content, "10 results");
  assert.equal(result.payload.data.duration_ms, 20000);
  assert.equal(lifecycle.call.type, "runtime.tool_call");
  assert.equal(lifecycle.result.type, "runtime.tool_result");
});

test("does not duplicate native runtime tool events", () => {
  const nativeCall = event(1, "runtime.tool_call", "call-native", { identifier: "web", api_name: "search" });
  const nativeResult = event(3, "runtime.tool_result", "call-native", { success: true });
  const events = normalizeToolTimelineEvents([
    nativeCall,
    { seq: 2, type: "tool.batch_planned", payload: { data: { calls: [{ call: { id: "call-native", name: "web_search", arguments: {} } }] } } },
    nativeResult,
    { seq: 4, type: "tool.call_result", payload: { data: { call_id: "call-native", name: "web_search", status: "succeeded", result: {} } } }
  ]);

  assert.equal(events.filter((item) => item.type === "runtime.tool_call").length, 1);
  assert.equal(events.filter((item) => item.type === "runtime.tool_result").length, 1);
});

test("keeps approval state visible after an approved tool finishes", () => {
  const call = event(1, "runtime.tool_call", "call-approved", {
    identifier: "web_search",
    approval_state: "pending",
    approval_source: "human",
    permission: { required: true, mode: "request_approval", risk: "read", reason: "network_access" }
  });
  const approved = event(2, "runtime.tool_intervention_approved", "call-approved", {
    approval_source: "user",
    decision_reason: "用户在工作台批准工具调用"
  });
  const result = event(3, "runtime.tool_result", "call-approved", { success: true });
  const lifecycle = buildToolCallLifecycles([call, approved, result]).get("call-approved");
  const approval = toolApprovalPresentation(call, lifecycle);

  assert.equal(approval.status, "approved");
  assert.equal(approval.label, "已批准（用户）");
  assert.equal(approval.detail.reason, "用户在工作台批准工具调用");
  assert.equal(approval.detail.risk, "read");
});

test("distinguishes tools that do not require approval", () => {
  const call = event(1, "runtime.tool_call", "call-free", {
    identifier: "web_search",
    approval_state: "not_required",
    permission: { required: false }
  });

  assert.equal(toolApprovalPresentation(call, {}).label, "无需审批");
});

test("presents the concrete error from a failed tool result", () => {
  const result = event(2, "runtime.tool_result", "call-read", {
    success: false,
    status: "failed",
    retryable: false,
    content: "unable to read file",
    error: {
      type: "read_failed",
      message: "open /opt/missing/manifest-schema.md: no such file or directory"
    }
  });

  assert.deepEqual(toolResultFailurePresentation(result), {
    message: "open /opt/missing/manifest-schema.md: no such file or directory",
    type: "read_failed",
    retryable: false
  });
});

test("reads nested protocol errors when the result content is JSON", () => {
  const result = event(2, "runtime.tool_result", "call-json", {
    success: false,
    content: JSON.stringify({
      protocol_version: "tma.tool_result.v1",
      error: { type: "path_not_found", message: "requested file does not exist" }
    })
  });

  assert.deepEqual(toolResultFailurePresentation(result), {
    message: "requested file does not exist",
    type: "path_not_found",
    retryable: undefined
  });
});
