import assert from "node:assert/strict";
import test from "node:test";
import { shouldSyncSessionForEvent } from "./sessionSyncEvents.js";

test("refreshes pending interventions for Agent Core durable events", () => {
  assert.equal(shouldSyncSessionForEvent({ type: "intervention.required" }), true);
  assert.equal(shouldSyncSessionForEvent({ type: "intervention.resolved" }), true);
});

test("keeps legacy intervention and terminal session refresh triggers", () => {
  assert.equal(shouldSyncSessionForEvent("runtime.tool_intervention_required"), true);
  assert.equal(shouldSyncSessionForEvent("runtime.plan_approval_approved"), true);
  assert.equal(shouldSyncSessionForEvent("session.status_idle"), true);
});

test("does not refetch the Session for high-frequency stream events", () => {
  assert.equal(shouldSyncSessionForEvent("llm.text"), false);
  assert.equal(shouldSyncSessionForEvent("tool.call_progress"), false);
  assert.equal(shouldSyncSessionForEvent("model.responded"), false);
});
