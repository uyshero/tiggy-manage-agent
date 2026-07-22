import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("typed control-plane services", () => {
  let server: TestServer | undefined;
  afterEach(async () => { await server?.close(); server = undefined; });

  it("evaluates effective workspace tool permissions", async () => {
    let captured: { method?: string; url?: string; body?: unknown } = {};
    server = await startServer(async (request, response) => {
      captured = {
        method: request.method,
        url: request.url,
        body: JSON.parse((await readBody(request)).toString()),
      };
      json(response, 200, {
        workspace_id: "workspace/1", agent_id: "agent/1",
        tool: "default.edit_file", path: "/workspace/src/main.go",
        decision: "ask", allowed: false, required: true,
        intervention_mode: "request_approval", approval_policy: "conditional",
        reason: "filesystem_write", risk: "write",
      });
    });
    const client = new TMAClient(server.baseURL);
    const result = await client.workspaceToolPermissions.evaluate("workspace/1", {
      agent_id: "agent/1", tool: "default.edit_file",
      path: "/workspace/src/main.go", intervention_mode: "request_approval",
    });

    expect(result.decision).toBe("ask");
    expect(captured).toEqual({
      method: "POST",
      url: "/v2/workspaces/workspace%2F1/tool-permissions/evaluate",
      body: {
        agent_id: "agent/1", tool: "default.edit_file",
        path: "/workspace/src/main.go", intervention_mode: "request_approval",
      },
    });
  });

  it("implements LLM concurrency headers and usage queries", async () => {
    const requests: Array<{ method: string; url: string; ifMatch?: string; ifNoneMatch?: string }> = [];
    server = await startServer(async (request, response) => {
      requests.push({
        method: request.method ?? "",
        url: request.url ?? "",
        ...(request.headers["if-match"] === undefined ? {} : { ifMatch: String(request.headers["if-match"]) }),
        ...(request.headers["if-none-match"] === undefined ? {} : { ifNoneMatch: String(request.headers["if-none-match"]) }),
      });
      await readBody(request);
      if (request.url?.startsWith("/v2/llm-usage")) {
        json(response, 200, { records: [], summary: { dynamic_counter: 7 } });
      } else if (request.url?.endsWith("/test")) {
        json(response, 200, {
          status: "succeeded", latency_ms: 12, authenticated: true,
          message: "diagnostic succeeded", retryable: false, checked_at: "2026-07-15T00:00:00Z",
        });
      } else if (request.url?.startsWith("/v2/llm-models") && request.method === "GET") {
        json(response, 200, { models: [] });
      } else if (request.url === "/v2/llm-providers" && request.method === "GET") {
        json(response, 200, { providers: [] });
      } else if (request.method === "DELETE") {
        response.writeHead(204);
        response.end();
      } else {
        json(response, request.method === "POST" && !request.url?.includes("/enable") && !request.url?.includes("/disable") ? 201 : 200, {
          id: "provider/1", provider_type: "openai", enabled: true, revision: 8,
          provider_id: "provider/1", model: "model/1", context_window_tokens: 128000,
          capability_type: "future_capability", is_default_vision: false,
          created_at: "2026-07-15T00:00:00Z", updated_at: "2026-07-15T00:00:00Z",
        });
      }
    });
    const client = new TMAClient(server.baseURL);
    await client.llm.listProviders();
    await client.llm.createProvider({ id: "provider/1", provider_type: "openai" });
    await client.llm.getProvider("provider/1");
    await client.llm.updateProvider("provider/1", 7, { base_url: "https://llm.example/v1" });
    await client.llm.setProviderEnabled("provider/1", 8, false);
    await client.llm.deleteProvider("provider/1", 9);
    await client.llm.listModels("provider/1");
    await client.llm.createModel({ provider_id: "provider/1", model: "model/1" });
    const model = await client.llm.updateModel(3, { provider_id: "provider/1", model: "model/1", capability_type: "vision" });
    await client.llm.deleteModel("provider/1", "model/1", 4);
    const providerDiagnostic = await client.llm.testProvider("provider/1");
    const modelDiagnostic = await client.llm.testModel("provider/1", "model/1");
    const usage = await client.llm.usage({
      workspaceId: "workspace/1", providerId: "provider/1", model: "model/1", status: "completed",
      groupBy: "model", from: new Date("2026-07-01T00:00:00Z"), to: "2026-07-15T00:00:00Z",
    });

    expect(model.capability_type).toBe("future_capability");
    expect(providerDiagnostic.status).toBe("succeeded");
    expect(modelDiagnostic.latency_ms).toBe(12);
    expect(usage.summary).toMatchObject({ dynamic_counter: 7 });
    expect(requests).toContainEqual(expect.objectContaining({ method: "POST", url: "/v2/llm-models", ifNoneMatch: "*" }));
    expect(requests).toContainEqual(expect.objectContaining({ method: "PATCH", url: "/v2/llm-providers/provider%2F1", ifMatch: '"7"' }));
    expect(requests).toContainEqual(expect.objectContaining({ method: "DELETE", url: "/v2/llm-models/provider%2F1/model%2F1", ifMatch: '"4"' }));
    expect(requests).toContainEqual(expect.objectContaining({ method: "POST", url: "/v2/llm-providers/provider%2F1/test" }));
    expect(requests).toContainEqual(expect.objectContaining({ method: "POST", url: "/v2/llm-models/provider%2F1/model%2F1/test" }));
    expect(requests.at(-1)?.url).toBe("/v2/llm-usage?workspace_id=workspace%2F1&provider_id=provider%2F1&model=model%2F1&status=completed&group_by=model&from=2026-07-01T00%3A00%3A00.000Z&to=2026-07-15T00%3A00%3A00Z");
  });

  it("escapes ObjectRef IDs and keeps Worker machine operations out of the SDK", async () => {
    const requests: string[] = [];
    server = await startServer(async (request, response) => {
      requests.push(`${request.method} ${request.url}`);
      const body = await readBody(request);
      if (request.url?.endsWith("/download?session_id=session%2F1")) {
        response.writeHead(200, { "content-type": "application/octet-stream" });
        response.end("object bytes");
      } else if (request.method === "DELETE") {
        response.writeHead(204);
        response.end();
      } else if (request.url?.startsWith("/v2/workers?")) {
        json(response, 200, { workers: [{ id: "worker/1", status: "future_worker_state" }] });
      } else if (request.url === "/v2/workers/diagnose") {
        json(response, 200, { invocation: JSON.parse(body.toString()), matches: 0, diagnostics: [{ reasons: ["runtime mismatch"], match: false }] });
      } else if (request.url === "/v2/workers/reap-expired" || request.url === "/v2/worker-work/reap-expired") {
        json(response, 200, { count: 0, expired: [] });
      } else if (request.url?.endsWith("/diagnose")) {
        json(response, 200, { work: { id: "work/1", status: "future_work_state", payload: { nested: { preserved: true } } }, reasons: ["worker unavailable"], actions: ["requeue"] });
      } else if (request.url?.startsWith("/v2/worker-work")) {
        json(response, request.url === "/v2/worker-work" ? 201 : 200, { id: "work/1", status: "queued", payload: { nested: { preserved: true } } });
      } else if (request.url?.startsWith("/v2/workers/")) {
        json(response, 200, { id: "worker/1", status: "active" });
      } else {
        json(response, request.method === "POST" ? 201 : 200, { id: "object/1", metadata: { nested: { preserved: true } } });
      }
    });
    const client = new TMAClient(server.baseURL);
    await client.objectRefs.create({ storage_provider: "local", bucket: "artifacts", object_key: "report.txt", size_bytes: 13 });
    await client.objectRefs.get("object/1");
    const download = await client.objectRefs.download("object/1", "session/1");
    await client.objectRefs.delete("object/1");
    const workers = await client.workers.list({ workspaceId: "workspace/1", status: "active" });
    await client.workers.get("worker/1");
    await client.workers.archive("worker/1");
    await client.workers.reapExpired({ workspace_id: "workspace/1", limit: 5 });
    const diagnosis = await client.workers.diagnose({ namespace: "tools", api: "read", input: { path: "a/b" } });
    await client.workerWork.enqueue({ work_type: "tool", payload: { nested: { preserved: true } } });
    await client.workerWork.get("work/1");
    await client.workerWork.cancel("work/1", { reason: "operator request" });
    await client.workerWork.requeue("work/1", { clear_worker: true });
    await client.workerWork.reapExpired({ limit: 5 });
    const workDiagnosis = await client.workerWork.diagnose("work/1");

    expect(await download.text()).toBe("object bytes");
    expect(workers[0]?.status).toBe("future_worker_state");
    expect(diagnosis.diagnostics[0]?.reasons).toEqual(["runtime mismatch"]);
    expect(workDiagnosis.work.status).toBe("future_work_state");
    expect(workDiagnosis.work.payload).toEqual({ nested: { preserved: true } });
    expect(requests).toContain("GET /v2/object-refs/object%2F1/download?session_id=session%2F1");
    expect(requests).toContain("GET /v2/workers/worker%2F1");
    expect(requests).toContain("GET /v2/worker-work/work%2F1/diagnose");
    for (const method of ["register", "heartbeat", "poll", "ack", "result"]) {
      expect(method in client.workers).toBe(false);
      expect(method in client.workerWork).toBe(false);
    }
    expect(requests.every((value) => !value.includes("/v1/"))).toBe(true);
  });

  it("covers MCP lifecycle, audit, observability, and environment variables", async () => {
    const requests: string[] = [];
    server = await startServer(async (request, response) => {
      requests.push(`${request.method} ${request.url}`);
      const body = await readBody(request);
      if (request.url?.startsWith("/v2/environment-variables")) {
        if (request.method === "DELETE") { response.writeHead(204); response.end(); return; }
        if (request.method === "GET") { json(response, 200, { variables: [] }); return; }
        expect(JSON.parse(body.toString())).toEqual({ value: "secret" });
        json(response, 200, { name: "SERVICE/API KEY", configured: true, created_at: "2026-07-15T00:00:00Z", updated_at: "2026-07-15T00:00:00Z" });
        return;
      }
      if (request.url?.includes("operator-audit")) { json(response, 200, { audit_records: [] }); return; }
      if (request.url?.includes("tool-permission-audit")) {
        json(response, 200, { records: [{
          session_id: "session/1", turn_id: "turn/1", call_id: "call/1",
          tool: "default.edit_file", path: "/workspace/src/main.go",
          decision: "ask", allowed: false, required: true,
          intervention_mode: "request_approval", approval_policy: "conditional",
          approval_status: "approved", execution_status: "succeeded",
          reason: "filesystem_write", risk: "write",
          matched_rule_id: "ask-src", rule_source: "session",
          created_at: "2026-07-15T00:00:00Z",
        }], next_cursor: "next/cursor", has_more: true });
        return;
      }
      if (request.url?.endsWith("integrity-keys")) { json(response, 200, { active_key_id: "key/1", historical_unidentified_blocking: 0, keys: [] }); return; }
      if (request.url?.includes("security-audit/replay")) { json(response, 200, { replayed: 2 }); return; }
      if (request.url === "/v2/observability/status") { json(response, 200, { enabled: true, exporter: { custom: true } }); return; }
      if (request.url === "/v2/observability/retry") { json(response, 200, { retried: 1 }); return; }
      if (request.url?.endsWith("runtime-status?workspace_id=workspace%2F1")) { json(response, 200, { checked_at: "2026-07-15T00:00:00Z", states: [] }); return; }
      if (request.url?.endsWith("/versions")) { json(response, 200, { versions: [{ id: "version/1", server_id: "mcp/1", version: 1, config: { identifier: "git", extension: { preserved: true } }, checksum_sha256: "a", created_at: "2026-07-15T00:00:00Z" }] }); return; }
      if (request.url?.endsWith("/versions/1/restore")) { json(response, 201, { server: mcpFixture("active"), source_version: 1, previous_version: 2, new_version: 3 }); return; }
      if (request.url?.endsWith("/test")) { json(response, 200, { server_id: "mcp/1", version: 1, result: { identifier: "git", kind: "mcp", status: "online", extension: { preserved: true } } }); return; }
      if (request.url === "/v2/mcp-servers?workspace_id=workspace%2F1") { json(response, 200, { servers: [mcpFixture("future_state")] }); return; }
      json(response, request.url === "/v2/mcp-servers" && request.method === "POST" ? 201 : 200, mcpFixture(request.url?.endsWith("/disable") ? "disabled" : request.method === "DELETE" ? "archived" : "active"));
    });
    const client = new TMAClient(server.baseURL);
    const servers = await client.mcp.list({ workspaceId: "workspace/1" });
    await client.mcp.runtimeStatus({ workspaceId: "workspace/1" });
    await client.mcp.create({ identifier: "git", name: "Git", config: { identifier: "git", command: "git-mcp", env: { TOKEN: { secret_ref: "MCP_TOKEN" } } } });
    await client.mcp.get("mcp/1");
    await client.mcp.update("mcp/1", { name: "Git MCP" });
    await client.mcp.setEnabled("mcp/1", true);
    await client.mcp.setEnabled("mcp/1", false);
    await client.mcp.archive("mcp/1");
    const testResult = await client.mcp.test("mcp/1");
    const versions = await client.mcp.versions("mcp/1");
    await client.mcp.restoreVersion("mcp/1", 1);
    await client.observability.status();
    await client.observability.retry();
    await client.observability.integrityKeys();
    await client.audit.list({ workspaceId: "workspace/1", sessionId: "session/1", principalId: "user/1", action: "mcp_registry.update", limit: 25 });
    await client.audit.listSession("session/1");
    const permissionAudit = await client.audit.listToolPermissions("session/1", { decision: "ask", tool: "default.edit_file", limit: 20, cursor: "cursor/1" });
    await client.audit.integrityKeys();
    await client.audit.replayDeadLetters(50);
    await client.environmentVariables.list({ workspaceId: "workspace/1" });
    await client.environmentVariables.put("SERVICE/API KEY", { value: "secret" }, { workspaceId: "workspace/1" });
    await client.environmentVariables.delete("SERVICE/API KEY", { workspaceId: "workspace/1" });

    expect(servers[0]?.status).toBe("future_state");
    expect(versions[0]?.config).toMatchObject({ extension: { preserved: true } });
    expect(testResult.result).toMatchObject({ extension: { preserved: true } });
    expect(permissionAudit.records[0]).toMatchObject({ call_id: "call/1", approval_status: "approved", execution_status: "succeeded" });
    expect(permissionAudit).toMatchObject({ next_cursor: "next/cursor", has_more: true });
    expect(requests).toContain("POST /v2/mcp-servers/mcp%2F1/versions/1/restore");
    expect(requests).toContain("GET /v2/operator-audit?workspace_id=workspace%2F1&session_id=session%2F1&principal_id=user%2F1&action=mcp_registry.update&limit=25");
    expect(requests).toContain("GET /v2/sessions/session%2F1/tool-permission-audit?decision=ask&tool=default.edit_file&limit=20&cursor=cursor%2F1");
    expect(requests).toContain("PUT /v2/environment-variables/SERVICE%2FAPI%20KEY?workspace_id=workspace%2F1");
    expect(requests).toContain("DELETE /v2/environment-variables/SERVICE%2FAPI%20KEY?workspace_id=workspace%2F1");
  });
});

function mcpFixture(status: string) {
  return {
    id: "mcp/1", workspace_id: "workspace/1", identifier: "git", name: "Git", status,
    current_version: 1, config: { identifier: "git" }, usage_count: 0,
    created_at: "2026-07-15T00:00:00Z", updated_at: "2026-07-15T00:00:00Z",
  };
}
