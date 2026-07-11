export function tracePath(sessionId, turnId, format) {
  const query = [];
  if (turnId) query.push(`turn_id=${encodeURIComponent(turnId)}`);
  if (format) query.push(`format=${encodeURIComponent(format)}`);
  return `/v1/sessions/${encodeURIComponent(sessionId)}/trace${query.length ? `?${query.join("&")}` : ""}`;
}

export function metricsPath(sessionId, turnId) {
  const query = [`session_id=${encodeURIComponent(sessionId)}`];
  if (turnId) query.push(`turn_id=${encodeURIComponent(turnId)}`);
  return `/metrics?${query.join("&")}`;
}

export async function getJSON(path) {
  const response = await fetch(path);
  if (!response.ok) throw new Error(await response.text());
  return response.json();
}

export async function getText(path) {
  const response = await fetch(path);
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

export function trace(sessionId, turnId, format) {
  return getJSON(tracePath(sessionId, turnId, format));
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

export function traceCatalog(filters = {}) {
  const params = new URLSearchParams();
  params.set("limit", String(filters.limit || 20));
  if (filters.offset) params.set("offset", String(filters.offset));
  if (filters.session) params.set("session_id", filters.session);
  if (filters.turn) params.set("turn_id", filters.turn);
  return getJSON(`/v1/traces?${params.toString()}`);
}

export function traceByID(traceID) {
  return getJSON(`/v1/traces/${encodeURIComponent(traceID)}`);
}

export function spanByID(traceID, spanID) {
  return getJSON(`/v1/traces/${encodeURIComponent(traceID)}/spans/${encodeURIComponent(spanID)}`);
}

export function spanCatalog(filters = {}) {
  const params = new URLSearchParams();
  params.set("limit", String(filters.limit || 20));
  if (filters.offset) params.set("offset", String(filters.offset));
  if (filters.session) params.set("session_id", filters.session);
  if (filters.turn) params.set("turn_id", filters.turn);
  if (filters.query) params.set("q", filters.query);
  if (filters.kind) params.set("kind", filters.kind);
  if (filters.status) params.set("status", filters.status);
  if (filters.critical) params.set("critical", filters.critical);
  if (filters.minDuration) params.set("min_duration_ms", filters.minDuration);
  return getJSON(`/v1/spans?${params.toString()}`);
}

export function session(sessionId) {
  return getJSON(`/v1/sessions/${encodeURIComponent(sessionId)}`);
}

export function usage(sessionId) {
  return getJSON(`/v1/sessions/${encodeURIComponent(sessionId)}/usage`);
}

export function summary(sessionId) {
  return getJSON(`/v1/sessions/${encodeURIComponent(sessionId)}/summary`);
}

export function artifacts(sessionId) {
  return getJSON(`/v1/sessions/${encodeURIComponent(sessionId)}/artifacts`);
}

export function artifactDownloadPath(sessionId, artifactId) {
  return `/v1/sessions/${encodeURIComponent(sessionId)}/artifacts/${encodeURIComponent(artifactId)}/download`;
}

export function events(sessionId) {
  return getJSON(`/v1/sessions/${encodeURIComponent(sessionId)}/events`);
}

export function interventions(sessionId, status) {
  const suffix = status ? `?status=${encodeURIComponent(status)}` : "";
  return getJSON(`/v1/sessions/${encodeURIComponent(sessionId)}/interventions${suffix}`);
}

export function metrics(sessionId, turnId) {
  return getText(metricsPath(sessionId, turnId));
}

export function observabilityStatus() {
  return getJSON("/v1/observability/status");
}

export function retryObservability() {
  return postJSON("/v1/observability/retry", {});
}

export function approveIntervention(sessionId, turnId, callId, body) {
  return postJSON(`/v1/sessions/${sessionId}/interventions/${turnId}/${callId}/approve`, body);
}

export function rejectIntervention(sessionId, turnId, callId, body) {
  return postJSON(`/v1/sessions/${sessionId}/interventions/${turnId}/${callId}/reject`, body);
}
