import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { marked } from "marked";
import * as inspectorAPI from "./api.js";
import * as utils from "./utils.js";
import "./styles.css";

window.TMAInspectorAPI = inspectorAPI;
window.TMAInspectorUtils = utils;

const {
  completionQualitySummary,
  formatDuration,
  formatTime,
  filterTaskPlans,
  highestEventSeq,
  isTerminalTurnStatus,
  mergeEventResponses,
  collectMCPProtocolOperations,
  collectToolSourceStats,
  mcpDiagnosticBadges,
  mcpResultSummary,
  normalizeToolSource,
  pillClass,
  pretty,
  sessionArtifactCLI,
  sessionArtifactCommand,
  stepClass,
  taskPlanStatusCounts,
  toolSourceLabel
} = utils;

const catalogPageSize = 20;
const artifactPreviewTextLimit = 10240;
const inspectorManualMarkdown = __INSPECTOR_MANUAL_MARKDOWN__;

function modelToolName(identifier, apiName) {
  const normalize = (value) => String(value || "").trim().replace(/[^a-zA-Z0-9_]/g, "_");
  return [normalize(identifier), normalize(apiName)].filter(Boolean).join("_");
}

function isAbortError(error) {
  return error?.name === "AbortError";
}

function softFail(promise, fallback) {
  return promise.catch((error) => {
    if (isAbortError(error)) throw error;
    return typeof fallback === "function" ? fallback(error) : fallback;
  });
}

function mergeCounts(previous = {}, next = {}) {
  const merged = { ...previous };
  Object.entries(next).forEach(([key, value]) => {
    merged[key] = Number(merged[key] || 0) + Number(value || 0);
  });
  return merged;
}

function inspectorHashParams() {
  return new URLSearchParams(String(window.location.hash || "").replace(/^#/, ""));
}

function setInspectorHash(values) {
  const params = inspectorHashParams();
  Object.entries(values || {}).forEach(([key, raw]) => {
    const value = String(raw || "").trim();
    if (value) params.set(key, value);
    else params.delete(key);
  });
  const next = params.toString();
  const url = `${window.location.pathname}${window.location.search}${next ? `#${next}` : ""}`;
  if (window.location.href !== `${window.location.origin}${url}`) {
    window.history.replaceState(null, "", url);
  }
}

function Empty({ children }) {
  return <span className="empty">{children}</span>;
}

function Panel({ title, children, className = "" }) {
  return (
    <section className={`panel ${className}`.trim()}>
      <h2>{title}</h2>
      <div className="content">{children}</div>
    </section>
  );
}

function Field({ label, children }) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
    </div>
  );
}

function Pill({ value }) {
  return <span className={pillClass(value || "unknown")}>{value || "unknown"}</span>;
}

function Meta({ children }) {
  return <div className="meta">{children}</div>;
}

function ManualDialog({ onClose }) {
  const manualHTML = useMemo(() => marked.parse(inspectorManualMarkdown, { gfm: true }), []);

  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    const handleKeyDown = (event) => {
      if (event.key === "Escape") onClose();
    };
    document.body.style.overflow = "hidden";
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [onClose]);

  return (
    <div className="manual-backdrop" role="presentation" onMouseDown={(event) => {
      if (event.target === event.currentTarget) onClose();
    }}>
      <section className="manual-dialog" role="dialog" aria-modal="true" aria-labelledby="manualTitle">
        <div className="manual-header">
          <div>
            <div className="manual-eyebrow">TMA Inspector</div>
            <h2 id="manualTitle">用户使用手册</h2>
          </div>
          <button className="secondary manual-close" type="button" onClick={onClose} aria-label="关闭用户手册" title="关闭用户手册">Close</button>
        </div>
        <article className="manual-body" dangerouslySetInnerHTML={{ __html: manualHTML }} />
      </section>
    </div>
  );
}

function ToolSourceChip({ source }) {
  const label = toolSourceLabel(source);
  if (!label) return null;
  return <span className={`source-chip ${String(source || "").trim().toLowerCase()}`}>{label}</span>;
}

function MCPDetails({ data }) {
  const badges = mcpDiagnosticBadges(data);
  const summary = mcpResultSummary(data);
  if (!badges.length && !summary) return null;
  return (
    <div className="mcp-details-wrap">
      {badges.length ? (
        <div className="mcp-details">
          {badges.map((badge) => <span className="mcp-badge" key={badge}>{badge}</span>)}
        </div>
      ) : null}
      {summary ? (
        <div className="mcp-result-card">
          <strong>{summary.title}</strong>
          <div className="mcp-result-facts">
            {summary.facts.map((fact) => <span key={fact}>{fact}</span>)}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function MCPProtocol({ operations }) {
  if (!operations.length) return <div id="mcpProtocol"><Empty>No MCP protocol operations loaded.</Empty></div>;
  const completed = operations.filter((operation) => operation.status === "completed").length;
  const failed = operations.filter((operation) => operation.status === "failed").length;
  const pending = operations.filter((operation) => operation.status === "pending").length;
  const transports = Array.from(new Set(operations.map((operation) => operation.transport).filter(Boolean)));

  return (
    <div className="mcp-protocol" id="mcpProtocol">
      <div className="mcp-protocol-summary">
        <div><strong>{operations.length}</strong><span>operations</span></div>
        <div><strong>{completed}</strong><span>completed</span></div>
        <div><strong>{failed}</strong><span>failed</span></div>
        <div><strong>{pending}</strong><span>pending</span></div>
      </div>
      <Meta>
        <span>transports {transports.join(", ") || "unknown"}</span>
        <span>payload values redacted</span>
      </Meta>
      <div className="mcp-operation-list">
        {operations.map((operation) => {
          const toolName = modelToolName(operation.identifier, operation.api_name) || "unpaired MCP operation";
          return (
            <article className={`mcp-operation ${operation.status}`} key={operation.key}>
              <div className="mcp-operation-head">
                <div>
                  <div className="mcp-operation-method">{operation.method}</div>
                  <strong>{toolName}</strong>
                </div>
                <Pill value={operation.status} />
              </div>
              <Meta>
                {operation.call_id ? <span>call {operation.call_id}</span> : null}
                {operation.transport ? <span>{operation.transport}</span> : null}
                {operation.protocol_version ? <span>protocol {operation.protocol_version}</span> : null}
                {operation.duration_ms !== null ? <span>{formatDuration(operation.duration_ms)}</span> : null}
              </Meta>
              <div className="mcp-operation-lifecycle">
                <div className="mcp-operation-node">
                  <span>Request</span>
                  <strong>{operation.request_seq ? `seq ${operation.request_seq}` : "not observed"}</strong>
                  <small>{formatTime(operation.request_time)}</small>
                </div>
                <div className="mcp-operation-link" aria-hidden="true" />
                <div className={`mcp-operation-node ${operation.response_seq ? "observed" : "pending"}`}>
                  <span>Response</span>
                  <strong>{operation.response_seq ? `seq ${operation.response_seq}` : "pending"}</strong>
                  <small>{formatTime(operation.response_time)}</small>
                </div>
              </div>
              {operation.diagnostics.length ? (
                <div className="mcp-details">
                  {operation.diagnostics.map((badge) => <span className="mcp-badge" key={badge}>{badge}</span>)}
                </div>
              ) : null}
              <div className="mcp-operation-facts">
                {operation.result_protocol ? <span>result {operation.result_protocol}</span> : null}
                {operation.error_type ? <span>error {operation.error_type}</span> : null}
                {operation.artifact_count ? <span>{operation.artifact_count} artifact(s)</span> : null}
                {operation.preview_truncated ? <span>preview truncated</span> : null}
              </div>
              {operation.result_summary ? (
                <div className="mcp-result-card">
                  <strong>{operation.result_summary.title}</strong>
                  <div className="mcp-result-facts">
                    {operation.result_summary.facts.map((fact) => <span key={fact}>{fact}</span>)}
                  </div>
                </div>
              ) : null}
            </article>
          );
        })}
      </div>
    </div>
  );
}

function StatCard({ label, value, sub }) {
  return (
    <div className="stat-card">
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
      <div className="stat-sub">{sub}</div>
    </div>
  );
}

function App() {
  const [status, setStatus] = useState("idle");
  const [manualOpen, setManualOpen] = useState(false);
  const [agentID, setAgentID] = useState("");
  const [agentCatalog, setAgentCatalog] = useState({ agents: [] });
  const [sessionCatalog, setSessionCatalog] = useState({ sessions: [] });
  const [selectionCatalogLoading, setSelectionCatalogLoading] = useState(true);
  const [sessionID, setSessionID] = useState("");
  const [traceID, setTraceID] = useState("");
  const [turnID, setTurnID] = useState("");
  const [format, setFormat] = useState("json");
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [globalSpanQuery, setGlobalSpanQuery] = useState("");
  const [globalSpanKind, setGlobalSpanKind] = useState("");
  const [globalSpanStatus, setGlobalSpanStatus] = useState("");
  const [globalSpanCritical, setGlobalSpanCritical] = useState("");
  const [globalSpanMinDuration, setGlobalSpanMinDuration] = useState("");
  const [spanFilter, setSpanFilter] = useState("");
  const [spanKind, setSpanKind] = useState("");
  const [selectedSpanID, setSelectedSpanID] = useState("");
  const [currentTrace, setCurrentTrace] = useState(null);
  const [traceCatalog, setTraceCatalog] = useState({ traces: [] });
  const [spanCatalog, setSpanCatalog] = useState({});
  const [sessionMeta, setSessionMeta] = useState(null);
  const [usage, setUsage] = useState(null);
  const [summary, setSummary] = useState(null);
  const [taskPlans, setTaskPlans] = useState({ plans: [] });
  const [artifacts, setArtifacts] = useState({ artifacts: [] });
  const [events, setEvents] = useState({ events: [] });
  const [interventions, setInterventions] = useState({ interventions: [] });
  const [metrics, setMetrics] = useState("No metrics loaded.");
  const [exporters, setExporters] = useState(null);
  const [raw, setRaw] = useState("No raw export loaded.");
  const [artifactPreview, setArtifactPreview] = useState(null);
  const [toolSourceFilter, setToolSourceFilter] = useState("");
  const bootingFromHash = useRef(false);
  const selectionRequestRef = useRef(0);
  const loadRequestRef = useRef(null);
  const eventsRef = useRef({ events: [] });
  const eventsSessionIDRef = useRef("");

  const spans = currentTrace?.spans || [];
  const agentSessions = useMemo(
    () => (sessionCatalog.sessions || []).filter((session) => session.agent_id === agentID),
    [agentID, sessionCatalog.sessions]
  );
  const selectedSpan = useMemo(
    () => spans.find((span) => span.span_id === selectedSpanID) || null,
    [spans, selectedSpanID]
  );
  const availableSpanKinds = useMemo(() => {
    return Array.from(new Set(spans.map((span) => span.kind).filter(Boolean))).sort();
  }, [spans]);
  const completionQuality = useMemo(() => completionQualitySummary(metrics), [metrics]);

  const syncInspectorHash = useCallback((overrides = {}) => {
    setInspectorHash({
      session: overrides.session ?? sessionID,
      turn: overrides.turn ?? turnID,
      trace: overrides.trace ?? currentTrace?.trace_id ?? traceID,
      span: overrides.span ?? selectedSpanID
    });
  }, [currentTrace?.trace_id, selectedSpanID, sessionID, traceID, turnID]);

  const renderTrace = useCallback((trace) => {
    const nextSpans = trace.spans || [];
    setCurrentTrace(trace);
    setTraceID(trace.trace_id || "");
    setTurnID(trace.turn_id || "");
    setSelectedSpanID((previous) => {
      if (previous && nextSpans.some((span) => span.span_id === previous)) return previous;
      return nextSpans[0]?.span_id || "";
    });
    setRaw(pretty(trace));
  }, []);

  async function loadTraceCatalog(nextSession = sessionID, nextTurn = turnID, cursor = "", append = false, selectionRequest = 0) {
    if (!nextSession) {
      const empty = { traces: [], next_cursor: "", has_more: false };
      setTraceCatalog(empty);
      return empty;
    }
    const response = await inspectorAPI.traceCatalog({ limit: catalogPageSize, cursor, session: nextSession, turn: nextTurn });
    if (selectionRequest && selectionRequestRef.current !== selectionRequest) return response;
    setTraceCatalog((previous) => append ? { ...response, traces: [...(previous.traces || []), ...(response.traces || [])] } : response);
    return response;
  }

  async function loadSpanCatalog(cursor = "", append = false, nextSession = sessionID, nextTurn = turnID, selectionRequest = 0) {
    if (!nextSession) {
      const empty = { spans: [], next_cursor: "", has_more: false };
      setSpanCatalog(empty);
      return empty;
    }
    const response = await inspectorAPI.spanCatalog({
      limit: catalogPageSize,
      cursor,
      session: nextSession,
      turn: nextTurn,
      query: globalSpanQuery.trim(),
      kind: globalSpanKind.trim(),
      status: globalSpanStatus.trim(),
      critical: globalSpanCritical.trim(),
      minDuration: globalSpanMinDuration.trim()
    });
    if (selectionRequest && selectionRequestRef.current !== selectionRequest) return response;
    setSpanCatalog((previous) => append ? {
      ...response,
      spans: [...(previous.spans || []), ...(response.spans || [])],
      kind_counts: mergeCounts(previous.kind_counts, response.kind_counts),
      status_counts: mergeCounts(previous.status_counts, response.status_counts),
      critical_counts: mergeCounts(previous.critical_counts, response.critical_counts)
    } : response);
    return response;
  }

  async function filterCatalogs(nextSession = sessionID, nextTurn = turnID, selectionRequest = 0) {
    if (!nextSession) {
      setTraceCatalog({ traces: [] });
      setSpanCatalog({ spans: [] });
      setStatus("select a session first");
      return;
    }
    setSelectedSpanID("");
    setInspectorHash({ session: nextSession, turn: nextTurn, trace: "", span: "" });
    await Promise.all([
      loadTraceCatalog(nextSession, nextTurn, "", false, selectionRequest),
      loadSpanCatalog("", false, nextSession, nextTurn, selectionRequest)
    ]);
    if (selectionRequest && selectionRequestRef.current !== selectionRequest) return;
    setStatus(`loaded trace catalogs for ${nextSession}`);
  }

  function clearInspectionResults() {
    if (loadRequestRef.current) loadRequestRef.current.abort();
    setTurnID("");
    setTraceID("");
    setSelectedSpanID("");
    setCurrentTrace(null);
    setTraceCatalog({ traces: [] });
    setSpanCatalog({ spans: [] });
    setSessionMeta(null);
    setUsage(null);
    setSummary(null);
    setTaskPlans({ plans: [] });
    setArtifacts({ artifacts: [] });
    setEvents({ events: [] });
    eventsRef.current = { events: [] };
    eventsSessionIDRef.current = "";
    setInterventions({ interventions: [] });
    setMetrics("No metrics loaded.");
    setExporters(null);
    setRaw("No raw export loaded.");
    setArtifactPreview(null);
    setAutoRefresh(false);
  }

  function selectAgent(nextAgentID) {
    selectionRequestRef.current += 1;
    clearInspectionResults();
    setAgentID(nextAgentID);
    setSessionID("");
    setInspectorHash({ session: "", turn: "", trace: "", span: "" });
    setStatus(nextAgentID ? "select a session" : "select an agent");
  }

  async function selectSession(nextSessionID) {
    const selectionRequest = selectionRequestRef.current + 1;
    selectionRequestRef.current = selectionRequest;
    clearInspectionResults();
    setSessionID(nextSessionID);
    setInspectorHash({ session: nextSessionID, turn: "", trace: "", span: "" });
    if (!nextSessionID) {
      setStatus("select a session");
      return;
    }
    await filterCatalogs(nextSessionID, "", selectionRequest);
    if (selectionRequestRef.current !== selectionRequest) return;
    await load(nextSessionID, "");
  }

  async function load(nextSession = sessionID, nextTurn = turnID, options = {}) {
    if (!nextSession) {
      setStatus("session required");
      return null;
    }

    const mode = options.mode || "manual";
    if (mode === "auto" && loadRequestRef.current) return null;
    if (loadRequestRef.current) loadRequestRef.current.abort();

    const controller = new AbortController();
    loadRequestRef.current = controller;
    const requestOptions = { signal: controller.signal };
    const incrementalEvents = Boolean(options.incrementalEvents) && eventsSessionIDRef.current === nextSession;
    const afterSeq = incrementalEvents ? highestEventSeq(eventsRef.current.events) : 0;

    setStatus(`loading ${nextSession}`);
    try {
      const trace = await inspectorAPI.trace(nextSession, nextTurn, "", requestOptions);
      const requests = [
        inspectorAPI.session(nextSession, requestOptions),
        softFail(inspectorAPI.usage(nextSession, requestOptions), (error) => ({ error: String(error) })),
        softFail(inspectorAPI.summary(nextSession, requestOptions), { summary_text: "", source_until_seq: 0 }),
        softFail(inspectorAPI.artifacts(nextSession, requestOptions), (error) => ({ artifacts: [], error: String(error) })),
        softFail(inspectorAPI.events(nextSession, afterSeq, requestOptions), (error) => ({ events: [], error: String(error) })),
        softFail(inspectorAPI.interventions(nextSession, "pending", requestOptions), (error) => ({ interventions: [], error: String(error) })),
        softFail(inspectorAPI.metrics(nextSession, nextTurn, requestOptions), (error) => String(error)),
        softFail(inspectorAPI.observabilityStatus(requestOptions), (error) => ({ error: String(error) })),
        softFail(inspectorAPI.taskPlans(nextSession, requestOptions), (error) => ({ plans: [], error: String(error) }))
      ];
      const results = await Promise.all(requests);
      if (controller.signal.aborted || loadRequestRef.current !== controller) return null;

      const nextEvents = incrementalEvents ? mergeEventResponses(eventsRef.current, results[4]) : results[4];
      renderTrace(trace);
      setSessionMeta(results[0]);
      setAgentID(results[0]?.agent_id || "");
      setSessionCatalog((previous) => (previous.sessions || []).some((session) => session.id === results[0]?.id)
        ? previous
        : { sessions: [results[0], ...(previous.sessions || [])].filter(Boolean) });
      setUsage(results[1]);
      setSummary(results[2]);
      setArtifacts(results[3]);
      setEvents(nextEvents);
      eventsRef.current = nextEvents;
      eventsSessionIDRef.current = nextSession;
      setInterventions(results[5]);
      setMetrics(results[6]);
      setExporters(results[7]);
      setTaskPlans(results[8]);

      const terminal = isTerminalTurnStatus(trace.status);
      if (terminal) setAutoRefresh(false);
      const stopped = terminal && autoRefresh ? `; auto refresh stopped (${trace.status})` : "";
      setStatus(`loaded ${nextSession} / ${trace.turn_id || "latest"}${stopped}`);
      setInspectorHash({ session: nextSession, turn: trace.turn_id || nextTurn, trace: trace.trace_id || "", span: "" });
      return trace;
    } catch (error) {
      if (isAbortError(error)) return null;
      throw error;
    } finally {
      if (loadRequestRef.current === controller) loadRequestRef.current = null;
    }
  }

  async function loadTraceByID(nextTraceID = traceID) {
    const selectionRequest = selectionRequestRef.current + 1;
    selectionRequestRef.current = selectionRequest;
    const value = String(nextTraceID || "").trim();
    if (!value) {
      setStatus("trace id required");
      return;
    }
    if (loadRequestRef.current) loadRequestRef.current.abort();
    setStatus(`loading trace ${value}`);
    const trace = await inspectorAPI.traceByID(value);
    if (selectionRequestRef.current !== selectionRequest) return;
    setTraceID(trace.trace_id || value);
    setSessionID(trace.session_id || "");
    setTurnID(trace.turn_id || "");
    await filterCatalogs(trace.session_id || "", "", selectionRequest);
    if (selectionRequestRef.current !== selectionRequest) return;
    await load(trace.session_id || "", trace.turn_id || "");
  }

  async function loadSpanByID(nextTraceID, nextSpanID) {
    const selectionRequest = selectionRequestRef.current + 1;
    selectionRequestRef.current = selectionRequest;
    const traceValue = String(nextTraceID || "").trim();
    const spanValue = String(nextSpanID || "").trim();
    if (!traceValue || !spanValue) {
      setStatus("trace id and span id required");
      return;
    }
    if (loadRequestRef.current) loadRequestRef.current.abort();
    setStatus(`loading span ${spanValue}`);
    const detail = await inspectorAPI.spanByID(traceValue, spanValue);
    if (selectionRequestRef.current !== selectionRequest) return;
    setSelectedSpanID(detail.span?.span_id || spanValue);
    setTraceID(detail.trace_id || traceValue);
    setSessionID(detail.session_id || "");
    setTurnID(detail.turn_id || "");
    await filterCatalogs(detail.session_id || "", "", selectionRequest);
    if (selectionRequestRef.current !== selectionRequest) return;
    await load(detail.session_id || "", detail.turn_id || "");
    setStatus(`loaded span ${detail.span?.span_id || spanValue}`);
  }

  async function exportTrace(download) {
    if (!sessionID) {
      setStatus("session required");
      return;
    }
    const data = await inspectorAPI.trace(sessionID, turnID, format);
    const text = pretty(data);
    setRaw(text);
    if (download) {
      const filename = [sessionID, turnID || "latest", format].join("-") + ".json";
      const blob = new Blob([text], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = filename;
      anchor.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    }
    setStatus(`${download ? "downloaded" : "previewed"} ${format}`);
  }

  async function copyText(text) {
    const value = String(text || "");
    if (!value) return;
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
    } else {
      const input = document.createElement("textarea");
      input.value = value;
      document.body.appendChild(input);
      input.select();
      document.execCommand("copy");
      input.remove();
    }
    setStatus("copied command");
  }

  async function previewArtifact(href, loadResponse) {
    const path = String(href || "");
    if (!path) return;
    setStatus("previewing artifact");
    const response = await (loadResponse ? loadResponse() : inspectorAPI.getBlob(path));
    const contentType = (response.headers.get("Content-Type") || "").split(";")[0].trim();
    const length = response.headers.get("Content-Length") || "";
    const blob = await response.blob();
    const base = { href: path, contentType: contentType || "application/octet-stream", size: length ? `${length} bytes` : `${blob.size} bytes` };
    if (contentType.startsWith("image/")) {
      setArtifactPreview({ ...base, kind: "image", url: URL.createObjectURL(blob) });
      setStatus("previewed artifact");
      return;
    }
    if (contentType === "application/json" || contentType.startsWith("text/") || contentType === "application/xml" || contentType === "application/yaml") {
      let text = await blob.text();
      if (contentType === "application/json") {
        try {
          text = JSON.stringify(JSON.parse(text), null, 2);
        } catch {
          // Keep the original text when the payload is not valid JSON.
        }
      }
      const truncated = text.length > artifactPreviewTextLimit;
      setArtifactPreview({
        ...base,
        kind: "text",
        text: truncated ? `${text.slice(0, artifactPreviewTextLimit)}\n... truncated ...` : text,
        truncated,
        originalChars: text.length,
        visibleChars: truncated ? artifactPreviewTextLimit : text.length
      });
      setStatus("previewed artifact");
      return;
    }
    setArtifactPreview({ ...base, kind: "binary" });
    setStatus("previewed artifact metadata");
  }

  async function retryExporters() {
    setStatus("retrying exporters");
    const result = await inspectorAPI.retryObservability();
    await load();
    setStatus(`retry attempted ${result.attempted || 0}, succeeded ${result.succeeded || 0}, failed ${result.failed || 0}`);
  }

  async function approveIntervention(intervention) {
    setStatus("approving");
    await inspectorAPI.approveIntervention(sessionID, intervention.turn_id, intervention.call_id, { reason: "approved from inspector" });
    await load();
    setStatus("approved");
  }

  async function rejectIntervention(intervention) {
    const reason = window.prompt("Reject reason", "rejected from inspector");
    if (reason === null) return;
    setStatus("rejecting");
    await inspectorAPI.rejectIntervention(sessionID, intervention.turn_id, intervention.call_id, { reason });
    await load();
    setStatus("rejected");
  }

  const bootInspectorFromHash = useCallback(async () => {
    if (bootingFromHash.current) return;
    bootingFromHash.current = true;
    setSelectionCatalogLoading(true);
    try {
      const params = inspectorHashParams();
      const nextSession = params.get("session") || "";
      const nextTurn = params.get("turn") || "";
      const nextTrace = params.get("trace") || "";
      const nextSpan = params.get("span") || "";
      const [agentsResponse, sessionsResponse] = await Promise.all([
        inspectorAPI.agents(),
        inspectorAPI.sessions()
      ]);
      setAgentCatalog(agentsResponse);
      setSessionCatalog(sessionsResponse);
      setSessionID(nextSession);
      setAgentID((sessionsResponse.sessions || []).find((session) => session.id === nextSession)?.agent_id || "");
      setTurnID(nextTurn);
      setTraceID(nextTrace);
      setSelectedSpanID(nextSpan);
      if (nextTrace) {
        await loadTraceByID(nextTrace);
        return;
      }
      if (nextSession) {
        await filterCatalogs(nextSession, "");
        await load(nextSession, nextTurn);
        return;
      }
      setTraceCatalog({ traces: [] });
      setSpanCatalog({ spans: [] });
      setInspectorHash({ session: nextSession, turn: nextTurn, trace: nextTrace, span: nextSpan });
      setStatus((agentsResponse.agents || []).length ? "select an agent" : "no agents available");
    } finally {
      setSelectionCatalogLoading(false);
      bootingFromHash.current = false;
    }
  }, []);

  useEffect(() => {
    bootInspectorFromHash().catch((error) => setStatus(error.message));
    const handler = () => bootInspectorFromHash().catch((error) => setStatus(error.message));
    window.addEventListener("hashchange", handler);
    return () => window.removeEventListener("hashchange", handler);
  }, [bootInspectorFromHash]);

  useEffect(() => {
    if (!autoRefresh) return undefined;
    if (!sessionID || isTerminalTurnStatus(currentTrace?.status)) return undefined;

    const refresh = () => {
      if (document.hidden) return;
      load(sessionID, turnID, { mode: "auto", incrementalEvents: true }).catch((error) => setStatus(error.message));
    };
    const handle = window.setInterval(refresh, 5000);
    const handleVisibilityChange = () => {
      if (!document.hidden) refresh();
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      window.clearInterval(handle);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [autoRefresh, sessionID, turnID, currentTrace?.status]);

  useEffect(() => {
    return () => {
      if (loadRequestRef.current) loadRequestRef.current.abort();
    };
  }, []);

  useEffect(() => {
    syncInspectorHash();
  }, [selectedSpanID]);

  const filteredSpans = useMemo(() => {
    const query = spanFilter.trim().toLowerCase();
    return spans.filter((span) => {
      if (spanKind && span.kind !== spanKind) return false;
      if (!query) return true;
      const attrs = Object.entries(span.attributes || {}).map(([key, value]) => `${key} ${value}`).join(" ");
      const eventText = (span.events || []).map((event) => [event.seq, event.type, event.name, event.message, event.summary].join(" ")).join(" ");
      return [span.name, span.span_id, span.parent_span_id, span.kind, span.status, attrs, eventText].join(" ").toLowerCase().includes(query);
    });
  }, [spanFilter, spanKind, spans]);

  const traceStats = currentTrace?.stats || {};
  const terminalTurn = isTerminalTurnStatus(currentTrace?.status);
  const toolSourceStats = useMemo(() => collectToolSourceStats(events.events || []), [events]);
  const mcpProtocolOperations = useMemo(() => collectMCPProtocolOperations(events.events || []), [events]);
  const filteredTimelineTrace = useMemo(() => {
    if (!toolSourceFilter) return currentTrace;
    if (!currentTrace) return currentTrace;
    const filteredSteps = (currentTrace.steps || []).filter((step) => normalizeToolSource(step?.data?.tool_source) === toolSourceFilter);
    return { ...currentTrace, steps: filteredSteps };
  }, [currentTrace, toolSourceFilter]);
  const filteredRecentEvents = useMemo(() => {
    const list = events.events || [];
    if (!toolSourceFilter) return list;
    return list.filter((event) => normalizeToolSource(event?.payload?.data?.tool_source) === toolSourceFilter);
  }, [events, toolSourceFilter]);
  const overviewCards = [
    { label: "Turn", value: currentTrace?.turn_id || "-", sub: currentTrace?.status || "running" },
    { label: "Duration", value: formatDuration(traceStats.duration_ms), sub: `${formatTime(traceStats.start_time)} -> ${formatTime(traceStats.end_time)}` },
    { label: "Steps", value: String(traceStats.step_count || 0), sub: "timeline events" },
    { label: "Spans", value: String(traceStats.span_count || 0), sub: "projected trace spans" },
    { label: "Tools", value: String(traceStats.tool_calls || 0), sub: `${traceStats.approval_waits || 0} approval waits` },
    { label: "MCP", value: String(toolSourceStats.mcp || 0), sub: `${toolSourceStats.total || 0} sourced tool events` },
    { label: "Errors", value: String(traceStats.errors || 0), sub: `${traceStats.artifact_count || 0} artifacts` }
  ];

  return (
    <>
      <header>
        <h1>TMA Inspector</h1>
        <div className="header-actions">
          <div className="meta" id="status">{status}</div>
          <button className="secondary manual-trigger" type="button" onClick={() => setManualOpen(true)}>用户手册</button>
        </div>
      </header>
      {manualOpen ? <ManualDialog onClose={() => setManualOpen(false)} /> : null}
      <div className="layout">
        <aside>
          <Panel title="Query">
            <div className="toolbar inspector-query-flow">
              <Field label="1. Agent">
                <select id="agent" value={agentID} disabled={selectionCatalogLoading} onChange={(event) => selectAgent(event.target.value)}>
                  <option value="">{selectionCatalogLoading ? "Loading agents..." : "Select an agent"}</option>
                  {agentID && !(agentCatalog.agents || []).some((agent) => agent.id === agentID) ? <option value={agentID}>{agentID}</option> : null}
                  {(agentCatalog.agents || []).map((agent) => (
                    <option key={agent.id} value={agent.id}>{agent.name || agent.id}</option>
                  ))}
                </select>
              </Field>
              <Field label="2. Session">
                <select id="session" value={sessionID} disabled={!agentID || selectionCatalogLoading} onChange={(event) => selectSession(event.target.value).catch((error) => setStatus(error.message))}>
                  <option value="">{agentID ? "Select a session" : "Select an agent first"}</option>
                  {sessionID && !agentSessions.some((session) => session.id === sessionID) ? <option value={sessionID}>{sessionID}</option> : null}
                  {agentSessions.map((session) => (
                    <option key={session.id} value={session.id}>
                      {session.title || session.id} ({session.status || "unknown"})
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="3. Trace">
                <select id="traceId" value={traceID} disabled={!sessionID || !(traceCatalog.traces || []).length} onChange={(event) => {
                  const value = event.target.value;
                  setTraceID(value);
                  if (value) loadTraceByID(value).catch((error) => setStatus(error.message));
                }}>
                  <option value="">{sessionID ? "Select a trace" : "Select a session first"}</option>
                  {traceID && !(traceCatalog.traces || []).some((trace) => trace.trace_id === traceID) ? <option value={traceID}>{traceID}</option> : null}
                  {(traceCatalog.traces || []).map((trace) => (
                    <option key={trace.trace_id} value={trace.trace_id}>
                      {trace.turn_id || trace.trace_id} ({trace.turn_status || "running"})
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="Turn">
                <select id="turn" value={turnID} onChange={(event) => {
                  const value = event.target.value;
                  setTurnID(value);
                  if (sessionID) load(sessionID, value).catch((error) => setStatus(error.message));
                }} disabled={!sessionID}>
                  <option value="">latest</option>
                  {(currentTrace?.turns || []).map((turn) => (
                    <option key={turn.turn_id} value={turn.turn_id}>{turn.turn_id} ({turn.status || "running"})</option>
                  ))}
                </select>
              </Field>
              <Field label="Export Format">
                <select id="format" value={format} onChange={(event) => setFormat(event.target.value)}>
                  <option value="json">Trace JSON</option>
                  <option value="perfetto">Perfetto JSON</option>
                  <option value="otel">OTel JSON</option>
                </select>
              </Field>
              <label className="toggle">
                <input type="checkbox" id="autoRefresh" checked={autoRefresh} disabled={!sessionID || terminalTurn} onChange={(event) => setAutoRefresh(event.target.checked)} />
                Auto refresh every 5s
              </label>
              <div className="actions">
                <button id="load" disabled={!sessionID} onClick={() => selectSession(sessionID).catch((error) => setStatus(error.message))}>Refresh</button>
                <button className="secondary" id="export" disabled={!sessionID} onClick={() => exportTrace(false).catch((error) => setStatus(error.message))}>Preview Export</button>
                <button className="secondary" id="download" disabled={!sessionID} onClick={() => exportTrace(true).catch((error) => setStatus(error.message))}>Download</button>
              </div>
            </div>
          </Panel>
          <RecentTraces traces={traceCatalog.traces || []} activeSession={sessionID} activeTurn={turnID} hasMore={Boolean(traceCatalog.has_more)} onMore={() => loadTraceCatalog(sessionID, "", traceCatalog.next_cursor || "", true).catch((error) => setStatus(error.message))} onLoadTrace={(id) => loadTraceByID(id).catch((error) => setStatus(error.message))} />
          <SpanSearch
            disabled={!sessionID}
            query={globalSpanQuery}
            kind={globalSpanKind}
            status={globalSpanStatus}
            critical={globalSpanCritical}
            minDuration={globalSpanMinDuration}
            response={spanCatalog}
            onChange={{ setGlobalSpanQuery, setGlobalSpanKind, setGlobalSpanStatus, setGlobalSpanCritical, setGlobalSpanMinDuration }}
            onSearch={() => loadSpanCatalog().catch((error) => setStatus(error.message))}
            onMore={() => loadSpanCatalog(spanCatalog.next_cursor || "", true).catch((error) => setStatus(error.message))}
            onLoadSpan={(trace, span) => loadSpanByID(trace, span).catch((error) => setStatus(error.message))}
          />
          <Turns turns={currentTrace?.turns || []} active={turnID} onSelect={(id) => {
            setTurnID(id);
            load(sessionID, id).catch((error) => setStatus(error.message));
          }} />
        </aside>
        <main>
          <section className="overview" id="overviewCards">
            {overviewCards.map((card) => <StatCard key={card.label} {...card} />)}
          </section>
          <section className="split">
            <Panel title="Session"><SessionMeta session={sessionMeta} /></Panel>
            <Panel title="Trace Summary"><TraceSummary trace={currentTrace} /></Panel>
          </section>
          <section className="split">
            <Panel title="Context Summary"><div className="summary" id="sessionSummary">{summary?.summary_text || "No session summary loaded."}</div></Panel>
            <Panel title="Usage"><Usage usage={usage} /></Panel>
          </section>
          <Panel title="Plan History"><PlanHistory response={taskPlans} /></Panel>
          <Panel title="Context Coverage"><ContextCoverage summary={summary} events={events} /></Panel>
          <Panel title="Context Budget"><ContextBudget trace={currentTrace} events={events} /></Panel>
          <Panel title="Tool Sources">
            <div className="stack">
              <div className="coverage-grid">
                {[
                  ["mcp", toolSourceStats.mcp],
                  ["worker_plugin", toolSourceStats.worker_plugin],
                  ["builtin", toolSourceStats.builtin],
                  ["other", toolSourceStats.other]
                ].map(([label, value]) => (
                  <div className="coverage-card" key={label}>
                    <div className="subtle">{label}</div>
                    <div className="coverage-value">{value || 0}</div>
                  </div>
                ))}
              </div>
              <div className="actions">
                <button className={toolSourceFilter === "" ? "" : "secondary"} type="button" onClick={() => setToolSourceFilter("")}>All</button>
                <button className={toolSourceFilter === "mcp" ? "" : "secondary"} type="button" onClick={() => setToolSourceFilter("mcp")}>MCP</button>
                <button className={toolSourceFilter === "worker_plugin" ? "" : "secondary"} type="button" onClick={() => setToolSourceFilter("worker_plugin")}>Worker Plugin</button>
                <button className={toolSourceFilter === "builtin" ? "" : "secondary"} type="button" onClick={() => setToolSourceFilter("builtin")}>Builtin</button>
              </div>
              <Meta>
                <span>active filter {toolSourceFilter || "all"}</span>
                <span>{toolSourceFilter ? `${filteredRecentEvents.length} matching recent events` : `${events.events?.length || 0} recent events`}</span>
              </Meta>
            </div>
          </Panel>
          <Panel title="MCP Protocol"><MCPProtocol operations={mcpProtocolOperations} /></Panel>
          <Panel title="Waterfall"><Waterfall trace={currentTrace} selectedSpanID={selectedSpanID} onSelect={(id) => setSelectedSpanID(id)} /></Panel>
          <Panel title="Spans">
            <div className="stack">
              <div className="span-controls">
                <Field label="Filter">
                  <input id="spanFilter" value={spanFilter} onChange={(event) => setSpanFilter(event.target.value)} autoComplete="off" placeholder="name, id, attribute, event" />
                </Field>
                <Field label="Kind">
                  <select id="spanKind" value={spanKind} onChange={(event) => setSpanKind(event.target.value)}>
                    <option value="">all kinds</option>
                    {availableSpanKinds.map((kind) => <option key={kind} value={kind}>{kind}</option>)}
                  </select>
                </Field>
                <button className="secondary" id="clearSpanFilter" type="button" onClick={() => { setSpanFilter(""); setSpanKind(""); }}>Clear</button>
              </div>
              <SpanDetail span={selectedSpan || filteredSpans[0] || spans[0]} onSelect={(id) => setSelectedSpanID(id)} />
              <SpansTable spans={filteredSpans} selectedSpanID={selectedSpanID} onSelect={(id) => setSelectedSpanID(id)} />
            </div>
          </Panel>
          <Panel title="Timeline"><Timeline trace={filteredTimelineTrace} onPreview={previewArtifact} onCopy={copyText} /></Panel>
          <section className="triple">
            <Panel title="Pending Approvals"><Interventions interventions={interventions.interventions || []} onApprove={approveIntervention} onReject={rejectIntervention} /></Panel>
            <Panel title="Artifacts"><Artifacts sessionID={sessionID} artifacts={artifacts.artifacts || []} onPreview={previewArtifact} onCopy={copyText} /></Panel>
            <Panel title="Recent Events"><RecentEvents events={filteredRecentEvents} /></Panel>
          </section>
          <Panel title="Artifact Preview"><ArtifactPreview preview={artifactPreview} /></Panel>
          <section className="triple">
            <Panel title="Completion Quality"><CompletionQuality summary={completionQuality} /></Panel>
            <Panel title="Exporters"><Exporters response={exporters} onRetry={() => retryExporters().catch((error) => setStatus(error.message))} /></Panel>
            <Panel title="Metrics"><pre id="metrics" className="code">{metrics}</pre></Panel>
          </section>
          <Panel title="Raw Export"><pre id="raw" className="code">{raw}</pre></Panel>
        </main>
      </div>
    </>
  );
}

function CompletionQuality({ summary }) {
  if (!summary?.attempts) return <div className="empty">No completion validation attempts for this turn.</div>;
  const retryPercent = Math.round(summary.retryRate * 100);
  const status = summary.fail > 0 ? "failed" : summary.retry > 0 ? "blocked" : "completed";
  return (
    <div className="completion-quality">
      <div className="completion-quality-head">
        <Pill value={status} />
        <span>{summary.attempts} validation attempt(s)</span>
      </div>
      <dl className="completion-quality-grid">
        <div><dt>Passed</dt><dd>{summary.pass}</dd></div>
        <div><dt>Retried</dt><dd>{summary.retry}</dd></div>
        <div><dt>Failed</dt><dd>{summary.fail}</dd></div>
        <div><dt>Retry rate</dt><dd>{retryPercent}%</dd></div>
      </dl>
      <div className="subtle">Validators: {summary.validators.join(", ") || "unknown"}</div>
    </div>
  );
}

function RecentTraces({ traces, activeSession, activeTurn, hasMore, onMore, onLoadTrace }) {
  return (
    <Panel title="Session Traces">
      <div className="stack">
        <div className="turn-list" id="traceCatalog">
          {traces.length ? traces.map((trace) => {
            const active = trace.session_id === activeSession && trace.turn_id === activeTurn;
            return (
              <button className={`turn-item ${active ? "active" : ""}`.trim()} type="button" data-trace-id={trace.trace_id || ""} key={trace.trace_id} onClick={() => onLoadTrace(trace.trace_id)}>
                <Meta><span>{trace.turn_id}</span><Pill value={trace.turn_status || "running"} /></Meta>
                <div className="subtle" style={{ marginTop: 6 }}>{trace.session_title || trace.session_id} | {formatDuration(trace.duration_ms)} | {trace.span_count || 0} spans</div>
                <div className="subtle">{trace.trace_id}</div>
                <div className="summary compact" style={{ marginTop: 8 }}>{trace.summary || "No summary."}</div>
              </button>
            );
          }) : <Empty>{activeSession ? "No traces found for this session." : "Select a session to load traces."}</Empty>}
        </div>
        {hasMore ? <button className="secondary" id="moreTraces" type="button" onClick={onMore}>Load more</button> : null}
      </div>
    </Panel>
  );
}

function SpanSearch({ disabled, query, kind, status, critical, minDuration, response, onChange, onSearch, onMore, onLoadSpan }) {
  const spans = response.spans || [];
  const aggregate = (entry, formatter = (key) => key) => entry ? Object.entries(entry).map(([key, value]) => `${formatter(key)}: ${value}`).join(" | ") : "";
  return (
    <Panel title="Session Span Search">
      <div className="stack">
        <Field label="Search">
          <input id="globalSpanQuery" value={query} disabled={disabled} onChange={(event) => onChange.setGlobalSpanQuery(event.target.value)} onKeyDown={(event) => {
            if (event.key === "Enter") onSearch();
          }} autoComplete="off" placeholder="name, id, attribute" />
        </Field>
        <div className="span-search-controls">
          <Field label="Kind">
            <select id="globalSpanKind" value={kind} disabled={disabled} onChange={(event) => onChange.setGlobalSpanKind(event.target.value)}>
              <option value="">all</option>
              <option value="interaction">interaction</option>
              <option value="llm">llm</option>
              <option value="tool">tool</option>
              <option value="approval">approval</option>
              <option value="context">context</option>
              <option value="event">event</option>
            </select>
          </Field>
          <Field label="Status">
            <input id="globalSpanStatus" value={status} disabled={disabled} onChange={(event) => onChange.setGlobalSpanStatus(event.target.value)} onKeyDown={(event) => {
              if (event.key === "Enter") onSearch();
            }} autoComplete="off" placeholder="ok, error, open" />
          </Field>
          <Field label="Critical">
            <select id="globalSpanCritical" value={critical} disabled={disabled} onChange={(event) => onChange.setGlobalSpanCritical(event.target.value)}>
              <option value="">all</option>
              <option value="true">critical</option>
              <option value="false">non-critical</option>
            </select>
          </Field>
          <Field label="Min Duration">
            <input id="globalSpanMinDuration" value={minDuration} disabled={disabled} onChange={(event) => onChange.setGlobalSpanMinDuration(event.target.value)} onKeyDown={(event) => {
              if (event.key === "Enter") onSearch();
            }} type="number" min="0" step="1" inputMode="numeric" placeholder="ms" />
          </Field>
        </div>
        <div className="actions"><button className="secondary" id="searchSpans" type="button" disabled={disabled} onClick={onSearch}>Search Spans</button></div>
        <div className="meta" id="spanAggregates">
          <span>{aggregate(response.kind_counts) || "no kind counts"}</span>
          <span>{aggregate(response.status_counts) || "no status counts"}</span>
          <span>{aggregate(response.critical_counts, (key) => key === "true" ? "critical" : "non-critical") || "no critical counts"}</span>
        </div>
        <div className="turn-list" id="spanCatalog">
          {spans.length ? spans.map((span) => (
            <button className="turn-item" type="button" key={`${span.trace_id}-${span.span_id}`} data-span-trace-id={span.trace_id || ""} data-span-id={span.span_id || ""} onClick={() => onLoadSpan(span.trace_id, span.span_id)}>
              <Meta>
                <span>{span.kind}</span>
                <Pill value={span.status || "unknown"} />
                <span>{formatDuration(span.duration_ms)}</span>
                {span.critical ? <Pill value="ok" /> : null}
              </Meta>
              <div><strong>{span.name || span.span_id}</strong></div>
              <div className="subtle">{[span.session_title || span.session_id, span.turn_id, span.span_id].filter(Boolean).join(" | ")}</div>
              <div className="subtle">depth {span.depth || 0} | self {formatDuration(span.self_duration_ms || 0)}</div>
            </button>
          )) : <Empty>{disabled ? "Select a session to search spans." : "No spans found for this session."}</Empty>}
        </div>
        {response.has_more ? <button className="secondary" id="moreSpans" type="button" onClick={onMore}>Load more</button> : null}
      </div>
    </Panel>
  );
}

function Turns({ turns, active, onSelect }) {
  return (
    <Panel title="Turns">
      <div className="turn-list" id="turns">
        {turns.length ? turns.map((turn) => (
          <button className={`turn-item ${turn.turn_id === active ? "active" : ""}`.trim()} type="button" data-turn={turn.turn_id} key={turn.turn_id} onClick={() => onSelect(turn.turn_id)}>
            <Meta><span>{turn.turn_id}</span><Pill value={turn.status || "running"} /></Meta>
            <div className="subtle" style={{ marginTop: 6 }}>{formatDuration(turn.duration_ms)} | {turn.step_count} steps | {turn.span_count} spans</div>
            <div className="summary compact" style={{ marginTop: 8 }}>{turn.summary || "No summary."}</div>
          </button>
        )) : <Empty>No turns loaded.</Empty>}
      </div>
    </Panel>
  );
}

function SessionMeta({ session }) {
  if (!session) return <Empty>No session loaded.</Empty>;
  return (
    <div className="stack" id="sessionMeta">
      <Meta><span>{session.id}</span><Pill value={session.status || "unknown"} /><span>{session.agent_id}</span><span>{session.environment_id}</span></Meta>
      <div><strong>{session.title || "Untitled session"}</strong></div>
      <Meta><span>created {formatTime(session.created_at)}</span><span>{session.created_by || ""}</span></Meta>
      <pre>{session.runtime_settings ? pretty(session.runtime_settings) : "{}"}</pre>
    </div>
  );
}

function TraceSummary({ trace }) {
  if (!trace) return <div id="traceMeta"><Empty>No trace loaded.</Empty></div>;
  const stats = trace.stats || {};
  return (
    <div className="stack">
      <div id="traceMeta">
        <Meta><span>{trace.session_id}</span><Pill value={trace.status || "running"} /><span>{trace.trace_id || ""}</span></Meta>
        <Meta><span>{stats.step_count || 0} steps</span><span>{stats.span_count || 0} spans</span><span>{stats.llm_requests || 0} llm</span><span>{stats.tool_calls || 0} tools</span><span>{stats.pending_approvals || 0} pending approvals</span></Meta>
      </div>
      <div className="summary compact" id="summary">{trace.summary || "No trace summary loaded."}</div>
    </div>
  );
}

function Usage({ usage }) {
  const summary = usage?.summary || {};
  return (
    <div className="stack">
      <div id="usageSummary" className="meta"><span>{summary.record_count || 0} records</span><span>{summary.total_tokens || 0} tokens</span><span>{formatDuration(summary.latency_ms || 0)}</span></div>
      <pre id="usage">{usage ? pretty(usage) : "No usage loaded."}</pre>
    </div>
  );
}

function PlanHistory({ response }) {
  const [statusFilter, setStatusFilter] = useState("");
  const plans = response?.plans || [];
  const planSessionID = plans[0]?.session_id || "";
  useEffect(() => setStatusFilter(""), [planSessionID]);
  const counts = taskPlanStatusCounts(plans);
  const filtered = filterTaskPlans(plans, statusFilter);
  const filters = [
    ["", "All", counts.total],
    ["active", "Active", counts.active],
    ["completed", "Completed", counts.completed],
    ["canceled", "Canceled", counts.canceled],
    ["superseded", "Superseded", counts.superseded]
  ];
  return (
    <div className="task-plan-history" id="taskPlanHistory">
      <div className="task-plan-summary" aria-label="Plan history summary">
        {filters.slice(1).map(([status, label, count]) => (
          <div key={status}><strong>{count}</strong><span>{label}</span></div>
        ))}
      </div>
      <div className="task-plan-filters" aria-label="Filter Plan history">
        {filters.map(([status, label, count]) => (
          <button
            key={status || "all"}
            className={statusFilter === status ? "" : "secondary"}
            type="button"
            onClick={() => setStatusFilter(status)}
          >{label} {count}</button>
        ))}
      </div>
      {response?.error ? <div className="subtle">{response.error}</div> : null}
      <div className="task-plan-list">
        {filtered.length ? filtered.map((plan, planIndex) => {
          const items = Array.isArray(plan.items) ? plan.items : [];
          const completed = items.filter((item) => item.status === "completed").length;
          return (
            <details className={`task-plan-entry ${plan.status || "unknown"}`} key={plan.id} open={plan.status === "active" || planIndex === 0}>
              <summary>
                <span className="task-plan-title">
                  <strong>{plan.title || plan.goal || plan.id}</strong>
                  <small>{plan.title && plan.goal ? plan.goal : plan.id}</small>
                </span>
                <span className="task-plan-entry-meta">
                  <Pill value={plan.status || "unknown"} />
                  <span>{completed}/{items.length} completed</span>
                </span>
              </summary>
              <div className="task-plan-detail">
                <Meta>
                  <span>{plan.handling_mode || "tracked"}</span>
                  <span>created {formatTime(plan.created_at)}</span>
                  <span>updated {formatTime(plan.updated_at)}</span>
                  {plan.created_turn_id ? <span>{plan.created_turn_id}</span> : null}
                </Meta>
                <ol className="task-plan-items">
                  {items.map((item, itemIndex) => (
                    <li className={item.status || "pending"} key={item.id || `${plan.id}-${itemIndex}`}>
                      <span className="task-plan-item-index">{itemIndex + 1}</span>
                      <div>
                        <div className="task-plan-item-head"><strong>{item.description || item.id}</strong><Pill value={item.status || "pending"} /></div>
                        {item.evidence ? <div className="task-plan-evidence"><span>Evidence</span>{item.evidence}</div> : null}
                      </div>
                    </li>
                  ))}
                </ol>
              </div>
            </details>
          );
        }) : <Empty>{plans.length ? "No Plans match this status." : "No Plan history loaded."}</Empty>}
      </div>
    </div>
  );
}

function ContextCoverage({ summary, events }) {
  const eventList = events?.events || [];
  const sourceSeq = Number(summary?.source_until_seq || 0);
  let latestSeq = 0;
  let covered = 0;
  const uncovered = [];
  eventList.forEach((event) => {
    const seq = Number(event.seq || 0);
    if (seq > latestSeq) latestSeq = seq;
    if (seq <= sourceSeq) covered += 1;
    else uncovered.push(event);
  });
  const cards = [["source_until_seq", sourceSeq], ["latest event seq", latestSeq], ["covered events", covered], ["unsummarized events", uncovered.length]];
  return (
    <div className="stack" id="contextCoverage">
      <div className="coverage-grid">{cards.map(([label, value]) => <div className="coverage-card" key={label}><div className="subtle">{label}</div><div className="coverage-value">{value}</div></div>)}</div>
      <Meta><span><Pill value={uncovered.length ? "pending" : "ok"} /></span><span>context diff from seq {sourceSeq}</span></Meta>
      <div className="coverage-events">
        {uncovered.length ? uncovered.slice(-8).reverse().map((event) => (
          <div className="span-event" key={event.seq}>
            <Meta><span>seq {event.seq || 0}</span><span>{event.type || ""}</span><span>{formatTime(event.created_at)}</span></Meta>
            <pre style={{ marginTop: 6 }}>{pretty(event.payload || {})}</pre>
          </div>
        )) : <Empty>No unsummarized events.</Empty>}
      </div>
    </div>
  );
}

function renderContextBudget(trace, events) {
  const steps = trace?.steps || [];
  const eventList = events?.events || [];
  const llmSteps = steps.filter((step) => step.type === "runtime.llm_request" || step.type === "runtime.tool_result" || step.data?.context_budget);
  const latest = [...llmSteps].reverse().find((step) => step.data?.context_budget) || null;
  const budget = latest?.data?.context_budget || {};
  const compactingEvents = eventList.filter((event) => event.type === "runtime.context_compacting" || event.type === "runtime.context_compacted" || event.type === "runtime.context_compaction_failed");
  const toolResultEvents = eventList.filter((event) => event.type === "runtime.tool_result");
  return { latest, budget, compactingEvents, toolResultEvents };
}

function ContextBudget({ trace, events }) {
  const { latest, budget, compactingEvents, toolResultEvents } = renderContextBudget(trace, events);
  const cards = [
    ["context_window_tokens", budget.context_window_tokens || 0],
    ["max_input_tokens", budget.max_input_tokens || 0],
    ["message_tokens", budget.message_tokens || 0],
    ["tool_schema_tokens", budget.tool_schema_tokens || 0],
    ["pinned_context_tokens", budget.pinned_context_tokens || 0],
    ["reserved_output_tokens", budget.reserved_output_tokens || 0],
    ["runtime.context_compacting", compactingEvents.length],
    ["runtime.tool_result", toolResultEvents.length]
  ];
  return (
    <div className="stack" id="contextBudget">
      {latest ? (
        <>
          <Meta>
            <span>turn {latest.turn_id || trace?.turn_id || "-"}</span>
            <span>step seq {latest.seq || "-"}</span>
            <span>pinned_context_included {String(Boolean(latest.data?.pinned_context_included))}</span>
            <span>summary_included {String(Boolean(latest.data?.summary_included))}</span>
            <span>context_truncated {String(Boolean(latest.data?.context_truncated))}</span>
          </Meta>
          <div className="coverage-grid">{cards.map(([label, value]) => <div className="coverage-card" key={label}><div className="subtle">{label}</div><div className="coverage-value">{value}</div></div>)}</div>
          <pre>{pretty(budget)}</pre>
        </>
      ) : <Empty>No context budget loaded.</Empty>}
    </div>
  );
}

function Waterfall({ trace, selectedSpanID, onSelect }) {
  const spans = trace?.spans || [];
  if (!spans.length) return <div className="stack" id="waterfall"><Empty>No span waterfall loaded.</Empty></div>;
  const graph = trace.graph || {};
  const stats = trace.stats || {};
  let total = Number(stats.duration_ms || 0);
  spans.forEach((span) => { total = Math.max(total, Number(span.start_offset_ms || 0) + Number(span.duration_ms || 0)); });
  total = Math.max(total, 1);
  return (
    <div className="stack" id="waterfall">
      <Meta><span>{spans.length} spans</span><span>{(graph.edges || []).length} edges</span><span>max depth {graph.max_depth || 0}</span><span>critical {formatDuration(graph.critical_path_duration_ms || 0)}</span></Meta>
      <div className="waterfall">
        {spans.map((span) => {
          let left = (Number(span.start_offset_ms || 0) / total) * 100;
          let width = (Number(span.duration_ms || 0) / total) * 100;
          left = Math.min(Math.max(left, 0), 99);
          width = Math.max(width, 0.5);
          if (left + width > 100) width = 100 - left;
          const rowClass = `waterfall-row ${span.span_id === selectedSpanID ? "selected" : ""} ${["error", "failed", "rejected"].includes(span.status) ? "error" : ""}`.trim();
          return (
            <div className={rowClass} data-waterfall-span={span.span_id} key={span.span_id} onClick={() => onSelect(span.span_id)}>
              <div className="waterfall-label" style={{ paddingLeft: Math.min(Number(span.depth || 0) * 14, 84) }}><strong>{span.name || span.span_id}</strong><div className="subtle">{span.kind || "unknown"} | {span.status || "unknown"} | self {formatDuration(span.self_duration_ms || 0)}</div></div>
              <div className="waterfall-track"><div className={`waterfall-bar ${span.critical ? "critical" : ""}`.trim()} style={{ left: `${left.toFixed(3)}%`, width: `${width.toFixed(3)}%` }} /></div>
              <div className="subtle">{formatDuration(span.duration_ms)}</div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function SpanDetail({ span, onSelect }) {
  if (!span) return <div className="span-detail" id="spanDetail"><Empty>Select a span to inspect events and attributes.</Empty></div>;
  return (
    <div className="span-detail" id="spanDetail">
      <div><strong>{span.name}</strong> <Pill value={span.status || "unknown"} /></div>
      <Meta><span>{span.span_id}</span><span>{span.kind || ""}</span><span>{formatDuration(span.duration_ms)}</span><span>seq {span.start_seq || 0} -&gt; {span.end_seq || 0}</span></Meta>
      {span.parent_span_id ? <div className="subtle">parent: {span.parent_span_id}</div> : null}
      {(span.child_span_ids || []).length ? <div className="actions">{span.child_span_ids.map((id) => <button className="secondary" type="button" data-span-select={id} key={id} onClick={() => onSelect(id)}>{id}</button>)}</div> : null}
      <div><strong>Events</strong></div>
      <div className="span-events">
        {(span.events || []).length ? span.events.map((event) => (
          <div className="span-event" key={`${event.seq}-${event.name || event.type}`}>
            <Meta><span>seq {event.seq || 0}</span><span>{event.type || ""}</span><span>{formatTime(event.time)}</span></Meta>
            <div style={{ marginTop: 6 }}><strong>{event.name || event.type || "event"}</strong></div>
            {event.message || event.summary ? <div className="subtle" style={{ marginTop: 6 }}>{event.message || event.summary}</div> : null}
          </div>
        )) : <Empty>No span events.</Empty>}
      </div>
      <div><strong>Attributes</strong></div>
      <div className="subtle">{Object.keys(span.attributes || {}).length ? Object.entries(span.attributes).map(([key, value]) => <div key={key}><strong>{key}</strong>: {String(value)}</div>) : "No attributes."}</div>
    </div>
  );
}

function SpansTable({ spans, selectedSpanID, onSelect }) {
  if (!spans.length) return <div className="table-wrap" id="spans"><Empty>No spans match the current filter.</Empty></div>;
  return (
    <div className="table-wrap" id="spans">
      <table className="span-table">
        <thead><tr><th>Name</th><th>Kind</th><th>Status</th><th>Duration</th><th>Range</th><th>Events</th><th>Attributes</th></tr></thead>
        <tbody>
          {spans.map((span) => {
            const attrs = Object.entries(span.attributes || {}).slice(0, 6);
            const events = (span.events || []).slice(0, 4);
            return (
              <tr className={`span-row ${span.span_id === selectedSpanID ? "selected" : ""}`.trim()} data-span={span.span_id} key={span.span_id} onClick={() => onSelect(span.span_id)}>
                <td><strong>{span.name}</strong><div className="subtle">{span.span_id}</div>{(span.child_span_ids || []).length ? <div className="subtle">{span.child_span_ids.length} child spans</div> : null}</td>
                <td>{span.kind}</td>
                <td><Pill value={span.status || "unknown"} /></td>
                <td>{formatDuration(span.duration_ms)}</td>
                <td>{span.start_seq || 0} -&gt; {span.end_seq || 0}</td>
                <td>{events.length ? events.map((event) => <div key={`${event.seq}-${event.name || event.type}`}>seq {event.seq || 0} {event.name || event.type || "event"}</div>) : <span className="subtle">No events</span>}{(span.events || []).length > 4 ? <div className="subtle">+{span.events.length - 4} more</div> : null}</td>
                <td>{attrs.length ? attrs.map(([key, value]) => <div key={key}>{key}: {String(value)}</div>) : <span className="subtle">No attributes</span>}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function ArtifactActions({ href, command, onPreview, onPreviewRequest, onCopy }) {
  if (!href && !command) return null;
  return (
    <div className="actions" style={{ marginTop: 6 }}>
      {href ? <><button className="secondary" type="button" data-preview={href} onClick={() => onPreviewRequest ? onPreviewRequest() : onPreview(href)}>Preview</button><a className="link" href={href} target="_blank" rel="noreferrer">Download</a></> : null}
      {command ? <button className="secondary" type="button" data-copy={command} onClick={() => onCopy(command)}>Copy CLI</button> : null}
    </div>
  );
}

function Timeline({ trace, onPreview, onCopy }) {
  const steps = trace?.steps || [];
  return (
    <div className="timeline" id="timeline">
      {steps.length ? steps.map((step) => {
        const truncated = step.content_truncated || step.state_truncated;
        return (
          <div className={stepClass(step)} key={step.seq}>
            <Meta><span>seq {step.end_seq && step.end_seq !== step.seq ? `${step.seq}-${step.end_seq}` : step.seq}</span><span>{step.type}</span><span>{step.identifier ? `${step.identifier}${step.api_name ? `.${step.api_name}` : ""}` : ""}</span><ToolSourceChip source={step?.data?.tool_source} /><span>{step.outcome || ""}</span><span>{formatTime(step.created_at)}</span></Meta>
            <div style={{ marginTop: 6 }}>{step.message || step.summary || ""}</div>
            <MCPDetails data={step?.data} />
            {truncated ? <div className="subtle" style={{ marginTop: 6 }}>tool result preview truncated{step.content_truncated ? `: ${step.visible_content_chars || 0}/${step.original_content_chars || 0} chars` : ""}{step.state_truncated ? `; state ${step.original_state_bytes || 0} bytes omitted` : ""}</div> : null}
            {(step.artifacts || []).length ? <div className="artifact-list">{step.artifacts.map((artifact) => {
              const label = [artifact.artifact_id || artifact.id || "(unknown)", artifact.name || "", artifact.artifact_type ? `[${artifact.artifact_type}]` : ""].filter(Boolean).join(" ");
              const href = artifact.download_path || "";
              const command = sessionArtifactCLI(href);
              return <div className="artifact-line" key={label}><div>{label}</div>{href ? <div className="subtle">download: {href}</div> : null}{command ? <div className="subtle">cli: {command}</div> : null}<ArtifactActions href={href} command={command} onPreview={onPreview} onCopy={onCopy} /></div>;
            })}</div> : null}
            {step.artifact_error ? <div className="subtle" style={{ marginTop: 6 }}>artifact error: {step.artifact_error}</div> : null}
          </div>
        );
      }) : <Empty>No timeline loaded.</Empty>}
    </div>
  );
}

function Interventions({ interventions, onApprove, onReject }) {
  return (
    <div className="list" id="interventions">
      {interventions.length ? interventions.map((intervention) => (
        <div className="list-item" key={intervention.call_id}>
          <div><strong>{intervention.tool_identifier}.{intervention.api_name}</strong></div>
          <Meta><span>{intervention.call_id}</span><Pill value={intervention.status || "pending"} /><span>{intervention.reason || ""}</span></Meta>
          <pre style={{ marginTop: 8 }}>{pretty(intervention.arguments || {})}</pre>
          <div className="actions"><button onClick={() => onApprove(intervention)}>Approve</button><button className="secondary" onClick={() => onReject(intervention)}>Reject</button></div>
        </div>
      )) : <Empty>No pending approvals.</Empty>}
    </div>
  );
}

function Artifacts({ sessionID, artifacts, onPreview, onCopy }) {
  return (
    <div className="list" id="artifacts">
      {artifacts.length ? artifacts.map((artifact) => {
        const href = inspectorAPI.artifactDownloadPath(sessionID, artifact.id);
        const command = sessionArtifactCommand(sessionID, artifact.id);
        return (
          <div className="list-item" key={artifact.id}>
            <div><strong>{artifact.name || artifact.id}</strong></div>
            <Meta><span>{artifact.artifact_type}</span><span>{artifact.object_ref_id || ""}</span><span>{artifact.turn_id || ""}</span></Meta>
            <div className="subtle">cli: {command}</div>
            <ArtifactActions href={href} command={command} onPreview={onPreview} onPreviewRequest={() => onPreview(href, () => inspectorAPI.downloadArtifact(sessionID, artifact.id))} onCopy={onCopy} />
          </div>
        );
      }) : <Empty>No artifacts.</Empty>}
    </div>
  );
}

function RecentEvents({ events }) {
  return (
    <div className="list" id="events">
      {events.length ? events.slice(-18).reverse().map((event) => (
        <div className="list-item" key={event.seq}>
          <Meta><span>seq {event.seq}</span><span>{event.type}</span><ToolSourceChip source={event?.payload?.data?.tool_source} /><span>{formatTime(event.created_at)}</span></Meta>
          <MCPDetails data={event?.payload?.data} />
          <pre style={{ marginTop: 8 }}>{pretty(event.payload || {})}</pre>
        </div>
      )) : <Empty>No events loaded.</Empty>}
    </div>
  );
}

function ArtifactPreview({ preview }) {
  if (!preview) return <div id="artifactPreview"><Empty>Select an artifact preview.</Empty></div>;
  return (
    <div id="artifactPreview">
      <div className="preview-meta"><span>{preview.contentType}</span><span>{preview.size}</span><span>{preview.href}</span></div>
      {preview.truncated ? <div className="subtle">preview truncated to {preview.visibleChars} chars from {preview.originalChars}; use Download for the full artifact.</div> : null}
      {preview.kind === "image" ? <img className="preview-media" src={preview.url} alt="artifact preview" /> : null}
      {preview.kind === "text" ? <pre className="code">{preview.text}</pre> : null}
      {preview.kind === "binary" ? <Empty>Binary artifact preview is not available inline. Use Download.</Empty> : null}
    </div>
  );
}

function Exporters({ response, onRetry }) {
  if (!response) return <div className="list" id="exporters"><Empty>No exporter status loaded.</Empty></div>;
  const { perfetto = {}, otlp = {}, sampling = {}, retry = {}, recent_runs: runs = [] } = response;
  return (
    <div className="list" id="exporters">
      <div className="list-item"><div><strong>Sampling</strong> <Pill value={sampling.enabled ? "pending" : "ok"} /></div><Meta><span>sample_rate {String(sampling.sample_rate === undefined ? 1 : sampling.sample_rate)}</span><span>{sampling.configured ? "configured" : "default"}</span></Meta></div>
      <div className="list-item"><div><strong>Retry</strong> <Pill value={retry.enabled ? "ok" : "unknown"} /></div><Meta><span>max_attempts {retry.max_attempts || 0}</span><span>pending_recent {retry.pending_recent_retries || 0}</span></Meta><div className="actions" style={{ marginTop: 8 }}><button className="secondary" type="button" onClick={onRetry}>Retry due exporters</button></div></div>
      <ExporterItem name="Perfetto" entry={perfetto} />
      <ExporterItem name="OTLP HTTP" entry={otlp} />
      {runs.length ? <><div className="list-item"><strong>Recent exporter runs</strong></div>{runs.slice(0, 5).map((run) => <ExporterRun run={run} key={`${run.exporter}-${run.started_at}-${run.trace_id}`} />)}</> : <Empty>No persisted exporter runs.</Empty>}
    </div>
  );
}

function ExporterItem({ name, entry }) {
  const state = entry.enabled ? "enabled" : "disabled";
  const configured = entry.configured ? "configured" : "not configured";
  const healthChecks = [
    ["last_success", "last success"],
    ["last_failure", "last failure"],
    ["last_attempt", "last attempt"]
  ];
  return (
    <div className="list-item">
      <div><strong>{name}</strong> <Pill value={entry.enabled ? "ok" : "unknown"} /> <Pill value={entry.configured ? "ok" : "unknown"} /></div>
      <Meta><span>{entry.destination || "no destination"}</span>{entry.token_provided ? <span>token configured</span> : null}<span>{state}</span><span>{configured}</span></Meta>
      <div style={{ marginTop: 8 }}>{healthChecks.some(([key]) => entry[key]) ? healthChecks.map(([key, label]) => entry[key] ? <HealthLine key={key} label={label} health={entry[key]} failure={key === "last_failure"} /> : null) : <Empty>No exporter attempts recorded.</Empty>}</div>
    </div>
  );
}

function HealthLine({ label, health, failure }) {
  return (
    <>
      <div className="health-line"><Pill value={failure ? "error" : "ok"} /><span>{label}</span><span>{[formatTime(health.at), health.session_id || "", health.turn_id || "", health.trace_id || ""].filter(Boolean).join(" | ")}</span></div>
      {health.message ? <div className="subtle">{health.message}</div> : null}
    </>
  );
}

function ExporterRun({ run }) {
  const state = run.status === "succeeded" ? "success" : run.status === "failed" ? "error" : run.status === "skipped" ? "pending" : "unknown";
  return (
    <div className="list-item">
      <Meta><span>{formatTime(run.finished_at || run.started_at)}</span><span>{run.exporter || ""}</span><Pill value={state} /></Meta>
      <div className="subtle">{[run.session_id, run.turn_id, run.trace_id].filter(Boolean).join(" | ")}</div>
      {run.next_retry_at ? <div className="subtle">attempt {run.attempt_count || 1} | next retry {formatTime(run.next_retry_at)}</div> : run.attempt_count ? <div className="subtle">attempt {run.attempt_count}</div> : null}
      {run.message ? <div className="subtle">{run.message}</div> : null}
    </div>
  );
}

createRoot(document.getElementById("root")).render(<App />);
