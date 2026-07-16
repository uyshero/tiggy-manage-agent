export function pretty(value) {
  return JSON.stringify(value, null, 2);
}

export function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

export function formatDuration(ms) {
  const value = Number(ms || 0);
  if (value < 1000) return `${value} ms`;
  return `${(value / 1000).toFixed(value < 10000 ? 2 : 1)} s`;
}

export function taskPlanStatusCounts(plans) {
  const counts = { total: 0, active: 0, completed: 0, canceled: 0, superseded: 0 };
  for (const plan of Array.isArray(plans) ? plans : []) {
    counts.total += 1;
    const status = String(plan?.status || "").trim().toLowerCase();
    if (Object.hasOwn(counts, status) && status !== "total") counts[status] += 1;
  }
  return counts;
}

export function filterTaskPlans(plans, status = "") {
  const items = Array.isArray(plans) ? plans : [];
  const normalized = String(status || "").trim().toLowerCase();
  if (!normalized) return items;
  return items.filter((plan) => String(plan?.status || "").trim().toLowerCase() === normalized);
}

export function pillClass(statusValue) {
  if (["completed", "ok", "success", "approved"].includes(statusValue)) return "pill ok";
  if (["waiting_approval", "pending", "blocked"].includes(statusValue)) return "pill warn";
  if (["failed", "error", "rejected"].includes(statusValue)) return "pill err";
  return "pill";
}

export function stepClass(step) {
  if (step.outcome === "error" || step.type === "runtime.failed") return "step error";
  if (step.type && step.type.includes("intervention")) return "step approval";
  if (step.type && step.type.includes("tool")) return "step tool";
  return "step";
}

export function isTerminalTurnStatus(status) {
  return ["completed", "failed", "canceled", "terminated"].includes(String(status || "").trim().toLowerCase());
}

export function normalizeToolSource(source) {
  return String(source || "").trim().toLowerCase();
}

export function toolSourceLabel(source) {
  switch (normalizeToolSource(source)) {
    case "mcp":
      return "MCP";
    case "worker_plugin":
      return "Worker Plugin";
    case "builtin":
      return "Builtin";
    default:
      return "";
  }
}

export function mcpDiagnosticBadges(data = {}) {
  if (!isPlainObject(data) || normalizeToolSource(data.tool_source) !== "mcp") return [];
  const badges = [];
  if (data.mcp_transport) badges.push(`transport ${data.mcp_transport}`);
  if (data.mcp_protocol_version) badges.push(`protocol ${data.mcp_protocol_version}`);
  if (data.mcp_tool_count !== undefined) badges.push(`${data.mcp_tool_count} tool(s)`);
  const capabilities = Array.isArray(data.mcp_capabilities) ? data.mcp_capabilities : [];
  if (capabilities.length) badges.push(`capabilities ${capabilities.join(", ")}`);
  if (data.mcp_oauth) badges.push("OAuth");
  if (data.mcp_listen) badges.push("SSE listener");
  if (data.mcp_expose_resources) badges.push("resources exposed");
  if (data.mcp_expose_prompts) badges.push("prompts exposed");
  return badges;
}

export function mcpResultSummary(data = {}) {
  if (!isPlainObject(data) || normalizeToolSource(data.tool_source) !== "mcp") return null;
  const state = data.state;
  if (!isPlainObject(state)) return null;
  if (state.truncated) {
    return {
      title: "MCP result state omitted",
      facts: [
        state.original_bytes ? `${state.original_bytes} original byte(s)` : "",
        "open full artifact or raw event for complete state"
      ].filter(Boolean)
    };
  }
  switch (state.protocol_version) {
    case "tma.mcp_result.v1":
      return summarizeMCPToolState(state);
    case "tma.mcp_context_result.v1":
      return summarizeMCPContextState(state);
    default:
      return null;
  }
}

export function collectToolSourceStats(events = []) {
  const counts = { mcp: 0, worker_plugin: 0, builtin: 0, other: 0, total: 0 };
  for (const event of events) {
    const source = normalizeToolSource(event?.payload?.data?.tool_source);
    if (!source) continue;
    counts.total += 1;
    if (source === "mcp" || source === "worker_plugin" || source === "builtin") counts[source] += 1;
    else counts.other += 1;
  }
  return counts;
}

export function collectMCPProtocolOperations(events = []) {
  const operations = [];
  const pendingByCallID = new Map();
  const orderedEvents = [...events].sort((left, right) => Number(left?.seq || 0) - Number(right?.seq || 0));

  for (const event of orderedEvents) {
    const data = event?.payload?.data;
    if (!isPlainObject(data) || normalizeToolSource(data.tool_source) !== "mcp") continue;
    if (event.type !== "runtime.tool_call" && event.type !== "runtime.tool_result") continue;

    const callID = String(data.id || "").trim();
    if (event.type === "runtime.tool_call") {
      const operation = newMCPProtocolOperation(event, data);
      const index = operations.push(operation) - 1;
      if (callID) {
        const pending = pendingByCallID.get(callID) || [];
        pending.push(index);
        pendingByCallID.set(callID, pending);
      }
      continue;
    }

    const pending = callID ? pendingByCallID.get(callID) || [] : [];
    const index = pending.length ? pending.shift() : -1;
    if (callID && !pending.length) pendingByCallID.delete(callID);
    const operation = index >= 0 ? operations[index] : newMCPProtocolOperation(null, data, event);
    applyMCPProtocolResult(operation, event, data);
    if (index < 0) operations.push(operation);
  }

  return operations.sort((left, right) => Number(left.request_seq || left.response_seq || 0) - Number(right.request_seq || right.response_seq || 0));
}

export function highestEventSeq(events = []) {
  return events.reduce((highest, event) => Math.max(highest, Number(event?.seq || 0)), 0);
}

export function mergeEventResponses(previous = {}, incoming = {}) {
  const merged = new Map();
  for (const event of [...(previous.events || []), ...(incoming.events || [])]) {
    const seq = Number(event?.seq || 0);
    if (seq > 0) merged.set(seq, event);
  }
  const response = {
    ...previous,
    ...incoming,
    events: Array.from(merged.values()).sort((left, right) => Number(left.seq || 0) - Number(right.seq || 0))
  };
  if (!Object.hasOwn(incoming, "error")) delete response.error;
  return response;
}

export function sessionArtifactCLI(downloadPath) {
  let path = String(downloadPath || "").trim();
  if (!path) return "";
  path = path.split("?")[0].split("#")[0];
  const prefix = "/v1/sessions/";
  if (!path.startsWith(prefix)) return "";
  const parts = path.slice(prefix.length).split("/");
  if (parts.length !== 4 || parts[1] !== "artifacts" || parts[3] !== "download") return "";
  if (!parts[0] || !parts[2]) return "";
  return `bin/tma session artifact download --session ${parts[0]} --artifact ${parts[2]}`;
}

export function sessionArtifactCommand(sessionId, artifactId) {
  const session = String(sessionId || "").trim();
  const artifact = String(artifactId || "").trim();
  if (!session || !artifact) return "";
  return `bin/tma session artifact download --session ${session} --artifact ${artifact}`;
}

function summarizeMCPToolState(state) {
  const content = Array.isArray(state.content) ? state.content : [];
  const types = uniqueStrings(content.map((item) => item?.type));
  const facts = [
    state.tool_name ? `tool ${state.tool_name}` : "",
    `is_error ${String(Boolean(state.is_error))}`,
    `${content.length} content item(s)`,
    types.length ? `content types ${types.join(", ")}` : "",
    state.structured_content !== undefined ? "structured content present" : "",
    isPlainObject(state.meta) ? `${Object.keys(state.meta).length} meta key(s)` : ""
  ].filter(Boolean);
  return { title: "MCP tool result", facts };
}

function summarizeMCPContextState(state) {
  const facts = [state.tool_name ? `tool ${state.tool_name}` : ""];
  if (Array.isArray(state.resources)) {
    facts.push(`${state.resources.length} resource(s)`);
  }
  if (Array.isArray(state.resource_templates)) {
    facts.push(`${state.resource_templates.length} resource template(s)`);
  }
  if (Array.isArray(state.contents)) {
    const mimeTypes = uniqueStrings(state.contents.map((item) => item?.mimeType || item?.mime_type));
    const textCount = state.contents.filter((item) => String(item?.text || "").trim() !== "").length;
    const blobCount = state.contents.filter((item) => String(item?.blob || "").trim() !== "").length;
    facts.push(`${state.contents.length} resource content item(s)`);
    if (mimeTypes.length) facts.push(`mime ${mimeTypes.join(", ")}`);
    if (textCount) facts.push(`${textCount} text item(s)`);
    if (blobCount) facts.push(`${blobCount} blob item(s)`);
  }
  if (Array.isArray(state.prompts)) {
    const argumentCount = state.prompts.reduce((total, prompt) => total + (Array.isArray(prompt?.arguments) ? prompt.arguments.length : 0), 0);
    facts.push(`${state.prompts.length} prompt(s)`);
    if (argumentCount) facts.push(`${argumentCount} prompt argument(s)`);
  }
  if (isPlainObject(state.prompt)) {
    const messages = Array.isArray(state.prompt.messages) ? state.prompt.messages : [];
    const roles = uniqueStrings(messages.map((message) => message?.role));
    facts.push(`${messages.length} prompt message(s)`);
    if (roles.length) facts.push(`roles ${roles.join(", ")}`);
  }
  return { title: "MCP context result", facts: facts.filter(Boolean) };
}

function newMCPProtocolOperation(requestEvent, data, responseEvent = null) {
  const requestSeq = Number(requestEvent?.seq || 0);
  const responseSeq = Number(responseEvent?.seq || 0);
  const apiName = String(data.api_name || "").trim();
  const callID = String(data.id || "").trim();
  return {
    key: `${callID || "unpaired"}:${requestSeq || responseSeq}`,
    call_id: callID,
    identifier: String(data.identifier || "").trim(),
    api_name: apiName,
    method: mcpMethodForAPI(apiName, data.state),
    status: "pending",
    request_seq: requestSeq,
    response_seq: responseSeq,
    request_time: requestEvent?.created_at || "",
    response_time: responseEvent?.created_at || "",
    duration_ms: null,
    transport: String(data.mcp_transport || "").trim(),
    protocol_version: String(data.mcp_protocol_version || "").trim(),
    diagnostics: mcpDiagnosticBadges(data),
    result_protocol: "",
    result_summary: null,
    error_type: "",
    artifact_count: 0,
    preview_truncated: false
  };
}

function applyMCPProtocolResult(operation, event, data) {
  const state = isPlainObject(data.state) ? data.state : {};
  operation.call_id ||= String(data.id || "").trim();
  operation.identifier ||= String(data.identifier || "").trim();
  operation.api_name ||= String(data.api_name || "").trim();
  operation.method = mcpMethodForAPI(operation.api_name, state);
  operation.response_seq = Number(event?.seq || 0);
  operation.response_time = event?.created_at || "";
  operation.transport ||= String(data.mcp_transport || "").trim();
  operation.protocol_version ||= String(data.mcp_protocol_version || "").trim();
  operation.diagnostics = uniqueStrings([...operation.diagnostics, ...mcpDiagnosticBadges(data)]);
  operation.result_protocol = String(state.protocol_version || "").trim();
  operation.result_summary = mcpResultSummary(data);
  operation.duration_ms = Number.isFinite(Number(data.duration_ms)) ? Number(data.duration_ms) : null;
  operation.artifact_count = Array.isArray(data.artifacts) ? data.artifacts.length : 0;
  operation.preview_truncated = Boolean(data?.context?.content_truncated || data?.context?.state_truncated || state.truncated);
  operation.error_type = mcpErrorType(data, state);
  operation.status = operation.error_type ? "failed" : "completed";
}

function mcpMethodForAPI(apiName, state = {}) {
  const normalized = String(apiName || state?.tool_name || "").trim();
  switch (normalized) {
    case "mcp_list_resources":
    case "__tma_mcp_list_resources":
      return "resources/list";
    case "mcp_list_resource_templates":
    case "__tma_mcp_list_resource_templates":
      return "resources/templates/list";
    case "mcp_read_resource":
    case "__tma_mcp_read_resource":
      return "resources/read";
    case "mcp_list_prompts":
    case "__tma_mcp_list_prompts":
      return "prompts/list";
    case "mcp_get_prompt":
    case "__tma_mcp_get_prompt":
      return "prompts/get";
    default:
      return "tools/call";
  }
}

function mcpErrorType(data, state) {
  if (isPlainObject(data.error)) {
    return String(data.error.type || data.error.code || "tool_error").trim();
  }
  if (data.error || data.success === false) return "tool_error";
  if (state.is_error) return "mcp_result_error";
  return "";
}

function uniqueStrings(values = []) {
  return Array.from(new Set(values.map((value) => String(value || "").trim()).filter(Boolean)));
}

function isPlainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
