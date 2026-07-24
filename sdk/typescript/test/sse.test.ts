import { afterEach, describe, expect, it } from "vitest";
import { SSESchemaError, TMAClient } from "../src/index.js";
import { json, startServer, type TestServer } from "./helpers.js";

describe("SSE", () => {
  let server: TestServer | undefined;
  afterEach(async () => { await server?.close(); server = undefined; });

  it("reconnects after 5xx and resumes after_seq", async () => {
    let calls = 0;
    server = await startServer((request, response) => {
      calls++;
      if (calls === 1) {
        json(response, 503, { error: { code: "service_unavailable", message: "retry", retryable: true } });
        return;
      }
      expect(request.url).toBe("/v2/sessions/sesn_1/events/stream?after_seq=4");
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.end(`data: {"id":"evt_5","session_id":"sesn_1","seq":5,"type":"future.event","created_at":"2026-07-15T00:00:00Z"}\n\n`);
    });
    const client = new TMAClient(server.baseURL);
    const stream = client.sessions.events("sesn_1", { afterSeq: 4, retryInitialMs: 1, retryMaxMs: 2 });
    const event = await stream.next();
    await stream.return(undefined);
    expect(event.value?.type).toBe("future.event");
    expect(calls).toBe(2);
  });

  it("returns schema errors without reconnecting", async () => {
    let calls = 0;
    server = await startServer((_request, response) => {
      calls++;
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.end("data: {\"type\":\"missing-seq\"}\n\n");
    });
    const client = new TMAClient(server.baseURL);
    const stream = client.sessions.events("sesn_1", { retryInitialMs: 1 });
    await expect(stream.next()).rejects.toBeInstanceOf(SSESchemaError);
    expect(calls).toBe(1);
  });

  it("reconnects when a durable stream stops delivering bytes", async () => {
    let calls = 0;
    server = await startServer((_request, response) => {
      calls++;
      response.writeHead(200, { "content-type": "text/event-stream" });
      if (calls === 1) {
        response.write(": connected\n\n");
        return;
      }
      response.end(`data: {"id":"evt_1","session_id":"sesn_1","seq":1,"type":"runtime.started","created_at":"2026-07-15T00:00:00Z"}\n\n`);
    });
    const client = new TMAClient(server.baseURL);
    const stream = client.sessions.events("sesn_1", { idleTimeoutMs: 20, retryInitialMs: 1, retryMaxMs: 2 });
    const event = await stream.next();
    await stream.return(undefined);
    expect(event.value?.seq).toBe(1);
    expect(calls).toBe(2);
  });

  it("streams transient live text without a durable cursor", async () => {
    server = await startServer((request, response) => {
      expect(request.url).toBe("/v2/sessions/sesn_1/live/stream");
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.end(`data: {"stream_seq":7,"session_id":"sesn_1","turn_id":"turn_1","type":"llm.text","operation":"append","content_format":"markdown","text":"hello","created_at":"2026-07-15T00:00:00Z"}\n\n`);
    });
    const client = new TMAClient(server.baseURL);
    const stream = client.sessions.liveEvents("sesn_1", { retryInitialMs: 1 });
    const event = await stream.next();
    await stream.return(undefined);
    expect(event.value).toMatchObject({ stream_seq: 7, type: "llm.text", text: "hello" });
  });

  it("streams transient tool progress without treating it as durable history", async () => {
    server = await startServer((request, response) => {
      expect(request.url).toBe("/v2/sessions/sesn_1/live/stream");
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.end(`data: {"stream_seq":8,"session_id":"sesn_1","turn_id":"turn_1","type":"tool.call_progress","call_id":"call_1","tool":"default_run_command","stage":"running","percent":40,"operation":"update","content_format":"text","text":"Installing dependencies","created_at":"2026-07-15T00:00:00Z"}\n\n`);
    });
    const client = new TMAClient(server.baseURL);
    const stream = client.sessions.liveEvents("sesn_1", { retryInitialMs: 1 });
    const event = await stream.next();
    await stream.return(undefined);
    expect(event.value).toMatchObject({ stream_seq: 8, type: "tool.call_progress", call_id: "call_1", percent: 40 });
  });

  it("rejects durable events on the transient live stream", async () => {
    server = await startServer((_request, response) => {
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.end(`data: {"id":"evt_1","session_id":"sesn_1","seq":1,"type":"runtime.llm_response","created_at":"2026-07-15T00:00:00Z"}\n\n`);
    });
    const client = new TMAClient(server.baseURL);
    const stream = client.sessions.liveEvents("sesn_1", { retryInitialMs: 1 });
    await expect(stream.next()).rejects.toBeInstanceOf(SSESchemaError);
  });

  it("stops locally when AbortSignal is canceled", async () => {
    server = await startServer((_request, response) => {
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.write(": connected\n\n");
    });
    const client = new TMAClient(server.baseURL);
    const controller = new AbortController();
    const stream = client.sessions.events("sesn_1", { signal: controller.signal, retryInitialMs: 1 });
    const pending = stream.next();
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
  });

  it("RunHandle waits for idle and returns agent output", async () => {
    server = await startServer((request, response) => {
      if (request.method === "POST") {
        json(response, 201, {
          run: { id: "turn_1", session_id: "sesn_1", status: "running", user_event_seq: 2, attempt: 1, started_at: "2026-07-15T00:00:00Z" },
          events: [{ id: "evt_1", session_id: "sesn_1", turn_id: "turn_1", seq: 1, type: "user.message", created_at: "2026-07-15T00:00:00Z" }],
          created: true,
        });
        return;
      }
      if (request.url?.includes("events/stream")) {
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end([
          `data: {"id":"evt_2","session_id":"sesn_1","turn_id":"turn_1","seq":2,"type":"agent.message","created_at":"2026-07-15T00:00:01Z","payload":{"content":"done"}}\n\n`,
          `data: {"id":"evt_3","session_id":"sesn_1","turn_id":"turn_1","seq":3,"type":"session.status_idle","created_at":"2026-07-15T00:00:02Z"}\n\n`,
        ].join(""));
        return;
      }
      json(response, 200, { id: "turn_1", session_id: "sesn_1", status: "completed", attempt: 1, started_at: "2026-07-15T00:00:00Z", ended_at: "2026-07-15T00:00:02Z" });
    });
    const client = new TMAClient(server.baseURL);
    const handle = await client.runs.start("sesn_1", { input: { content: [{ type: "text", text: "run" }] } });
    expect(handle.created).toBe(true);
    expect(handle.initialEvents).toHaveLength(1);
    expect(Object.isFrozen(handle.initialEvents)).toBe(true);
    const result = await handle.wait();
    expect(result.run.status).toBe("completed");
    expect(result.output).toEqual({ content: "done" });
  });
});
