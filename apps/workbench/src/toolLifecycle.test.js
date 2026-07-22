import assert from "node:assert/strict";
import test from "node:test";
import { buildToolCallLifecycles, normalizeToolTimelineEvents, terminalToolLifecycleEvent, toolCallID } from "./toolLifecycle.js";

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
        call: { id: "call-core-1", name: "web.search", arguments: { query: "农业新闻", limit: 10 } },
        approval_state: "not_required",
        disposition: "execute"
      }] } }
    },
    {
      seq: 11,
      type: "tool.call_started",
      payload: { data: { call_id: "call-core-1", name: "web.search", status: "running" } }
    },
    {
      seq: 12,
      type: "tool.call_result",
      payload: { data: {
        call_id: "call-core-1",
        name: "web.search",
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
  assert.equal(call.payload.data.identifier, "web.search");
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
    { seq: 2, type: "tool.batch_planned", payload: { data: { calls: [{ call: { id: "call-native", name: "web.search", arguments: {} } }] } } },
    nativeResult,
    { seq: 4, type: "tool.call_result", payload: { data: { call_id: "call-native", name: "web.search", status: "succeeded", result: {} } } }
  ]);

  assert.equal(events.filter((item) => item.type === "runtime.tool_call").length, 1);
  assert.equal(events.filter((item) => item.type === "runtime.tool_result").length, 1);
});
