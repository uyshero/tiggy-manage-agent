import assert from "node:assert/strict";
import test from "node:test";
import { durableEventReplacesLiveReply } from "./chatLiveReply.js";

const liveReply = { turnID: "turn_1", toolRound: 2, text: "I will inspect it." };

test("tool lifecycle events do not retract a live reply", () => {
  for (const type of ["runtime.tool_call", "tool.batch_planned", "tool.call_started"]) {
    assert.equal(durableEventReplacesLiveReply({ type, payload: { turn_id: "turn_1" } }, liveReply), false, type);
  }
});

test("same-round durable progress replaces its live counterpart", () => {
  assert.equal(durableEventReplacesLiveReply({
    type: "runtime.progress_message",
    payload: { turn_id: "turn_1", data: { tool_round: 2, text: "I will inspect it." } }
  }, liveReply), true);
  assert.equal(durableEventReplacesLiveReply({
    type: "runtime.progress_message",
    payload: { turn_id: "turn_1", data: { tool_round: 1, text: "Earlier text" } }
  }, liveReply), false);
});

test("a visible final answer or explicit failure ends the live reply", () => {
  assert.equal(durableEventReplacesLiveReply({
    type: "agent.message",
    payload: { turn_id: "turn_1", content: [{ type: "text", text: "Done" }] }
  }, liveReply), true);
  assert.equal(durableEventReplacesLiveReply({
    type: "agent.message",
    payload: { turn_id: "turn_2", content: "Other turn" }
  }, liveReply), false);
  assert.equal(durableEventReplacesLiveReply({ type: "runtime.failed", payload: { turn_id: "turn_1" } }, liveReply), true);
});
