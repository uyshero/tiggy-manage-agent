import assert from "node:assert/strict";
import test from "node:test";

import { browserSessionPath, createBrowserAPI } from "./browserApi.js";

test("browser extension API keeps requests under the scoped v2 prefix", async () => {
  const calls = [];
  const api = createBrowserAPI({
    request(path, options) {
      calls.push({ path, options });
      return Promise.resolve({ browser_session_id: "sesn/1" });
    }
  });
  await api.action("sesn/1", "open", { url: "https://example.com" });
  assert.equal(calls[0].path, "/v2/extensions/browser/sessions/sesn%2F1/open");
  assert.equal(calls[0].options.method, "POST");
  assert.equal(browserSessionPath("sesn/1"), "/v2/extensions/browser/sessions/sesn%2F1");
  assert.match(api.frameURL("sesn/1", 7), /\/frame\?v=7$/);
});
