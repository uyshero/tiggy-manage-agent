import assert from "node:assert/strict";
import test from "node:test";
import { buildToolCallLifecycles, terminalToolLifecycleEvent, toolCallID } from "./toolLifecycle.js";

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
