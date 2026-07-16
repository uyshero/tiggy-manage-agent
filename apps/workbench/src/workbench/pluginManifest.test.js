import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  PluginManifestError,
  checkPluginCompatibility,
  definePluginManifest,
  satisfiesSemverRange
} from "./pluginManifest.js";

function manifest(overrides = {}) {
  return {
    protocol_version: "tma.workbench_plugin.v1",
    id: "com.example.research",
    name: "科研工作台",
    description: "科研项目管理",
    version: "1.2.0",
    entry: "./dist/index.js",
    surfaces: ["web_desktop", "web_tablet", "web_mobile"],
    engines: { workbench_api: ">=1.0.0 <2.0.0", design_system: ">=1.0.0 <2.0.0" },
    permissions: ["research.projects.read", "ai.invoke"],
    contributes: {
      navigation: [{ id: "projects", group: "workspace", title: "科研项目", route: "/plugins/com.example.research/projects" }],
      routes: [{
        id: "projects",
        path: "/plugins/com.example.research/projects",
        component: "ProjectsPage",
        required_permissions: ["research.projects.read"]
      }],
      commands: [{
        id: "com.example.research.generate-report",
        title: "生成报告",
        risk: "write",
        required_permissions: ["ai.invoke"]
      }]
    },
    ...overrides
  };
}

test("plugin manifest normalizes and freezes Phase 0A contributions", () => {
  const value = definePluginManifest(manifest());
  assert.equal(value.id, "com.example.research");
  assert.equal(value.contributes.routes[0].component, "ProjectsPage");
  assert.deepEqual(value.contributes.commands[0].required_permissions, ["ai.invoke"]);
  assert.equal(Object.isFrozen(value), true);
  assert.equal(Object.isFrozen(value.contributes.routes), true);
});

test("plugin manifest rejects paths, permissions, namespaces, and package escapes", () => {
  const invalidRoute = manifest();
  invalidRoute.contributes.routes[0].path = "/settings";
  assert.throws(() => definePluginManifest(invalidRoute), PluginManifestError);

  const undeclaredPermission = manifest();
  undeclaredPermission.contributes.routes[0].required_permissions = ["files.read"];
  assert.throws(
    () => definePluginManifest(undeclaredPermission),
    (error) => error instanceof PluginManifestError && error.code === "undeclared_permission"
  );

  const wrongCommandNamespace = manifest();
  wrongCommandNamespace.contributes.commands[0].id = "com.other.generate-report";
  assert.throws(() => definePluginManifest(wrongCommandNamespace), /plugin namespace/);

  assert.throws(() => definePluginManifest(manifest({ entry: "../remote.js" })), /relative path/);
  const futureContribution = manifest();
  futureContribution.contributes.widgets = [];
  assert.throws(() => definePluginManifest(futureContribution), /widgets/);
});

test("navigation must reference a declared plugin route", () => {
  const value = manifest();
  value.contributes.navigation[0].route = "/plugins/com.example.research/missing";
  assert.throws(() => definePluginManifest(value), /declared route/);
});

test("compatibility reports engine and surface reasons without hiding the manifest", () => {
  const result = checkPluginCompatibility(manifest(), {
    workbenchAPIVersion: "2.0.0",
    designSystemVersion: "1.5.0",
    surface: "desktop_shell"
  });
  assert.equal(result.compatible, false);
  assert.deepEqual(result.reasons.map((reason) => reason.code), ["surface_unsupported", "workbench_api_incompatible"]);
  assert.equal(result.manifest.id, "com.example.research");
  assert.equal(satisfiesSemverRange("1.4.2", ">=1.0.0 <2.0.0"), true);
  assert.equal(satisfiesSemverRange("2.0.0", ">=1.0.0 <2.0.0"), false);
});

test("plugin JSON Schema is valid JSON and identifies the v1 protocol", async () => {
  const schema = JSON.parse(await readFile(new URL("./plugin.schema.json", import.meta.url), "utf8"));
  assert.equal(schema.properties.protocol_version.const, "tma.workbench_plugin.v1");
  assert.deepEqual(schema.required.includes("contributes"), true);
});

export { manifest as pluginManifestFixture };
