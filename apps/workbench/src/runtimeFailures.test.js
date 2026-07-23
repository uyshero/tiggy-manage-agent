import test from "node:test";
import assert from "node:assert/strict";
import { runtimeFailurePresentation } from "./runtimeFailures.js";

test("uses nested runtime failure details instead of the generic event message", () => {
  const result = runtimeFailurePresentation({
    message: "Agent runtime failed.",
    data: {
      code: "context_compaction_failed",
      message: "agent context compaction failed"
    }
  });

  assert.match(result.detail, /上下文压缩失败/);
  assert.match(result.detail, /agent context compaction failed/);
  assert.match(result.detail, /context_compaction_failed/);
  assert.doesNotMatch(result.detail, /Agent runtime failed/);
});

test("presents structured provider failures with actionable metadata", () => {
  const result = runtimeFailurePresentation({
    data: {
      code: "context_compaction_failed",
      message: "context compaction failed: provider request failed",
      provider_error: {
        class: "context_length",
        code: "http_400",
        status_code: 400,
        attempts: 2,
        message: "request payload is too large"
      }
    }
  });

  assert.match(result.detail, /上下文压缩失败/);
  assert.match(result.detail, /request payload is too large/);
  assert.equal(result.providerError.status_code, 400);
});

test("infers provider metadata for persisted legacy runtime failures", () => {
  const result = runtimeFailurePresentation({
    message: "Agent runtime failed.",
    data: {
      code: "http_400",
      message: "provider request failed (invalid_request/http_400): invalid parameter"
    }
  });

  assert.match(result.detail, /模型服务无法处理当前请求/);
  assert.equal(result.providerError.class, "invalid_request");
  assert.equal(result.providerError.status_code, 400);
});
