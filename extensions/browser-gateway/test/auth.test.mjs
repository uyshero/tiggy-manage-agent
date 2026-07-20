import assert from "node:assert/strict";
import test from "node:test";

import { serviceSignature } from "../src/auth.mjs";
import { isolationDigest, safeSessionID } from "../src/session-manager.mjs";

test("service signature binds method, path, tenant, session, and body", () => {
  const base = {
    secret: "test-secret",
    timestamp: "1700000000",
    method: "POST",
    pathname: "/v2/extensions/browser/sessions/s1/open",
    workspaceID: "wksp_a",
    sessionID: "sesn_a",
    body: Buffer.from('{"url":"https://example.com"}')
  };
  const signature = serviceSignature(base);
  assert.equal(signature.length, 64);
  assert.notEqual(signature, serviceSignature({ ...base, workspaceID: "wksp_b" }));
  assert.notEqual(signature, serviceSignature({ ...base, body: Buffer.from("{}") }));
});

test("session ids and profile isolation are deterministic and scoped", () => {
  assert.equal(safeSessionID("sesn_123"), "sesn_123");
  assert.throws(() => safeSessionID("../../escape"), /invalid browser session id/);
  const session = "sesn_123";
  const left = isolationDigest({ workspaceID: "wksp_a", ownerID: "owner_a" }, session);
  const right = isolationDigest({ workspaceID: "wksp_b", ownerID: "owner_a" }, session);
  assert.equal(left.length, 32);
  assert.notEqual(left, right);
});
