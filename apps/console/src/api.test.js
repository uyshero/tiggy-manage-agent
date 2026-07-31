import assert from "node:assert/strict";
import test from "node:test";
import { createConsoleAPI } from "./api.js";

test("Console delegates structured errors to the Core SDK", async () => {
  let requestedURL = "";
  const api = createConsoleAPI({
    baseURL: "https://tma.example",
    fetch: async (input) => {
      requestedURL = String(input);
      return new Response(JSON.stringify({ error: { code: "forbidden", message: "denied", request_id: "req_1", retryable: false } }), { status: 403 });
    },
  });
  await assert.rejects(() => api.getContext(), (error) => error.message === "denied" && error.code === "forbidden");
  assert.equal(requestedURL, "https://tma.example/v2/administration/context");
});
