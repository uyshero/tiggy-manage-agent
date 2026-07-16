import assert from "node:assert/strict";
import test from "node:test";

import { createStaticPluginRegistry, PluginRuntimeError } from "./pluginRuntime.js";

function manifest(overrides = {}) {
  return {
    protocol_version: "tma.workbench_plugin.v1",
    id: "com.example.research",
    name: "科研工作台",
    version: "1.0.0",
    entry: "./dist/index.js",
    surfaces: ["web_desktop", "web_tablet", "web_mobile"],
    engines: { workbench_api: ">=1.0.0 <2.0.0", design_system: ">=1.0.0 <2.0.0" },
    permissions: ["research.projects.read", "ai.invoke"],
    contributes: {
      navigation: [{ id: "projects", group: "workspace", title: "科研项目", route: "/plugins/com.example.research/projects" }],
      routes: [{ id: "projects", path: "/plugins/com.example.research/projects", component: "ProjectsPage", required_permissions: ["research.projects.read"] }],
      commands: [{ id: "com.example.research.generate-report", title: "生成报告", risk: "write", required_permissions: ["ai.invoke"] }]
    },
    ...overrides
  };
}

function services() {
  return {
    permissions: {
      has: async () => true,
      require: async () => true
    },
    dialog: { confirm() {}, form() {}, choice() {}, open() {} },
    notifications: { show() {} },
    resources: { listRelated() {}, preview() {}, open() {} },
    tasks: { list() {} },
    artifacts: { list() {} },
    http: { request() {} }
  };
}

function runtime(overrides = {}) {
  return createStaticPluginRegistry({
    workbenchAPIVersion: "1.0.0",
    designSystemVersion: "1.0.0",
    surface: "web_desktop",
    navigationGroups: ["workspace"],
    scope: { workspaceId: "wksp_01", userId: "user_01", roles: ["researcher"] },
    services: services(),
    ...overrides
  });
}

test("static runtime activates and fully removes declared contributions", async () => {
  const registry = runtime();
  let activationCount = 0;
  let deactivationCount = 0;
  const plugin = {
    id: "com.example.research",
    activate(context) {
      activationCount += 1;
      context.commands.register("com.example.research.generate-report", async (input) => ({ title: input.title }));
    },
    deactivate() { deactivationCount += 1; }
  };
  registry.registerPackage({ manifest: manifest(), plugin, components: { ProjectsPage: () => null } });
  await registry.activate(plugin.id);
  await registry.activate(plugin.id);

  assert.equal(activationCount, 1);
  assert.equal(registry.navigation.list().length, 1);
  assert.equal(registry.routes.list().length, 1);
  assert.equal(registry.commands.list()[0].available, true);
  assert.deepEqual(await registry.commands.execute("com.example.research.generate-report", { title: "报告" }, { permissions: services().permissions }), { title: "报告" });

  await registry.deactivate(plugin.id);
  assert.equal(deactivationCount, 1);
  assert.equal(registry.navigation.list().length, 0);
  assert.equal(registry.routes.list().length, 0);
  assert.equal(registry.commands.list().length, 0);
});

test("activation failure rolls back routes, navigation, and commands", async () => {
  const registry = runtime();
  registry.registerPackage({
    manifest: manifest(),
    components: { ProjectsPage: () => null },
    plugin: {
      id: "com.example.research",
      activate(context) {
        context.commands.register("com.example.research.generate-report", () => undefined);
        throw new Error("activation exploded");
      }
    }
  });
  await assert.rejects(
    registry.activate("com.example.research"),
    (error) => error instanceof PluginRuntimeError && error.code === "activation_failed"
  );
  assert.equal(registry.get("com.example.research").status, "failed");
  assert.equal(registry.navigation.list().length, 0);
  assert.equal(registry.routes.list().length, 0);
  assert.equal(registry.commands.list().length, 0);
});

test("runtime rejects undeclared commands and missing declared handlers", async () => {
  const undeclared = runtime();
  undeclared.registerPackage({
    manifest: manifest(),
    components: { ProjectsPage: () => null },
    plugin: {
      id: "com.example.research",
      activate(context) { context.commands.register("com.example.research.hidden", () => undefined); }
    }
  });
  await assert.rejects(
    undeclared.activate("com.example.research"),
    (error) => error instanceof PluginRuntimeError && error.code === "command_not_declared"
  );

  const missing = runtime();
  missing.registerPackage({
    manifest: manifest(),
    components: { ProjectsPage: () => null },
    plugin: { id: "com.example.research", activate() {} }
  });
  await assert.rejects(
    missing.activate("com.example.research"),
    (error) => error instanceof PluginRuntimeError && error.code === "command_handler_missing"
  );
});

test("runtime keeps incompatible plugins visible but refuses activation", async () => {
  const registry = runtime({ workbenchAPIVersion: "2.0.0" });
  const record = registry.registerPackage({
    manifest: manifest(),
    components: { ProjectsPage: () => null },
    plugin: { id: "com.example.research", activate() {} }
  });
  assert.equal(record.status, "incompatible");
  assert.equal(record.compatibilityReasons[0].code, "workbench_api_incompatible");
  await assert.rejects(registry.activate(record.id), /incompatible/);
});

test("runtime keeps policy-disabled plugins out of active registries", async () => {
  const registry = runtime();
  const record = registry.registerPackage({
    manifest: manifest(),
    enablement: { workspaces: ["wksp_other"] },
    components: { ProjectsPage: () => null },
    plugin: { id: "com.example.research", activate() {} }
  });
  assert.equal(record.status, "disabled");
  assert.deepEqual(record.enablementReasons, ["workspace_not_enabled"]);
  await assert.rejects(
    registry.activate(record.id),
    (error) => error instanceof PluginRuntimeError && error.code === "plugin_disabled"
  );
  assert.equal(registry.navigation.list().length, 0);
});

test("deactivation errors do not leave plugin contributions behind", async () => {
  const registry = runtime();
  registry.registerPackage({
    manifest: manifest(),
    components: { ProjectsPage: () => null },
    plugin: {
      id: "com.example.research",
      activate(context) { context.commands.register("com.example.research.generate-report", () => undefined); },
      deactivate() { throw new Error("cleanup failed"); }
    }
  });
  await registry.activate("com.example.research");
  await assert.rejects(registry.deactivate("com.example.research"), /failed while deactivating/);
  assert.equal(registry.navigation.list().length, 0);
  assert.equal(registry.routes.list().length, 0);
  assert.equal(registry.commands.list().length, 0);
});

test("plugin permission facade rejects capabilities missing from the manifest", async () => {
  const registry = runtime();
  registry.registerPackage({
    manifest: manifest({ contributes: { navigation: [], routes: [], commands: [] } }),
    components: {},
    plugin: {
      id: "com.example.research",
      async activate(context) { await context.permissions.require(["files.write"]); }
    }
  });
  await assert.rejects(
    registry.activate("com.example.research"),
    (error) => error instanceof PluginRuntimeError && error.code === "permission_not_declared"
  );
});

test("catalog loading isolates plugin activation failures", async () => {
  const registry = runtime();
  const secondManifest = manifest({
    id: "com.example.stable",
    name: "稳定插件",
    permissions: [],
    contributes: { navigation: [], routes: [], commands: [] }
  });
  const results = await registry.load([
    {
      manifest: manifest(),
      components: { ProjectsPage: () => null },
      plugin: { id: "com.example.research", activate() { throw new Error("failed"); } }
    },
    {
      manifest: secondManifest,
      components: {},
      plugin: { id: "com.example.stable", activate() {} }
    }
  ]);
  assert.deepEqual(results.map((item) => item.status), ["failed", "active"]);
});

test("unregister removes the package even when deactivate fails", async () => {
  const registry = runtime();
  registry.registerPackage({
    manifest: manifest({ contributes: { navigation: [], routes: [], commands: [] } }),
    components: {},
    plugin: {
      id: "com.example.research",
      activate() {},
      deactivate() { throw new Error("failed"); }
    }
  });
  await registry.activate("com.example.research");
  await assert.rejects(registry.unregisterPackage("com.example.research"), /failed while deactivating/);
  assert.equal(registry.get("com.example.research"), null);
});
