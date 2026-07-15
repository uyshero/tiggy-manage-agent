import assert from "node:assert/strict";
import test from "node:test";

import { definePluginEnablement, evaluatePluginEnablement } from "./pluginPolicy.js";

test("plugin enablement matches organization, workspace, and any allowed role", () => {
  const result = evaluatePluginEnablement({
    organizations: ["org_01"],
    workspaces: ["wksp_01"],
    roles: ["researcher", "admin"]
  }, {
    organizationId: "org_01",
    workspaceId: "wksp_01",
    roles: ["member", "researcher"]
  });
  assert.equal(result.enabled, true);
});

test("plugin enablement returns deterministic exclusion reasons", () => {
  const result = evaluatePluginEnablement({
    workspaces: ["wksp_02"],
    roles: ["researcher"],
    excludedRoles: ["suspended"]
  }, {
    workspaceId: "wksp_01",
    roles: ["member", "suspended"]
  });
  assert.deepEqual(result.reasons, ["workspace_not_enabled", "role_not_enabled", "role_excluded"]);
});

test("plugin enablement rejects unknown policy fields", () => {
  assert.throws(() => definePluginEnablement({ tenant: ["org_01"] }), /tenant/);
});

test("explicit allowlists can enable a default-disabled plugin", () => {
  const result = evaluatePluginEnablement({ defaultEnabled: false, workspaces: ["wksp_01"] }, {
    workspaceId: "wksp_01",
    roles: ["member"]
  });
  assert.equal(result.enabled, true);
});
