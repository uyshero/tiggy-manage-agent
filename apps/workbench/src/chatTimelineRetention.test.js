import test from "node:test";
import assert from "node:assert/strict";
import { retainedProcessText } from "./chatTimelineRetention.js";

test("retains persisted model progress text", () => {
  assert.equal(retainedProcessText({
    type: "runtime.progress_message",
    payload: { data: { text: "I will inspect the current files." } }
  }), "I will inspect the current files.");
});

test("retains durable thinking text from supported payload shapes", () => {
  assert.equal(retainedProcessText({
    type: "runtime.thinking",
    payload: { message: "Checking the current state." }
  }), "Checking the current state.");
  assert.equal(retainedProcessText({
    type: "runtime.thinking",
    payload: { data: { text: "Preparing the next step." } }
  }), "Preparing the next step.");
});

test("does not expose unrelated event content as process text", () => {
  assert.equal(retainedProcessText({
    type: "agent.message",
    payload: { message: "Final answer" }
  }), "");
});
