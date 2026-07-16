import assert from "node:assert/strict";
import test from "node:test";

import { PermissionDeniedError, createPermissionService } from "./permissionService.js";

test("permission service requires every declared grant", async () => {
  const service = createPermissionService({ grants: ["research.projects.read", "ai.invoke"] });
  assert.equal(await service.has("research.projects.read"), true);
  assert.equal(await service.has(["research.projects.read", "ai.invoke"]), true);
  assert.equal(await service.has(["research.projects.read", "files.write"]), false);
  assert.equal(await service.require([]), true);
});

test("permission service reports the exact denied permissions", async () => {
  const service = createPermissionService({ grants: ["research.projects.read"] });
  await assert.rejects(
    service.require(["research.projects.read", "ai.invoke", "files.write"]),
    (error) => error instanceof PermissionDeniedError &&
      error.code === "permission_denied" &&
      JSON.stringify(error.permissions) === JSON.stringify(["ai.invoke", "files.write"])
  );
});
