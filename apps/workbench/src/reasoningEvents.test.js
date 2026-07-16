import test from "node:test";
import assert from "node:assert/strict";
import { isReasoningChunk, mergeReasoningChunks } from "./reasoningEvents.js";

function chunk(seq, text, options = {}) {
  return {
    seq,
    type: "runtime.llm_chunk",
    payload: {
      turn_id: options.turnID || "turn_1",
      data: {
        index: seq,
        text,
        tool_round: options.toolRound || 0,
        type: options.type || "reasoning"
      }
    }
  };
}

test("identifies only reasoning llm chunks", () => {
  assert.equal(isReasoningChunk(chunk(1, "plan")), true);
  assert.equal(isReasoningChunk(chunk(2, "answer", { type: "text" })), false);
  assert.equal(isReasoningChunk({ type: "runtime.thinking", payload: {} }), false);
});

test("merges consecutive reasoning chunks in the same turn and tool round", () => {
  const source = [chunk(1, "check "), chunk(2, "evidence")];
  const merged = mergeReasoningChunks(source);

  assert.equal(merged.length, 1);
  assert.equal(merged[0].payload.data.text, "check evidence");
  assert.equal(merged[0].payload.data.end_index, 2);
  assert.equal(source[0].payload.data.text, "check ");
});

test("does not merge reasoning across a text chunk or tool round", () => {
  const merged = mergeReasoningChunks([
    chunk(1, "first"),
    chunk(2, "answer", { type: "text" }),
    chunk(3, "second"),
    chunk(4, "third", { toolRound: 1 })
  ]);

  assert.equal(merged.length, 4);
});
