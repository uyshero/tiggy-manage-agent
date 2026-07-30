import test from "node:test";
import assert from "node:assert/strict";
import { contextCompactionFailureDedupeKey, contextCompactionPresentation, isContextCompactionEvent } from "./contextCompactionEvents.js";

test("presents active and historical context compaction starts", () => {
  const event = {
    type: "runtime.context_compacting",
    payload: { data: { id: "compaction_attempt_2", number: 2 } }
  };

  assert.equal(isContextCompactionEvent(event), true);
  assert.deepEqual(
    { status: contextCompactionPresentation(event, { active: true }).status, label: contextCompactionPresentation(event, { active: true }).statusLabel },
    { status: "running", label: "压缩中" }
  );
  assert.match(contextCompactionPresentation(event).detail, /第 2 次/);
  assert.equal(contextCompactionPresentation(event).statusLabel, "已开始");
});

test("presents completed context compaction token estimate", () => {
  const result = contextCompactionPresentation({
    type: "runtime.context_compacted",
    payload: { data: { attempt_id: "compaction_attempt_2", estimated_input_tokens: 4321 } }
  });

  assert.equal(result.title, "上下文压缩完成");
  assert.equal(result.status, "completed");
  assert.match(result.detail, /4,321/);
  assert.equal(result.detailObject.estimated_input_tokens, 4321);
});

test("presents context compaction failure details", () => {
  const result = contextCompactionPresentation({
    type: "runtime.context_compaction_failed",
    payload: {
      data: {
        code: "context_compaction_failed",
        message: "provider rejected the compacted request"
      }
    }
  });

  assert.equal(result.title, "上下文压缩失败");
  assert.equal(result.status, "error");
  assert.match(result.detail, /provider rejected the compacted request/);
  assert.equal(result.detailObject.error_code, "context_compaction_failed");
});

test("matches generic and explicit compaction failures from the same loop revision", () => {
  const payload = {
    turn_id: "turn_1",
    loop_revision: 7,
    data: { code: "context_compaction_failed" }
  };

  assert.equal(
    contextCompactionFailureDedupeKey({ type: "runtime.failed", payload }),
    contextCompactionFailureDedupeKey({ type: "runtime.context_compaction_failed", payload })
  );
  assert.equal(contextCompactionFailureDedupeKey({ type: "runtime.failed", payload: { ...payload, data: { code: "model_request_failed" } } }), "");
});
