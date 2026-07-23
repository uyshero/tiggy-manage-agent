import assert from "node:assert/strict";
import test from "node:test";

import {
  agent,
  agentConfigVersions,
  agentToolingHealth,
  agents,
  approveIntervention,
  archiveMarketplacePolicy,
  archiveSession,
  archiveSkill,
  archiveSkillAssetRetentionPolicy,
  artifactDownloadPath,
  artifacts,
  compareSessions,
  createAgent,
  createEnvironment,
  createLLMProvider,
  createMarketplaceEntry,
  createMarketplacePolicy,
  createMCPServer,
  createSkillAssetRetentionPolicy,
  currentPrincipal,
  createSession,
  defaultAgent,
  deleteEnvironmentVariable,
  deleteLLMProvider,
  deleteMCPServer,
  deleteSession,
  deleteLLMModel,
  discoverInternalSkillsMarketplace,
  discoverSkillsMarketplace,
  disableSkill,
  downloadArtifact,
  environmentVariables,
  evaluateWorkspaceToolPermission,
  events,
  enableSkill,
  exportAgent,
  importAgent,
  interventions,
  interruptSession,
  llmModels,
  llmProviders,
  marketplaceEntries,
  marketplaceEntry,
  marketplacePolicies,
  marketplacePolicy,
  mcpServerRuntimeStatus,
  mcpServers,
  mcpServerVersions,
  observabilityStatus,
  objectRefDownloadPath,
  previewInternalSkillsMarketplace,
  previewSkillAssetGC,
  previewSkillsMarketplace,
  publishMarketplacePolicyVersion,
  publishSkillAssetRetentionPolicyVersion,
  rejectIntervention,
  retryObservability,
  rerunSession,
  restoreSession,
  restoreMCPServerVersion,
  rollbackAgentConfigVersion,
  runSkillAssetGC,
  session,
  sessionRuntimeCapabilities,
  sessionRuntimeConfig,
  sessionToolPermissionAudit,
  sessionTaskGroup,
  sessionTaskGroups,
  sendSessionMessage,
  sessions,
  steerSession,
  skillAssetGCRuns,
  skillAssetGCTombstones,
  skillAssetRetentionEffective,
  skillAssetRetentionPolicies,
  skills,
  skillVersions,
  skillPackageDownloadPath,
  summary,
  streamSessionEvents,
  streamSessionLiveEvents,
  taskPlan,
  taskGroupTemplates,
  taskTemplates,
  testLLMModel,
  testLLMProvider,
  testMCPServer,
  transitionMarketplaceEntry,
  trace,
  traceByID,
  traceCatalog,
  tracePath,
  spanByID,
  spanCatalog,
  uploadSessionArtifact,
  usage,
  installInternalSkillsMarketplace,
  installSkillsMarketplace,
  putEnvironmentVariable,
  setLLMProviderEnabled,
  setMCPServerEnabled,
  updateLLMProvider,
  updateMarketplaceEntry,
  updateMCPServer,
  updateSessionMetadata,
  updateSessionRuntimeSettings,
  upgradeSessionConfig,
  updateAgent,
  updateWorkspaceToolPermissions,
  workspaceToolPermissions,
  upsertLLMModel
} from "./api.js";

function response(body = {}) {
  return {
    ok: true,
    json: async () => body
  };
}

test("LLM control-plane writes use typed v2 SDK methods and exact revision headers", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    requests.push({ path: String(path), options });
    if (options.method === "DELETE") return new Response(null, { status: 204 });
    return response({ revision: requests.length, enabled: true });
  };
  const controller = new AbortController();
  try {
    await createLLMProvider({ id: "provider/1", provider_type: "fake" }, { signal: controller.signal });
    await updateLLMProvider("provider/1", 2, { base_url: "https://llm.example/v1" }, { signal: controller.signal });
    await setLLMProviderEnabled("provider/1", 3, false, { signal: controller.signal });
    await setLLMProviderEnabled("provider/1", 4, true, { signal: controller.signal });
    await deleteLLMProvider("provider/1", 5, { signal: controller.signal });
    await upsertLLMModel({ provider_id: "provider/1", model: "new-model" }, undefined, { signal: controller.signal });
    await upsertLLMModel({ provider_id: "provider/1", model: "existing-model" }, 7, { signal: controller.signal });
    await deleteLLMModel("provider/1", "model name", 9, { signal: controller.signal });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map((request) => request.path), [
    "http://localhost/v2/llm-providers",
    "http://localhost/v2/llm-providers/provider%2F1",
    "http://localhost/v2/llm-providers/provider%2F1/disable",
    "http://localhost/v2/llm-providers/provider%2F1/enable",
    "http://localhost/v2/llm-providers/provider%2F1",
    "http://localhost/v2/llm-models",
    "http://localhost/v2/llm-models",
    "http://localhost/v2/llm-models/provider%2F1/model%20name"
  ]);
  assert.deepEqual(requests.map((request) => request.options.method), [
    "POST", "PATCH", "POST", "POST", "DELETE", "POST", "POST", "DELETE"
  ]);
  assert.deepEqual(requests.map((request) => new Headers(request.options.headers).get("If-Match")), [
    null, '"2"', '"3"', '"4"', '"5"', null, '"7"', '"9"'
  ]);
  assert.deepEqual(requests.map((request) => new Headers(request.options.headers).get("If-None-Match")), [
    null, null, null, null, null, "*", null, null
  ]);
  assert.ok(requests.every((request) => request.options.signal === controller.signal));
});

test("LLM diagnostic operations use typed v2 SDK methods and encode identifiers", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return response({ status: "succeeded", latency_ms: 1, authenticated: true, message: "ok", retryable: false, checked_at: "2026-07-15T00:00:00Z" });
  };
  try {
    await testLLMProvider("provider/id");
    await testLLMModel("provider/id", "model name");
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.deepEqual(requests.map((request) => request.path), [
    "http://localhost/v2/llm-providers/provider%2Fid/test",
    "http://localhost/v2/llm-models/provider%2Fid/model%20name/test"
  ]);
  assert.ok(requests.every((request) => request.options.method === "POST"));
  assert.deepEqual(requests.map((request) => JSON.parse(request.options.body)), [{}, {}]);
});

test("read-only Auth and Session operations use the v2 Core SDK", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path) => {
    requests.push(String(path));
    if (String(path).endsWith("/v2/auth/me")) return response({ authenticated: true, principal: { subject: "user_1" } });
    if (String(path).includes("/v2/sessions?")) return response({ sessions: [{ id: "session/1", status: "active" }] });
    return response({ id: "session/1", status: "active" });
  };
  try {
    const auth = await currentPrincipal();
    const list = await sessions({ limit: 5, workspace: "workspace/1", status: "active", includeArchived: true });
    const detail = await session("session/1");
    assert.equal(auth.principal.subject, "user_1");
    assert.equal(list.sessions[0].id, "session/1");
    assert.equal(detail.id, "session/1");
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests, [
    "http://localhost/v2/auth/me",
    "http://localhost/v2/sessions?workspace_id=workspace%2F1&status=active&include_archived=true&limit=5",
    "http://localhost/v2/sessions/session%2F1"
  ]);
});

test("read-only Agent and LLM catalogs use the v2 Core SDK without changing response shapes", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path) => {
    const url = String(path);
    requests.push(url);
    if (url.endsWith("/v2/agents")) return response({ agents: [{ id: "agent/1", name: "Agent" }] });
    if (url.endsWith("/v2/agents/default")) return response({ id: "agent/default", name: "Default" });
    if (url.includes("/v2/agents/")) return response({ id: "agent/1", name: "Agent" });
    if (url.endsWith("/v2/llm-providers")) return response({ providers: [{ id: "provider/1", enabled: true }] });
    if (url.includes("/v2/llm-models")) return response({ models: [{ provider_id: "provider/1", model: "model 1" }] });
    return response();
  };
  try {
    const agentList = await agents();
    const defaultValue = await defaultAgent();
    const detail = await agent("agent/1");
    const providers = await llmProviders();
    const models = await llmModels("provider/1");
    assert.equal(agentList.agents[0].id, "agent/1");
    assert.equal(defaultValue.id, "agent/default");
    assert.equal(detail.id, "agent/1");
    assert.equal(providers.providers[0].id, "provider/1");
    assert.equal(models.models[0].model, "model 1");
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests, [
    "http://localhost/v2/agents",
    "http://localhost/v2/agents/default",
    "http://localhost/v2/agents/agent%2F1",
    "http://localhost/v2/llm-providers",
    "http://localhost/v2/llm-models?provider_id=provider%2F1"
  ]);
});

test("legacy task templates remain on v1", async () => {
  let requestedPath = "";
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path) => {
    requestedPath = String(path);
    return response({ templates: [] });
  };
  try {
    await taskTemplates();
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.equal(requestedPath, "/v1/task-templates");
});

test("read-only MCP, Skills, environment, and observability queries use v2 SDK services", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path) => {
    const url = String(path);
    requests.push(url);
    if (url.includes("/v2/mcp-servers/runtime-status")) return response({ checked_at: "2026-07-15T00:00:00Z", states: [] });
    if (url.includes("/v2/mcp-servers")) return response({ servers: [{ id: "mcp/1", status: "active" }] });
    if (url.includes("/v2/environment-variables")) return response({ variables: [{ name: "API_KEY", configured: true }] });
    if (url.endsWith("/versions")) return response({ versions: [{ id: "version/1", version: 1 }] });
    if (url.includes("/v2/skills")) return response({ skills: [{ id: "skill/1", identifier: "review" }] });
    return response({ enabled: true, exporter: { status: "ready" } });
  };
  try {
    const servers = await mcpServers("workspace/1");
    const runtime = await mcpServerRuntimeStatus("workspace/1");
    const variables = await environmentVariables("workspace/1");
    const skillList = await skills({ workspaceId: "workspace/1", includeArchived: true });
    const versions = await skillVersions("skill/1");
    const observability = await observabilityStatus();
    assert.equal(servers.servers[0].id, "mcp/1");
    assert.equal(runtime.states.length, 0);
    assert.deepEqual(variables.variables[0], { name: "API_KEY", configured: true });
    assert.equal(skillList.skills[0].id, "skill/1");
    assert.equal(versions.versions[0].version, 1);
    assert.equal(observability.enabled, true);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests, [
    "http://localhost/v2/mcp-servers?workspace_id=workspace%2F1",
    "http://localhost/v2/mcp-servers/runtime-status?workspace_id=workspace%2F1",
    "http://localhost/v2/environment-variables?workspace_id=workspace%2F1",
    "http://localhost/v2/skills?workspace_id=workspace%2F1&include_archived=true",
    "http://localhost/v2/skills/skill%2F1/versions",
    "http://localhost/v2/observability/status"
  ]);
});

test("MCP lifecycle and environment variable writes use typed v2 SDK services", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    requests.push({ path: String(path), options });
    if (options.method === "DELETE" && String(path).includes("/environment-variables/")) {
      return new Response(null, { status: 204 });
    }
    if (String(path).endsWith("/test")) {
      return response({ status: "online", tools: [{ name: "extension_tool" }] });
    }
    if (String(path).endsWith("/restore")) {
      return response({ server: { id: "server/1", status: "active" }, restored_from_version: 3, new_version: 4 });
    }
    if (String(path).includes("/environment-variables/")) {
      return response({ name: "API/KEY", configured: true });
    }
    return response({ id: "server/1", status: "active", config: { extension: { preserved: true } } });
  };
  const controller = new AbortController();
  const config = {
    transport: "stdio",
    command: "fixture",
    extension: { preserved: true }
  };
  try {
    await createMCPServer({ workspace_id: "workspace/1", identifier: "fixture", name: "Fixture", config }, { signal: controller.signal });
    await updateMCPServer("server/1", { name: "Updated", config }, { signal: controller.signal });
    await setMCPServerEnabled("server/1", false, { signal: controller.signal });
    await setMCPServerEnabled("server/1", true, { signal: controller.signal });
    await testMCPServer("server/1", { signal: controller.signal });
    await deleteMCPServer("server/1", { signal: controller.signal });
    await restoreMCPServerVersion("server/1", 3, { signal: controller.signal });
    await putEnvironmentVariable("API/KEY", "secret", "workspace/1", { signal: controller.signal });
    await deleteEnvironmentVariable("API/KEY", "workspace/1", { signal: controller.signal });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map((request) => request.path), [
    "http://localhost/v2/mcp-servers",
    "http://localhost/v2/mcp-servers/server%2F1",
    "http://localhost/v2/mcp-servers/server%2F1/disable",
    "http://localhost/v2/mcp-servers/server%2F1/enable",
    "http://localhost/v2/mcp-servers/server%2F1/test",
    "http://localhost/v2/mcp-servers/server%2F1",
    "http://localhost/v2/mcp-servers/server%2F1/versions/3/restore",
    "http://localhost/v2/environment-variables/API%2FKEY?workspace_id=workspace%2F1",
    "http://localhost/v2/environment-variables/API%2FKEY?workspace_id=workspace%2F1"
  ]);
  assert.deepEqual(requests.map((request) => request.options.method), [
    "POST", "PATCH", "POST", "POST", "POST", "DELETE", "POST", "PUT", "DELETE"
  ]);
  assert.deepEqual(JSON.parse(requests[0].options.body), {
    workspace_id: "workspace/1",
    identifier: "fixture",
    name: "Fixture",
    config
  });
  assert.deepEqual(JSON.parse(requests[1].options.body), { name: "Updated", config });
  assert.deepEqual(JSON.parse(requests[7].options.body), { value: "secret" });
  assert.ok(requests.every((request) => request.options.signal === controller.signal));
});

test("read-only version, runtime, and Marketplace detail queries use v2 SDK services", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path) => {
    const url = String(path);
    requests.push(url);
    if (url.includes("/v2/agents/")) return response({ config_versions: [{ version: 3 }] });
    if (url.includes("/v2/mcp-servers/")) return response({ versions: [{ version: 2 }] });
    if (url.endsWith("/runtime-config")) return response({ session_id: "session/1", tools: { extension: { preserved: true } } });
    if (url.endsWith("/runtime-capabilities")) return response({ default_runtime: "cloud", available_runtimes: ["cloud"] });
    if (url.includes("/v2/skill-marketplace-entries?")) return response({ entries: [{ id: "entry/1", status: "draft" }] });
    if (url.includes("/v2/skill-marketplace-entries/")) return response({ id: "entry/1", status: "draft" });
    if (url.includes("/v2/skill-marketplace-policies?")) return response({ policies: [{ id: "policy/1", status: "active" }] });
    return response({ policy: { id: "policy/1", status: "active" }, version: { version: 1, config: { allowed_owners: ["acme"] } } });
  };
  try {
    const agentVersions = await agentConfigVersions("agent/1");
    const mcpVersions = await mcpServerVersions("mcp/1");
    const runtimeConfig = await sessionRuntimeConfig("session/1");
    const runtimeCapabilities = await sessionRuntimeCapabilities("session/1");
    const entries = await marketplaceEntries({ workspaceId: "workspace/1", status: "draft", includeWithdrawn: true });
    const entry = await marketplaceEntry("entry/1", "workspace/1");
    const policies = await marketplacePolicies({ organizationId: "org/1", workspaceId: "workspace/1", includeArchived: true });
    const policy = await marketplacePolicy("policy/1");
    assert.equal(agentVersions.config_versions[0].version, 3);
    assert.equal(mcpVersions.versions[0].version, 2);
    assert.deepEqual(runtimeConfig.tools, { extension: { preserved: true } });
    assert.equal(runtimeCapabilities.default_runtime, "cloud");
    assert.equal(entries.entries[0].id, "entry/1");
    assert.equal(entry.id, "entry/1");
    assert.equal(policies.policies[0].id, "policy/1");
    assert.equal(policy.version.version, 1);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests, [
    "http://localhost/v2/agents/agent%2F1/config-versions",
    "http://localhost/v2/mcp-servers/mcp%2F1/versions",
    "http://localhost/v2/sessions/session%2F1/runtime-config",
    "http://localhost/v2/sessions/session%2F1/runtime-capabilities",
    "http://localhost/v2/skill-marketplace-entries?workspace_id=workspace%2F1&status=draft&include_withdrawn=true",
    "http://localhost/v2/skill-marketplace-entries/entry%2F1?workspace_id=workspace%2F1",
    "http://localhost/v2/skill-marketplace-policies?organization_id=org%2F1&workspace_id=workspace%2F1&include_archived=true",
    "http://localhost/v2/skill-marketplace-policies/policy%2F1"
  ]);
});

test("Session events, Artifact reads/downloads, and Interventions use v2 SDK services", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    const url = String(path);
    requests.push({ url, options });
    if (url.includes("/events")) return response({ events: [{ id: "event/1", type: "future.event" }] });
    if (url.endsWith("/artifacts")) return response({ artifacts: [{ id: "artifact/1", metadata: { extension: { preserved: true } } }] });
    if (url.endsWith("/download")) return { ok: true, headers: new Headers({ "content-type": "text/plain" }) };
    return response({ interventions: [{ call_id: "call/1", status: "pending" }] });
  };
  try {
    const controller = new AbortController();
    const eventList = await events("session/1", 4);
    const artifactList = await artifacts("session/1");
    const download = await downloadArtifact("session/1", "artifact/1", { signal: controller.signal });
    const interventionList = await interventions("session/1", "pending");
    assert.equal(eventList.events[0].type, "future.event");
    assert.deepEqual(artifactList.artifacts[0].metadata, { extension: { preserved: true } });
    assert.equal(download.headers.get("content-type"), "text/plain");
    assert.equal(interventionList.interventions[0].call_id, "call/1");
    assert.equal(requests[2].options.signal, controller.signal);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map((request) => request.url), [
    "http://localhost/v2/sessions/session%2F1/events?after_seq=4",
    "http://localhost/v2/sessions/session%2F1/artifacts",
    "http://localhost/v2/sessions/session%2F1/artifacts/artifact%2F1/download",
    "http://localhost/v2/sessions/session%2F1/interventions?status=pending"
  ]);
  assert.equal(artifactDownloadPath("session/1", "artifact/1"), "/v2/sessions/session%2F1/artifacts/artifact%2F1/download");
});

test("Session event stream uses the v2 SDK with after_seq and AbortSignal", async () => {
  let request;
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    request = { path: String(path), options };
    return new Response(
      'data: {"id":"event/5","session_id":"session/1","seq":5,"type":"future.event","created_at":"2026-07-15T00:00:00Z"}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    );
  };
  const controller = new AbortController();
  try {
    const stream = streamSessionEvents("session/1", { afterSeq: 4, signal: controller.signal });
    const result = await stream.next();
    assert.equal(result.value.type, "future.event");
    await stream.return();
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.equal(request.path, "http://localhost/v2/sessions/session%2F1/events/stream?after_seq=4");
  assert.equal(request.options.signal, controller.signal);
  assert.equal(new Headers(request.options.headers).get("Accept"), "text/event-stream");
});

test("Session live stream uses the transient v2 SDK endpoint without after_seq", async () => {
  let request;
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    request = { path: String(path), options };
    return new Response(
      'data: {"stream_seq":1,"session_id":"session/1","turn_id":"turn/1","type":"llm.text","operation":"append","content_format":"markdown","text":"hello","created_at":"2026-07-15T00:00:00Z"}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    );
  };
  const controller = new AbortController();
  try {
    const stream = streamSessionLiveEvents("session/1", { signal: controller.signal });
    const result = await stream.next();
    assert.equal(result.value.text, "hello");
    await stream.return();
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.equal(request.path, "http://localhost/v2/sessions/session%2F1/live/stream");
  assert.equal(request.options.signal, controller.signal);
});

test("current Session task Plan treats 404 as an empty snapshot", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () => new Response(JSON.stringify({ error: "task plan not found" }), {
    status: 404,
    headers: { "content-type": "application/json" }
  });
  try {
    assert.deepEqual(await taskPlan("session/1"), { plan: null });
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("Session interrupt cancels the newest active Run through the v2 SDK", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    requests.push({ path: String(path), options });
    if (options.method === "GET") {
      return response({ runs: [
        { id: "run/completed", session_id: "session/1", status: "completed" },
        { id: "run/active", session_id: "session/1", status: "waiting_approval" }
      ] });
    }
    return response({ id: "run/active", session_id: "session/1", status: "interrupted" });
  };
  const controller = new AbortController();
  try {
    const result = await interruptSession("session/1", { signal: controller.signal });
    assert.equal(result.run.status, "interrupted");
    assert.deepEqual(result.events, []);
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.deepEqual(requests.map((request) => `${request.options.method} ${request.path}`), [
    "GET http://localhost/v2/sessions/session%2F1/runs",
    "POST http://localhost/v2/sessions/session%2F1/runs/run%2Factive/cancel"
  ]);
  assert.ok(requests.every((request) => request.options.signal === controller.signal));
});

test("Session interrupt rejects when no active Run exists", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () => response({ runs: [{ id: "run/1", status: "future_terminal" }] });
  try {
    await assert.rejects(() => interruptSession("session/1"), /no active Run/);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("Session messages start Runs while preserving the legacy busy queue", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    const url = String(path);
    requests.push({ path: url, options });
    if (url.includes("session%2Fconflict/runs")) {
      return new Response(JSON.stringify({
        error: { code: "session_busy", message: "Session is busy", request_id: "req_1", retryable: false }
      }), { status: 409, headers: { "Content-Type": "application/json" } });
    }
    if (url.endsWith("/events")) {
      return response({ events: [{ id: "queued/event", seq: 9, type: "user.message" }] });
    }
    return response({
      run: { id: "run/1", session_id: "session/1", status: "running", user_event_seq: 2, attempt: 1, started_at: "2026-07-15T00:00:00Z" },
      events: [
        { id: "event/1", session_id: "session/1", turn_id: "run/1", seq: 1, type: "session.status_running", created_at: "2026-07-15T00:00:00Z" },
        { id: "event/2", session_id: "session/1", turn_id: "run/1", seq: 2, type: "user.message", created_at: "2026-07-15T00:00:00Z" }
      ],
      created: true
    });
  };
  try {
    const started = await sendSessionMessage("session/1", "Analyze", {
      attachments: [{ artifact_id: "artifact/1" }],
      idempotencyKey: "message/1"
    });
    const queued = await sendSessionMessage("session/queued", "Next", { queued: true, attachments: [] });
    const raced = await sendSessionMessage("session/conflict", "Later", { queueIfBusy: true, attachments: [] });
    assert.equal(started.run.id, "run/1");
    assert.equal(started.events[1].type, "user.message");
    assert.equal(queued.events[0].id, "queued/event");
    assert.equal(raced.events[0].id, "queued/event");
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.deepEqual(requests.map((request) => `${request.options.method} ${request.path}`), [
    "POST http://localhost/v2/sessions/session%2F1/runs",
    "POST http://localhost/v2/sessions/session%2Fqueued/events",
    "POST http://localhost/v2/sessions/session%2Fconflict/runs",
    "POST http://localhost/v2/sessions/session%2Fconflict/events"
  ]);
  assert.deepEqual(JSON.parse(requests[0].options.body), {
    input: {
      content: [{ type: "text", text: "Analyze" }],
      attachments: [{ artifact_id: "artifact/1" }]
    },
    idempotency_key: "message/1"
  });
});

test("Session steering appends a text control event to the active Run", async () => {
  const requests = [];
  const controller = new AbortController();
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    requests.push({ path: String(path), options });
    return response({
      events: [{
        id: "event/steer",
        session_id: "session/1",
        turn_id: "run/1",
        seq: 8,
        type: "user.steer",
        payload: { content: [{ type: "text", text: "先检查测试失败原因" }] }
      }]
    });
  };
  try {
    const result = await steerSession("session/1", "先检查测试失败原因", { signal: controller.signal });
    assert.equal(result.events[0].type, "user.steer");
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.equal(requests.length, 1);
  assert.equal(requests[0].path, "http://localhost/v2/sessions/session%2F1/events");
  assert.equal(requests[0].options.method, "POST");
  assert.equal(requests[0].options.signal, controller.signal);
  assert.deepEqual(JSON.parse(requests[0].options.body), {
    events: [{
      type: "user.steer",
      payload: { content: [{ type: "text", text: "先检查测试失败原因" }] }
    }]
  });
});

test("Intervention decisions use typed v2 SDK methods without changing response shape", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    requests.push({ path: String(path), options });
    const decision = String(path).endsWith("/approve") ? "approved" : "rejected";
    return response({
      intervention: { session_id: "session/1", turn_id: "turn/1", call_id: "call/1", status: decision },
      events: [{ id: `event/${decision}`, type: `runtime.tool_intervention_${decision}`, seq: requests.length }]
    });
  };
  try {
    const controller = new AbortController();
    const approved = await approveIntervention("session/1", "turn/1", "call/1", {
      reason: "approved from app",
      response: { permission_rule_suggestion_id: "suggest-agent-123" }
    }, { signal: controller.signal });
    const rejected = await rejectIntervention("session/1", "turn/1", "call/1", { reason: "unsafe operation" }, { signal: controller.signal });
    assert.equal(approved.intervention.status, "approved");
    assert.equal(approved.events[0].seq, 1);
    assert.equal(rejected.intervention.status, "rejected");
    assert.equal(rejected.events[0].seq, 2);
    assert.equal(requests.every((request) => request.options.signal === controller.signal), true);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map((request) => request.path), [
    "http://localhost/v2/sessions/session%2F1/interventions/turn%2F1/call%2F1/approve",
    "http://localhost/v2/sessions/session%2F1/interventions/turn%2F1/call%2F1/reject"
  ]);
  assert.deepEqual(requests.map((request) => JSON.parse(request.options.body)), [
    { reason: "approved from app", response: { permission_rule_suggestion_id: "suggest-agent-123" } },
    { reason: "unsafe operation" }
  ]);
});

test("Artifact upload uses the typed v2 SDK multipart transport", async () => {
  let request;
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    request = { path: String(path), options };
    return response({
      object_ref: { id: "object/1", content_type: "text/plain", size_bytes: 13 },
      artifact: { id: "artifact/1", name: "report.txt", artifact_type: "file" },
      workspace_path: "/mnt/data/uploads/artifact_1/report.txt"
    });
  };
  try {
    const file = new Blob(["artifact body"], { type: "text/plain" });
    Object.defineProperty(file, "name", { value: "report.txt" });
    const controller = new AbortController();
    const upload = await uploadSessionArtifact("session/1", file, { description: "User report", signal: controller.signal });
    assert.equal(upload.artifact.id, "artifact/1");
    assert.equal(upload.object_ref.id, "object/1");
    assert.equal(upload.workspace_path, "/mnt/data/uploads/artifact_1/report.txt");
    assert.equal(request.options.signal, controller.signal);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(request.path, "http://localhost/v2/sessions/session%2F1/artifacts/upload");
  assert.equal(request.options.method, "POST");
  assert.equal(request.options.headers instanceof Headers ? request.options.headers.has("Content-Type") : Boolean(request.options.headers?.["Content-Type"]), false);
  assert.equal(request.options.body.get("name"), "report.txt");
  assert.equal(request.options.body.get("artifact_type"), "file");
  assert.equal(request.options.body.get("description"), "User report");
  assert.equal(request.options.body.get("metadata"), JSON.stringify({ source: "user_upload" }));
  assert.equal(request.options.body.get("file").name, "report.txt");
  assert.equal(request.options.body.get("file").type, "text/plain");
});

test("native ObjectRef and Skill package links target encoded v2 resources", () => {
  assert.equal(
    objectRefDownloadPath("object/ref", "session/1"),
    "/v2/object-refs/object%2Fref/download?session_id=session%2F1"
  );
  assert.equal(objectRefDownloadPath("object/ref", ""), "/v2/object-refs/object%2Fref/download");
  assert.equal(
    skillPackageDownloadPath("skill/ref", "3/latest"),
    "/v2/skills/skill%2Fref/versions/3%2Flatest/package"
  );
});

test("Session reports, comparison, and Orchestration reads use typed v2 SDK services", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    const url = String(path);
    requests.push({ url, options });
    if (url.endsWith("/usage")) return response({ session_id: "session/1", summary: { total_tokens: 12 }, records: [] });
    if (url.endsWith("/summary")) return response({ session_id: "session/1", summary_text: "Summary", source_until_seq: 7 });
    if (url.includes("/session-comparisons?")) return response({ left_session_id: "left/1", right_session_id: "right/1", differences: [] });
    if (url.endsWith("/agent/task-group-templates")) return response({ templates: [{ id: "review" }] });
    if (url.endsWith("/task-groups")) return response({ task_groups: [{ id: "group/1", strategy: "parallel" }] });
    return response({ id: "group/1", strategy: "parallel" });
  };
  try {
    const controller = new AbortController();
    const usageValue = await usage("session/1", { signal: controller.signal });
    const summaryValue = await summary("session/1", { signal: controller.signal });
    const comparison = await compareSessions("left/1", "right/1", { signal: controller.signal });
    const templates = await taskGroupTemplates({ signal: controller.signal });
    const groups = await sessionTaskGroups("session/1", { signal: controller.signal });
    const group = await sessionTaskGroup("session/1", "group/1", { signal: controller.signal });
    assert.equal(usageValue.summary.total_tokens, 12);
    assert.equal(summaryValue.source_until_seq, 7);
    assert.equal(comparison.right_session_id, "right/1");
    assert.equal(templates.templates[0].id, "review");
    assert.equal(groups.task_groups[0].id, "group/1");
    assert.equal(group.id, "group/1");
    assert.equal(requests.every((request) => request.options.signal === controller.signal), true);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map((request) => request.url), [
    "http://localhost/v2/sessions/session%2F1/usage",
    "http://localhost/v2/sessions/session%2F1/summary",
    "http://localhost/v2/session-comparisons?left_session_id=left%2F1&right_session_id=right%2F1",
    "http://localhost/v2/agent/task-group-templates",
    "http://localhost/v2/sessions/session%2F1/task-groups",
    "http://localhost/v2/sessions/session%2F1/task-groups/group%2F1"
  ]);
});

test("Trace and Span helpers use typed v2 SDK cursor pagination", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    const url = String(path);
    requests.push({ path: url, options });
    if (url.includes("/v2/traces?") || url.includes("/v2/spans?")) {
      return response({ items: [{ id: "item/1" }], next_cursor: "next/cursor", has_more: true });
    }
    return response({ trace_id: "trace/1", spans: [] });
  };
  const controller = new AbortController();
  const options = { signal: controller.signal };
  try {
    const traces = await traceCatalog({
      workspace: "workspace/1",
      session: "session/1",
      turn: "turn/1",
      sessionStatus: "active",
      includeArchived: true,
      limit: 10,
      cursor: "trace/cursor"
    }, options);
    const spans = await spanCatalog({
      workspace: "workspace/1",
      trace: "trace/1",
      session: "session/1",
      turn: "turn/1",
      kind: "llm",
      status: "error",
      query: "provider/model",
      critical: false,
      minDuration: 2,
      maxDuration: 20,
      minSelfDuration: 1,
      includeArchived: true,
      limit: 5,
      cursor: "span/cursor"
    }, options);
    await traceByID("trace/1", options);
    await spanByID("trace/1", "span/1", options);
    await trace("session/1", "turn/1", "", options);
    await trace("session/1", "turn/1", "perfetto", options);
    assert.deepEqual(traces, { traces: [{ id: "item/1" }], next_cursor: "next/cursor", has_more: true });
    assert.deepEqual(spans, { spans: [{ id: "item/1" }], next_cursor: "next/cursor", has_more: true });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map((request) => request.path), [
    "http://localhost/v2/traces?workspace_id=workspace%2F1&session_id=session%2F1&turn_id=turn%2F1&session_status=active&include_archived=true&limit=10&cursor=trace%2Fcursor",
    "http://localhost/v2/spans?workspace_id=workspace%2F1&trace_id=trace%2F1&session_id=session%2F1&turn_id=turn%2F1&kind=llm&status=error&q=provider%2Fmodel&critical=false&min_duration_ms=2&max_duration_ms=20&min_self_duration_ms=1&include_archived=true&limit=5&cursor=span%2Fcursor",
    "http://localhost/v2/traces/trace%2F1",
    "http://localhost/v2/traces/trace%2F1/spans/span%2F1",
    "http://localhost/v2/sessions/session%2F1/trace?turn_id=turn%2F1",
    "http://localhost/v2/sessions/session%2F1/trace?turn_id=turn%2F1&format=perfetto"
  ]);
  assert.ok(requests.every((request) => request.options.signal === controller.signal));
  assert.equal(tracePath("session/1", "turn/1", "perfetto"), "/v2/sessions/session%2F1/trace?turn_id=turn%2F1&format=perfetto");
});

test("Marketplace discovery and Skill retention reads use typed v2 SDK services", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    const url = String(path);
    requests.push({ url, options });
    if (url.includes("/marketplace/discover?")) return response({ candidates: [{ identifier: "review", extension: { preserved: true } }] });
    if (url.includes("/marketplace/internal?")) return response({ candidates: [{ identifier: "internal-review", tags: ["qa", "secure"] }] });
    if (url.includes("/skill-asset-retention/effective?")) return response({ workspace_id: "workspace/1", source: "workspace", config: { max_age_days: 30 } });
    if (url.includes("/skill-asset-retention/policies?")) return response({ policies: [{ id: "policy/1", config: { extension: { preserved: true } } }] });
    if (url.includes("/skill-asset-gc/runs?")) return response({ runs: [{ id: "run/1", status: "future_status" }] });
    return response({ tombstones: [{ id: "tombstone/1", status: "retained" }] });
  };
  try {
    const controller = new AbortController();
    const discovered = await discoverSkillsMarketplace({
      sessionId: "session/1", query: "review skill", repository: "acme/skills", limit: 10
    }, { signal: controller.signal });
    const internal = await discoverInternalSkillsMarketplace({
      sessionId: "session/1", query: "internal", category: "quality", tags: ["qa", "secure"], limit: 20
    }, { signal: controller.signal });
    const effective = await skillAssetRetentionEffective("workspace/1", { signal: controller.signal });
    const policies = await skillAssetRetentionPolicies({
      organizationId: "org/1", workspaceId: "workspace/1", includeArchived: true
    }, { signal: controller.signal });
    const runs = await skillAssetGCRuns("workspace/1", 15, { signal: controller.signal });
    const tombstones = await skillAssetGCTombstones("workspace/1", 15, { signal: controller.signal });
    assert.deepEqual(discovered.candidates[0].extension, { preserved: true });
    assert.deepEqual(internal.candidates[0].tags, ["qa", "secure"]);
    assert.equal(effective.config.max_age_days, 30);
    assert.deepEqual(policies.policies[0].config, { extension: { preserved: true } });
    assert.equal(runs.runs[0].status, "future_status");
    assert.equal(tombstones.tombstones[0].id, "tombstone/1");
    assert.equal(requests.every((request) => request.options.signal === controller.signal), true);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map((request) => request.url), [
    "http://localhost/v2/skills/marketplace/discover?session_id=session%2F1&query=review+skill&repository=acme%2Fskills&limit=10",
    "http://localhost/v2/skills/marketplace/internal?session_id=session%2F1&query=internal&category=quality&tag=qa&tag=secure&limit=20",
    "http://localhost/v2/skill-asset-retention/effective?workspace_id=workspace%2F1",
    "http://localhost/v2/skill-asset-retention/policies?organization_id=org%2F1&workspace_id=workspace%2F1&include_archived=true",
    "http://localhost/v2/skill-asset-gc/runs?workspace_id=workspace%2F1&limit=15",
    "http://localhost/v2/skill-asset-gc/tombstones?workspace_id=workspace%2F1&limit=15"
  ]);
});

test("extension management writes use typed v2 SDK services", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    requests.push({ path: String(path), options });
    return response({ status: "active", extension: { preserved: true } });
  };
  const controller = new AbortController();
  const options = { signal: controller.signal };
  try {
    await createEnvironment({ name: "Sandbox", workspace_id: "workspace/1" }, options);
    await archiveSkill("skill/1", options);
    await previewSkillsMarketplace({ session_id: "session/1", source: { extension: true } }, options);
    await installSkillsMarketplace({ session_id: "session/1", candidate: { extension: true } }, options);
    await previewInternalSkillsMarketplace({ session_id: "session/1", candidate: { extension: true } }, options);
    await installInternalSkillsMarketplace({ session_id: "session/1", candidate: { extension: true } }, options);
    await enableSkill("skill/1", { session_id: "session/1", inputs: { dynamic: true } }, options);
    await disableSkill("skill/1", { session_id: "session/1" }, options);
    await createMarketplacePolicy({ name: "Policy", config: { extension: true } }, options);
    await publishMarketplacePolicyVersion("policy/1", { extension: true }, options);
    await archiveMarketplacePolicy("policy/1", options);
    await createMarketplaceEntry({ skill_id: "skill/1", metadata: { extension: true } }, options);
    await updateMarketplaceEntry("entry/1", { metadata: { extension: false } }, options);
    await transitionMarketplaceEntry("entry/1", "submit", { reason: "ready" }, options);
    await transitionMarketplaceEntry("entry/1", "publish", { reason: "approved" }, options);
    await transitionMarketplaceEntry("entry/1", "withdraw", { reason: "retired" }, options);
    await createSkillAssetRetentionPolicy({ name: "Retention", config: { extension: true } }, options);
    await publishSkillAssetRetentionPolicyVersion("retention/1", { extension: true }, options);
    await archiveSkillAssetRetentionPolicy("retention/1", options);
    await previewSkillAssetGC({ workspace_id: "workspace/1", limit: 10 }, options);
    await runSkillAssetGC({ workspace_id: "workspace/1", limit: 10 }, options);
    await retryObservability(options);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map((request) => request.path), [
    "http://localhost/v2/environments",
    "http://localhost/v2/skills/skill%2F1/archive",
    "http://localhost/v2/skills/marketplace/preview",
    "http://localhost/v2/skills/marketplace/install",
    "http://localhost/v2/skills/marketplace/internal/preview",
    "http://localhost/v2/skills/marketplace/internal/install",
    "http://localhost/v2/skills/skill%2F1/enable",
    "http://localhost/v2/skills/skill%2F1/disable",
    "http://localhost/v2/skill-marketplace-policies",
    "http://localhost/v2/skill-marketplace-policies/policy%2F1/versions",
    "http://localhost/v2/skill-marketplace-policies/policy%2F1/archive",
    "http://localhost/v2/skill-marketplace-entries",
    "http://localhost/v2/skill-marketplace-entries/entry%2F1",
    "http://localhost/v2/skill-marketplace-entries/entry%2F1/submit",
    "http://localhost/v2/skill-marketplace-entries/entry%2F1/publish",
    "http://localhost/v2/skill-marketplace-entries/entry%2F1/withdraw",
    "http://localhost/v2/skill-asset-retention/policies",
    "http://localhost/v2/skill-asset-retention/policies/retention%2F1/versions",
    "http://localhost/v2/skill-asset-retention/policies/retention%2F1/archive",
    "http://localhost/v2/skill-asset-gc/preview",
    "http://localhost/v2/skill-asset-gc/run",
    "http://localhost/v2/observability/retry"
  ]);
  assert.deepEqual(requests.map((request) => request.options.method), [
    "POST", "POST", "POST", "POST", "POST", "POST", "POST", "POST",
    "POST", "POST", "POST", "POST", "PATCH", "POST", "POST", "POST",
    "POST", "POST", "POST", "POST", "POST", "POST"
  ]);
  assert.deepEqual(JSON.parse(requests[6].options.body), {
    session_id: "session/1",
    inputs: { dynamic: true }
  });
  assert.deepEqual(JSON.parse(requests[9].options.body), { config: { extension: true } });
  assert.deepEqual(JSON.parse(requests[17].options.body), { config: { extension: true } });
  assert.ok(requests.every((request) => request.options.signal === controller.signal));
  assert.throws(
    () => transitionMarketplaceEntry("entry/1", "approve", {}, options),
    /unsupported Marketplace entry transition/
  );
});

test("Session lifecycle writes use typed v2 SDK services and preserve request bodies", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    const url = String(path);
    const body = options.body ? JSON.parse(options.body) : undefined;
    requests.push({ url, method: options.method, body, signal: options.signal, headers: new Headers(options.headers) });
    if (options.method === "DELETE") return { ok: true, status: 204 };
    if (url.endsWith("/rerun")) {
      return response({ source_session_id: "session/1", source_event_seq: 4, session: { id: "rerun/1" }, events: [] });
    }
    return response({ id: url.endsWith("/v2/sessions") ? "session/1" : "session/source", ...body });
  };
  try {
    const controller = new AbortController();
    const created = await createSession({ environment_id: "environment/1", agent_id: "agent/1", title: "Task" }, { signal: controller.signal });
    const archived = await archiveSession("session/source", { signal: controller.signal });
    const restored = await restoreSession("session/source", { signal: controller.signal });
    const rerun = await rerunSession("session/source", { title: "Rerun", message_seq: 4 }, { signal: controller.signal });
    const metadata = await updateSessionMetadata("session/source", { pinned: false, tags: ["qa", "sdk"] }, { signal: controller.signal });
    const runtime = await updateSessionRuntimeSettings("session/source", 7, {
      intervention_mode: "request_approval", tool_runtime: "cloud_sandbox", cloud_sandbox_allow_network: false,
      permission_rules: [{
		id: "session-src", tool: "default_edit_file", argument: "path",
        pattern: "/workspace/src/**", behavior: "allow"
      }]
    }, { signal: controller.signal });
    await upgradeSessionConfig("session/source", { to_version: 3, updated_by: "workbench" }, { signal: controller.signal });
    await deleteSession("session/source", { signal: controller.signal });
    assert.equal(created.id, "session/1");
    assert.equal(archived.id, "session/source");
    assert.equal(restored.id, "session/source");
    assert.equal(rerun.session.id, "rerun/1");
    assert.deepEqual(metadata.tags, ["qa", "sdk"]);
    assert.equal(runtime.cloud_sandbox_allow_network, false);
    assert.equal(requests.every((request) => request.signal === controller.signal), true);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map(({ method, url }) => `${method} ${url}`), [
    "POST http://localhost/v2/sessions",
    "POST http://localhost/v2/sessions/session%2Fsource/archive",
    "POST http://localhost/v2/sessions/session%2Fsource/restore",
    "POST http://localhost/v2/sessions/session%2Fsource/rerun",
    "PATCH http://localhost/v2/sessions/session%2Fsource",
    "PATCH http://localhost/v2/sessions/session%2Fsource/runtime-settings",
    "POST http://localhost/v2/sessions/session%2Fsource/config/upgrade",
    "DELETE http://localhost/v2/sessions/session%2Fsource"
  ]);
  assert.deepEqual(requests[0].body, { environment_id: "environment/1", agent_id: "agent/1", title: "Task" });
  assert.deepEqual(requests[1].body, {});
  assert.deepEqual(requests[2].body, {});
  assert.deepEqual(requests[3].body, { title: "Rerun", message_seq: 4 });
  assert.deepEqual(requests[4].body, { pinned: false, tags: ["qa", "sdk"] });
  assert.equal(requests[5].body.cloud_sandbox_allow_network, false);
  assert.equal(requests[5].headers.get("If-Match"), `"7"`);
  assert.deepEqual(requests[5].body.permission_rules, [{
	id: "session-src", tool: "default_edit_file", argument: "path",
    pattern: "/workspace/src/**", behavior: "allow"
  }]);
  assert.deepEqual(requests[6].body, { to_version: 3, updated_by: "workbench" });
  assert.equal(requests[7].body, undefined);
});

test("Agent lifecycle, portability, rollback, and tooling health use typed v2 SDK services", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    const url = String(path);
    const body = options.body ? JSON.parse(options.body) : undefined;
    requests.push({ url, method: options.method, body, signal: options.signal });
    if (url.endsWith("/export")) {
      return response({
        format: "tma.agent", schema_version: 1, exported_at: "2026-07-15T00:00:00Z",
        source_agent_id: "agent/1", agent: { name: "Review", system: "Review changes", tools: { extension: { preserved: true } } }
      });
    }
    if (url.endsWith("/rollback")) {
      return response({ agent: { id: "agent/1", name: "Review" }, previous_version: 4, source_version: 2, new_version: 5 });
    }
    if (url.endsWith("/tooling-health")) {
      return response({ agent_id: "agent/1", checked_at: "2026-07-15T00:00:00Z", mcp: [], skills: [], extension: { preserved: true } });
    }
    return response({ id: "agent/1", name: body?.name || body?.agent?.name || "Review", config_version: { tools: body?.tools || body?.agent?.tools || {} } });
  };
  try {
    const controller = new AbortController();
    const created = await createAgent({ name: "Review", system: "Review changes", tools: { custom: { enabled: true } } }, { signal: controller.signal });
    const updated = await updateAgent("agent/1", { name: "Review v2", tools: { extension: { preserved: true } } }, { signal: controller.signal });
    const exportedResponse = await exportAgent("agent/1", { signal: controller.signal });
    const exported = await exportedResponse.json();
    const imported = await importAgent({
      format: "tma.agent", schema_version: 1,
      agent: { name: "Imported", system: "Imported system", tools: { extension: { preserved: true } } }
    }, { signal: controller.signal });
    const rollback = await rollbackAgentConfigVersion("agent/1", 2, { signal: controller.signal });
    const health = await agentToolingHealth("agent/1", { kind: "mcp", identifier: "git" }, { signal: controller.signal });
    assert.equal(created.id, "agent/1");
    assert.deepEqual(updated.config_version.tools, { extension: { preserved: true } });
    assert.equal(exported.source_agent_id, "agent/1");
    assert.equal(exportedResponse.headers.get("Content-Disposition"), 'attachment; filename="agent-agent/1.json"');
    assert.equal(imported.name, "Imported");
    assert.equal(rollback.new_version, 5);
    assert.deepEqual(health.extension, { preserved: true });
    assert.equal(requests.every((request) => request.signal === controller.signal), true);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(requests.map(({ method, url }) => `${method} ${url}`), [
    "POST http://localhost/v2/agents",
    "PATCH http://localhost/v2/agents/agent%2F1",
    "GET http://localhost/v2/agents/agent%2F1/export",
    "POST http://localhost/v2/agents/import",
    "POST http://localhost/v2/agents/agent%2F1/config-versions/2/rollback",
    "POST http://localhost/v2/agents/agent%2F1/tooling-health"
  ]);
  assert.deepEqual(requests[0].body.tools, { custom: { enabled: true } });
  assert.deepEqual(requests[1].body.tools, { extension: { preserved: true } });
  assert.deepEqual(requests[3].body.agent.tools, { extension: { preserved: true } });
  assert.deepEqual(requests[4].body, {});
  assert.deepEqual(requests[5].body, { kind: "mcp", identifier: "git" });
});

test("Workspace tool permissions use the typed v2 SDK and encode workspace ids", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : undefined;
    requests.push({ url: String(path), method: options.method, body, headers: new Headers(options.headers) });
    return response({ workspace_id: "workspace/1", permission_rules: body?.permission_rules || [], revision: body ? 4 : 3, updated_by: "operator", updated_at: "2026-07-21T00:00:00Z" });
  };
  const rules = [{ id: "deny-secrets", tool: "default_edit_file", argument: "path", pattern: "/workspace/secrets/**", behavior: "deny" }];
  try {
    await workspaceToolPermissions("workspace/1");
    const updated = await updateWorkspaceToolPermissions("workspace/1", rules, 3);
    await evaluateWorkspaceToolPermission("workspace/1", {
	  agent_id: "agent/1", tool: "default_edit_file", path: "/workspace/src/main.go",
      intervention_mode: "request_approval"
    });
    assert.deepEqual(updated.permission_rules, rules);
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.deepEqual(requests.map(({ method, url }) => `${method} ${url}`), [
    "GET http://localhost/v2/workspaces/workspace%2F1/tool-permissions",
    "PUT http://localhost/v2/workspaces/workspace%2F1/tool-permissions",
    "POST http://localhost/v2/workspaces/workspace%2F1/tool-permissions/evaluate"
  ]);
  assert.deepEqual(requests[1].body, { permission_rules: rules });
  assert.equal(requests[1].headers.get("If-Match"), `"3"`);
  assert.deepEqual(requests[2].body, {
	agent_id: "agent/1", tool: "default_edit_file", path: "/workspace/src/main.go",
    intervention_mode: "request_approval"
  });
});

test("Session tool permission audit uses the typed v2 SDK and encodes filters", async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (path, options = {}) => {
    requests.push({ url: String(path), method: options.method });
    return response({ records: [{ call_id: "call/1", decision: "ask" }], next_cursor: "next/cursor", has_more: true });
  };
  try {
    const page = await sessionToolPermissionAudit("session/1", {
      decision: "ask",
	  tool: "default_edit_file",
      limit: 20,
      cursor: "cursor/1"
    });
    assert.deepEqual(page, { records: [{ call_id: "call/1", decision: "ask" }], next_cursor: "next/cursor", has_more: true });
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.deepEqual(requests, [{
    method: "GET",
	url: "http://localhost/v2/sessions/session%2F1/tool-permission-audit?decision=ask&tool=default_edit_file&limit=20&cursor=cursor%2F1"
  }]);
});
