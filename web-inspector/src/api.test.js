import test from "node:test";
import assert from "node:assert/strict";
import {
  artifactDownloadPath,
  artifacts,
  approveIntervention,
  downloadArtifact,
  events,
  interventions,
  observabilityStatus,
  rejectIntervention,
  session,
  summary,
  spanByID,
  spanCatalog,
  trace,
  traceByID,
  traceCatalog,
  usage
} from "./api.js";

test("events requests only records after the supplied sequence", async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (path, options) => {
    calls.push({ path, options });
    return { ok: true, json: async () => ({ events: [] }) };
  };
  try {
    const controller = new AbortController();
    await events("sesn/a", 42, { signal: controller.signal });
    assert.equal(calls[0].path, "http://localhost/v2/sessions/sesn%2Fa/events?after_seq=42");
    assert.equal(calls[0].options.signal, controller.signal);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("Inspector approval decisions use the v2 SDK with escaped IDs and cancellation", async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (path, options) => {
    calls.push({ path: String(path), options });
    return { ok: true, json: async () => ({ status: "resolved" }) };
  };
  try {
    const controller = new AbortController();
    await approveIntervention("session/1", "turn/1", "call/1", { reason: "approved from inspector" }, { signal: controller.signal });
    await rejectIntervention("session/1", "turn/1", "call/1", { reason: "not permitted" }, { signal: controller.signal });
    assert.deepEqual(calls.map((call) => call.path), [
      "http://localhost/v2/sessions/session%2F1/interventions/turn%2F1/call%2F1/approve",
      "http://localhost/v2/sessions/session%2F1/interventions/turn%2F1/call%2F1/reject"
    ]);
    assert.deepEqual(calls.map((call) => JSON.parse(call.options.body)), [
      { reason: "approved from inspector" },
      { reason: "not permitted" }
    ]);
    assert.equal(calls.every((call) => call.options.signal === controller.signal), true);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("Inspector Session, Artifact, Intervention, and Observability reads use v2 SDK services", async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (path, options) => {
    const url = String(path);
    calls.push({ url, options });
    if (url.endsWith("/artifacts")) return { ok: true, json: async () => ({ artifacts: [{ id: "artifact/1" }] }) };
    if (url.endsWith("/artifacts/artifact%2F1/download")) return { ok: true, headers: new Headers({ "content-type": "text/plain" }) };
    if (url.includes("/interventions")) return { ok: true, json: async () => ({ interventions: [{ call_id: "call/1" }] }) };
    if (url.endsWith("/observability/status")) return { ok: true, json: async () => ({ enabled: true }) };
    if (url.endsWith("/usage")) return { ok: true, json: async () => ({ session_id: "session/1", summary: { total_tokens: 12 }, records: [] }) };
    if (url.endsWith("/summary")) return { ok: true, json: async () => ({ session_id: "session/1", summary_text: "Summary", source_until_seq: 7 }) };
    return { ok: true, json: async () => ({ id: "session/1" }) };
  };
  try {
    const controller = new AbortController();
    const sessionValue = await session("session/1", { signal: controller.signal });
    const artifactList = await artifacts("session/1", { signal: controller.signal });
    const interventionList = await interventions("session/1", "pending", { signal: controller.signal });
    const status = await observabilityStatus({ signal: controller.signal });
    const usageValue = await usage("session/1", { signal: controller.signal });
    const summaryValue = await summary("session/1", { signal: controller.signal });
    const download = await downloadArtifact("session/1", "artifact/1", { signal: controller.signal });
    assert.equal(sessionValue.id, "session/1");
    assert.equal(artifactList.artifacts[0].id, "artifact/1");
    assert.equal(interventionList.interventions[0].call_id, "call/1");
    assert.equal(status.enabled, true);
    assert.equal(usageValue.summary.total_tokens, 12);
    assert.equal(summaryValue.source_until_seq, 7);
    assert.equal(download.headers.get("content-type"), "text/plain");
    assert.equal(calls.every((call) => call.options.signal === controller.signal), true);
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(calls.map((call) => call.url), [
    "http://localhost/v2/sessions/session%2F1",
    "http://localhost/v2/sessions/session%2F1/artifacts",
    "http://localhost/v2/sessions/session%2F1/interventions?status=pending",
    "http://localhost/v2/observability/status",
    "http://localhost/v2/sessions/session%2F1/usage",
    "http://localhost/v2/sessions/session%2F1/summary",
    "http://localhost/v2/sessions/session%2F1/artifacts/artifact%2F1/download"
  ]);
  assert.equal(artifactDownloadPath("session/1", "artifact/1"), "/v2/sessions/session%2F1/artifacts/artifact%2F1/download");
});

test("trace forwards request cancellation options", async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (path, options) => {
    calls.push({ path, options });
    return { ok: true, json: async () => ({ trace_id: "trace_1" }) };
  };
  try {
    const controller = new AbortController();
    await trace("sesn_1", "turn_1", "", { signal: controller.signal });
    assert.equal(calls[0].path, "http://localhost/v2/sessions/sesn_1/trace?turn_id=turn_1");
    assert.equal(calls[0].options.signal, controller.signal);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("Trace and Span catalogs use opaque v2 cursors and preserve Inspector response wrappers", async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (path) => {
    const url = String(path);
    calls.push(url);
    if (url.includes("/v2/traces?")) {
      return { ok: true, json: async () => ({ items: [{ trace_id: "trace/1" }], next_cursor: "trace-next", has_more: true }) };
    }
    if (url.includes("/v2/spans?")) {
      return { ok: true, json: async () => ({
        items: [
          { trace_id: "trace/1", span_id: "span/1", kind: "tool", status: "ok", critical: false },
          { trace_id: "trace/1", span_id: "span/2", kind: "tool", status: "error", critical: true }
        ],
        next_cursor: "span-next",
        has_more: true
      }) };
    }
    if (url.includes("/spans/")) return { ok: true, json: async () => ({ trace_id: "trace/1", span: { span_id: "span/1" } }) };
    return { ok: true, json: async () => ({ trace_id: "trace/1" }) };
  };
  try {
    const traces = await traceCatalog({ limit: 10, cursor: "trace/next", session: "session/1", turn: "turn/1" });
    const spans = await spanCatalog({
      limit: 10,
      cursor: "span/next",
      session: "session/1",
      turn: "turn/1",
      query: "read file",
      kind: "tool",
      status: "ok",
      critical: "false",
      minDuration: "5"
    });
    const traceDetail = await traceByID("trace/1");
    const spanDetail = await spanByID("trace/1", "span/1");
    assert.deepEqual(traces, { traces: [{ trace_id: "trace/1" }], next_cursor: "trace-next", has_more: true });
    assert.equal(spans.spans.length, 2);
    assert.deepEqual(spans.kind_counts, { tool: 2 });
    assert.deepEqual(spans.status_counts, { ok: 1, error: 1 });
    assert.deepEqual(spans.critical_counts, { false: 1, true: 1 });
    assert.equal(traceDetail.trace_id, "trace/1");
    assert.equal(spanDetail.span.span_id, "span/1");
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.deepEqual(calls, [
    "http://localhost/v2/traces?session_id=session%2F1&turn_id=turn%2F1&limit=10&cursor=trace%2Fnext",
    "http://localhost/v2/spans?session_id=session%2F1&turn_id=turn%2F1&kind=tool&status=ok&q=read+file&critical=false&min_duration_ms=5&limit=10&cursor=span%2Fnext",
    "http://localhost/v2/traces/trace%2F1",
    "http://localhost/v2/traces/trace%2F1/spans/span%2F1"
  ]);
});
