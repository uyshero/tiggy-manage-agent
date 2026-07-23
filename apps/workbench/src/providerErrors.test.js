import test from "node:test";
import assert from "node:assert/strict";
import { providerErrorPresentation } from "./providerErrors.js";

test("adds a friendly description while preserving the original provider error", () => {
  const result = providerErrorPresentation({
    class: "server",
    status_code: 503,
    attempts: 3,
    retry_after_ms: 2500,
    message: "upstream overloaded"
  });

  assert.equal(result.description, "模型服务暂时不可用，请稍后重试。");
  assert.equal(result.original, "upstream overloaded");
  assert.equal(result.detail, "模型服务暂时不可用，请稍后重试。 原始错误：upstream overloaded（HTTP 503，已尝试 3 次，建议等待 3 秒）");
});

test("uses a useful fallback without discarding an unknown error", () => {
  const result = providerErrorPresentation({ class: "custom" }, "connection reset by peer");

  assert.equal(result.description, "模型请求失败，请根据原始错误排查。");
  assert.equal(result.original, "connection reset by peer");
});

test("derives the HTTP status from normalized provider error codes", () => {
  const result = providerErrorPresentation({
    class: "invalid_request",
    code: "http_400",
    message: "invalid request"
  });

  assert.match(result.detail, /HTTP 400/);
});
