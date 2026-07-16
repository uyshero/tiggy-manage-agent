import assert from "node:assert/strict";
import test from "node:test";

import {
  WorkbenchContractError,
  defineCommand,
  definePluginContext,
  defineResourceRef,
  isCommandDefinition,
  isResourceRef
} from "./contracts.js";

test("defineResourceRef normalizes and freezes a resource", () => {
  const resource = defineResourceRef({
    id: "artifact_01",
    type: "artifact",
    title: "研究报告.md",
    source: "session:session_01",
    mimeType: "text/markdown",
    previewable: true,
    metadata: { size_bytes: 128, tags: ["report"] }
  });

  assert.equal(resource.title, "研究报告.md");
  assert.equal(resource.metadata.size_bytes, 128);
  assert.equal(Object.isFrozen(resource), true);
  assert.equal(Object.isFrozen(resource.metadata), true);
  assert.equal(Object.isFrozen(resource.metadata.tags), true);
  assert.equal(isResourceRef(resource), true);
});

test("defineResourceRef rejects unknown types and non-JSON metadata", () => {
  assert.throws(
    () => defineResourceRef({ id: "1", type: "database", title: "数据", source: "test", metadata: {} }),
    (error) => error instanceof WorkbenchContractError && error.field === "resource.type"
  );
  assert.throws(
    () => defineResourceRef({ id: "1", type: "file", title: "数据", source: "test", metadata: { callback() {} } }),
    (error) => error instanceof WorkbenchContractError && error.field === "resource.metadata.callback"
  );
  assert.equal(isResourceRef({}), false);
});

test("defineCommand validates namespaced permissions and immutable schemas", () => {
  const command = defineCommand({
    id: "com.example.research.generate-report",
    title: "生成报告",
    risk: "write",
    requiredPermissions: ["ai.invoke", "files.read", "ai.invoke"],
    contexts: ["research.project"],
    inputSchema: {
      type: "object",
      properties: { project_id: { type: "string" } },
      required: ["project_id"]
    }
  });

  assert.deepEqual(command.requiredPermissions, ["ai.invoke", "files.read"]);
  assert.equal(command.risk, "write");
  assert.equal(Object.isFrozen(command), true);
  assert.equal(Object.isFrozen(command.inputSchema.properties), true);
  assert.equal(isCommandDefinition(command), true);
});

test("defineCommand rejects unnamespaced IDs, invalid permissions, and risks", () => {
  assert.throws(
    () => defineCommand({ id: "generate-report", title: "生成报告" }),
    (error) => error instanceof WorkbenchContractError && error.field === "command.id"
  );
  assert.throws(
    () => defineCommand({ id: "research.generate-report", title: "生成报告", requiredPermissions: ["AI Invoke"] }),
    (error) => error instanceof WorkbenchContractError && error.field === "command.requiredPermissions[0]"
  );
  assert.throws(
    () => defineCommand({ id: "research.generate-report", title: "生成报告", risk: "dangerous" }),
    (error) => error instanceof WorkbenchContractError && error.field === "command.risk"
  );
  assert.equal(isCommandDefinition({ id: "research.generate", title: "生成", risk: "read" }), true);
});

function service(methods) {
  return Object.fromEntries(methods.map((method) => [method, () => undefined]));
}

function validPluginContext() {
  return {
    plugin: { id: "com.example.research-workbench", version: "1.0.0" },
    scope: { organizationId: "org_01", workspaceId: "workspace_01", userId: "user_01", roles: ["member"] },
    permissions: service(["has", "require"]),
    commands: service(["register", "execute"]),
    dialog: service(["confirm", "form", "choice", "open"]),
    notifications: service(["show"]),
    resources: service(["listRelated", "preview", "open"]),
    tasks: service(["list"]),
    artifacts: service(["list"]),
    http: service(["request"])
  };
}

test("definePluginContext freezes identity and preserves runtime services", () => {
  const input = validPluginContext();
  input.http = new class ScopedHttpService {
    request() {}
  }();
  const context = definePluginContext(input);

  assert.equal(context.plugin.id, "com.example.research-workbench");
  assert.deepEqual(context.scope.roles, ["member"]);
  assert.equal(context.commands, input.commands);
  assert.equal(context.tasks, input.tasks);
  assert.equal(context.artifacts, input.artifacts);
  assert.equal(context.http, input.http);
  assert.equal(Object.isFrozen(context), true);
  assert.equal(Object.isFrozen(context.plugin), true);
  assert.equal(Object.isFrozen(context.scope), true);
});

test("definePluginContext rejects missing methods and invalid versions", () => {
  const missingMethod = validPluginContext();
  delete missingMethod.dialog.form;
  assert.throws(
    () => definePluginContext(missingMethod),
    (error) => error instanceof WorkbenchContractError && error.field === "context.dialog.form"
  );

  const invalidVersion = validPluginContext();
  invalidVersion.plugin.version = "next";
  assert.throws(
    () => definePluginContext(invalidVersion),
    (error) => error instanceof WorkbenchContractError && error.field === "context.plugin.version"
  );
});
