import React, { useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import * as api from "./api.js";
import { formatTaskTime, formatTime, pillClass, pretty } from "./utils.js";
import "./styles.css";

const activeSessionStorageKey = "tma.workbench.active-session";
const sessionEventStreamTypes = [
  "agent.message",
  "runtime.started",
  "runtime.thinking",
  "runtime.llm_request",
  "runtime.llm_delta",
  "runtime.llm_response",
  "runtime.tool_call",
  "runtime.tool_result",
  "runtime.tool_intervention_required",
  "runtime.tool_intervention_approved",
  "runtime.tool_intervention_rejected",
  "runtime.failed",
  "runtime.completed",
  "runtime.context_compacting",
  "runtime.context_compacted",
  "runtime.context_compaction_failed",
  "runtime.span_started",
  "runtime.span_event",
  "runtime.span_ended",
  "session.status_provisioning",
  "session.status_idle",
  "session.status_running",
  "session.status_interrupting",
  "session.status_compacting",
  "session.status_failed",
  "session.status_terminated",
  "session.config_updated"
];
const sessionSyncEventTypes = new Set([
  "agent.message",
  "runtime.tool_intervention_required",
  "runtime.tool_intervention_approved",
  "runtime.tool_intervention_rejected",
  "runtime.failed",
  "runtime.completed",
  "session.status_idle",
  "session.status_failed",
  "session.status_terminated"
]);

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

function Pill({ value }) {
  return <span className={pillClass(value || "unknown")}>{value || "unknown"}</span>;
}

function Meta({ children }) {
  return <div className="meta">{children}</div>;
}

function TaskStatusIcon({ status }) {
  return <span aria-hidden="true" className={`task-status-icon ${status || "unknown"}`} />;
}

function ArchiveIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="M3 3.5h10l-1 2H4l-1-2Zm1 3h8v5.5H4V6.5Zm2 2h4" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.4" />
    </svg>
  );
}

function DeleteIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="M5.5 4.5h5m-4-1.5h3m-4 3v6h5v-6m-7-1.5h9" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.4" />
    </svg>
  );
}

function payload(event) {
  return event?.payload || {};
}

function eventData(event) {
  const data = payload(event).data;
  return data && typeof data === "object" && !Array.isArray(data) ? data : {};
}

function objectValue(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function firstValue(object, keys) {
  for (const key of keys) {
    const value = object?.[key];
    if (value !== undefined && value !== null && String(value).trim() !== "") return value;
  }
  return "";
}

function valueText(value) {
  if (Array.isArray(value)) return value.map((item) => String(item)).join(" ");
  if (value && typeof value === "object") return JSON.stringify(value);
  return String(value || "");
}

function toolTitle(identifier, apiName) {
  const key = [identifier, apiName].filter(Boolean).join(".");
  const titles = {
    "default.run_command": "Run command",
    "default.execute_code": "Run code",
    "default.read_file": "Read file",
    "default.write_file": "Write file",
    "default.edit_file": "Edit file",
    "web.search": "Search web",
    "web.crawl": "Read web page",
    "browser.open": "Open browser",
    "browser.click": "Click in browser",
    "browser.type": "Type in browser",
    "browser.takeover": "Browser takeover",
    "computer.get_state": "Inspect desktop",
    "computer.screenshot": "Capture screen",
    "computer.click": "Click desktop",
    "computer.type_text": "Type on desktop",
    "computer.hotkey": "Press hotkey",
    "computer.launch_app": "Launch app",
    "computer.open_url": "Open URL",
    "computer.search_web": "Search in browser"
  };
  return titles[key] || titles[`${identifier}.${apiName}`] || key || "Use tool";
}

function toolRisk(identifier, apiName, reason = "") {
  const api = String(apiName || "");
  const riskReason = String(reason || "");
  if (riskReason.includes("network") || ["run_command", "execute_code", "write_file", "edit_file", "click", "type_text", "hotkey", "launch_app", "open_url"].includes(api)) {
    return "high";
  }
  if (identifier === "web" || identifier === "browser" || identifier === "computer") return "medium";
  return "low";
}

function toolDetail(identifier, apiName, args = {}) {
  const command = firstValue(args, ["command", "cmd"]);
  if (apiName === "run_command" || apiName === "execute_code") {
    const suffix = valueText(firstValue(args, ["args", "arguments"]));
    return shortText([command, suffix].filter(Boolean).join(" "), 220) || "Execute code or a shell command.";
  }
  if (["read_file", "write_file", "edit_file"].includes(apiName)) {
    return shortText(valueText(firstValue(args, ["path", "file_path", "target_path", "filename"])), 220) || "Access a workspace file.";
  }
  if (apiName === "search") {
    return shortText(valueText(firstValue(args, ["query", "q"])), 220) || "Search the web.";
  }
  if (apiName === "crawl" || apiName === "open_url") {
    return shortText(valueText(firstValue(args, ["url", "target_url"])), 220) || "Open or read a web page.";
  }
  if (identifier === "computer" || identifier === "browser") {
    return shortText(valueText(firstValue(args, ["app", "name", "url", "text", "selector", "capture_mode", "keys", "key"])), 220) || "Interact with a visible app or browser.";
  }
  return shortText(valueText(args), 220);
}

function normalizeToolParts(identifier = "", apiName = "") {
  const rawIdentifier = String(identifier || "");
  const rawApiName = String(apiName || "");
  if (rawApiName) return { identifier: rawIdentifier, apiName: rawApiName };
  const normalized = rawIdentifier.replace(/-/g, "_");
  const aliases = {
    web_search: { identifier: "web", apiName: "search" },
    web_crawl: { identifier: "web", apiName: "crawl" },
    browser: { identifier: "browser", apiName: "open_url" },
    computer: { identifier: "computer", apiName: "launch_app" },
    run_command: { identifier: "default", apiName: "run_command" },
    execute_code: { identifier: "default", apiName: "execute_code" },
    read_file: { identifier: "default", apiName: "read_file" },
    write_file: { identifier: "default", apiName: "write_file" },
    edit_file: { identifier: "default", apiName: "edit_file" }
  };
  if (aliases[normalized]) return aliases[normalized];
  const dotIndex = rawIdentifier.indexOf(".");
  if (dotIndex > 0) {
    return {
      identifier: rawIdentifier.slice(0, dotIndex),
      apiName: rawIdentifier.slice(dotIndex + 1)
    };
  }
  return { identifier: rawIdentifier, apiName: rawApiName };
}

function toolSummary({ identifier, apiName, args = {}, reason = "", success }) {
  const parts = normalizeToolParts(identifier, apiName);
  const risk = toolRisk(parts.identifier, parts.apiName, reason);
  const title = toolTitle(parts.identifier, parts.apiName);
  const detail = toolDetail(parts.identifier, parts.apiName, args);
  const status = success === true ? "Completed" : success === false ? "Failed" : "";
  return {
    detail,
    label: [parts.identifier, parts.apiName].filter(Boolean).join(".") || "tool",
    risk,
    status,
    title
  };
}

function approvalSummary(intervention) {
  return toolSummary({
    identifier: intervention.tool_identifier,
    apiName: intervention.api_name,
    args: objectValue(intervention.arguments),
    reason: intervention.reason
  });
}

function ApprovalCard({ intervention, onApprove, onReject, busy }) {
  const summary = approvalSummary(intervention);
  return (
    <div className={`approval-card risk-${summary.risk}`}>
      <div className="approval-card-header">
        <div>
          <strong>{summary.title}</strong>
          <Meta>
            <span>{summary.label}</span>
            <Pill value={intervention.status || "pending"} />
            <span className={`risk-chip ${summary.risk}`}>{summary.risk} risk</span>
          </Meta>
        </div>
        <div className="approval-actions">
          <button type="button" disabled={busy} onClick={() => onApprove(intervention)}>{busy ? "Approving..." : "Approve"}</button>
          <button className="secondary" type="button" disabled={busy} onClick={() => onReject(intervention)}>Reject</button>
        </div>
      </div>
      {summary.detail ? <div className="approval-summary">{summary.detail}</div> : null}
      {intervention.reason ? <div className="approval-reason">{intervention.reason}</div> : null}
      <details className="approval-details">
        <summary>Arguments</summary>
        <pre>{pretty(intervention.arguments || {})}</pre>
      </details>
    </div>
  );
}

function eventText(event) {
  const data = payload(event);
  const content = data.content;
  if (Array.isArray(content)) {
    return content.map((item) => item.text || item.content || "").filter(Boolean).join("\n");
  }
  if (typeof content === "string") return content;
  return data.message || data.summary || data.text || "";
}

function decodeHTML(value) {
  return String(value || "")
    .replace(/&quot;/g, "\"")
    .replace(/&apos;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&");
}

function stripTags(value) {
  return decodeHTML(String(value || "").replace(/<[^>]+>/g, "")).trim();
}

function parseSeedToolCall(source) {
  const text = String(source || "");
  const functionMatch = text.match(/<function\s+[^>]*name=["']([^"']+)["'][^>]*>/i);
  const name = functionMatch ? decodeHTML(functionMatch[1]) : "tool";
  const args = {};
  const parameterPattern = /<parameter\s+[^>]*name=["']([^"']+)["'][^>]*>([\s\S]*?)<\/parameter>/gi;
  let parameterMatch = parameterPattern.exec(text);
  while (parameterMatch) {
    args[decodeHTML(parameterMatch[1])] = stripTags(parameterMatch[2]);
    parameterMatch = parameterPattern.exec(text);
  }
  const parts = normalizeToolParts(name);
  const summary = toolSummary({
    identifier: parts.identifier || name,
    apiName: parts.apiName,
    args
  });
  return {
    args,
    rawName: name,
    summary
  };
}

function messageParts(text) {
  const source = String(text || "");
  const parts = [];
  const pattern = /<seed:tool_call>[\s\S]*?<\/seed:tool_call>/gi;
  let cursor = 0;
  let match = pattern.exec(source);
  while (match) {
    const before = source.slice(cursor, match.index).trim();
    if (before) parts.push({ type: "text", text: before });
    parts.push({ type: "tool", tool: parseSeedToolCall(match[0]) });
    cursor = match.index + match[0].length;
    match = pattern.exec(source);
  }
  const after = source.slice(cursor).trim();
  if (after) parts.push({ type: "text", text: after });
  return parts.length ? parts : [{ type: "text", text: source }];
}

function cleanMessageText(text) {
  return messageParts(text)
    .filter((part) => part.type === "text")
    .map((part) => part.text)
    .join("\n")
    .trim();
}

function shortText(value, maxLength = 180) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  if (!text) return "";
  return text.length > maxLength ? `${text.slice(0, maxLength - 1)}...` : text;
}

function maxSeq(events) {
  return (events || []).reduce((maximum, event) => Math.max(maximum, Number(event.seq || 0)), 0);
}

function isActivityEvent(event) {
  return Boolean(event?.type) && (event.type.startsWith("runtime.") || event.type.startsWith("session.status_"));
}

function activitySummary(event) {
  const summary = activityView(event).detail || eventText(event);
  return shortText(summary || event.type || "");
}

function compactActivityEvents(sourceEvents) {
  const compacted = [];
  const relevantEvents = (sourceEvents || []).filter((event) => isActivityEvent(event) || event.type === "agent.message");
  relevantEvents.forEach((event) => {
    const activity = activityView(event);
    const previous = compacted[compacted.length - 1];
    const signature = [activity.title, activity.detail, activity.kind].join("|");
    const isStreamingNoise = event.type === "runtime.llm_delta";
    if (previous && previous.signature === signature && (isStreamingNoise || event.type === previous.type)) {
      previous.count += 1;
      previous.event = event;
      previous.activity = activity;
      return;
    }
    compacted.push({
      activity,
      count: 1,
      event,
      signature,
      type: event.type
    });
  });
  return compacted.slice(-10).reverse();
}

function activityView(event) {
  const data = eventData(event);
  switch (event?.type) {
    case "agent.message":
      return { title: "Agent replied", detail: shortText(cleanMessageText(eventText(event)) || "Prepared a tool action.", 180), kind: "ok" };
    case "runtime.started":
      return { title: "Started working", detail: "Preparing this turn.", kind: "running" };
    case "runtime.thinking":
      return { title: "Thinking", detail: "Planning the next step.", kind: "running" };
    case "runtime.llm_request":
      return { title: "Asking model", detail: "Generating the next action or reply.", kind: "running" };
    case "runtime.llm_delta":
      return { title: "Writing reply", detail: "Streaming the response.", kind: "running" };
    case "runtime.llm_response":
      return { title: "Model responded", detail: "Checking whether tools are needed.", kind: "running" };
    case "runtime.tool_call": {
      const summary = toolSummary({
        identifier: data.identifier,
        apiName: data.api_name,
        args: objectValue(data.arguments),
        reason: data.reason
      });
      return { title: summary.title, detail: summary.detail || summary.label, kind: summary.risk === "high" ? "warn" : "tool" };
    }
    case "runtime.tool_result": {
      const summary = toolSummary({
        identifier: data.identifier,
        apiName: data.api_name,
        args: objectValue(data.arguments),
        reason: data.reason,
        success: data.success
      });
      const artifactCount = Array.isArray(data.artifacts) ? data.artifacts.length : 0;
      const detail = data.success === false
        ? data.error || data.message || "The tool returned an error."
        : artifactCount ? `${artifactCount} result file${artifactCount === 1 ? "" : "s"} generated.` : summary.detail;
      return { title: data.success === false ? `${summary.title} failed` : `${summary.title} finished`, detail: shortText(detail, 180), kind: data.success === false ? "error" : "ok" };
    }
    case "runtime.tool_intervention_required": {
      const summary = toolSummary({
        identifier: data.identifier,
        apiName: data.api_name,
        args: objectValue(data.arguments),
        reason: data.reason
      });
      return { title: `Needs approval: ${summary.title}`, detail: summary.detail || data.reason || "Review before continuing.", kind: "warn" };
    }
    case "runtime.tool_intervention_approved":
      return { title: "Approval granted", detail: data.decision_reason || "Continuing the task.", kind: "ok" };
    case "runtime.tool_intervention_rejected":
      return { title: "Approval rejected", detail: data.decision_reason || "The tool call was not allowed.", kind: "error" };
    case "runtime.completed":
    case "session.status_idle":
      return { title: "Task idle", detail: payload(event).last_turn_status === "failed" ? payload(event).reason || "Last turn failed." : "Ready for the next message.", kind: payload(event).last_turn_status === "failed" ? "error" : "ok" };
    case "runtime.failed":
    case "session.status_failed":
      return { title: "Task failed", detail: payload(event).reason || payload(event).message || "Something failed while running.", kind: "error" };
    case "session.status_running":
      return { title: "Running", detail: "The agent is working.", kind: "running" };
    case "session.status_interrupting":
      return { title: "Interrupting", detail: "Stopping the current turn.", kind: "warn" };
    case "session.status_terminated":
      return { title: "Archived", detail: "This task has been archived.", kind: "muted" };
    default:
      return { title: event?.type || "Activity", detail: turnActivityLabel(event) || eventText(event), kind: "muted" };
  }
}

function artifactName(artifact) {
  return artifact?.name || artifact?.id || "artifact";
}

function artifactExtension(artifact) {
  const name = artifactName(artifact).toLowerCase();
  const index = name.lastIndexOf(".");
  return index >= 0 ? name.slice(index + 1) : "";
}

function artifactMetadata(artifact) {
  const metadata = artifact?.metadata;
  return metadata && typeof metadata === "object" && !Array.isArray(metadata) ? metadata : {};
}

function previewKindForArtifact(artifact, contentType = "") {
  const type = String(contentType || artifactMetadata(artifact).content_type || "").toLowerCase();
  const extension = artifactExtension(artifact);
  if (type.startsWith("image/") || ["png", "jpg", "jpeg", "gif", "webp", "svg"].includes(extension)) return "image";
  if (
    type.startsWith("text/") ||
    type.includes("json") ||
    type.includes("xml") ||
    ["txt", "md", "markdown", "json", "jsonl", "csv", "tsv", "log", "xml", "html", "css", "js", "jsx", "ts", "tsx", "go", "py", "sh", "yaml", "yml"].includes(extension)
  ) {
    return "text";
  }
  return "download";
}

function turnActivityLabel(event) {
  if (!event) return "";
  switch (event.type) {
    case "runtime.started":
      return "本轮已开始，正在处理...";
    case "runtime.thinking":
      return "正在思考并生成回复...";
    case "runtime.llm_request":
      return "正在和模型对话...";
    case "runtime.llm_delta":
      return "正在生成回复...";
    case "runtime.llm_response":
      return "模型已返回结果，正在整理...";
    case "runtime.tool_call":
      return "正在调用工具...";
    case "runtime.tool_result":
      return "工具已返回结果，正在继续...";
    case "runtime.tool_intervention_required":
      return "工具调用需要确认后才能继续。";
    case "runtime.tool_intervention_approved":
      return "审批已通过，正在继续执行...";
    default:
      return "";
  }
}

function turnSignal(events, options = {}) {
  const sinceSeq = Number(options.sinceSeq || 0);
  const sessionStatus = options.sessionStatus || "";
  const waitingForReply = Boolean(options.waitingForReply);
  const includeSuccess = Boolean(options.includeSuccess);
  const interventions = options.interventions || [];
  const currentEvents = (events || []).filter((event) => Number(event.seq || 0) > sinceSeq);
  const latestEvent = currentEvents[currentEvents.length - 1] || null;
  const reverseEvents = [...currentEvents].reverse();
  const latestAgentMessage = reverseEvents.find((event) => event.type === "agent.message");
  const failureEvent = reverseEvents.find((event) =>
    event.type === "runtime.failed" ||
    (event.type === "session.status_idle" && payload(event).last_turn_status === "failed") ||
    event.type === "session.status_failed"
  );
  const approvalEvent = reverseEvents.find((event) => event.type === "runtime.tool_intervention_required");
  const progressEvent = reverseEvents.find((event) =>
    [
      "runtime.started",
      "runtime.thinking",
      "runtime.llm_request",
      "runtime.llm_delta",
      "runtime.llm_response",
      "runtime.tool_call",
      "runtime.tool_result",
      "runtime.tool_intervention_approved"
    ].includes(event.type)
  );

  if (interventions.length) {
    return {
      kind: "approval",
      title: interventions.length === 1 ? "等待审批" : `${interventions.length} 个审批待处理`,
      detail: approvalEvent ? turnActivityLabel(approvalEvent) : "Approve or reject to continue."
    };
  }
  if (failureEvent) {
    const reason = payload(failureEvent).reason || payload(failureEvent).message || eventText(failureEvent) || "Turn failed.";
    return {
      kind: "error",
      title: "本轮失败",
      detail: reason
    };
  }
  if (latestAgentMessage && includeSuccess) {
    return {
      kind: "success",
      title: "已回复",
      detail: shortText(eventText(latestAgentMessage)) || "Agent reply received."
    };
  }
  if (waitingForReply || progressEvent || sessionStatus === "running" || sessionStatus === "interrupting") {
    return {
      kind: "thinking",
      title: "正在思考",
      detail: turnActivityLabel(progressEvent || latestEvent) || "正在生成回复..."
    };
  }
  return null;
}

function TurnBanner({ signal }) {
  if (!signal) return null;
  return (
    <div className={`turn-banner ${signal.kind}`}>
      <div>
        <strong>{signal.title}</strong>
        <div className="subtle">{signal.detail}</div>
      </div>
    </div>
  );
}

function ToolActionCard({ tool }) {
  const summary = tool.summary;
  return (
    <div className={`tool-action-card risk-${summary.risk}`}>
      <div className="tool-action-head">
        <div>
          <strong>{summary.title}</strong>
          <Meta>
            <span>{summary.label || tool.rawName}</span>
            <span className={`risk-chip ${summary.risk}`}>{summary.risk} risk</span>
          </Meta>
        </div>
        <span className="tool-action-status">Prepared</span>
      </div>
      {summary.detail ? <div className="tool-action-detail">{summary.detail}</div> : null}
      {Object.keys(tool.args || {}).length ? (
        <details className="tool-action-details">
          <summary>Details</summary>
          <pre>{pretty(tool.args)}</pre>
        </details>
      ) : null}
    </div>
  );
}

function ProcessEventCard({ event }) {
  const data = eventData(event);
  const error = objectValue(data.error);
  const args = objectValue(data.arguments);
  const artifacts = Array.isArray(data.artifacts) ? data.artifacts : [];
  let title = event.type;
  let metaLabel = event.type;
  let preview = "";
  let detailObject = null;
  let tone = "muted";

  if (event.type === "runtime.tool_call") {
    const summary = toolSummary({
      identifier: data.identifier,
      apiName: data.api_name,
      args,
      reason: data.reason
    });
    title = summary.title;
    metaLabel = `Call · ${summary.label}`;
    preview = summary.detail || "Executing tool.";
    detailObject = args;
    tone = summary.risk === "high" ? "warn" : "tool";
  } else if (event.type === "runtime.tool_result") {
    const summary = toolSummary({
      identifier: data.identifier,
      apiName: data.api_name,
      args,
      reason: data.reason,
      success: data.success
    });
    title = data.success === false ? `${summary.title} failed` : `${summary.title} finished`;
    metaLabel = `Result · ${summary.label}`;
    preview = data.success === false
      ? error.message || data.message || "The tool returned an error."
      : shortText(data.content || summary.detail || "", 240) || (artifacts.length ? `${artifacts.length} artifact${artifacts.length === 1 ? "" : "s"} generated.` : "Tool finished successfully.");
    detailObject = {
      arguments: Object.keys(args).length ? args : undefined,
      content: data.content || undefined,
      state: data.state && Object.keys(objectValue(data.state)).length ? data.state : undefined,
      artifacts: artifacts.length ? artifacts : undefined,
      error: Object.keys(error).length ? error : undefined
    };
    tone = data.success === false ? "error" : "ok";
  } else if (event.type === "runtime.tool_intervention_required") {
    const summary = toolSummary({
      identifier: data.identifier,
      apiName: data.api_name,
      args,
      reason: data.reason
    });
    title = `Approval needed: ${summary.title}`;
    metaLabel = `Approval · ${summary.label}`;
    preview = summary.detail || data.reason || "Review before continuing.";
    detailObject = args;
    tone = "warn";
  } else if (event.type === "runtime.tool_intervention_approved") {
    title = "Approval granted";
    metaLabel = "Approval";
    preview = data.decision_reason || "The tool call can continue.";
    tone = "ok";
  } else if (event.type === "runtime.tool_intervention_rejected") {
    title = "Approval rejected";
    metaLabel = "Approval";
    preview = data.decision_reason || "The tool call was stopped.";
    tone = "error";
  }

  return (
    <details className={`process-card ${tone}`}>
      <summary>
        <div className="process-card-head">
          <div>
            <strong>{title}</strong>
            <Meta><span>{metaLabel}</span><span>{formatTime(event.created_at)}</span></Meta>
          </div>
        </div>
        {preview ? <div className="process-card-preview">{preview}</div> : null}
      </summary>
      {detailObject ? <pre className="process-card-detail">{pretty(detailObject)}</pre> : null}
    </details>
  );
}

function MessageBody({ event }) {
  const text = eventText(event);
  const parts = event.type === "agent.message" ? messageParts(text) : [{ type: "text", text }];
  if (!parts.length || (parts.length === 1 && parts[0].type === "text" && !parts[0].text)) {
    return <div className="message-text">（空消息）</div>;
  }
  return (
    <div className="message-body">
      {parts.map((part, index) => (
        part.type === "tool" ? (
          <ToolActionCard key={`tool-${index}`} tool={part.tool} />
        ) : (
          <div className="message-text" key={`text-${index}`}>{part.text}</div>
        )
      ))}
    </div>
  );
}

function mergeEvents(currentEvents, nextEvents) {
  const merged = new Map();
  [...(currentEvents || []), ...(nextEvents || [])].forEach((event) => {
    merged.set(`${event.seq}-${event.type}`, event);
  });
  return [...merged.values()].sort((left, right) => Number(left.seq || 0) - Number(right.seq || 0));
}

function delay(milliseconds) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

function sessionStatusRank(status) {
  switch (String(status || "")) {
    case "running":
      return 0;
    case "interrupting":
      return 1;
    case "idle":
      return 2;
    case "failed":
      return 3;
    case "terminated":
      return 4;
    default:
      return 5;
  }
}

function parseSessionRuntimeSettings(raw) {
  if (!raw || typeof raw !== "object") return {};
  return {
    interventionMode: typeof raw.intervention_mode === "string" ? raw.intervention_mode : "",
    llmModel: typeof raw.llm_model === "string" ? raw.llm_model : "",
    llmProvider: typeof raw.llm_provider === "string" ? raw.llm_provider : "",
    toolRuntime: typeof raw.tool_runtime === "string" ? raw.tool_runtime : ""
  };
}

function rememberedSessionID() {
  try {
    return window.localStorage.getItem(activeSessionStorageKey) || "";
  } catch {
    return "";
  }
}

function rememberSession(sessionID) {
  try {
    window.localStorage.setItem(activeSessionStorageKey, sessionID);
  } catch {}
}

function forgetSession() {
  try {
    window.localStorage.removeItem(activeSessionStorageKey);
  } catch {}
}

function WorkbenchApp() {
  const [status, setStatus] = useState("ready");
  const [agentID, setAgentID] = useState("");
  const [environmentID, setEnvironmentID] = useState("");
  const [sessionID, setSessionID] = useState("");
  const [task, setTask] = useState("");
  const [taskSearch, setTaskSearch] = useState("");
  const [eventsResponse, setEventsResponse] = useState({ events: [] });
  const [interventionResponse, setInterventionResponse] = useState({ interventions: [] });
  const [artifactResponse, setArtifactResponse] = useState({ artifacts: [] });
  const [sessionMeta, setSessionMeta] = useState(null);
  const [waitingForReply, setWaitingForReply] = useState(false);
  const [recentSessions, setRecentSessions] = useState([]);
  const [decidingApprovalID, setDecidingApprovalID] = useState("");
  const [sessionAction, setSessionAction] = useState("");
  const [artifactPreview, setArtifactPreview] = useState(null);
  const [runtimeConfig, setRuntimeConfig] = useState(null);
  const [runtimeCapabilities, setRuntimeCapabilities] = useState({ default_runtime: "cloud_sandbox", available_runtimes: ["cloud_sandbox"] });
  const [modelOptions, setModelOptions] = useState([]);
  const [defaultAgentConfig, setDefaultAgentConfig] = useState(null);
  const [settingsDraft, setSettingsDraft] = useState({
    interventionMode: "request_approval",
    llmModel: "",
    llmProvider: "",
    toolRuntime: "cloud_sandbox"
  });
  const [savingSettings, setSavingSettings] = useState(false);
  const [approvalsOpen, setApprovalsOpen] = useState(false);
  const [visibleTaskCount, setVisibleTaskCount] = useState(10);
  const eventStreamCursorRef = useRef(0);
  const sessionSyncTimerRef = useRef(null);
  const artifactPreviewURLRef = useRef("");
  const artifactPreviewRequestRef = useRef(0);
  const threadRef = useRef(null);
  const shouldAutoScrollRef = useRef(true);
  const pendingApprovalCountRef = useRef(0);
  const scrollFrameRef = useRef(0);

  useEffect(() => {
    document.title = "TMA Workbench";
    let active = true;
    async function restoreRecentSession() {
      setStatus("loading history");
      const response = await api.sessions({ limit: 30 });
      if (!active) return;
      const sessions = response.sessions || [];
      setRecentSessions(sessions);
      const remembered = rememberedSessionID();
      const selected = sessions.find((session) => session.id === remembered) || sessions[0];
      if (!selected) {
        await loadPreSessionDefaults();
        setStatus("ready");
        return;
      }
      setSessionID(selected.id);
      setAgentID(selected.agent_id || "");
      setEnvironmentID(selected.environment_id || "");
      rememberSession(selected.id);
      await loadSession(selected.id);
      if (active) setStatus("history restored");
    }
    restoreRecentSession().catch((error) => {
      if (active) setStatus(error.message);
    });
    return () => {
      active = false;
    };
  }, []);

  async function loadPreSessionDefaults() {
    const [defaultAgent, providersResponse] = await Promise.all([
      api.defaultAgent(),
      api.llmProviders()
    ]);
    const enabledProviders = (providersResponse.providers || []).filter((provider) => provider.enabled !== false);
    const modelResponses = await Promise.all(enabledProviders.map((provider) => api.llmModels(provider.id).catch(() => ({ models: [] }))));
    const options = enabledProviders.flatMap((provider, index) => (
      (modelResponses[index].models || []).map((model) => ({
        label: `${provider.id} / ${model.model}`,
        llmModel: model.model,
        llmProvider: provider.id
      }))
    ));
    setDefaultAgentConfig(defaultAgent);
    setModelOptions(options);
    setSettingsDraft((current) => ({
      ...current,
      llmModel: current.llmModel || defaultAgent.config_version?.llm_model || "",
      llmProvider: current.llmProvider || defaultAgent.config_version?.llm_provider || "",
      toolRuntime: current.toolRuntime || "cloud_sandbox"
    }));
  }

  const events = eventsResponse.events || [];
  const conversationEvents = useMemo(() => events
    .filter((event) => event.type === "user.message" || event.type === "agent.message")
    .sort((left, right) => Number(left.seq || 0) - Number(right.seq || 0)), [events]);
  const chatTimelineEvents = useMemo(() => events
    .filter((event) => [
      "user.message",
      "agent.message",
      "runtime.tool_call",
      "runtime.tool_result",
      "runtime.tool_intervention_required",
      "runtime.tool_intervention_approved",
      "runtime.tool_intervention_rejected"
    ].includes(event.type))
    .sort((left, right) => Number(left.seq || 0) - Number(right.seq || 0)), [events]);
  const interventions = interventionResponse.interventions || [];
  const artifacts = artifactResponse.artifacts || [];
  const lastUserSeq = useMemo(() => {
    return events.reduce((maximum, event) => (
      event.type === "user.message" ? Math.max(maximum, Number(event.seq || 0)) : maximum
    ), 0);
  }, [events]);
  const liveSignal = useMemo(() => turnSignal(events, {
    sinceSeq: lastUserSeq,
    sessionStatus: sessionMeta?.status,
    waitingForReply,
    interventions,
    includeSuccess: waitingForReply
  }), [events, interventions, lastUserSeq, sessionMeta?.status, waitingForReply]);
  const runState = useMemo(() => {
    if (interventions.length) return "waiting approval";
    if (liveSignal?.kind === "thinking") return "running";
    if (liveSignal?.kind === "success") return "completed";
    if (liveSignal?.kind === "error") return "failed";
    if (sessionMeta?.status) return sessionMeta.status;
    return sessionID ? "active" : "not started";
  }, [interventions.length, liveSignal?.kind, sessionID, sessionMeta?.status]);
  const hasPendingApprovals = interventions.length > 0;
  const activityEvents = useMemo(() => {
    return compactActivityEvents(events);
  }, [events]);
  const filteredTaskSessions = useMemo(() => {
    const query = taskSearch.trim().toLowerCase();
    const matches = recentSessions.filter((session) => {
      if (!query) return true;
      const title = String(session.title || "").toLowerCase();
      const id = String(session.id || "").toLowerCase();
      return title.includes(query) || id.includes(query);
    });
    return [...matches].sort((left, right) => {
      if (left.id === sessionID) return -1;
      if (right.id === sessionID) return 1;
      const statusRank = sessionStatusRank(left.status) - sessionStatusRank(right.status);
      if (statusRank !== 0) return statusRank;
      return new Date(right.created_at || 0).getTime() - new Date(left.created_at || 0).getTime();
    });
  }, [recentSessions, sessionID, taskSearch]);
  const visibleTaskSessions = useMemo(() => {
    return filteredTaskSessions.slice(0, visibleTaskCount);
  }, [filteredTaskSessions, visibleTaskCount]);
  const hasMoreTasks = filteredTaskSessions.length > visibleTaskCount;
  const runtimeOptions = useMemo(() => {
    const available = new Set(runtimeCapabilities.available_runtimes || ["cloud_sandbox"]);
    const options = [{ value: "cloud_sandbox", label: "Sandbox" }];
    if (available.has("local_system") || settingsDraft.toolRuntime === "local_system") {
      options.push({ value: "local_system", label: "Local" });
    }
    return options;
  }, [runtimeCapabilities.available_runtimes, settingsDraft.toolRuntime]);
  const selectedModelValue = settingsDraft.llmProvider && settingsDraft.llmModel ? `${settingsDraft.llmProvider}::${settingsDraft.llmModel}` : "";
  async function loadSession(value) {
    const [nextSession, nextEvents, nextInterventions, nextArtifacts] = await Promise.all([
      api.session(value).catch((error) => ({ error: String(error), id: value })),
      api.events(value).catch((error) => ({ events: [], error: String(error) })),
      api.interventions(value, "pending").catch((error) => ({ interventions: [], error: String(error) })),
      api.artifacts(value).catch((error) => ({ artifacts: [], error: String(error) }))
    ]);
    setSessionMeta(nextSession);
    if (!nextSession.error) {
      setAgentID(nextSession.agent_id || "");
      setEnvironmentID(nextSession.environment_id || "");
    }
    eventStreamCursorRef.current = maxSeq(nextEvents.events || []);
    setEventsResponse(nextEvents);
    setInterventionResponse(nextInterventions);
    setArtifactResponse(nextArtifacts);
    if (nextSession?.id) {
      setRecentSessions((current) => [nextSession, ...current.filter((item) => item.id !== nextSession.id)]);
    }
    return {
      session: nextSession,
      events: nextEvents.events || [],
      interventions: nextInterventions.interventions || []
    };
  }

  async function loadSessionSettings(value, sessionValue = sessionMeta) {
    const nextSessionID = String(value || "").trim();
    if (!nextSessionID) {
      setRuntimeConfig(null);
      setModelOptions([]);
      setRuntimeCapabilities({ default_runtime: "cloud_sandbox", available_runtimes: ["cloud_sandbox"] });
      setSettingsDraft({
        interventionMode: "request_approval",
        llmModel: "",
        llmProvider: "",
        toolRuntime: "cloud_sandbox"
      });
      return;
    }
    const [config, capabilities, providersResponse] = await Promise.all([
      api.sessionRuntimeConfig(nextSessionID),
      api.sessionRuntimeCapabilities(nextSessionID),
      api.llmProviders()
    ]);
    const enabledProviders = (providersResponse.providers || []).filter((provider) => provider.enabled !== false);
    const modelResponses = await Promise.all(enabledProviders.map((provider) => api.llmModels(provider.id).catch(() => ({ models: [] }))));
    const options = enabledProviders.flatMap((provider, index) => (
      (modelResponses[index].models || []).map((model) => ({
        label: `${provider.id} / ${model.model}`,
        llmModel: model.model,
        llmProvider: provider.id
      }))
    ));
    setRuntimeConfig(config);
    setRuntimeCapabilities(capabilities);
    setModelOptions(options);
    const parsedSettings = parseSessionRuntimeSettings(sessionValue?.runtime_settings || {});
    const preferredRuntime = parsedSettings.toolRuntime || capabilities.default_runtime || "cloud_sandbox";
    setSettingsDraft({
      interventionMode: parsedSettings.interventionMode || "request_approval",
      llmModel: parsedSettings.llmModel || config.llm_model || "",
      llmProvider: parsedSettings.llmProvider || config.llm_provider || "",
      toolRuntime: preferredRuntime === "auto" ? "cloud_sandbox" : preferredRuntime
    });
  }

  async function syncSession(value = sessionID) {
    const sessionValue = String(value || "").trim();
    if (!sessionValue) return null;
    const [nextSession, nextInterventions, nextArtifacts] = await Promise.all([
      api.session(sessionValue).catch((error) => ({ error: String(error), id: sessionValue })),
      api.interventions(sessionValue, "pending").catch((error) => ({ interventions: [], error: String(error) })),
      api.artifacts(sessionValue).catch((error) => ({ artifacts: [], error: String(error) }))
    ]);
    setSessionMeta(nextSession);
    if (!nextSession.error) {
      setAgentID(nextSession.agent_id || "");
      setEnvironmentID(nextSession.environment_id || "");
      setRecentSessions((current) => [nextSession, ...current.filter((item) => item.id !== nextSession.id)]);
    }
    setInterventionResponse(nextInterventions);
    setArtifactResponse(nextArtifacts);
    return {
      session: nextSession,
      interventions: nextInterventions.interventions || []
    };
  }

  useEffect(() => {
    const currentSessionID = String(sessionID || "").trim();
    if (!currentSessionID || sessionMeta?.id !== currentSessionID || sessionMeta?.error) return undefined;
    const afterSeq = Number(eventStreamCursorRef.current || 0);
    const source = new EventSource(`/v1/sessions/${encodeURIComponent(currentSessionID)}/events/stream?after_seq=${afterSeq}`);
    let active = true;

    function queueSessionSync() {
      if (sessionSyncTimerRef.current) {
        window.clearTimeout(sessionSyncTimerRef.current);
      }
      sessionSyncTimerRef.current = window.setTimeout(() => {
        sessionSyncTimerRef.current = null;
        syncSession(currentSessionID).catch((error) => {
          if (active) setStatus(error.message);
        });
      }, 150);
    }

    function applyStreamEvent(event) {
      if (!active || !event) return;
      eventStreamCursorRef.current = Math.max(eventStreamCursorRef.current || 0, Number(event.seq || 0));
      setEventsResponse((current) => ({
        ...current,
        events: mergeEvents(current.events, [event])
      }));
      if (sessionSyncEventTypes.has(event.type)) {
        queueSessionSync();
      }
    }

    function handleStreamMessage(nativeEvent) {
      try {
        applyStreamEvent(JSON.parse(nativeEvent.data));
      } catch (error) {
        if (active) setStatus(error.message);
      }
    }

    sessionEventStreamTypes.forEach((type) => {
      source.addEventListener(type, handleStreamMessage);
    });
    source.onerror = () => {
      if (!active) return;
      if (source.readyState === EventSource.CLOSED) return;
      setStatus((current) => (current === "waiting for reply" ? "stream reconnecting" : current));
    };

    return () => {
      active = false;
      source.close();
      if (sessionSyncTimerRef.current) {
        window.clearTimeout(sessionSyncTimerRef.current);
        sessionSyncTimerRef.current = null;
      }
    };
  }, [sessionID, sessionMeta?.id]);

  useEffect(() => {
    if (!waitingForReply) return;
    if (!liveSignal) {
      if (sessionMeta?.status === "failed") {
        setStatus("reply failed");
        setWaitingForReply(false);
      }
      return;
    }
    if (liveSignal.kind === "thinking") {
      setStatus("thinking");
      return;
    }
    if (liveSignal.kind === "approval") {
      setStatus("waiting approval");
      setWaitingForReply(false);
      return;
    }
    if (liveSignal.kind === "success") {
      setStatus("replied");
      setWaitingForReply(false);
      return;
    }
    if (liveSignal.kind === "error") {
      setStatus(`reply failed: ${liveSignal.detail}`);
      setWaitingForReply(false);
    }
  }, [waitingForReply, liveSignal, sessionMeta?.status]);

  useEffect(() => {
    loadSessionSettings(sessionID, sessionMeta).catch((error) => setStatus(error.message));
  }, [sessionID, sessionMeta]);

  useEffect(() => {
    return () => {
      if (scrollFrameRef.current) {
        window.cancelAnimationFrame(scrollFrameRef.current);
        scrollFrameRef.current = 0;
      }
      if (artifactPreviewURLRef.current) {
        URL.revokeObjectURL(artifactPreviewURLRef.current);
        artifactPreviewURLRef.current = "";
      }
    };
  }, []);

  useEffect(() => {
    shouldAutoScrollRef.current = true;
  }, [sessionID]);

  useEffect(() => {
    setVisibleTaskCount(10);
  }, [taskSearch]);

  useEffect(() => {
    if (!threadRef.current || !shouldAutoScrollRef.current) return;
    window.requestAnimationFrame(() => {
      const node = threadRef.current;
      if (!node) return;
      node.scrollTop = node.scrollHeight;
    });
  }, [chatTimelineEvents.length, hasPendingApprovals, waitingForReply]);

  useEffect(() => {
    if (hasPendingApprovals && interventions.length > pendingApprovalCountRef.current) {
      setApprovalsOpen(true);
    }
    if (!hasPendingApprovals) {
      setApprovalsOpen(false);
    }
    pendingApprovalCountRef.current = interventions.length;
  }, [hasPendingApprovals, interventions.length]);

  async function refresh(nextSessionID = sessionID) {
    const value = String(nextSessionID || "").trim();
    if (!value) {
      setStatus("session required");
      return;
    }
    setStatus("refreshing");
    await loadSession(value);
    await loadSessionSettings(value);
    rememberSession(value);
    setStatus("synced");
  }

  async function startSession() {
    setStatus("creating session");
    const agent = agentID.trim() ? { id: agentID.trim() } : (defaultAgentConfig || await api.defaultAgent());
    const environment = environmentID.trim() ? { id: environmentID.trim() } : await api.createEnvironment({
      name: "Workbench Environment",
      config: { type: "cloud" }
    });
    const session = await api.createSession({
      agent_id: agent.id,
      environment_id: environment.id,
      title: task.trim() ? task.trim().slice(0, 80) : "New workbench task"
    });
    setAgentID(agent.id);
    setEnvironmentID(environment.id);
    setSessionID(session.id);
    setSessionMeta(session);
    rememberSession(session.id);
    setRecentSessions((current) => [session, ...current.filter((item) => item.id !== session.id)]);
    const shouldApplyInitialSettings = Boolean(
      settingsDraft.interventionMode ||
      settingsDraft.toolRuntime ||
      settingsDraft.llmProvider ||
      settingsDraft.llmModel
    );
    if (shouldApplyInitialSettings) {
      const updatedSession = await api.updateSessionRuntimeSettings(session.id, {
        intervention_mode: settingsDraft.interventionMode || "request_approval",
        llm_model: settingsDraft.llmModel || agent.config_version?.llm_model || "",
        llm_provider: settingsDraft.llmProvider || agent.config_version?.llm_provider || "",
        tool_runtime: settingsDraft.toolRuntime || "cloud_sandbox"
      });
      setSessionMeta(updatedSession);
      await loadSessionSettings(session.id, updatedSession);
    }
    if (task.trim()) {
      await sendTask(session.id);
    } else {
      await refresh(session.id);
    }
  }

  async function sendTask(nextSessionID = sessionID) {
    const value = String(nextSessionID || "").trim();
    const text = task.trim();
    if (!value) {
      setStatus("session required");
      return;
    }
    if (!text) {
      setStatus("task required");
      return;
    }
    const queued = Boolean(waitingForReply || sessionMeta?.status === "running" || sessionMeta?.status === "interrupting");
    setStatus(queued ? "queued message" : "sending task");
    const response = await api.sendSessionMessage(value, text, { preferLatest: false });
    const appendedEvents = response.events || [];
    const userEvent = appendedEvents.find((event) => event.type === "user.message");
    setEventsResponse((current) => ({
      ...current,
      events: mergeEvents(current.events, appendedEvents)
    }));
    eventStreamCursorRef.current = Math.max(eventStreamCursorRef.current || 0, maxSeq(appendedEvents));
    setTask("");
    setWaitingForReply(true);
    setStatus("waiting for reply");
    if (userEvent?.seq) {
      eventStreamCursorRef.current = Math.max(eventStreamCursorRef.current || 0, Number(userEvent.seq || 0));
    }
  }

  async function archiveTask(targetSessionID) {
    const nextSessionID = String(targetSessionID || "").trim();
    if (!nextSessionID) return;
    setSessionAction(`archive:${nextSessionID}`);
    setStatus("archiving");
    try {
      const archived = await api.archiveSession(nextSessionID);
      setRecentSessions((current) => current.filter((item) => item.id !== nextSessionID));
      if (sessionID === nextSessionID) {
        setSessionMeta(archived);
        setWaitingForReply(false);
      }
      setStatus("archived");
    } finally {
      setSessionAction("");
    }
  }

  async function deleteTask(targetSessionID) {
    const nextSessionID = String(targetSessionID || "").trim();
    if (!nextSessionID) return;
    const confirmed = window.confirm(`Delete session ${nextSessionID}? This removes the session and its events.`);
    if (!confirmed) return;
    setSessionAction(`delete:${nextSessionID}`);
    setStatus("deleting");
    try {
      await api.deleteSession(nextSessionID);
      setRecentSessions((current) => current.filter((item) => item.id !== nextSessionID));
      if (sessionID === nextSessionID) {
        startNewTask();
      }
      setStatus("deleted");
    } finally {
      setSessionAction("");
    }
  }

  function clearArtifactPreview() {
    artifactPreviewRequestRef.current += 1;
    if (artifactPreviewURLRef.current) {
      URL.revokeObjectURL(artifactPreviewURLRef.current);
      artifactPreviewURLRef.current = "";
    }
    setArtifactPreview(null);
  }

  async function previewArtifact(artifact) {
    if (!sessionID || !artifact?.id) return;
    const requestID = artifactPreviewRequestRef.current + 1;
    artifactPreviewRequestRef.current = requestID;
    if (artifactPreviewURLRef.current) {
      URL.revokeObjectURL(artifactPreviewURLRef.current);
      artifactPreviewURLRef.current = "";
    }
    setArtifactPreview({ artifact, status: "loading" });
    try {
      const response = await api.getBlob(api.artifactDownloadPath(sessionID, artifact.id));
      const contentType = response.headers.get("Content-Type") || artifactMetadata(artifact).content_type || "";
      const contentLength = Number(response.headers.get("Content-Length") || 0);
      const kind = previewKindForArtifact(artifact, contentType);
      if (kind === "image") {
        const blob = await response.blob();
        if (artifactPreviewRequestRef.current !== requestID) return;
        const objectUrl = URL.createObjectURL(blob);
        artifactPreviewURLRef.current = objectUrl;
        setArtifactPreview({ artifact, status: "ready", kind, contentType, objectUrl });
        return;
      }
      if (kind === "text") {
        if (contentLength > 512 * 1024) {
          setArtifactPreview({ artifact, status: "error", kind, contentType, error: "Preview is too large. Download the file to inspect it." });
          return;
        }
        let text = await response.text();
        if (artifactPreviewRequestRef.current !== requestID) return;
        if (contentType.toLowerCase().includes("json")) {
          try {
            text = JSON.stringify(JSON.parse(text), null, 2);
          } catch {}
        }
        const truncated = text.length > 64000;
        setArtifactPreview({
          artifact,
          status: "ready",
          kind,
          contentType,
          text: truncated ? `${text.slice(0, 64000)}\n\n[Preview truncated]` : text
        });
        return;
      }
      setArtifactPreview({ artifact, status: "error", kind, contentType, error: "No inline preview for this file type yet. Download it to inspect." });
    } catch (error) {
      if (artifactPreviewRequestRef.current === requestID) {
        setArtifactPreview({ artifact, status: "error", error: error.message });
      }
    }
  }

  async function approve(intervention) {
    setDecidingApprovalID(intervention.call_id);
    setWaitingForReply(true);
    setStatus("approving");
    try {
      const response = await api.approveIntervention(sessionID, intervention.turn_id, intervention.call_id, { reason: "approved from app" });
      setEventsResponse((current) => ({
        ...current,
        events: mergeEvents(current.events, response.events || [])
      }));
      eventStreamCursorRef.current = Math.max(eventStreamCursorRef.current || 0, maxSeq(response.events || []));
      setInterventionResponse((current) => ({
        ...current,
        interventions: (current.interventions || []).filter((item) => item.call_id !== intervention.call_id)
      }));
      setWaitingForReply(true);
      setStatus("waiting for reply");
    } catch (error) {
      setWaitingForReply(false);
      throw error;
    } finally {
      setDecidingApprovalID("");
    }
  }

  async function reject(intervention) {
    const reason = window.prompt("Reject reason", "rejected from app");
    if (reason === null) return;
    setDecidingApprovalID(intervention.call_id);
    setStatus("rejecting");
    try {
      await api.rejectIntervention(sessionID, intervention.turn_id, intervention.call_id, { reason });
      await refresh();
    } finally {
      setDecidingApprovalID("");
    }
  }

  async function openSession(session) {
    setStatus("loading chat");
    setSessionID(session.id);
    setAgentID(session.agent_id || "");
    setEnvironmentID(session.environment_id || "");
    rememberSession(session.id);
    await loadSession(session.id);
    await loadSessionSettings(session.id, session);
    setStatus("history restored");
  }

  function startNewTask() {
    setAgentID("");
    setEnvironmentID("");
    setSessionID("");
    setSessionMeta(null);
    setEventsResponse({ events: [] });
    setInterventionResponse({ interventions: [] });
    setArtifactResponse({ artifacts: [] });
    setWaitingForReply(false);
    setRuntimeConfig(null);
    setRuntimeCapabilities({ default_runtime: "cloud_sandbox", available_runtimes: ["cloud_sandbox"] });
    setSettingsDraft({
      interventionMode: "request_approval",
      llmModel: defaultAgentConfig?.config_version?.llm_model || "",
      llmProvider: defaultAgentConfig?.config_version?.llm_provider || "",
      toolRuntime: "cloud_sandbox"
    });
    setApprovalsOpen(false);
    setStatus("ready");
    eventStreamCursorRef.current = 0;
    forgetSession();
    loadPreSessionDefaults().catch((error) => setStatus(error.message));
  }

  function handleThreadScroll(event) {
    const node = event.currentTarget;
    if (scrollFrameRef.current) return;
    scrollFrameRef.current = window.requestAnimationFrame(() => {
      scrollFrameRef.current = 0;
      const distanceFromBottom = node.scrollHeight - node.scrollTop - node.clientHeight;
      const nextNearBottom = distanceFromBottom < 96;
      shouldAutoScrollRef.current = nextNearBottom;
    });
  }

  async function applySessionSettings(patch) {
    const nextDraft = { ...settingsDraft, ...patch };
    setSettingsDraft(nextDraft);
    if (!sessionID) return;
    setSavingSettings(true);
    setStatus("saving settings");
    try {
      const updatedSession = await api.updateSessionRuntimeSettings(sessionID, {
        intervention_mode: nextDraft.interventionMode,
        llm_model: nextDraft.llmModel,
        llm_provider: nextDraft.llmProvider,
        tool_runtime: nextDraft.toolRuntime
      });
      setSessionMeta(updatedSession);
      await loadSessionSettings(sessionID, updatedSession);
      setStatus("settings saved");
    } finally {
      setSavingSettings(false);
    }
  }

  const starterTasks = [
    {
      detail: "Read the repo, identify the major layers, and summarize how the current system fits together.",
      prompt: "Review this repository and summarize the current architecture.",
      title: "Understand the codebase"
    },
    {
      detail: "Turn rough product ideas into a concrete plan with scope, risks, and the next step.",
      prompt: "Create a small change plan for the next UI iteration.",
      title: "Plan the next iteration"
    },
    {
      detail: "Run the relevant checks, capture failures, and point to the most useful fix path.",
      prompt: "Run the relevant checks and report what is failing.",
      title: "Check build and quality"
    }
  ];
  const sendButtonLabel = sessionID ? "Send" : "Start";
  const hasTaskSearch = Boolean(taskSearch.trim());

  return (
    <div className="user-app">
      <header className="user-topbar">
        <div className="topbar-brand">
          <div className="topbar-label">TMA Workbench</div>
          <div className="topbar-context">{sessionMeta?.title || sessionID || "General agent workspace"}</div>
        </div>
        <div className="topbar-status">
          <span className="status-readout">{status}</span>
          <Pill value={runState} />
        </div>
      </header>
      <div className="user-layout">
        <aside className="user-sidebar">
          <Panel title="Workspace">
            <div className="stack">
              <button type="button" onClick={startNewTask}>New Task</button>
            </div>
          </Panel>
          <Panel title="Tasks" className="tasks-panel">
            <div className="stack task-panel-content">
              <input
                value={taskSearch}
                onChange={(event) => setTaskSearch(event.target.value)}
                placeholder="Search tasks..."
              />
              <div className="task-section-list task-section-scroll">
                {filteredTaskSessions.length ? (
                  <div className="task-section">
                    <div className="task-section-title">All tasks</div>
                    <div className="turn-list">
                      {visibleTaskSessions.map((session) => (
                        <div
                          className={`turn-item task-nav-item ${session.id === sessionID ? "active" : ""}`}
                          key={session.id}
                        >
                          <button
                            className="task-nav-open"
                            type="button"
                            title={session.title || "Untitled task"}
                            onClick={() => openSession(session).catch((error) => setStatus(error.message))}
                          >
                            <div className="task-nav-row">
                              <TaskStatusIcon status={session.status} />
                              <strong>{session.title || "Untitled task"}</strong>
                              <span className="task-nav-time">{formatTaskTime(session.created_at)}</span>
                            </div>
                          </button>
                          <div className="task-inline-actions">
                            {session.status !== "terminated" ? (
                              <button
                                className="icon-button"
                                type="button"
                                title="Archive"
                                aria-label="Archive"
                                disabled={sessionAction === `archive:${session.id}`}
                                onClick={() => archiveTask(session.id).catch((error) => setStatus(error.message))}
                              >
                                <ArchiveIcon />
                              </button>
                            ) : null}
                            <button
                              className="icon-button danger"
                              type="button"
                              title="Delete"
                              aria-label="Delete"
                              disabled={sessionAction === `delete:${session.id}`}
                              onClick={() => deleteTask(session.id).catch((error) => setStatus(error.message))}
                            >
                              <DeleteIcon />
                            </button>
                          </div>
                        </div>
                      ))}
                    </div>
                    {hasMoreTasks ? (
                      <button className="secondary task-more-button" type="button" onClick={() => setVisibleTaskCount((current) => current + 10)}>
                        Show more tasks
                      </button>
                    ) : null}
                  </div>
                ) : null}
                {!filteredTaskSessions.length ? (
                  <Empty>{hasTaskSearch ? "No tasks match your search." : "No tasks yet."}</Empty>
                ) : null}
              </div>
            </div>
          </Panel>
        </aside>
        <main className="user-main">
          <>
            <section className="user-thread" onScroll={handleThreadScroll} ref={threadRef}>
                {hasPendingApprovals ? (
                  <div className="approval-alert">
                    <div>
                      <strong>{interventions.length} approval{interventions.length === 1 ? "" : "s"} waiting</strong>
                      <div className="subtle">Review the pending tool call before the agent continues.</div>
                    </div>
                    <button type="button" onClick={() => setApprovalsOpen(true)}>Review</button>
                  </div>
                ) : null}
                {chatTimelineEvents.length ? chatTimelineEvents.map((event) => {
                  if (event.type === "user.message" || event.type === "agent.message") {
                    const role = event.type === "user.message" ? "user" : "agent";
                    return (
                      <article className={`message ${role}`} key={`${event.seq}-${event.type}`}>
                        <Meta><strong>{role === "user" ? "你" : "通用智能体"}</strong><span>{formatTime(event.created_at)}</span></Meta>
                        <MessageBody event={event} />
                      </article>
                    );
                  }
                  return <ProcessEventCard event={event} key={`${event.seq}-${event.type}`} />;
                }) : (
                  <div className="welcome-state">
                    <div className="welcome-hero">
                      <div className="welcome-eyebrow">Managed Agent Workspace</div>
                      <h2>Start a task and let the general agent move it forward.</h2>
                      <p>
                        TMA can inspect code, run checks, edit files, use tools, and keep a session moving until the work is done.
                        Pick a starting point below or type your own request.
                      </p>
                      <div className="welcome-tags">
                        <span>Code review</span>
                        <span>File edits</span>
                        <span>Build and test</span>
                        <span>Tool orchestration</span>
                      </div>
                    </div>
                    <div className="starter-grid">
                      {starterTasks.map((starter) => (
                        <button className="starter-card" type="button" key={starter.title} onClick={() => setTask(starter.prompt)}>
                          <strong>{starter.title}</strong>
                          <div>{starter.detail}</div>
                        </button>
                      ))}
                    </div>
                    <div className="welcome-note">
                      <strong>Tip</strong>
                      <span>You can preselect the model, approval mode, and runtime before the task starts.</span>
                    </div>
                  </div>
                )}
                {waitingForReply ? (
                  <article className="message agent pending">
                    <Meta><strong>通用智能体</strong></Meta>
                    <div className="message-text">正在思考并生成回复…</div>
                  </article>
                ) : null}
              </section>
              <section className="composer">
                <div className="composer-shell">
                  <textarea value={task} onChange={(event) => setTask(event.target.value)} placeholder="Ask TMA to build, inspect, edit, or run something..." />
                  <div className="composer-footer">
                    <div className="composer-settings-inline">
                      <label className="composer-setting">
                        <span>Approval</span>
                        <select
                          disabled={savingSettings}
                          value={settingsDraft.interventionMode}
                          onChange={(event) => applySessionSettings({ interventionMode: event.target.value }).catch((error) => setStatus(error.message))}
                        >
                          <option value="request_approval">Ask first</option>
                          <option value="approve_for_me">Approve for me</option>
                          <option value="full_access">Full access</option>
                        </select>
                      </label>
                      <label className="composer-setting">
                        <span>Runtime</span>
                        <select
                          disabled={savingSettings}
                          value={settingsDraft.toolRuntime}
                          onChange={(event) => applySessionSettings({ toolRuntime: event.target.value }).catch((error) => setStatus(error.message))}
                        >
                          {runtimeOptions.map((option) => (
                            <option key={option.value} value={option.value}>{option.label}</option>
                          ))}
                        </select>
                      </label>
                      <label className="composer-setting composer-setting-model">
                        <span>Model</span>
                        <select
                          disabled={!modelOptions.length || savingSettings}
                          value={selectedModelValue}
                          onChange={(event) => {
                            const [llmProvider, llmModel] = String(event.target.value || "").split("::");
                            applySessionSettings({ llmModel: llmModel || "", llmProvider: llmProvider || "" }).catch((error) => setStatus(error.message));
                          }}
                        >
                          {!modelOptions.length ? <option value="">No models</option> : null}
                          {modelOptions.map((option) => (
                            <option key={`${option.llmProvider}:${option.llmModel}`} value={`${option.llmProvider}::${option.llmModel}`}>
                              {option.label}
                            </option>
                          ))}
                        </select>
                      </label>
                    </div>
                    <div className="composer-actions-inline">
                      {waitingForReply && !hasPendingApprovals ? (
                        <span className="subtle">New messages will queue behind the current run.</span>
                      ) : !sessionID ? (
                        <span className="subtle">Current selections will be applied when the task starts.</span>
                      ) : null}
                      <button type="button" disabled={hasPendingApprovals} onClick={() => sessionID ? sendTask().catch((error) => setStatus(error.message)) : startSession().catch((error) => setStatus(error.message))}>{sendButtonLabel}</button>
                    </div>
                  </div>
                </div>
              </section>
          </>
        </main>
        <aside className="user-sidebar right">
          <Panel title="Activity">
            <div className="list activity-list">
              {activityEvents.length ? activityEvents.map((item) => {
                const activity = item.activity;
                const event = item.event;
                return (
                  <div className={`list-item activity-item ${activity.kind}`} key={`${event.seq}-${event.type}-${item.count}`}>
                    <div className="activity-head">
                      <strong>{activity.title}</strong>
                      <span>{formatTime(event.created_at)}</span>
                    </div>
                    <div className="subtle">
                      {activity.detail || activitySummary(event) || "No details."}
                      {item.count > 1 ? <span> · {item.count} updates</span> : null}
                    </div>
                  </div>
                );
              }) : <Empty>No runtime activity yet.</Empty>}
            </div>
          </Panel>
          <Panel title="Artifacts">
            <div className="list">
              {artifacts.length ? artifacts.map((artifact) => {
                const href = api.artifactDownloadPath(sessionID, artifact.id);
                const previewKind = previewKindForArtifact(artifact);
                return (
                  <div className="list-item" key={artifact.id}>
                    <div><strong>{artifact.name || artifact.id}</strong></div>
                    <Meta><span>{artifact.artifact_type}</span><span>{artifact.turn_id || ""}</span></Meta>
                    {artifact.description ? <div className="subtle">{artifact.description}</div> : null}
                    <div className="artifact-actions">
                      <button className="secondary" type="button" disabled={previewKind === "download"} onClick={() => previewArtifact(artifact).catch((error) => setStatus(error.message))}>Preview</button>
                      <a className="link" href={href} target="_blank" rel="noreferrer">Download</a>
                    </div>
                  </div>
                );
              }) : <Empty>No artifacts.</Empty>}
            </div>
            {artifactPreview ? (
              <div className="artifact-preview">
                <div className="artifact-preview-header">
                  <div>
                    <strong>{artifactName(artifactPreview.artifact)}</strong>
                    <div className="subtle">{artifactPreview.contentType || artifactPreview.artifact?.artifact_type || "artifact"}</div>
                  </div>
                  <button className="secondary" type="button" onClick={clearArtifactPreview}>Close</button>
                </div>
                {artifactPreview.status === "loading" ? <Empty>Loading preview...</Empty> : null}
                {artifactPreview.status === "error" ? <div className="artifact-preview-error">{artifactPreview.error}</div> : null}
                {artifactPreview.status === "ready" && artifactPreview.kind === "image" ? (
                  <img className="preview-media" src={artifactPreview.objectUrl} alt={artifactName(artifactPreview.artifact)} />
                ) : null}
                {artifactPreview.status === "ready" && artifactPreview.kind === "text" ? (
                  <pre className="artifact-preview-text">{artifactPreview.text || ""}</pre>
                ) : null}
              </div>
            ) : null}
          </Panel>
          <Panel title="Session">
            {sessionMeta ? (
              <div className="stack">
                <Meta><span>{sessionMeta.id}</span><Pill value={sessionMeta.status || "unknown"} /></Meta>
                <div><strong>{sessionMeta.title || "Untitled session"}</strong></div>
                <pre>{pretty(sessionMeta.runtime_settings || {})}</pre>
              </div>
            ) : <Empty>No session loaded.</Empty>}
          </Panel>
        </aside>
      </div>
      {approvalsOpen ? (
        <div className="approval-modal-backdrop" role="presentation" onClick={() => setApprovalsOpen(false)}>
          <section className="approval-modal" role="dialog" aria-modal="true" aria-label="Approvals" onClick={(event) => event.stopPropagation()}>
            <div className="approval-modal-header">
              <div>
                <h2>Approvals</h2>
                <div className="subtle">{sessionID ? `Session ${sessionID}` : "No session selected"}</div>
              </div>
              <div className="approval-modal-actions">
                <button className="secondary" type="button" onClick={() => refresh().catch((error) => setStatus(error.message))}>Refresh</button>
                <button className="secondary" type="button" onClick={() => setApprovalsOpen(false)}>Close</button>
              </div>
            </div>
            <div className="approval-list-main">
              {interventions.length ? interventions.map((intervention) => (
                <ApprovalCard
                  key={intervention.call_id}
                  intervention={intervention}
                  busy={decidingApprovalID === intervention.call_id}
                  onApprove={(item) => approve(item).catch((error) => setStatus(error.message))}
                  onReject={(item) => reject(item).catch((error) => setStatus(error.message))}
                />
              )) : (
                <div className="empty-state compact">
                  <h2>No pending approvals</h2>
                  <div>When a tool call needs confirmation, it will appear here with Approve and Reject actions.</div>
                </div>
              )}
            </div>
          </section>
        </div>
      ) : null}
    </div>
  );
}

createRoot(document.getElementById("root")).render(<WorkbenchApp />);
