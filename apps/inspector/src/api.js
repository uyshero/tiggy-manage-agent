import { coreSDK } from "./core-sdk.js";

export function tracePath(sessionId, turnId, format) {
  const query = [];
  if (turnId) query.push(`turn_id=${encodeURIComponent(turnId)}`);
  if (format) query.push(`format=${encodeURIComponent(format)}`);
  return `/v2/sessions/${encodeURIComponent(sessionId)}/trace${query.length ? `?${query.join("&")}` : ""}`;
}

export function metricsPath(sessionId, turnId) {
  const query = [`session_id=${encodeURIComponent(sessionId)}`];
  if (turnId) query.push(`turn_id=${encodeURIComponent(turnId)}`);
  return `/metrics?${query.join("&")}`;
}

export async function getJSON(path, options = {}) {
  const response = await fetch(path, options);
  if (!response.ok) throw new Error(await response.text());
  return response.json();
}

export async function getText(path, options = {}) {
  const response = await fetch(path, options);
  if (!response.ok) throw new Error(await response.text());
  return response.text();
}

export async function postJSON(path, body) {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {})
  });
  if (!response.ok) throw new Error(await response.text());
  return response.json();
}

export async function getBlob(path) {
  const response = await fetch(path);
  if (!response.ok) throw new Error(await response.text());
  return response;
}

export function trace(sessionId, turnId, format, options = {}) {
  return format
    ? coreSDK.traces.exportSession(sessionId, format, turnId || undefined, options.signal)
    : coreSDK.traces.getSession(sessionId, turnId || undefined, options.signal);
}

export async function agents(options = {}) {
  return { agents: await coreSDK.agents.list(options.signal) };
}

export async function sessions(options = {}) {
  return {
    sessions: await coreSDK.sessions.list({ limit: 100, includeArchived: true }, options.signal)
  };
}

export function createAgent(body) {
  return postJSON("/v1/agents", body);
}

export function createEnvironment(body) {
  return postJSON("/v1/environments", body);
}

export function createSession(body) {
  return postJSON("/v1/sessions", body);
}

export function sendSessionMessage(sessionId, text) {
  return postJSON(`/v1/sessions/${encodeURIComponent(sessionId)}/events`, {
    events: [{
      type: "user.message",
      payload: {
        content: [{ type: "text", text }]
      }
    }]
  });
}

export async function traceCatalog(filters = {}) {
  const page = await coreSDK.traces.list({
    limit: filters.limit || 20,
    ...(filters.cursor ? { cursor: filters.cursor } : {}),
    ...(filters.session ? { sessionId: filters.session } : {}),
    ...(filters.turn ? { turnId: filters.turn } : {})
  });
  return { traces: page.items, next_cursor: page.next_cursor, has_more: page.has_more };
}

export function traceByID(traceID, options = {}) {
  return coreSDK.traces.get(traceID, options.signal);
}

export function spanByID(traceID, spanID, options = {}) {
  return coreSDK.traces.getSpan(traceID, spanID, options.signal);
}

export async function spanCatalog(filters = {}) {
  const page = await coreSDK.traces.listSpans({
    limit: filters.limit || 20,
    ...(filters.cursor ? { cursor: filters.cursor } : {}),
    ...(filters.session ? { sessionId: filters.session } : {}),
    ...(filters.turn ? { turnId: filters.turn } : {}),
    ...(filters.query ? { search: filters.query } : {}),
    ...(filters.kind ? { kind: filters.kind } : {}),
    ...(filters.status ? { status: filters.status } : {}),
    ...(filters.critical === "true" ? { critical: true } : filters.critical === "false" ? { critical: false } : {}),
    ...(filters.minDuration ? { minDurationMs: Number(filters.minDuration) } : {})
  });
  return {
    spans: page.items,
    next_cursor: page.next_cursor,
    has_more: page.has_more,
    kind_counts: countBy(page.items, (span) => span.kind),
    status_counts: countBy(page.items, (span) => span.status || "unknown"),
    critical_counts: countBy(page.items, (span) => String(Boolean(span.critical)))
  };
}

function countBy(items, keyFor) {
  return items.reduce((counts, item) => {
    const key = keyFor(item);
    counts[key] = (counts[key] || 0) + 1;
    return counts;
  }, {});
}

export function session(sessionId, options = {}) {
  return coreSDK.sessions.get(sessionId, options.signal);
}

export function usage(sessionId, options = {}) {
  return coreSDK.sessions.usage(sessionId, options.signal);
}

export function summary(sessionId, options = {}) {
  return coreSDK.sessions.summary(sessionId, options.signal);
}

export async function taskPlans(sessionId, options = {}) {
  return { plans: await coreSDK.sessions.taskPlans(sessionId, options.signal) };
}

export async function artifacts(sessionId, options = {}) {
  return { artifacts: await coreSDK.artifacts.list(sessionId, options.signal) };
}

export function artifactDownloadPath(sessionId, artifactId) {
  return `/v2/sessions/${encodeURIComponent(sessionId)}/artifacts/${encodeURIComponent(artifactId)}/download`;
}

export function downloadArtifact(sessionId, artifactId, options = {}) {
  return coreSDK.artifacts.download(sessionId, artifactId, options.signal);
}

export async function events(sessionId, afterSeq = 0, options = {}) {
  return { events: await coreSDK.sessions.listEvents(sessionId, afterSeq, options.signal) };
}

export async function interventions(sessionId, status, options = {}) {
  return { interventions: await coreSDK.interventions.list(sessionId, status, options.signal) };
}

export function metrics(sessionId, turnId, options = {}) {
  return getText(metricsPath(sessionId, turnId), options);
}

export function observabilityStatus(options = {}) {
  return coreSDK.observability.status(options.signal);
}

export function retryObservability() {
  return postJSON("/v1/observability/retry", {});
}

export function approveIntervention(sessionId, turnId, callId, body = {}, options = {}) {
  return coreSDK.interventions.approve(sessionId, turnId, callId, body.reason || "", options.signal);
}

export function rejectIntervention(sessionId, turnId, callId, body = {}, options = {}) {
  return coreSDK.interventions.reject(sessionId, turnId, callId, body.reason || "", options.signal);
}
