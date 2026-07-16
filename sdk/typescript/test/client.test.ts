import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError, TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("TMAClient", () => {
  let server: TestServer | undefined;
  afterEach(async () => { await server?.close(); server = undefined; });

  it("injects TokenSource and preserves typed query and resource encoding", async () => {
    const tokenSource = vi.fn(async () => "rotated-token");
    server = await startServer((request, response) => {
      expect(request.headers.authorization).toBe("Bearer rotated-token");
      expect(request.url).toBe("/v2/traces?session_id=sesn%2F1&limit=5&cursor=opaque%2Fnext");
      json(response, 200, {
        items: [{ trace_id: "trace/1", session_id: "sesn/1", turn_id: "turn/1", turn_status: "future_state", duration_ms: 1, step_count: 1, span_count: 0, tool_calls: 0, errors: 0 }],
        next_cursor: "next",
        has_more: true,
      });
    });
    const client = new TMAClient(server.baseURL, { tokenSource });
    const page = await client.traces.list({ sessionId: "sesn/1", limit: 5, cursor: "opaque/next" });
    expect(page.items[0]?.turn_status).toBe("future_state");
    expect(page.next_cursor).toBe("next");
    expect(tokenSource).toHaveBeenCalledOnce();
  });

  it("escapes IDs, handles 201 and 204, and preserves dynamic JSON", async () => {
    const requests: string[] = [];
    server = await startServer(async (request, response) => {
      requests.push(`${request.method} ${request.url}`);
      if (request.method === "POST") {
        const body = JSON.parse((await readBody(request)).toString()) as Record<string, unknown>;
        expect(body.agent).toMatchObject({ tools: { custom: { enabled: true } } });
        json(response, 201, { id: "agt/imported", name: "Imported" });
        return;
      }
      response.writeHead(204);
      response.end();
    });
    const client = new TMAClient(server.baseURL);
    await client.agents.import({
      format: "tma.agent",
      schema_version: 1,
      agent: { name: "A", llm_provider: "fake", llm_model: "fake", system: "S", tools: { custom: { enabled: true } } },
    });
    await client.sessions.delete("sesn/delete");
    expect(requests).toEqual(["POST /v2/agents/import", "DELETE /v2/sessions/sesn%2Fdelete"]);
  });

  it("decodes structured API errors", async () => {
    server = await startServer((_request, response) => {
      json(response, 409, { error: { code: "idempotency_conflict", message: "already used", request_id: "req_1", retryable: false, details: { resource_type: "run" } } });
    });
    const client = new TMAClient(server.baseURL);
    const error = await client.runs.start("sesn_1", { input: { text: "x" }, idempotency_key: "same" }).catch((value: unknown) => value);
    expect(error).toBeInstanceOf(APIError);
    expect(error).toMatchObject({ status: 409, code: "idempotency_conflict", requestId: "req_1", retryable: false, details: { resource_type: "run" } });
  });

  it("uploads multipart without replacing its boundary and downloads bytes", async () => {
    let uploaded = false;
    server = await startServer(async (request, response) => {
      if (request.method === "POST") {
        expect(request.headers["content-type"]).toMatch(/^multipart\/form-data; boundary=/);
        const body = (await readBody(request)).toString();
        expect(body).toContain('name="description"');
        expect(body).toContain("SDK upload");
        expect(body).toContain('filename="report.txt"');
        expect(body).toContain("artifact body");
        uploaded = true;
        json(response, 201, { artifact: { id: "art/1" }, object_ref: { id: "obj/1" } });
        return;
      }
      response.writeHead(200, { "content-type": "text/plain" });
      response.end("artifact body");
    });
    const client = new TMAClient(server.baseURL);
    await client.artifacts.upload("sesn/1", { description: "SDK upload" }, {
      filename: "report.txt",
      contentType: "text/plain",
      body: new Blob(["artifact body"]),
    });
    const response = await client.artifacts.download("sesn/1", "art/1");
    expect(uploaded).toBe(true);
    expect(await response.text()).toBe("artifact body");
  });

  it("keeps unknown Run status values", async () => {
    server = await startServer((_request, response) => {
      json(response, 200, { id: "turn_1", session_id: "sesn_1", status: "future_terminal", attempt: 1, started_at: "2026-07-15T00:00:00Z" });
    });
    const client = new TMAClient(server.baseURL);
    const run = await client.runs.get("sesn_1", "turn_1");
    expect(run.status).toBe("future_terminal");
  });

  it("exposes typed Session summary, task Plan, and usage reads with escaped IDs", async () => {
    const requests: string[] = [];
    server = await startServer((request, response) => {
      requests.push(`${request.method} ${request.url}`);
      if (request.url?.endsWith("/summary")) {
        json(response, 200, {
          session_id: "session/1", summary_text: "Current summary", source_until_seq: 7,
          created_at: "2026-07-15T00:00:00Z", updated_at: "2026-07-15T00:00:00Z",
        });
        return;
      }
      const taskPlan = {
        id: "plan_1", workspace_id: "default", owner_id: "user", session_id: "session/1",
        goal: "Ship", handling_mode: "planned", status: "active", items: [],
        created_at: "2026-07-15T00:00:00Z", updated_at: "2026-07-15T00:00:00Z",
      };
      if (request.url?.endsWith("/task-plans")) {
        json(response, 200, { plans: [taskPlan] });
        return;
      }
      if (request.url?.endsWith("/task-plan")) {
        json(response, 200, { plan: taskPlan });
        return;
      }
      json(response, 200, { session_id: "session/1", summary: { total_tokens: 12 }, records: [] });
    });
    const client = new TMAClient(server.baseURL);
    const summary = await client.sessions.summary("session/1");
    const taskPlan = await client.sessions.taskPlan("session/1");
    const taskPlans = await client.sessions.taskPlans("session/1");
    const usage = await client.sessions.usage("session/1");
    expect(summary.source_until_seq).toBe(7);
    expect(taskPlan.id).toBe("plan_1");
    expect(taskPlans).toHaveLength(1);
    expect(usage.summary.total_tokens).toBe(12);
    expect(requests).toEqual([
      "GET /v2/sessions/session%2F1/summary",
      "GET /v2/sessions/session%2F1/task-plan",
      "GET /v2/sessions/session%2F1/task-plans",
      "GET /v2/sessions/session%2F1/usage",
    ]);
  });

  it("upgrades a Session config through the typed request and response", async () => {
    server = await startServer(async (request, response) => {
      expect(request.method).toBe("POST");
      expect(request.url).toBe("/v2/sessions/session%2F1/config/upgrade");
      expect(JSON.parse((await readBody(request)).toString())).toEqual({ to_version: 3, updated_by: "workbench" });
      json(response, 200, {
        old_agent_config_version: 2,
        new_agent_config_version: 3,
        latest_agent_config_version: 4,
        changed: true,
      });
    });
    const client = new TMAClient(server.baseURL);
    const result = await client.sessions.upgradeConfig("session/1", { to_version: 3, updated_by: "workbench" });
    expect(result).toMatchObject({ old_agent_config_version: 2, new_agent_config_version: 3, changed: true });
  });

  it("accepts queued Session event writes with a 202 response", async () => {
    server = await startServer(async (request, response) => {
      expect(request.method).toBe("POST");
      expect(request.url).toBe("/v2/sessions/session%2F1/events");
      expect(JSON.parse((await readBody(request)).toString())).toEqual({
        events: [{ type: "user.message", payload: { content: [{ type: "text", text: "queued" }] } }],
      });
      json(response, 202, { queued: true, events: [] });
    });
    const client = new TMAClient(server.baseURL);
    const result = await client.sessions.appendEvents("session/1", {
      events: [{ type: "user.message", payload: { content: [{ type: "text", text: "queued" }] } }],
    });
    expect(result.queued).toBe(true);
  });
});
