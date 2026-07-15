import assert from "node:assert/strict";
import test from "node:test";

import {
  ExtensionRegistryError,
  createCommandRegistry,
  createNavigationRegistry,
  createRouteRegistry
} from "./extensionRegistries.js";

test("navigation registry enforces host groups, ordering, and cleanup", () => {
  const registry = createNavigationRegistry({ groups: ["workspace"] });
  const snapshots = [];
  registry.subscribe((items) => snapshots.push(items.length));
  const removeLater = registry.register("com.example.two", { id: "two", group: "workspace", title: "第二项", route: "/plugins/com.example.two/page", order: 20 });
  const removeFirst = registry.register("com.example.one", { id: "one", group: "workspace", title: "第一项", route: "/plugins/com.example.one/page", order: 10 });
  assert.deepEqual(registry.list().map((item) => item.id), ["one", "two"]);
  assert.throws(
    () => registry.register("com.example.bad", { id: "bad", group: "root", title: "越权", route: "/plugins/com.example.bad/page" }),
    (error) => error instanceof ExtensionRegistryError && error.code === "navigation_group_denied"
  );
  removeFirst();
  removeLater();
  assert.deepEqual(snapshots, [0, 1, 2, 1, 0]);
});

test("route registry binds component exports and prevents path conflicts", () => {
  const registry = createRouteRegistry();
  const Page = () => null;
  const remove = registry.register("com.example.one", {
    id: "page",
    path: "/plugins/com.example.one/page",
    component: "Page",
    required_permissions: ["pages.read"]
  }, Page);
  assert.equal(registry.get("/plugins/com.example.one/page").component, Page);
  assert.throws(
    () => registry.register("com.example.two", { id: "page", path: "/plugins/com.example.one/page", component: "Page" }, Page),
    (error) => error instanceof ExtensionRegistryError && error.code === "route_exists"
  );
  remove();
  assert.equal(registry.get("/plugins/com.example.one/page"), null);
});

test("command registry requires declarations, permissions, and emits audit hooks", async () => {
  const audit = [];
  const registry = createCommandRegistry({
    beforeExecute: async (command) => audit.push(`start:${command.id}`),
    afterExecute: async (command, result) => audit.push(`finish:${command.id}:${result.ok}`)
  });
  const removeDeclaration = registry.declare("com.example.research", {
    id: "com.example.research.generate",
    title: "生成",
    risk: "write",
    required_permissions: ["ai.invoke"],
    contexts: []
  });
  assert.throws(
    () => registry.registerHandler("com.example.other", "com.example.research.generate", () => undefined),
    (error) => error instanceof ExtensionRegistryError && error.code === "command_not_declared"
  );
  const removeHandler = registry.registerHandler("com.example.research", "com.example.research.generate", async (input) => ({ reportID: input.id }));
  const required = [];
  const result = await registry.execute("com.example.research.generate", { id: "report_01" }, {
    permissions: { require: async (permissions) => required.push(...permissions) }
  });
  assert.deepEqual(result, { reportID: "report_01" });
  assert.deepEqual(required, ["ai.invoke"]);
  assert.deepEqual(audit, ["start:com.example.research.generate", "finish:com.example.research.generate:true"]);
  removeHandler();
  await assert.rejects(registry.execute("com.example.research.generate", {}), /not active/);
  removeDeclaration();
});

test("command registry audits permission denials", async () => {
  const audit = [];
  const registry = createCommandRegistry({
    afterExecute: async (_command, result) => audit.push(result.ok)
  });
  registry.declare("com.example.research", {
    id: "com.example.research.read",
    title: "读取",
    required_permissions: ["research.read"]
  });
  registry.registerHandler("com.example.research", "com.example.research.read", () => "unreachable");
  await assert.rejects(
    registry.execute("com.example.research.read", {}, { permissions: { require: async () => { throw new Error("denied"); } } }),
    /denied/
  );
  assert.deepEqual(audit, [false]);
});
