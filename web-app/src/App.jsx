import React, { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { createRoot } from "react-dom/client";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import "./auth.js";
import * as api from "./api.js";
import SkillsManagement from "./SkillsManagement.jsx";
import { formatDuration, formatTaskTime, formatTime, pillClass, pretty } from "./utils.js";
import { buildToolCallLifecycles, terminalToolLifecycleEvent, toolCallID } from "./toolLifecycle.js";
import { groupMCPRuntimeStates, mcpRuntimeFailureLabel, mcpRuntimeStateLabel, summarizeMCPRuntimeStates } from "./mcpRuntimeStatus.js";
import { providerErrorPresentation } from "./providerErrors.js";
import { isReasoningChunk, mergeReasoningChunks } from "./reasoningEvents.js";
import DialogHost from "./workbench/DialogHost.jsx";
import { createDialogService } from "./workbench/dialogService.js";
import NotificationHost from "./workbench/NotificationHost.jsx";
import { createNotificationService } from "./workbench/notificationService.js";
import {
  createRelatedResourceService,
  isPreviewCancelledError
} from "./workbench/relatedResourceService.js";
import {
  artifactToResourceRef,
  createSessionArtifactProvider,
  htmlPreviewDocument,
  isHTMLResource,
  isMarkdownResource
} from "./workbench/sessionArtifactAdapter.js";
import PluginRouteHost from "./workbench/PluginRouteHost.jsx";
import { createPermissionService } from "./workbench/permissionService.js";
import { createStaticPluginRegistry } from "./workbench/pluginRuntime.js";
import { loadStaticPluginCatalog } from "./plugins/index.jsx";
import "./styles.css";

const activeSessionStorageKey = "tma.workbench.active-session";
const workflowStoragePrefix = "tma.workbench.workflow.";
const workbenchDialogService = createDialogService();
const workbenchNotificationService = createNotificationService();
const workbenchRelatedResourceService = createRelatedResourceService();
workbenchRelatedResourceService.registerProvider(createSessionArtifactProvider({
  downloadArtifact: api.downloadArtifact,
  artifactDownloadPath: api.artifactDownloadPath
}));
const workbenchHostPermissionService = createPermissionService({ grants: [] });
const workbenchTaskService = Object.freeze({
  async list(filters = {}) {
    const response = await api.sessions({
      limit: filters.limit || 50,
      ...(filters.workspaceId ? { workspace: filters.workspaceId } : {}),
      ...(filters.status ? { status: filters.status } : {}),
      ...(filters.includeArchived ? { includeArchived: true } : {})
    });
    return response.sessions || [];
  }
});
const workbenchArtifactService = Object.freeze({
  async list(sessionID) {
    const response = await api.artifacts(sessionID);
    return response.artifacts || [];
  }
});
const workbenchScopedHTTPService = Object.freeze({
  async request(path, options = {}) {
    const target = String(path || "").trim();
    if (!target.startsWith("/v2/") || target.startsWith("//")) {
      throw new Error("Plugin HTTP requests must target the scoped /v2 API.");
    }
    const requestOptions = { ...options };
    if (requestOptions.body && typeof requestOptions.body === "object" && !(requestOptions.body instanceof FormData)) {
      requestOptions.headers = { "Content-Type": "application/json", ...(requestOptions.headers || {}) };
      requestOptions.body = JSON.stringify(requestOptions.body);
    }
    const response = await fetch(target, requestOptions);
    const contentType = response.headers.get("Content-Type") || "";
    const body = contentType.includes("json") ? await response.json() : await response.text();
    if (!response.ok) throw new Error(typeof body === "string" ? body : body?.error || `HTTP ${response.status}`);
    return body;
  }
});
const maxComposerFiles = 10;
const maxComposerFileBytes = 64 * 1024 * 1024;
const modelCapabilityOptions = [
  { value: "text", label: "文本" },
  { value: "text_image", label: "文本 + 图片解析" },
  { value: "image_generation", label: "图片生成" },
  { value: "video_generation", label: "视频生成" },
  { value: "embedding", label: "Embedding" },
  { value: "reranker", label: "Reranker" }
];
const embeddingProtocolOptions = ["openai_embeddings", "tei_embeddings", "ollama_embed"];
const rerankerProtocolOptions = ["jina_rerank", "cohere_rerank", "vllm_score"];

function defaultModelCapabilities(capabilityType, current = {}) {
  if (capabilityType === "embedding") {
    return {
      dimensions: Number(current.dimensions || 1024),
      distance_metric: current.distance_metric || "cosine",
      normalized: current.normalized !== false,
      max_batch_size: Number(current.max_batch_size || 32),
      protocol: current.protocol || "openai_embeddings"
    };
  }
  if (capabilityType === "reranker") {
    return {
      max_candidates: Number(current.max_candidates || 50),
      protocol: current.protocol || "jina_rerank"
    };
  }
  return {};
}
const supportedVisionImageTypes = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

function workbenchSurface() {
  if (window.matchMedia("(max-width: 640px)").matches) return "web_mobile";
  if (window.matchMedia("(max-width: 1100px)").matches) return "web_tablet";
  return "web_desktop";
}

function pluginPathFromHash() {
  try {
    const value = decodeURIComponent(String(window.location.hash || "").replace(/^#/, ""));
    return value.startsWith("/plugins/") ? value : "";
  } catch {
    return "";
  }
}
const sessionSyncEventTypes = new Set([
  "agent.message",
  "runtime.tool_intervention_required",
  "runtime.tool_intervention_approved",
  "runtime.tool_intervention_rejected",
  "runtime.failed",
  "runtime.completed",
  "session.status_idle",
  "session.status_failed",
  "session.status_terminated",
  "session.config_updated"
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

function DeleteIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="M5.5 4.5h5m-4-1.5h3m-4 3v6h5v-6m-7-1.5h9" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.4" />
    </svg>
  );
}

function RefreshIcon() {
  return <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M20 6v5h-5M4 18v-5h5M6.1 9a7 7 0 0 1 11.5-2.6L20 9M4 15l2.4 2.6A7 7 0 0 0 17.9 15" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.8" /></svg>;
}

function PinIcon({ filled = false }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="M5 2.5h6l-1 3 2 2v1H8.7V13L8 14l-.7-1V8.5H4v-1l2-2-1-3Z" fill={filled ? "currentColor" : "none"} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.25" />
    </svg>
  );
}

function MoreIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <circle cx="3.5" cy="8" r="1" fill="currentColor" />
      <circle cx="8" cy="8" r="1" fill="currentColor" />
      <circle cx="12.5" cy="8" r="1" fill="currentColor" />
    </svg>
  );
}

function FolderIcon({ open = false }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d={open ? "M2.5 5.5h11l-1.2 7h-8.6l-1.2-7Zm1-2h3l1 1h5v1" : "M2.5 4h4l1 1h6v7.5h-11V4Z"} fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.3" />
    </svg>
  );
}

function FileIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="M4 2.5h5l3 3v8H4v-11Zm5 0v3h3" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.3" />
    </svg>
  );
}

function CloseIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="m4 4 8 8m0-8-8 8" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.5" />
    </svg>
  );
}

function formatFileSize(size) {
  const bytes = Number(size || 0);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(bytes >= 10 * 1024 * 1024 ? 0 : 1)} MB`;
}

function composerFileID(file) {
  return `${file.name}-${file.size}-${file.lastModified}-${Math.random().toString(36).slice(2)}`;
}

function clipboardImageFile(file, index = 0) {
  const type = String(file?.type || "").toLowerCase();
  const extensions = {
    "image/gif": "gif",
    "image/jpeg": "jpg",
    "image/png": "png",
    "image/webp": "webp"
  };
  const extension = extensions[type] || "png";
  const timestamp = new Date().toISOString().replace(/[-:]/g, "").replace(/\.\d{3}Z$/, "");
  return new File([file], `clipboard-image-${timestamp}-${index + 1}.${extension}`, {
    type: file.type || `image/${extension}`,
    lastModified: Date.now() + index
  });
}

function composerAttachmentValue(item) {
  return item?.file || item || {};
}

function isSkillZIPAttachment(item) {
  const value = composerAttachmentValue(item);
  const name = String(value.name || "").trim().toLowerCase();
  const contentType = String(value.type || value.content_type || "").trim().toLowerCase();
  return name.endsWith(".zip") || contentType === "application/zip" || contentType === "application/x-zip-compressed";
}

function defaultComposerTask(text, attachments) {
  const requested = String(text || "").trim();
  if (requested) return requested;
  const files = Array.isArray(attachments) ? attachments : [];
  if (files.length === 1 && isSkillZIPAttachment(files[0])) {
    return "请将我上传的 ZIP 作为离线 Skill 安装。先调用 skills.preview，使用附件的 Session artifact_id，不要使用 workspace_path、主机路径或 URL。仅当 policy.allowed=true 且 install_state=new_install 或 upgrade 时再调用 skills.install；升级时设置 upgrade_existing=true，并原样携带 Preview 返回的 policy pin。安装完成后不要自动启用，先告诉我可以发起 skills.enable。";
  }
  return "请处理我上传的文件。";
}

function modelCapabilityLabel(value) {
  return modelCapabilityOptions.find((option) => option.value === value)?.label || "文本";
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

const builtInToolNamespaces = new Set(["default", "artifact", "browser", "agent", "skills", "web", "computer"]);

function humanizeToolName(value = "") {
  return String(value || "")
    .replace(/[._-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function toolSource(identifier, source = "", manifestType = "") {
  const normalizedSource = String(source || "").trim().toLowerCase();
  if (normalizedSource) return normalizedSource;
  const normalizedType = String(manifestType || "").trim().toLowerCase();
  if (normalizedType === "mcp_server") return "mcp";
  if (normalizedType === "process_plugin") return "worker_plugin";
  if (builtInToolNamespaces.has(String(identifier || "").trim().toLowerCase())) return "builtin";
  return "tool";
}

function toolSourceLabel(source) {
  switch (String(source || "").trim().toLowerCase()) {
    case "mcp":
      return "MCP";
    case "worker_plugin":
      return "Worker Plugin";
    case "builtin":
      return "Builtin";
    default:
      return "Tool";
  }
}

function toolTitle(identifier, apiName, source = "") {
  const key = [identifier, apiName].filter(Boolean).join(".");
  const titles = {
    "default.run_command": "执行命令",
    "default.execute_code": "执行代码",
    "default.read_file": "读取文件",
    "default.write_file": "写入文件",
    "default.edit_file": "编辑文件",
    "web.search": "搜索网页",
    "web.crawl": "读取网页",
    "browser.open": "打开浏览器",
    "browser.click": "浏览器点击",
    "browser.type": "浏览器输入",
    "browser.takeover": "接管浏览器",
    "computer.get_state": "检查桌面",
    "computer.screenshot": "截取屏幕",
    "computer.click": "桌面点击",
    "computer.type_text": "桌面输入",
    "computer.hotkey": "按下快捷键",
    "computer.launch_app": "启动应用",
    "computer.open_url": "打开网址",
    "computer.search_web": "浏览器内搜索",
    "skills.search": "查找 Skill",
    "skills.inspect": "检查 Skill",
    "skills.discover": "发现 Skill",
    "skills.preview": "安全预览 Skill",
    "skills.read_asset": "读取 Skill 资产",
    "skills.install": "安装 Skill",
    "skills.enable": "启用 Skill",
    "skills.disable": "停用 Skill"
  };
  if (titles[key] || titles[`${identifier}.${apiName}`]) return titles[key] || titles[`${identifier}.${apiName}`];
  if (String(source || "").trim().toLowerCase() === "mcp") return humanizeToolName(apiName) || humanizeToolName(key) || "MCP 工具";
  return key || "调用工具";
}

function toolRisk(identifier, apiName, reason = "") {
  const api = String(apiName || "");
  const riskReason = String(reason || "");
  if (identifier === "skills" && ["install", "enable", "disable"].includes(api)) return "high";
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
    return shortText([command, suffix].filter(Boolean).join(" "), 220) || "执行代码或命令。";
  }
  if (["read_file", "write_file", "edit_file"].includes(apiName)) {
    return shortText(valueText(firstValue(args, ["path", "file_path", "target_path", "filename"])), 220) || "访问工作区文件。";
  }
  if (apiName === "search") {
    return shortText(valueText(firstValue(args, ["query", "q"])), 220) || "搜索网页。";
  }
  if (apiName === "crawl" || apiName === "open_url") {
    return shortText(valueText(firstValue(args, ["url", "target_url"])), 220) || "打开或读取网页。";
  }
  if (identifier === "computer" || identifier === "browser") {
    return shortText(valueText(firstValue(args, ["app", "name", "url", "text", "selector", "capture_mode", "keys", "key"])), 220) || "与可见应用或浏览器交互。";
  }
  if (identifier === "skills") {
    const source = objectValue(args.source);
    const sourceLabel = source.provider === "artifact"
      ? `Session Artifact ${source.artifact_id || ""}`.trim()
      : [source.repository, source.ref, source.path].filter(Boolean).join(" · ");
    if (apiName === "preview") return sourceLabel ? `检查 ${sourceLabel}` : "检查 Skill 来源与安全策略。";
    if (apiName === "install") return [args.identifier || "待推导标识", sourceLabel].filter(Boolean).join(" · ");
    if (apiName === "enable") return `${args.identifier || "Skill"}${args.version ? ` v${args.version}` : ""} · 发布新的 Agent 配置版本`;
    if (apiName === "disable") return `${args.identifier || "Skill"} · 从新的 Agent 配置版本移除`;
    return shortText(valueText(firstValue(args, ["identifier", "query", "path"])), 220) || "访问当前 workspace 的 Skills Registry。";
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

function toolSummary({ identifier, apiName, args = {}, reason = "", success, source = "", manifestType = "" }) {
  const parts = normalizeToolParts(identifier, apiName);
  const resolvedSource = toolSource(parts.identifier, source, manifestType);
  const risk = toolRisk(parts.identifier, parts.apiName, reason);
  const title = toolTitle(parts.identifier, parts.apiName, resolvedSource);
  const detail = toolDetail(parts.identifier, parts.apiName, args);
  const status = success === true ? "Completed" : success === false ? "Failed" : "";
  return {
    detail,
    label: [parts.identifier, parts.apiName].filter(Boolean).join(".") || "tool",
    source: resolvedSource,
    sourceLabel: toolSourceLabel(resolvedSource),
    risk,
    status,
    title
  };
}

const builtinToolNamespaces = [
  { key: "default", title: "默认工具", description: "文件读写、命令执行和代码运行。" },
  { key: "browser", title: "浏览器工具", description: "打开页面、读取内容、点击输入和截图。" },
  { key: "web", title: "网页检索", description: "搜索公开网页并抓取页面正文。" },
  { key: "agent", title: "子智能体", description: "派生子任务、等待结果、收集输出。" },
  { key: "skills", title: "技能工具", description: "检索技能、查看技能和读取技能资产。" }
];

function parseToolPolicy(raw) {
  if (!raw) return { explicit: false, enabledToolPatterns: [], runtime: "" };
  if (Array.isArray(raw)) {
    return {
      explicit: true,
      enabledToolPatterns: raw.map((value) => String(value || "").trim()).filter(Boolean),
      runtime: ""
    };
  }
  if (typeof raw !== "object") return { explicit: false, enabledToolPatterns: [], runtime: "" };
  const enabledTools = Array.isArray(raw.enabled_tools) ? raw.enabled_tools : [];
  const toolsList = Array.isArray(raw.tools) ? raw.tools : [];
  return {
    explicit: true,
    enabledToolPatterns: [...enabledTools, ...toolsList].map((value) => String(value || "").trim()).filter(Boolean),
    runtime: String(raw.runtime || "").trim()
  };
}

function parseSkillsConfig(raw) {
  if (!raw) return { enabled: [] };
  if (Array.isArray(raw)) {
    return {
      enabled: raw.map((skill) => ({ skill: String(skill || "").trim() })).filter((item) => item.skill)
    };
  }
  if (typeof raw !== "object") return { enabled: [] };
  const enabled = Array.isArray(raw.enabled) ? raw.enabled : [];
  return {
    enabled: enabled
      .map((item) => {
        if (!item || typeof item !== "object") return null;
        return {
          skill: String(item.skill || "").trim(),
          version: Number(item.version || 0) || 0
        };
      })
      .filter((item) => item?.skill)
  };
}

function parseMCPServers(raw) {
  if (!raw) return [];
  let source = raw;
  if (Array.isArray(raw)) {
    source = { servers: raw };
  }
  if (typeof source !== "object") return [];
  let servers = [];
  if (Array.isArray(source.servers)) {
    servers = source.servers;
  } else if (Array.isArray(source.mcpServers)) {
    servers = source.mcpServers;
  } else if (source.mcpServers && typeof source.mcpServers === "object") {
    servers = Object.entries(source.mcpServers).map(([identifier, value]) => ({ identifier, ...objectValue(value) }));
  } else if (source.servers && typeof source.servers === "object") {
    servers = Object.entries(source.servers).map(([identifier, value]) => ({ identifier, ...objectValue(value) }));
  }
  return servers
    .map((server) => {
      const item = objectValue(server);
      const identifier = String(item.identifier || item.id || item.name || "").trim();
      if (!identifier) return null;
      return {
        command: String(item.command || "").trim(),
        description: String(item.description || "").trim(),
        disabled: Boolean(item.disabled),
        identifier,
        includeTools: Array.isArray(item.include_tools) ? item.include_tools.map((value) => String(value || "").trim()).filter(Boolean) : [],
        loggingLevel: String(item.logging?.level || "").trim(),
        transport: String(item.transport || "stdio").trim(),
        url: String(item.url || "").trim(),
        title: String(item.title || "").trim()
      };
    })
    .filter(Boolean);
}

function toolNamespaceEnabled(namespace, policy) {
  if (!policy.explicit) return true;
  return policy.enabledToolPatterns.some((pattern) => pattern === namespace || pattern.startsWith(`${namespace}.`));
}

function runtimeSupportsToolItem(identifier, runtime) {
  const normalizedRuntime = String(runtime || "").trim() || "cloud_sandbox";
  if (identifier === "browser.takeover" || identifier === "browser.close") {
    return normalizedRuntime === "local_system";
  }
  return true;
}

function toolingGuidanceLabel(item) {
  switch (item.kind) {
    case "tool_namespace":
      return `工具命名空间 ${item.name}`;
    case "tool_api":
      return `工具 API ${item.name}`;
    case "skill":
      return `技能 ${item.title || item.name}`;
    case "mcp":
      return `MCP ${item.title || item.name}`;
    default:
      return item.title || item.name;
  }
}

function toolingHealthCapabilities(result) {
  return Array.isArray(result?.capabilities)
    ? result.capabilities.map((value) => String(value || "").trim()).filter(Boolean)
    : [];
}

function toolingHealthCatalogMetric(result, field) {
  const value = Number(result?.[field] || 0);
  return Number.isFinite(value) && value > 0 ? value : 0;
}

function toolingHostMetric(host, field) {
  const value = Number(host?.[field] || 0);
  return Number.isFinite(value) && value >= 0 ? value : 0;
}

function buildToolingCatalog({ config, installedSkills, preferredRuntime }) {
  const toolPolicy = parseToolPolicy(config?.tools);
  const skillsConfig = parseSkillsConfig(config?.skills);
  const mcpServers = parseMCPServers(config?.mcp);
  const enabledSkillMap = new Map(skillsConfig.enabled.map((item) => [item.skill, item]));

  const toolItems = [];
  for (const namespace of builtinToolNamespaces) {
    const enabled = toolNamespaceEnabled(namespace.key, toolPolicy);
    const selectable = enabled && runtimeSupportsToolItem(namespace.key, preferredRuntime);
    toolItems.push({
      category: "tools",
      description: enabled ? namespace.description : "当前智能体配置未启用这个工具命名空间。",
      disabledReason: enabled ? "" : "未在当前智能体工具配置中启用",
      key: `tool-namespace:${namespace.key}`,
      kind: "tool_namespace",
      name: namespace.key,
      selectable,
      status: selectable ? "available" : "disabled",
      title: namespace.title
    });
  }
  for (const pattern of toolPolicy.enabledToolPatterns) {
    if (!pattern.includes(".")) continue;
    const selectable = runtimeSupportsToolItem(pattern, preferredRuntime);
    toolItems.push({
      category: "tools",
      description: "当前智能体显式配置的工具 API。",
      disabledReason: selectable ? "" : "当前运行环境下不可用",
      key: `tool-api:${pattern}`,
      kind: "tool_api",
      name: pattern,
      selectable,
      status: selectable ? "available" : "disabled",
      title: pattern
    });
  }

  const knownSkillKeys = new Set();
  const skillItems = (installedSkills || []).map((skill) => {
    knownSkillKeys.add(skill.identifier);
    const binding = enabledSkillMap.get(skill.identifier);
    const archived = skill.status === "archived";
    const selectable = Boolean(binding) && !archived;
    return {
      category: "skills",
      description: binding ? (skill.description || "已为当前智能体启用。") : "该技能已安装，但当前智能体尚未启用。",
      disabledReason: archived ? "技能已归档" : (!binding ? "当前智能体未启用该技能" : ""),
      key: `skill:${skill.identifier}`,
      kind: "skill",
      name: skill.identifier,
      selectable,
      status: selectable ? "available" : "disabled",
      title: skill.title || skill.identifier
    };
  });
  for (const binding of skillsConfig.enabled) {
    if (knownSkillKeys.has(binding.skill)) continue;
    skillItems.push({
      category: "skills",
      description: "当前智能体配置中引用了这个技能，但工作区里没有找到对应安装项。",
      disabledReason: "工作区中未找到已安装版本",
      key: `skill:${binding.skill}`,
      kind: "skill",
      name: binding.skill,
      selectable: false,
      status: "disabled",
      title: binding.skill
    });
  }

  const mcpItems = mcpServers.map((server) => {
    const selectable = !server.disabled && (server.transport === "streamable_http" ? Boolean(server.url) : Boolean(server.command));
    const hintedTools = server.includeTools.length ? `仅包含：${server.includeTools.join(", ")}` : "";
    return {
      category: "mcp",
      description: server.description || hintedTools || (server.transport === "streamable_http" ? `URL：${server.url || "未配置 URL"}` : `命令：${server.command || "未配置命令"}`),
      disabledReason: selectable ? "" : "MCP 服务配置不完整",
      key: `mcp:${server.identifier}`,
      kind: "mcp",
      name: server.identifier,
      selectable,
      status: selectable ? "available" : "disabled",
      title: server.title || server.identifier
    };
  });

  return {
    items: [...toolItems, ...skillItems, ...mcpItems],
    sections: [
      { key: "tools", title: "工具", items: toolItems },
      { key: "skills", title: "Skills", items: skillItems },
      { key: "mcp", title: "MCP", items: mcpItems }
    ]
  };
}

function buildGuidedTaskMessage(task, selectedItems) {
  const text = String(task || "").trim();
  if (!selectedItems.length) return text;
  const lines = selectedItems.map((item) => `- ${toolingGuidanceLabel(item)}`);
  return [
    "请优先考虑使用以下已选择的能力来完成这次任务；如果其中某项并不适合当前问题，就忽略它，按最佳方案继续：",
    ...lines,
    "",
    "用户原始请求：",
    text
  ].join("\n");
}

function processPreview(identifier, apiName, args = {}, result = {}, source = "") {
  const parts = normalizeToolParts(identifier, apiName);
  const command = shortText([firstValue(args, ["command", "cmd"]), valueText(firstValue(args, ["args", "arguments"]))].filter(Boolean).join(" "), 180);
  const path = shortText(valueText(firstValue(args, ["path", "file_path", "target_path", "filename"])), 180);
  const query = shortText(valueText(firstValue(args, ["query", "q"])), 180);
  const url = shortText(valueText(firstValue(args, ["url", "target_url"])), 180);
  const content = shortText(String(result.content || ""), 220);
  const error = objectValue(result.error);
  if (result.success === false) {
    return error.message || content || toolDetail(parts.identifier, parts.apiName, args) || "工具执行失败。";
  }

  if (parts.identifier === "skills") {
    const state = objectValue(result.state);
    if (parts.apiName === "preview") {
      const policy = objectValue(state.policy);
      const security = objectValue(state.security);
      const findingCount = Array.isArray(security.findings) ? security.findings.length : 0;
      return [
        `${state.title || state.identifier || args.identifier || "Skill"} · ${state.install_state || "previewed"}`,
        `Policy ${policy.allowed === true ? "允许" : policy.allowed === false ? "阻止" : "待确认"}`,
        `${Number(security.scanned_files || 0)} 个文件已扫描`,
        `${findingCount} 个发现`
      ].join(" · ");
    }
    if (parts.apiName === "install") {
      const skill = objectValue(state.skill);
      const version = objectValue(state.version);
      return `${skill.title || skill.identifier || args.identifier || "Skill"} v${version.version || 1} 已${state.upgraded ? "升级" : "安装"}。`;
    }
    if (parts.apiName === "enable") {
      const binding = objectValue(state.binding);
      return `${binding.skill || args.identifier || "Skill"} v${binding.version || args.version || 1} 已写入 Agent 配置${state.requires_session_upgrade ? "；当前 Session 仍固定旧配置，需要升级或新建 Session" : ""}。`;
    }
    if (parts.apiName === "disable") {
      const binding = objectValue(state.binding);
      return state.removed === false
        ? `${binding.skill || args.identifier || "Skill"} 已处于停用状态。`
        : `${binding.skill || args.identifier || "Skill"} 已从 Agent 配置移除${state.requires_session_upgrade ? "；当前 Session 仍固定旧配置，需要应用新版本" : ""}。`;
    }
  }

  if (parts.apiName === "run_command") {
    return command ? `命令：${command}` : "命令执行完成。";
  }
  if (parts.apiName === "execute_code") {
    const language = firstValue(args, ["language"]);
    return language ? `代码：${language}` : "代码执行完成。";
  }
  if (parts.apiName === "read_file") {
    return path ? `${String(source).trim().toLowerCase() === "mcp" ? "MCP 读取" : "读取"}：${path}` : "文件读取完成。";
  }
  if (parts.apiName === "write_file") {
    return path ? `写入：${path}` : "文件写入完成。";
  }
  if (parts.apiName === "edit_file") {
    return path ? `编辑：${path}` : "文件编辑完成。";
  }
  if (parts.apiName === "search") {
    return query ? `搜索：${query}` : "搜索完成。";
  }
  if (parts.apiName === "crawl" || parts.apiName === "open_url") {
    return url ? `打开：${url}` : "网页读取完成。";
  }
  return content || toolDetail(parts.identifier, parts.apiName, args) || "工具执行完成。";
}

function approvalSummary(intervention) {
  return toolSummary({
    identifier: intervention.tool_identifier,
    apiName: intervention.api_name,
    args: objectValue(intervention.arguments),
    reason: intervention.reason
  });
}

function ApprovalCard({ intervention, onApprove, onReject, busy, active }) {
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
          <button type="button" disabled={busy} onClick={() => onApprove(intervention)}>{active ? "审批中..." : "批准"}</button>
          <button className="secondary" type="button" disabled={busy} onClick={() => onReject(intervention)}>拒绝</button>
        </div>
      </div>
      {summary.detail ? <div className="approval-summary">{summary.detail}</div> : null}
      {intervention.reason ? <div className="approval-reason">{intervention.reason}</div> : null}
      <div className="subtle">调用 {intervention.call_id} · 任务轮次 {intervention.turn_id}</div>
      <details className="approval-details">
        <summary>参数</summary>
        <pre>{pretty(intervention.arguments || {})}</pre>
      </details>
    </div>
  );
}

function ToolPickerModal({ loading, error, sections, selectedKeys, onToggle, onClose, onClear }) {
  return (
    <div className="tool-picker-backdrop" role="presentation" onClick={onClose}>
      <section className="tool-picker-modal" role="dialog" aria-modal="true" aria-label="工具与技能" onClick={(event) => event.stopPropagation()}>
        <div className="tool-picker-header">
          <div>
            <h2>工具与技能</h2>
            <div className="subtle">选择后会在发送时作为提示，优先引导智能体使用这些能力。</div>
          </div>
          <div className="tool-picker-actions">
            <button className="secondary" type="button" onClick={onClear} disabled={!selectedKeys.length}>清空选择</button>
            <button className="secondary" type="button" onClick={onClose}>关闭</button>
          </div>
        </div>
        {loading ? <div className="empty-state compact">正在加载可用能力...</div> : null}
        {!loading && error ? <div className="artifact-preview-error">{error}</div> : null}
        {!loading && !error ? (
          <div className="tool-picker-sections">
            {sections.map((section) => (
              <div className="tool-picker-section" key={section.key}>
                <div className="tool-picker-section-title">{section.title}</div>
                {section.items.length ? (
                  <div className="tool-picker-list">
                    {section.items.map((item) => {
                      const checked = selectedKeys.includes(item.key);
                      return (
                        <label className={`tool-picker-item ${item.selectable ? "" : "disabled"} ${checked ? "selected" : ""}`.trim()} key={item.key}>
                          <input type="checkbox" checked={checked} disabled={!item.selectable} onChange={() => onToggle(item.key)} />
                          <div className="tool-picker-item-body">
                            <div className="tool-picker-item-head">
                              <strong>{item.title}</strong>
                              <span className={`tool-picker-status ${item.status}`}>{item.selectable ? "可用" : "不可用"}</span>
                            </div>
                            <div className="subtle">{item.description}</div>
                            {!item.selectable && item.disabledReason ? <div className="tool-picker-reason">{item.disabledReason}</div> : null}
                          </div>
                        </label>
                      );
                    })}
                  </div>
                ) : (
                  <div className="empty-state compact">当前没有相关配置。</div>
                )}
              </div>
            ))}
          </div>
        ) : null}
      </section>
    </div>
  );
}

function TaskTemplateModal({ onClose, onSelect, templates }) {
  return (
    <div className="tool-picker-backdrop" role="presentation" onClick={onClose}>
      <section className="task-template-modal" role="dialog" aria-modal="true" aria-label="任务模板" onClick={(event) => event.stopPropagation()}>
        <header className="tool-picker-header">
          <div>
            <h2>任务模板</h2>
            <div className="subtle">选择常用任务配置，或按顺序工作流逐步执行。</div>
          </div>
          <button className="secondary" type="button" onClick={onClose}>关闭</button>
        </header>
        <div className="task-template-grid">
          {templates.map((template) => (
            <article className="task-template-option" key={template.id}>
              <div className="task-template-option-head">
                <span>{template.category}</span>
                <strong>{template.title}</strong>
              </div>
              <p>{template.description}</p>
              <div className="task-template-bindings">
                {(template.tools || []).map((tool) => <span key={`tool:${tool}`}>{tool}</span>)}
                {(template.skills || []).map((skill) => <span className="skill" key={`skill:${skill}`}>{skill}</span>)}
              </div>
              <ol className="task-template-steps">
                {(template.workflow_steps || []).map((step) => <li key={step.id}>{step.title}</li>)}
              </ol>
              <div className="task-template-actions">
                <button className="secondary" type="button" onClick={() => onSelect(template, false)}>填充任务</button>
                <button type="button" onClick={() => onSelect(template, true)}>使用工作流</button>
              </div>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}

function ComparisonRunColumn({ label, run }) {
  if (!run) return <div className="comparison-run empty">等待选择运行。</div>;
  const usage = run.usage?.summary || {};
  return (
    <section className="comparison-run">
      <header>
        <span>{label}</span>
        <strong>{run.session?.title || run.session?.id}</strong>
        <small>{run.session?.id}</small>
      </header>
      <dl className="comparison-metrics">
        <div><dt>模型</dt><dd>{run.llm_provider} / {run.llm_model}</dd></div>
        <div><dt>配置版本</dt><dd>#{run.session?.agent_config_version || 1}</dd></div>
        <div><dt>耗时</dt><dd>{Number(run.duration_ms || 0).toLocaleString()} ms</dd></div>
        <div><dt>Token</dt><dd>{Number(usage.total_tokens || 0).toLocaleString()}</dd></div>
        <div><dt>输入 / 输出</dt><dd>{Number(usage.input_tokens || 0).toLocaleString()} / {Number(usage.output_tokens || 0).toLocaleString()}</dd></div>
        <div><dt>结果文件</dt><dd>{run.artifacts?.length || 0}</dd></div>
      </dl>
      <div className="comparison-copy">
        <span>最终结果</span>
        <pre>{run.result || "当前运行还没有最终回复。"}</pre>
      </div>
      {run.artifacts?.length ? (
        <div className="comparison-files">
          {run.artifacts.map((artifact) => <span key={artifact.id}>{artifact.name}</span>)}
        </div>
      ) : null}
    </section>
  );
}

function SessionComparisonModal({
  sessions,
  leftID,
  rightID,
  onLeftChange,
  onRightChange,
  onCompare,
  onClose,
  loading,
  result,
  modelOptions,
  variantModel,
  onVariantModelChange,
  onCreateVariant,
  creatingVariant
}) {
  const selectableSessions = sessions.filter((session) => session.status !== "terminated");
  return (
    <div className="tool-picker-backdrop" role="presentation" onClick={onClose}>
      <section className="comparison-modal" role="dialog" aria-modal="true" aria-label="任务对比" onClick={(event) => event.stopPropagation()}>
        <header className="tool-picker-header">
          <div>
            <h2>任务对比</h2>
            <div className="subtle">并排查看模型、配置、耗时、Token、文件和最终结果。</div>
          </div>
          <button className="secondary" type="button" onClick={onClose}>关闭</button>
        </header>
        <div className="comparison-controls">
          <label>
            <span>左侧运行</span>
            <select value={leftID} onChange={(event) => onLeftChange(event.target.value)}>
              {selectableSessions.map((session) => <option value={session.id} key={session.id}>{session.title || session.id}</option>)}
            </select>
          </label>
          <label>
            <span>右侧运行</span>
            <select value={rightID} onChange={(event) => onRightChange(event.target.value)}>
              {selectableSessions.map((session) => <option value={session.id} key={session.id}>{session.title || session.id}</option>)}
            </select>
          </label>
          <button type="button" disabled={!leftID || !rightID || leftID === rightID || loading} onClick={onCompare}>
            {loading ? "加载中..." : "刷新对比"}
          </button>
        </div>
        {result ? (
          <div className="comparison-grid">
            <ComparisonRunColumn label="基准" run={result.left} />
            <ComparisonRunColumn label="变体" run={result.right} />
          </div>
        ) : <Empty>选择两个不同任务后加载对比。</Empty>}
        <div className="comparison-variant-bar">
          <div>
            <strong>创建模型变体</strong>
            <span>复制左侧运行的 prompt、Agent 版本和运行设置，仅替换模型。</span>
          </div>
          <select aria-label="变体模型" value={variantModel} onChange={(event) => onVariantModelChange(event.target.value)}>
            {modelOptions.map((option) => (
              <option value={`${option.llmProvider}::${option.llmModel}`} key={`${option.llmProvider}:${option.llmModel}`}>
                {option.label}
              </option>
            ))}
          </select>
          <button type="button" disabled={!leftID || !variantModel || creatingVariant} onClick={onCreateVariant}>
            {creatingVariant ? "创建中..." : "运行变体"}
          </button>
        </div>
      </section>
    </div>
  );
}

function TaskMetadataModal({ session, tags, onTagsChange, onSave, onClose, saving, onTogglePin }) {
  if (!session) return null;
  return (
    <div className="tool-picker-backdrop" role="presentation" onClick={onClose}>
      <section className="task-metadata-modal" role="dialog" aria-modal="true" aria-label="任务信息" onClick={(event) => event.stopPropagation()}>
        <header className="tool-picker-header">
          <div>
            <h2>任务信息</h2>
            <div className="subtle">{session.title || session.id}</div>
          </div>
          <button className="secondary" type="button" onClick={onClose}>关闭</button>
        </header>
        <label className="task-metadata-field">
          <span>标签</span>
          <input value={tags} onChange={(event) => onTagsChange(event.target.value)} placeholder="新闻, 代码, 调研" />
          <small>最多 8 个，以逗号分隔。</small>
        </label>
        <div className="task-summary-detail">
          <span>会话摘要</span>
          <p>{session.summary_text || "当前会话还没有可显示的结论。"}</p>
        </div>
        <footer className="task-metadata-actions">
          <button className="secondary" type="button" disabled={saving} onClick={onTogglePin}>
            {session.pinned_at ? "取消置顶" : "置顶任务"}
          </button>
          <button type="button" disabled={saving} onClick={onSave}>{saving ? "保存中..." : "保存标签"}</button>
        </footer>
      </section>
    </div>
  );
}

function WorkflowProgress({ onStop, run }) {
  if (!run) return null;
  const completed = run.steps.filter((step) => step.status === "completed").length;
  const progress = run.steps.length ? Math.round((completed / run.steps.length) * 100) : 0;
  return (
    <section className={`workflow-progress ${run.status}`} aria-label="工作流进度">
      <header>
        <div>
          <span>多步骤工作流</span>
          <strong>{run.templateTitle}</strong>
        </div>
        <div className="workflow-progress-actions">
          <span>{completed}/{run.steps.length}</span>
          {run.status === "running" ? <button className="secondary" type="button" onClick={onStop}>停止</button> : null}
        </div>
      </header>
      <div className="workflow-progress-track"><span style={{ width: `${progress}%` }} /></div>
      <ol>
        {run.steps.map((step, index) => (
          <li className={step.status} key={step.id}>
            <span>{step.status === "completed" ? "✓" : index + 1}</span>
            <div><strong>{step.title}</strong><small>{step.status === "running" ? "执行中" : step.status === "completed" ? "已完成" : step.status === "failed" ? "失败" : step.status === "canceled" ? "已停止" : "等待"}</small></div>
          </li>
        ))}
      </ol>
    </section>
  );
}

function workflowStorageKey(sessionID) {
  return `${workflowStoragePrefix}${sessionID}`;
}

function persistWorkflowRun(run) {
  try {
    if (!run?.sessionID) return;
    window.localStorage.setItem(workflowStorageKey(run.sessionID), JSON.stringify(run));
  } catch {}
}

function readWorkflowRun(sessionID) {
  try {
    const raw = window.localStorage.getItem(workflowStorageKey(sessionID));
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

function workflowStepMessage(template, baseTask, stepIndex) {
  const steps = template.workflow_steps || [];
  const step = steps[stepIndex];
  return [
    `执行「${template.title}」工作流，第 ${stepIndex + 1}/${steps.length} 步：${step.title}。`,
    `整体目标：${baseTask}`,
    `本步骤任务：${step.instruction}`,
    "只完成本步骤，并清楚说明本步骤的产出、证据和留给下一步的上下文。不要提前跳到后续步骤。"
  ].join("\n\n");
}

function editableMCPServers(raw) {
  if (!raw || typeof raw !== "object") return [];
  const source = raw.servers ?? raw.mcpServers ?? [];
  if (Array.isArray(source)) return source.map((item) => ({ ...objectValue(item) }));
  if (source && typeof source === "object") {
    return Object.entries(source).map(([identifier, value]) => ({ identifier, ...objectValue(value) }));
  }
  return [];
}

function editableMCPBindings(raw) {
  if (!raw || typeof raw !== "object" || !Array.isArray(raw.bindings)) return [];
  return raw.bindings.map((binding) => ({
    server_id: String(binding?.server_id || ""),
    version: Number(binding?.version || 0),
    ...(binding?.identifier ? { identifier: String(binding.identifier) } : {})
  })).filter((binding) => binding.server_id);
}

function agentEditorDraft(agent) {
  const config = agent?.config_version || {};
  const toolPolicy = parseToolPolicy(config.tools);
  const enabledNamespaces = toolPolicy.explicit
    ? builtinToolNamespaces.filter((item) => toolNamespaceEnabled(item.key, toolPolicy)).map((item) => item.key)
    : builtinToolNamespaces.map((item) => item.key);
  const namespaceKeys = new Set(builtinToolNamespaces.map((item) => item.key));
  return {
    llmModel: config.llm_model || "",
    llmProvider: config.llm_provider || "",
		mcpBindings: editableMCPBindings(config.mcp),
    mcpServers: editableMCPServers(config.mcp),
    name: agent?.name || "",
    selectedSkills: parseSkillsConfig(config.skills).enabled.map((item) => item.skill),
    selectedTools: enabledNamespaces,
    system: config.system || "",
    toolPatterns: toolPolicy.enabledToolPatterns.filter((pattern) => !namespaceKeys.has(pattern)),
    toolRuntime: toolPolicy.runtime || ""
  };
}

function agentConfigVersionMetrics(version) {
  const toolPolicy = parseToolPolicy(version?.tools);
  return {
    mcp: editableMCPServers(version?.mcp).filter((server) => !server.disabled).length + editableMCPBindings(version?.mcp).length,
    skills: parseSkillsConfig(version?.skills).enabled.length,
    tools: toolPolicy.explicit ? toolPolicy.enabledToolPatterns.length : builtinToolNamespaces.length
  };
}

function AgentConfigEditor({ agent, mcpRegistryServers = [], modelOptions, onRollback, onSave, rollingBackVersion, saving, skills }) {
  const [draft, setDraft] = useState(() => agentEditorDraft(agent));
  const [rollbackCandidate, setRollbackCandidate] = useState(0);
  const [versionError, setVersionError] = useState("");
  const [versionLoading, setVersionLoading] = useState(false);
  const [versions, setVersions] = useState([]);
  useEffect(() => {
    setDraft(agentEditorDraft(agent));
  }, [agent?.id, agent?.current_config_version]);
  useEffect(() => {
    let active = true;
    setRollbackCandidate(0);
    setVersionError("");
    setVersions([]);
    if (!agent?.id) return () => { active = false; };
    setVersionLoading(true);
    api.agentConfigVersions(agent.id).then((response) => {
      if (!active) return;
      setVersions([...(response.config_versions || [])].sort((left, right) => Number(right.version || 0) - Number(left.version || 0)));
    }).catch((error) => {
      if (active) setVersionError(error.message);
    }).finally(() => {
      if (active) setVersionLoading(false);
    });
    return () => { active = false; };
  }, [agent?.id, agent?.current_config_version]);

  if (!agent) return <Empty>请先选择一个智能体。</Empty>;
  const installedSkillIDs = new Set(skills.map((skill) => skill.identifier));
  const skillOptions = [
    ...skills.map((skill) => ({
      description: skill.description || "工作区技能",
      disabled: skill.status === "archived",
      identifier: skill.identifier,
      title: skill.title || skill.identifier
    })),
    ...draft.selectedSkills.filter((identifier) => !installedSkillIDs.has(identifier)).map((identifier) => ({
      description: "当前配置引用了该技能，但工作区中未找到安装项。",
      disabled: true,
      identifier,
      title: identifier
    }))
  ];

  function toggleListValue(key, value) {
    setDraft((current) => ({
      ...current,
      [key]: current[key].includes(value) ? current[key].filter((item) => item !== value) : [...current[key], value]
    }));
  }

  function updateMCP(index, patch) {
    setDraft((current) => ({
      ...current,
      mcpServers: current.mcpServers.map((server, serverIndex) => serverIndex === index ? { ...server, ...patch } : server)
    }));
  }

  function toggleMCPBinding(server) {
    setDraft((current) => {
      const exists = current.mcpBindings.some((binding) => binding.server_id === server.id);
      return {
        ...current,
        mcpBindings: exists
          ? current.mcpBindings.filter((binding) => binding.server_id !== server.id)
          : [...current.mcpBindings, { server_id: server.id, version: Number(server.current_version || 1) }]
      };
    });
  }

  function upgradeMCPBinding(server) {
    setDraft((current) => ({
      ...current,
      mcpBindings: current.mcpBindings.map((binding) => binding.server_id === server.id
        ? { ...binding, version: Number(server.current_version || binding.version || 1) }
        : binding)
    }));
  }

  const canSave = Boolean(draft.name.trim() && draft.llmProvider && draft.llmModel && !saving);
  return (
    <div className="agent-editor">
      <div className="agent-editor-grid">
        <label className="agent-editor-field">
          <span>Agent 名称</span>
          <input value={draft.name} onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} />
        </label>
        <label className="agent-editor-field">
          <span>默认模型</span>
          <select
            value={`${draft.llmProvider}::${draft.llmModel}`}
            onChange={(event) => {
              const option = modelOptions.find((item) => `${item.llmProvider}::${item.llmModel}` === event.target.value);
              if (option) setDraft((current) => ({ ...current, llmProvider: option.llmProvider, llmModel: option.llmModel }));
            }}
          >
            {!modelOptions.some((item) => item.llmProvider === draft.llmProvider && item.llmModel === draft.llmModel) ? (
              <option value={`${draft.llmProvider}::${draft.llmModel}`}>{draft.llmProvider} / {draft.llmModel}</option>
            ) : null}
            {modelOptions.map((option) => (
              <option key={`${option.llmProvider}::${option.llmModel}`} value={`${option.llmProvider}::${option.llmModel}`}>{option.label}</option>
            ))}
          </select>
        </label>
      </div>
      <label className="agent-editor-field">
        <span>System prompt</span>
        <textarea rows="7" value={draft.system} onChange={(event) => setDraft((current) => ({ ...current, system: event.target.value }))} placeholder="定义智能体的角色、边界和工作方式" />
      </label>
      <section className="agent-editor-section">
        <div className="agent-editor-section-head">
          <div><strong>Tools</strong><div className="subtle">控制该 Agent 默认可调用的工具命名空间。</div></div>
          <span className="agent-editor-count">{draft.selectedTools.length}/{builtinToolNamespaces.length}</span>
        </div>
        <div className="agent-option-grid">
          {builtinToolNamespaces.map((item) => (
            <label className="agent-option" key={item.key}>
              <input type="checkbox" checked={draft.selectedTools.includes(item.key)} onChange={() => toggleListValue("selectedTools", item.key)} />
              <span><strong>{item.title}</strong><small>{item.description}</small></span>
            </label>
          ))}
        </div>
      </section>
      <section className="agent-editor-section">
        <div className="agent-editor-section-head">
          <div><strong>Skills</strong><div className="subtle">选择运行时注入给该 Agent 的工作区技能。</div></div>
          <span className="agent-editor-count">{draft.selectedSkills.length}</span>
        </div>
        {skillOptions.length ? (
          <div className="agent-option-grid">
            {skillOptions.map((skill) => (
              <label className={`agent-option ${skill.disabled ? "disabled" : ""}`} key={skill.identifier}>
                <input type="checkbox" disabled={skill.disabled} checked={draft.selectedSkills.includes(skill.identifier)} onChange={() => toggleListValue("selectedSkills", skill.identifier)} />
                <span><strong>{skill.title}</strong><small>{skill.description}</small></span>
              </label>
            ))}
          </div>
        ) : <Empty>工作区还没有可启用的技能。</Empty>}
      </section>
      <section className="agent-editor-section">
        <div className="agent-editor-section-head">
          <div><strong>MCP 服务</strong><div className="subtle">绑定工作区服务，或保留 Agent 专用内嵌配置。</div></div>
          <button className="secondary" type="button" onClick={() => setDraft((current) => ({ ...current, mcpServers: [...current.mcpServers, { identifier: "", transport: "stdio", stdio_framing: "json_lines", command: "", disabled: false }] }))}>添加服务</button>
        </div>
        {mcpRegistryServers.length ? (
          <div className="mcp-binding-list">
            {mcpRegistryServers.map((server) => {
              const binding = draft.mcpBindings.find((item) => item.server_id === server.id);
              const behind = binding && Number(binding.version) < Number(server.current_version);
              return (
                <div className={`mcp-binding-row ${server.status !== "active" ? "disabled" : ""}`} key={server.id}>
                  <label>
                    <input type="checkbox" disabled={server.status !== "active"} checked={Boolean(binding)} onChange={() => toggleMCPBinding(server)} />
                    <span><strong>{server.name}</strong><small>{server.identifier} · v{binding?.version || server.current_version} · {server.config?.transport || "stdio"}</small></span>
                  </label>
                  {behind ? <button className="secondary" type="button" onClick={() => upgradeMCPBinding(server)}>升级到 v{server.current_version}</button> : null}
                </div>
              );
            })}
          </div>
        ) : null}
        {draft.mcpServers.length ? (
          <div className="mcp-editor-list">
            {draft.mcpServers.map((server, index) => (
              <div className="mcp-editor-row" key={`${server.identifier || "new"}-${index}`}>
                <label className="agent-editor-field">
                  <span>标识</span>
                  <input value={server.identifier || server.id || server.name || ""} onChange={(event) => updateMCP(index, { identifier: event.target.value })} placeholder="例如 filesystem" />
                </label>
                <label className="agent-editor-field mcp-transport-field">
                  <span>传输</span>
                  <select value={server.transport || "stdio"} onChange={(event) => updateMCP(index, { transport: event.target.value })}>
                    <option value="stdio">stdio</option>
                    <option value="streamable_http">Streamable HTTP</option>
                  </select>
                </label>
                <label className="agent-editor-field mcp-command-field">
                  <span>{(server.transport || "stdio") === "streamable_http" ? "服务 URL" : "启动命令"}</span>
                  {(server.transport || "stdio") === "streamable_http" ? (
                    <input value={server.url || ""} onChange={(event) => updateMCP(index, { url: event.target.value })} placeholder="https://mcp.example.com/mcp" />
                  ) : (
                    <input value={server.command || ""} onChange={(event) => updateMCP(index, { command: event.target.value })} placeholder="例如 npx -y @modelcontextprotocol/server-filesystem" />
                  )}
                </label>
                <label className="agent-editor-field mcp-logging-field">
                  <span>日志级别</span>
                  <select value={server.logging?.level || ""} onChange={(event) => updateMCP(index, { logging: event.target.value ? { level: event.target.value } : undefined })}>
                    <option value="">不设置</option>
                    {["debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"].map((level) => <option key={level} value={level}>{level}</option>)}
                  </select>
                </label>
                <label className="mcp-enabled-toggle">
                  <input type="checkbox" checked={!server.disabled} onChange={(event) => updateMCP(index, { disabled: !event.target.checked })} />
                  <span>启用</span>
                </label>
                <label className="mcp-enabled-toggle">
                  <input type="checkbox" checked={Boolean(server.expose?.resources)} onChange={(event) => updateMCP(index, { expose: { ...objectValue(server.expose), resources: event.target.checked } })} />
                  <span>资源工具</span>
                </label>
                <label className="mcp-enabled-toggle">
                  <input type="checkbox" checked={Boolean(server.expose?.prompts)} onChange={(event) => updateMCP(index, { expose: { ...objectValue(server.expose), prompts: event.target.checked } })} />
                  <span>Prompt 工具</span>
                </label>
                <label className="mcp-enabled-toggle">
                  <input type="checkbox" disabled={(server.transport || "stdio") !== "streamable_http"} checked={Boolean(server.listen)} onChange={(event) => updateMCP(index, { listen: event.target.checked })} />
                  <span>SSE 监听</span>
                </label>
                <button className="icon-button danger" type="button" title="移除 MCP 服务" aria-label={`移除 MCP 服务 ${server.identifier || index + 1}`} onClick={() => setDraft((current) => ({ ...current, mcpServers: current.mcpServers.filter((_, serverIndex) => serverIndex !== index) }))}><DeleteIcon /></button>
              </div>
            ))}
          </div>
        ) : <Empty>尚未配置 MCP 服务。</Empty>}
      </section>
      <div className="agent-editor-footer">
        <div className="subtle">保存会创建配置版本 #{Number(agent.current_config_version || 0) + 1}，已有会话继续使用原版本。</div>
        <button type="button" disabled={!canSave} onClick={() => onSave({
          name: draft.name.trim(),
          llm_provider: draft.llmProvider,
          llm_model: draft.llmModel,
          system: draft.system,
          tools: { enabled_tools: [...draft.selectedTools, ...draft.toolPatterns], ...(draft.toolRuntime ? { runtime: draft.toolRuntime } : {}) },
          skills: { enabled: draft.selectedSkills.map((skill) => ({ skill })) },
          mcp: { bindings: draft.mcpBindings, servers: draft.mcpServers.filter((server) => {
            const identifier = String(server.identifier || server.id || server.name || "").trim();
            return identifier && ((server.transport || "stdio") === "streamable_http" ? String(server.url || "").trim() : String(server.command || "").trim());
          }) }
        })}>{saving ? "保存中..." : "保存配置"}</button>
      </div>
      <section className="agent-editor-section agent-version-history">
        <div className="agent-editor-section-head">
          <div><strong>版本历史</strong><div className="subtle">回滚会复制历史配置并生成新的当前版本，已有会话不会改变。</div></div>
          <span className="agent-editor-count">{versions.length}</span>
        </div>
        {versionError ? <div className="agent-version-error">{versionError}</div> : null}
        {versionLoading ? <Empty>正在加载版本历史...</Empty> : null}
        {!versionLoading && !versions.length && !versionError ? <Empty>还没有可显示的配置版本。</Empty> : null}
        {versions.length ? (
          <div className="agent-version-list">
            {versions.map((version) => {
              const current = Number(version.version) === Number(agent.current_config_version);
              const metrics = agentConfigVersionMetrics(version);
              const confirming = rollbackCandidate === Number(version.version);
              const rollingBack = rollingBackVersion === Number(version.version);
              return (
                <article className={`agent-version-row ${current ? "current" : ""}`} key={version.version}>
                  <div className="agent-version-main">
                    <div className="agent-version-heading">
                      <strong>版本 #{version.version}</strong>
                      {current ? <span className="agent-version-current">当前</span> : null}
                    </div>
                    <div className="agent-version-model">{version.llm_provider || "-"} / {version.llm_model || "-"}</div>
                    <div className="agent-version-meta">
                      <span>{formatTime(version.created_at)}</span>
                      <span>Tools {metrics.tools}</span>
                      <span>Skills {metrics.skills}</span>
                      <span>MCP {metrics.mcp}</span>
                    </div>
                  </div>
                  {!current && !confirming ? (
                    <button className="secondary agent-version-rollback" type="button" disabled={Boolean(rollingBackVersion) || saving} onClick={() => setRollbackCandidate(Number(version.version))}>回滚到此版本</button>
                  ) : null}
                  {confirming ? (
                    <div className="agent-version-confirm">
                      <span>将版本 #{version.version} 复制为版本 #{Number(agent.current_config_version || 0) + 1}</span>
                      <div>
                        <button className="secondary" type="button" disabled={rollingBack} onClick={() => setRollbackCandidate(0)}>取消</button>
                        <button type="button" disabled={rollingBack} onClick={async () => {
                          setVersionError("");
                          try {
                            await onRollback(Number(version.version));
                            setRollbackCandidate(0);
                          } catch (error) {
                            setVersionError(error.message);
                          }
                        }}>{rollingBack ? "回滚中..." : "确认回滚"}</button>
                      </div>
                    </div>
                  ) : null}
                </article>
              );
            })}
          </div>
        ) : null}
      </section>
    </div>
  );
}

function EnvironmentVariablesSettings({ workspaceID }) {
  const [variables, setVariables] = useState([]);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  async function load() {
    setError("");
    try {
      const response = await api.environmentVariables(workspaceID);
      setVariables(response.variables || []);
    } catch (loadError) {
      setError(loadError.message);
    }
  }

  useEffect(() => {
    load();
  }, [workspaceID]);

  async function save(event) {
    event.preventDefault();
    const normalizedName = name.trim();
    if (!normalizedName) return;
    setBusy(`save:${normalizedName}`);
    setError("");
    setMessage("");
    try {
      await api.putEnvironmentVariable(normalizedName, value, workspaceID);
      setName("");
      setValue("");
      setMessage(`${normalizedName} 已安全保存`);
      await load();
    } catch (saveError) {
      setError(saveError.message);
    } finally {
      setBusy("");
    }
  }

  async function remove(variableName) {
    if (!window.confirm(`删除环境变量 ${variableName}？`)) return;
    setBusy(`delete:${variableName}`);
    setError("");
    setMessage("");
    try {
      await api.deleteEnvironmentVariable(variableName, workspaceID);
      setMessage(`${variableName} 已删除`);
      await load();
    } catch (deleteError) {
      setError(deleteError.message);
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="settings-content-stack environment-settings">
      <div className="settings-hero-card environment-security-status">
        <div>
          <strong>运行时环境变量</strong>
          <div className="environment-security-badges">
            <span>工作区隔离</span><span>AES-256-GCM</span><span>仅调用时注入</span><span>值不可回读</span>
          </div>
        </div>
        <strong className="environment-count">{variables.length}</strong>
      </div>
      <form className="settings-card environment-variable-form" onSubmit={save}>
        <div className="settings-card-title">添加或轮换变量</div>
        <div className="environment-form-grid">
          <label>
            <span>Key</span>
            <input autoComplete="off" spellCheck="false" value={name} onChange={(event) => setName(event.target.value)} placeholder="SERVICE_API_KEY" pattern="[A-Za-z_][A-Za-z0-9_]*" maxLength={128} required />
          </label>
          <label>
            <span>Value</span>
            <input autoComplete="new-password" type="password" value={value} onChange={(event) => setValue(event.target.value)} placeholder="••••••••••••" required />
          </label>
          <button type="submit" disabled={Boolean(busy) || !name.trim()}>{busy.startsWith("save:") ? "保存中..." : "保存"}</button>
        </div>
        {error ? <div className="health-error">{error}</div> : null}
        {message ? <div className="environment-message">{message}</div> : null}
      </form>
      <div className="settings-card environment-variable-list">
        <div className="settings-card-title">已配置变量</div>
        {variables.length ? variables.map((variable) => (
          <div className="settings-row" key={variable.name}>
            <div className="environment-variable-name">
              <code>{variable.name}</code>
              <div className="subtle">更新于 {formatTime(variable.updated_at)}</div>
            </div>
            <div className="settings-row-actions">
              <span className="environment-configured">已配置</span>
              <button className="secondary danger" type="button" disabled={Boolean(busy)} onClick={() => remove(variable.name)}>{busy === `delete:${variable.name}` ? "删除中..." : "删除"}</button>
            </div>
          </div>
        )) : <Empty>当前工作区没有环境变量。</Empty>}
      </div>
    </div>
  );
}

function llmDiagnosticSummary(result) {
  if (!result) return "";
  const metrics = [`${Number(result.latency_ms || 0)} ms`];
  if (Number(result.dimensions) > 0) metrics.push(`${result.dimensions} 维`);
  if (Number(result.candidate_count) > 0) metrics.push(`${result.candidate_count} 个结果`);
  if (!result.capability_type) metrics.push(result.authenticated ? "认证已通过" : "未使用凭证");
  if (result.status === "succeeded") return `测试成功 · ${metrics.join(" · ")}`;
  return `${result.message || "测试失败"} · ${result.error_type || "unknown"} · ${metrics[0]}`;
}

function ModelCatalogRow({ model, busy, onDelete, onSave, onTest, testBusy, testResult }) {
  const [contextWindowTokens, setContextWindowTokens] = useState(String(model.context_window_tokens || 128000));
  const [capabilityType, setCapabilityType] = useState(model.capability_type || "text");
  const [capabilities, setCapabilities] = useState(defaultModelCapabilities(model.capability_type || "text", model.capabilities));

  useEffect(() => {
    setContextWindowTokens(String(model.context_window_tokens || 128000));
    setCapabilityType(model.capability_type || "text");
    setCapabilities(defaultModelCapabilities(model.capability_type || "text", model.capabilities));
  }, [model.context_window_tokens, model.capability_type, model.capabilities]);

  const changed = Number(contextWindowTokens) !== Number(model.context_window_tokens)
    || capabilityType !== (model.capability_type || "text")
    || JSON.stringify(capabilities) !== JSON.stringify(defaultModelCapabilities(model.capability_type || "text", model.capabilities));

  function changeCapabilityType(nextType) {
    setCapabilityType(nextType);
    setCapabilities(defaultModelCapabilities(nextType));
    if ((nextType === "embedding" || nextType === "reranker") && Number(contextWindowTokens) === 128000) {
      setContextWindowTokens("8192");
    }
  }

  return (
    <form
      className="model-catalog-row"
      onSubmit={(event) => {
        event.preventDefault();
        onSave(model.model, Number(contextWindowTokens), capabilityType, capabilities);
      }}
    >
      <div className="model-catalog-name">
        <strong>{model.model}</strong>
        <span>更新于 {formatTime(model.updated_at || model.created_at)}</span>
      </div>
      <label>
        <span>上下文窗口</span>
        <div className="model-token-input">
          <input
            type="number"
            min="1000"
            step="1000"
            value={contextWindowTokens}
            onChange={(event) => setContextWindowTokens(event.target.value)}
            required
          />
          <span>tokens</span>
        </div>
      </label>
      <label>
        <span>模型能力</span>
        <select value={capabilityType} onChange={(event) => changeCapabilityType(event.target.value)}>
          {modelCapabilityOptions.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}
        </select>
      </label>
      {capabilityType === "embedding" ? (
        <div className="model-capability-fields">
          <label><span>向量维度</span><input type="number" min="1" max="65535" value={capabilities.dimensions || ""} onChange={(event) => setCapabilities((current) => ({ ...current, dimensions: Number(event.target.value) }))} required /></label>
          <label><span>距离度量</span><select value={capabilities.distance_metric || "cosine"} onChange={(event) => setCapabilities((current) => ({ ...current, distance_metric: event.target.value }))}><option value="cosine">Cosine</option><option value="l2">L2</option><option value="inner_product">Inner product</option></select></label>
          <label><span>调用协议</span><select value={capabilities.protocol || "openai_embeddings"} onChange={(event) => setCapabilities((current) => ({ ...current, protocol: event.target.value }))}>{embeddingProtocolOptions.map((protocol) => <option value={protocol} key={protocol}>{protocol}</option>)}</select></label>
          <label><span>批量上限</span><input type="number" min="1" max="4096" value={capabilities.max_batch_size || ""} onChange={(event) => setCapabilities((current) => ({ ...current, max_batch_size: Number(event.target.value) }))} required /></label>
          <label className="model-capability-toggle"><input type="checkbox" checked={capabilities.normalized !== false} onChange={(event) => setCapabilities((current) => ({ ...current, normalized: event.target.checked }))} /><span>输出已归一化</span></label>
        </div>
      ) : null}
      {capabilityType === "reranker" ? (
        <div className="model-capability-fields reranker">
          <label><span>调用协议</span><select value={capabilities.protocol || "jina_rerank"} onChange={(event) => setCapabilities((current) => ({ ...current, protocol: event.target.value }))}>{rerankerProtocolOptions.map((protocol) => <option value={protocol} key={protocol}>{protocol}</option>)}</select></label>
          <label><span>最大候选数</span><input type="number" min="1" max="1000" value={capabilities.max_candidates || ""} onChange={(event) => setCapabilities((current) => ({ ...current, max_candidates: Number(event.target.value) }))} required /></label>
        </div>
      ) : null}
      <div className="model-catalog-actions">
        <button className="secondary" type="button" title={changed ? "请先保存模型配置" : "测试模型"} disabled={busy || testBusy || changed} onClick={() => onTest(model)}>
          {testBusy ? "测试中..." : "测试模型"}
        </button>
        <button className="secondary" type="submit" disabled={busy || !changed || Number(contextWindowTokens) <= 0}>
          {busy ? "处理中..." : "保存"}
        </button>
        <button className="icon-button danger" type="button" title={`删除模型 ${model.model}`} aria-label={`删除模型 ${model.model}`} disabled={busy} onClick={() => onDelete(model.model)}><DeleteIcon /></button>
      </div>
      {testResult ? <div className={`llm-diagnostic-result ${testResult.status === "succeeded" ? "ok" : "failed"}`}>{llmDiagnosticSummary(testResult)}</div> : null}
    </form>
  );
}

function ModelManagementSettings({ onCatalogChanged, onOpenEnvironment }) {
  const [providers, setProviders] = useState([]);
  const [models, setModels] = useState([]);
  const [selectedProviderID, setSelectedProviderID] = useState("");
  const [creatingProvider, setCreatingProvider] = useState(false);
  const [providerDraft, setProviderDraft] = useState({ id: "", providerType: "openai-compatible", baseURL: "", apiKeyEnv: "", enabled: true });
  const [modelDraft, setModelDraft] = useState({ model: "", contextWindowTokens: "128000", capabilityType: "text", capabilities: {} });
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [providerTestResults, setProviderTestResults] = useState({});
  const [modelTestResults, setModelTestResults] = useState({});

  async function loadCatalog(preferredProviderID = "") {
    const [providerResponse, modelResponse] = await Promise.all([api.llmProviders(), api.llmModels()]);
    const nextProviders = providerResponse.providers || [];
    const nextModels = modelResponse.models || [];
    setProviders(nextProviders);
    setModels(nextModels);
    setSelectedProviderID((current) => {
      const preferred = preferredProviderID || current;
      return nextProviders.some((provider) => provider.id === preferred) ? preferred : (nextProviders[0]?.id || "");
    });
    return { providers: nextProviders, models: nextModels };
  }

  useEffect(() => {
    let active = true;
    Promise.all([api.llmProviders(), api.llmModels()]).then(([providerResponse, modelResponse]) => {
      if (!active) return;
      const nextProviders = providerResponse.providers || [];
      setProviders(nextProviders);
      setModels(modelResponse.models || []);
      setSelectedProviderID((current) => current || nextProviders[0]?.id || "");
    }).catch((loadError) => {
      if (active) setError(loadError.message);
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => {
      active = false;
    };
  }, []);

  const selectedProvider = providers.find((provider) => provider.id === selectedProviderID) || null;
  const selectedModels = models.filter((model) => model.provider_id === selectedProviderID);
  const enabledProviderCount = providers.filter((provider) => provider.enabled !== false).length;
  const enabledProviderIDs = new Set(providers.filter((provider) => provider.enabled !== false).map((provider) => provider.id));
  const visionModels = models.filter((model) => enabledProviderIDs.has(model.provider_id) && model.capability_type === "text_image");
  const embeddingModels = models.filter((model) => enabledProviderIDs.has(model.provider_id) && model.capability_type === "embedding");
  const rerankerModels = models.filter((model) => enabledProviderIDs.has(model.provider_id) && model.capability_type === "reranker");
  const defaultVisionModel = models.find((model) => model.is_default_vision) || null;
  const defaultEmbeddingModel = models.find((model) => model.is_default_embedding) || null;
  const defaultRerankerModel = models.find((model) => model.is_default_reranker) || null;
  const providerDraftDirty = Boolean(!creatingProvider && selectedProvider) && (
    providerDraft.providerType.trim() !== (selectedProvider.provider_type || "openai-compatible")
    || providerDraft.baseURL.trim() !== (selectedProvider.base_url || "")
    || providerDraft.apiKeyEnv.trim() !== (selectedProvider.api_key_env || "")
    || providerDraft.enabled !== (selectedProvider.enabled !== false)
  );

  useEffect(() => {
    if (creatingProvider || !selectedProvider) return;
    setProviderDraft({
      id: selectedProvider.id,
      providerType: selectedProvider.provider_type || "openai-compatible",
      baseURL: selectedProvider.base_url || "",
      apiKeyEnv: selectedProvider.api_key_env || "",
      enabled: selectedProvider.enabled !== false
    });
  }, [creatingProvider, selectedProvider]);

  async function refreshCatalog(providerID, successMessage) {
    await loadCatalog(providerID);
    await onCatalogChanged?.();
    setMessage(successMessage);
  }

  async function saveProvider(event) {
    event.preventDefault();
    const providerID = providerDraft.id.trim();
    if (!providerID || !providerDraft.providerType.trim()) return;
    setBusy("provider-save");
    setError("");
    setMessage("");
    try {
      const body = {
        provider_type: providerDraft.providerType.trim(),
        base_url: providerDraft.baseURL.trim(),
        api_key_env: providerDraft.apiKeyEnv.trim(),
        enabled: providerDraft.enabled
      };
      if (creatingProvider) {
        await api.createLLMProvider({ id: providerID, ...body });
      } else {
        await api.updateLLMProvider(providerID, selectedProvider.revision, body);
      }
      setCreatingProvider(false);
      await refreshCatalog(providerID, creatingProvider ? `Provider ${providerID} 已添加。` : `Provider ${providerID} 已更新。`);
    } catch (saveError) {
      setError(saveError.message);
    } finally {
      setBusy("");
    }
  }

  async function toggleProvider() {
    if (!selectedProvider) return;
    const nextEnabled = selectedProvider.enabled === false;
    setBusy("provider-toggle");
    setError("");
    setMessage("");
    try {
      await api.setLLMProviderEnabled(selectedProvider.id, selectedProvider.revision, nextEnabled);
      await refreshCatalog(selectedProvider.id, `${selectedProvider.id} 已${nextEnabled ? "启用" : "停用"}。`);
    } catch (toggleError) {
      setError(toggleError.message);
    } finally {
      setBusy("");
    }
  }

  async function testProvider() {
    if (!selectedProvider || providerDraftDirty) return;
    setBusy("provider-test");
    setError("");
    setMessage("");
    try {
      const result = await api.testLLMProvider(selectedProvider.id);
      setProviderTestResults((current) => ({ ...current, [selectedProvider.id]: result }));
    } catch (testError) {
      setError(testError.message);
    } finally {
      setBusy("");
    }
  }

  async function deleteProvider() {
    if (!selectedProvider) return;
    const providerModelCount = selectedModels.length;
    const detail = providerModelCount ? `，并同时删除其下 ${providerModelCount} 个模型` : "";
    if (!window.confirm(`确认删除 Provider ${selectedProvider.id}${detail}？此操作无法撤销。`)) return;
    setBusy("provider-delete");
    setError("");
    setMessage("");
    try {
      const deletedProviderID = selectedProvider.id;
      await api.deleteLLMProvider(deletedProviderID, selectedProvider.revision);
      await refreshCatalog("", `Provider ${deletedProviderID} 已删除。`);
    } catch (deleteError) {
      setError(deleteError.message);
    } finally {
      setBusy("");
    }
  }

  async function saveModel(modelName, contextWindowTokens, capabilityType = "text", capabilities = {}, isNew = false, isDefaultVision = undefined) {
    if (!selectedProvider || !String(modelName || "").trim() || Number(contextWindowTokens) <= 0) return;
    const normalizedModelName = String(modelName).trim();
    setBusy(`model:${normalizedModelName}`);
    setError("");
    setMessage("");
    try {
      const existingModel = models.find((model) => model.provider_id === selectedProvider.id && model.model === normalizedModelName);
      const effectiveDefaultVision = typeof isDefaultVision === "boolean"
        ? isDefaultVision
        : (capabilityType === "text_image" ? Boolean(existingModel?.is_default_vision) : false);
      await api.upsertLLMModel({
        provider_id: selectedProvider.id,
        model: normalizedModelName,
        context_window_tokens: Number(contextWindowTokens),
        capability_type: capabilityType,
        capabilities: defaultModelCapabilities(capabilityType, capabilities),
        is_default_vision: effectiveDefaultVision
      }, isNew ? undefined : existingModel?.revision);
      if (isNew) setModelDraft({ model: "", contextWindowTokens: "128000", capabilityType: "text", capabilities: {} });
      await refreshCatalog(selectedProvider.id, `模型 ${normalizedModelName} 已保存。`);
    } catch (saveError) {
      setError(saveError.message);
    } finally {
      setBusy("");
    }
  }

  async function saveDefaultVisionModel(value) {
    const [providerID, modelName] = String(value || "").split("::");
    const target = models.find((model) => model.provider_id === providerID && model.model === modelName);
    const current = models.find((model) => model.is_default_vision);
    const model = target || current;
    if (!model) return;
    setBusy("vision-default");
    setError("");
    setMessage("");
    try {
      await api.upsertLLMModel({
        provider_id: model.provider_id,
        model: model.model,
        context_window_tokens: model.context_window_tokens,
        capability_type: model.capability_type || "text",
        capabilities: model.capabilities || {},
        is_default_vision: Boolean(target)
      }, model.revision);
      await refreshCatalog(selectedProviderID, target ? `统一图片视觉模型已设为 ${target.provider_id} / ${target.model}。` : "已清除统一图片视觉模型。");
    } catch (saveError) {
      setError(saveError.message);
    } finally {
      setBusy("");
    }
  }

  async function saveDefaultKnowledgeModel(kind, value) {
    const field = kind === "embedding" ? "is_default_embedding" : "is_default_reranker";
    const current = models.find((model) => model[field]);
    const [providerID, modelName] = String(value || "").split("::");
    const target = models.find((model) => model.provider_id === providerID && model.model === modelName);
    const model = target || current;
    if (!model) return;
    setBusy(`${kind}-default`);
    setError("");
    setMessage("");
    try {
      await api.upsertLLMModel({
        provider_id: model.provider_id,
        model: model.model,
        context_window_tokens: model.context_window_tokens,
        capability_type: model.capability_type,
        capabilities: model.capabilities || {},
        [field]: Boolean(target)
      }, model.revision);
      await refreshCatalog(selectedProviderID, target ? `统一默认 ${kind === "embedding" ? "Embedding" : "Reranker"} 模型已设为 ${target.provider_id} / ${target.model}。` : `已清除统一默认 ${kind === "embedding" ? "Embedding" : "Reranker"} 模型。`);
    } catch (saveError) {
      setError(saveError.message);
    } finally {
      setBusy("");
    }
  }

  async function deleteModel(modelName) {
    if (!selectedProvider || !window.confirm(`确认删除模型 ${selectedProvider.id} / ${modelName}？此操作无法撤销。`)) return;
    setBusy(`model:${modelName}`);
    setError("");
    setMessage("");
    try {
      const model = models.find((candidate) => candidate.provider_id === selectedProvider.id && candidate.model === modelName);
      if (!model) throw new Error("模型目录已变化，请刷新后重试。");
      await api.deleteLLMModel(selectedProvider.id, modelName, model.revision);
      await refreshCatalog(selectedProvider.id, `模型 ${modelName} 已删除。`);
    } catch (deleteError) {
      setError(deleteError.message);
    } finally {
      setBusy("");
    }
  }

  async function testModel(model) {
    if (!selectedProvider || !model) return;
    const resultKey = `${model.provider_id}::${model.model}`;
    setBusy(`model-test:${model.model}`);
    setError("");
    setMessage("");
    try {
      const result = await api.testLLMModel(model.provider_id, model.model);
      setModelTestResults((current) => ({ ...current, [resultKey]: result }));
    } catch (testError) {
      setError(testError.message);
    } finally {
      setBusy("");
    }
  }

  function startCreateProvider() {
    setCreatingProvider(true);
    setProviderDraft({ id: "", providerType: "openai-compatible", baseURL: "", apiKeyEnv: "", enabled: true });
    setError("");
    setMessage("");
  }

  if (loading) return <div className="settings-card"><div className="subtle">正在加载模型目录...</div></div>;

  return (
    <div className="settings-content-stack model-management">
      <div className="model-management-summary">
        <div>
          <span>Provider</span>
          <strong>{providers.length}</strong>
          <small>{enabledProviderCount} 个已启用</small>
        </div>
        <div>
          <span>模型</span>
          <strong>{models.length}</strong>
          <small>已登记到模型目录</small>
        </div>
        <div className="model-management-security">
          <span>凭证管理</span>
          <strong>环境变量</strong>
          <button className="secondary" type="button" onClick={onOpenEnvironment}>管理密钥</button>
        </div>
      </div>
      {error ? <div className="health-error">{error}</div> : null}
      {message ? <div className="environment-message">{message}</div> : null}
      <section className="settings-card model-default-settings">
        <div className="settings-card-title">默认模型</div>
        <div className="model-default-grid">
          <label>
            <span>图片视觉</span>
            <select disabled={busy === "vision-default"} value={defaultVisionModel ? `${defaultVisionModel.provider_id}::${defaultVisionModel.model}` : ""} onChange={(event) => saveDefaultVisionModel(event.target.value)}>
              <option value="">未配置</option>
              {visionModels.map((model) => <option value={`${model.provider_id}::${model.model}`} key={`${model.provider_id}:${model.model}`}>{model.provider_id} / {model.model}</option>)}
            </select>
          </label>
          <label>
            <span>Embedding</span>
            <select disabled={busy === "embedding-default"} value={defaultEmbeddingModel ? `${defaultEmbeddingModel.provider_id}::${defaultEmbeddingModel.model}` : ""} onChange={(event) => saveDefaultKnowledgeModel("embedding", event.target.value)}>
              <option value="">未配置</option>
              {embeddingModels.map((model) => <option value={`${model.provider_id}::${model.model}`} key={`${model.provider_id}:${model.model}`}>{model.provider_id} / {model.model}</option>)}
            </select>
          </label>
          <label>
            <span>Reranker</span>
            <select disabled={busy === "reranker-default"} value={defaultRerankerModel ? `${defaultRerankerModel.provider_id}::${defaultRerankerModel.model}` : ""} onChange={(event) => saveDefaultKnowledgeModel("reranker", event.target.value)}>
              <option value="">未配置</option>
              {rerankerModels.map((model) => <option value={`${model.provider_id}::${model.model}`} key={`${model.provider_id}:${model.model}`}>{model.provider_id} / {model.model}</option>)}
            </select>
          </label>
        </div>
      </section>
      <div className="model-management-layout">
        <section className="settings-card model-provider-list">
          <div className="model-section-heading">
            <div>
              <div className="settings-card-title">Providers</div>
              <span>{providers.length} 个连接</span>
            </div>
            <button type="button" onClick={startCreateProvider}>添加</button>
          </div>
          {providers.length ? providers.map((provider) => (
            <button
              className={`model-provider-item ${!creatingProvider && provider.id === selectedProviderID ? "active" : ""}`}
              type="button"
              key={provider.id}
              onClick={() => {
                setCreatingProvider(false);
                setSelectedProviderID(provider.id);
                setError("");
                setMessage("");
              }}
            >
              <span className={`model-provider-status ${provider.enabled === false ? "disabled" : ""}`} aria-hidden="true" />
              <span>
                <strong>{provider.id}</strong>
                <small>{provider.provider_type} · {models.filter((model) => model.provider_id === provider.id).length} 个模型</small>
              </span>
              <em>{provider.enabled === false ? "已停用" : "已启用"}</em>
            </button>
          )) : <Empty>还没有模型 Provider。</Empty>}
        </section>

        <div className="model-management-detail">
          <form className="settings-card model-provider-editor" onSubmit={saveProvider}>
            <div className="model-section-heading">
              <div>
                <div className="settings-card-title">{creatingProvider ? "添加 Provider" : "Provider 配置"}</div>
                <span>{creatingProvider ? "接入一个 OpenAI 兼容模型服务" : selectedProvider?.id || "未选择"}</span>
              </div>
              {!creatingProvider && selectedProvider ? (
                <div className="model-section-actions">
                  <button className="secondary" type="button" title={providerDraftDirty ? "请先保存 Provider 配置" : "测试连接"} disabled={Boolean(busy) || providerDraftDirty} onClick={testProvider}>
                    {busy === "provider-test" ? "测试中..." : "测试连接"}
                  </button>
                  <button className="secondary" type="button" disabled={Boolean(busy)} onClick={toggleProvider}>
                    {busy === "provider-toggle" ? "处理中..." : (selectedProvider.enabled === false ? "启用" : "停用")}
                  </button>
                  <button className="icon-button danger" type="button" title={`删除 Provider ${selectedProvider.id}`} aria-label={`删除 Provider ${selectedProvider.id}`} disabled={Boolean(busy)} onClick={deleteProvider}><DeleteIcon /></button>
                </div>
              ) : null}
            </div>
            <div className="model-provider-form-grid">
              <label>
                <span>Provider ID</span>
                <input value={providerDraft.id} disabled={!creatingProvider} onChange={(event) => setProviderDraft((current) => ({ ...current, id: event.target.value }))} placeholder="openai-production" pattern="[A-Za-z0-9._-]+" required />
              </label>
              <label>
                <span>协议类型</span>
                <select value={providerDraft.providerType} onChange={(event) => setProviderDraft((current) => ({ ...current, providerType: event.target.value }))}>
                  <option value="openai-compatible">OpenAI Compatible</option>
                  <option value="openai">OpenAI</option>
                  <option value="fake">Fake（本地测试）</option>
                </select>
              </label>
              <label className="model-provider-wide-field">
                <span>Base URL</span>
                <input value={providerDraft.baseURL} onChange={(event) => setProviderDraft((current) => ({ ...current, baseURL: event.target.value }))} placeholder="https://api.openai.com/v1" type="url" />
              </label>
              <label className="model-provider-wide-field">
                <span>API Key 环境变量</span>
                <input value={providerDraft.apiKeyEnv} onChange={(event) => setProviderDraft((current) => ({ ...current, apiKeyEnv: event.target.value }))} placeholder="OPENAI_API_KEY" pattern="[A-Za-z_][A-Za-z0-9_]*" />
                <small>这里只保存变量名，不保存密钥明文。</small>
              </label>
            </div>
            <div className="model-editor-actions">
              {creatingProvider ? <button className="secondary" type="button" disabled={Boolean(busy)} onClick={() => setCreatingProvider(false)}>取消</button> : null}
              <button type="submit" disabled={Boolean(busy) || !providerDraft.id.trim()}>{busy === "provider-save" ? "保存中..." : (creatingProvider ? "添加 Provider" : "保存配置")}</button>
            </div>
            {!creatingProvider && selectedProvider && providerTestResults[selectedProvider.id] ? (
              <div className={`llm-diagnostic-result provider ${providerTestResults[selectedProvider.id].status === "succeeded" ? "ok" : "failed"}`}>
                {llmDiagnosticSummary(providerTestResults[selectedProvider.id])}
              </div>
            ) : null}
          </form>

          {!creatingProvider && selectedProvider ? (
            <section className="settings-card model-catalog">
              <div className="model-section-heading">
                <div>
                  <div className="settings-card-title">模型目录</div>
                  <span>{selectedProvider.id} · {selectedModels.length} 个模型</span>
                </div>
              </div>
              <form
                className="model-create-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  saveModel(modelDraft.model, modelDraft.contextWindowTokens, modelDraft.capabilityType, modelDraft.capabilities, true);
                }}
              >
                <label>
                  <span>模型 ID</span>
                  <input value={modelDraft.model} onChange={(event) => setModelDraft((current) => ({ ...current, model: event.target.value }))} placeholder="gpt-4.1" required />
                </label>
                <label>
                  <span>上下文窗口</span>
                  <input type="number" min="1000" step="1000" value={modelDraft.contextWindowTokens} onChange={(event) => setModelDraft((current) => ({ ...current, contextWindowTokens: event.target.value }))} required />
                </label>
                <label>
                  <span>模型能力</span>
                  <select value={modelDraft.capabilityType} onChange={(event) => {
                    const capabilityType = event.target.value;
                    setModelDraft((current) => ({
                      ...current,
                      capabilityType,
                      contextWindowTokens: (capabilityType === "embedding" || capabilityType === "reranker") && current.contextWindowTokens === "128000" ? "8192" : current.contextWindowTokens,
                      capabilities: defaultModelCapabilities(capabilityType)
                    }));
                  }}>
                    {modelCapabilityOptions.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}
                  </select>
                </label>
                {modelDraft.capabilityType === "embedding" ? (
                  <div className="model-create-capabilities">
                    <label><span>向量维度</span><input type="number" min="1" max="65535" value={modelDraft.capabilities.dimensions || ""} onChange={(event) => setModelDraft((current) => ({ ...current, capabilities: { ...current.capabilities, dimensions: Number(event.target.value) } }))} required /></label>
                    <label><span>距离</span><select value={modelDraft.capabilities.distance_metric || "cosine"} onChange={(event) => setModelDraft((current) => ({ ...current, capabilities: { ...current.capabilities, distance_metric: event.target.value } }))}><option value="cosine">Cosine</option><option value="l2">L2</option><option value="inner_product">Inner product</option></select></label>
                    <label><span>协议</span><select value={modelDraft.capabilities.protocol || "openai_embeddings"} onChange={(event) => setModelDraft((current) => ({ ...current, capabilities: { ...current.capabilities, protocol: event.target.value } }))}>{embeddingProtocolOptions.map((protocol) => <option value={protocol} key={protocol}>{protocol}</option>)}</select></label>
                    <label><span>批量上限</span><input type="number" min="1" max="4096" value={modelDraft.capabilities.max_batch_size || ""} onChange={(event) => setModelDraft((current) => ({ ...current, capabilities: { ...current.capabilities, max_batch_size: Number(event.target.value) } }))} required /></label>
                    <label className="model-capability-toggle"><input type="checkbox" checked={modelDraft.capabilities.normalized !== false} onChange={(event) => setModelDraft((current) => ({ ...current, capabilities: { ...current.capabilities, normalized: event.target.checked } }))} /><span>输出已归一化</span></label>
                  </div>
                ) : null}
                {modelDraft.capabilityType === "reranker" ? (
                  <div className="model-create-capabilities reranker">
                    <label><span>协议</span><select value={modelDraft.capabilities.protocol || "jina_rerank"} onChange={(event) => setModelDraft((current) => ({ ...current, capabilities: { ...current.capabilities, protocol: event.target.value } }))}>{rerankerProtocolOptions.map((protocol) => <option value={protocol} key={protocol}>{protocol}</option>)}</select></label>
                    <label><span>候选上限</span><input type="number" min="1" max="1000" value={modelDraft.capabilities.max_candidates || ""} onChange={(event) => setModelDraft((current) => ({ ...current, capabilities: { ...current.capabilities, max_candidates: Number(event.target.value) } }))} required /></label>
                  </div>
                ) : null}
                <button type="submit" disabled={Boolean(busy) || !modelDraft.model.trim()}>{busy.startsWith("model:") ? "保存中..." : "添加模型"}</button>
              </form>
              <div className="model-catalog-list">
                {selectedModels.length ? selectedModels.map((model) => (
                  <ModelCatalogRow
                    key={`${model.provider_id}:${model.model}`}
                    model={model}
                    busy={busy === `model:${model.model}`}
                    testBusy={busy === `model-test:${model.model}`}
                    testResult={modelTestResults[`${model.provider_id}::${model.model}`]}
                    onDelete={deleteModel}
                    onTest={testModel}
                    onSave={(modelName, tokens, capabilityType, capabilities) => saveModel(modelName, tokens, capabilityType, capabilities)}
                  />
                )) : <Empty>这个 Provider 还没有登记模型。</Empty>}
              </div>
            </section>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function MCPRegistrySettings({ onChanged, onRefreshRuntime, runtimeCheckedAt, runtimeError, runtimeLoading, runtimeStates, servers, workspaceID }) {
  const emptyDraft = { identifier: "", name: "", description: "", config: JSON.stringify({ transport: "streamable_http", url: "https://mcp.example.com/mcp", listen: true }, null, 2) };
  const [selectedID, setSelectedID] = useState("");
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState(emptyDraft);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [testResults, setTestResults] = useState({});
  const [deleteCandidate, setDeleteCandidate] = useState("");
  const [versions, setVersions] = useState([]);
  const [versionsLoading, setVersionsLoading] = useState(false);
  const [versionError, setVersionError] = useState("");
  const [restoreCandidate, setRestoreCandidate] = useState(0);
  const selected = servers.find((server) => server.id === selectedID) || null;
  const runtimeByServer = groupMCPRuntimeStates(runtimeStates);
  const selectedRuntimeStates = selected ? (runtimeByServer[selected.id] || []) : [];
  const selectedRuntimeSummary = summarizeMCPRuntimeStates(selectedRuntimeStates);

  useEffect(() => {
    if (creating) return;
    const next = selected || servers[0] || null;
    if (!next) {
      setSelectedID("");
      setDraft(emptyDraft);
      return;
    }
    if (next.id !== selectedID) setSelectedID(next.id);
    setDraft({ identifier: next.identifier || "", name: next.name || "", description: next.description || "", config: JSON.stringify(next.config || {}, null, 2) });
  }, [creating, selectedID, selected?.current_version, servers]);

  useEffect(() => {
    let active = true;
    if (creating || !selected?.id) {
      setVersions([]);
      setVersionsLoading(false);
      setVersionError("");
      return () => { active = false; };
    }
    setVersions([]);
    setVersionsLoading(true);
    setVersionError("");
    api.mcpServerVersions(selected.id).then((response) => {
      if (active) {
        setVersions(response.versions || []);
        setVersionsLoading(false);
      }
    }).catch((loadError) => {
      if (active) {
        setVersionError(loadError.message);
        setVersionsLoading(false);
      }
    });
    return () => { active = false; };
  }, [creating, selected?.id, selected?.current_version]);

  function choose(server) {
    setCreating(false);
    setSelectedID(server.id);
    setDraft({ identifier: server.identifier || "", name: server.name || "", description: server.description || "", config: JSON.stringify(server.config || {}, null, 2) });
    setError("");
    setMessage("");
    setDeleteCandidate("");
    setRestoreCandidate(0);
  }

  async function save(event) {
    event.preventDefault();
    setBusy("save");
    setError("");
    setMessage("");
    try {
      const config = JSON.parse(draft.config);
      const saved = creating
        ? await api.createMCPServer({ workspace_id: workspaceID, identifier: draft.identifier.trim(), name: draft.name.trim(), description: draft.description.trim(), config })
        : await api.updateMCPServer(selected.id, { name: draft.name.trim(), description: draft.description.trim(), config });
      setCreating(false);
      setSelectedID(saved.id);
      setMessage(`已保存 ${saved.name} v${saved.current_version}`);
      await onChanged(saved.id);
    } catch (saveError) {
      setError(saveError.message);
    } finally {
      setBusy("");
    }
  }

  async function testServer(server) {
    setBusy(`test:${server.id}`);
    setError("");
    try {
      const response = await api.testMCPServer(server.id);
      setTestResults((current) => ({ ...current, [server.id]: response.result }));
    } catch (testError) {
      setError(testError.message);
    } finally {
      setBusy("");
    }
  }

  async function toggleServer(server) {
    setBusy(`toggle:${server.id}`);
    setError("");
    try {
      await api.setMCPServerEnabled(server.id, server.status !== "active");
      await onChanged(server.id);
    } catch (toggleError) {
      setError(toggleError.message);
    } finally {
      setBusy("");
    }
  }

  async function archiveServer(server) {
    setBusy(`delete:${server.id}`);
    setError("");
    try {
      await api.deleteMCPServer(server.id);
      setDeleteCandidate("");
      setSelectedID("");
      await onChanged("");
    } catch (deleteError) {
      setError(deleteError.message);
    } finally {
      setBusy("");
    }
  }

  async function restoreVersion(server, sourceVersion) {
    setBusy(`restore:${sourceVersion}`);
    setError("");
    setVersionError("");
    setMessage("");
    try {
      const result = await api.restoreMCPServerVersion(server.id, sourceVersion);
      setRestoreCandidate(0);
      setSelectedID(result.server.id);
      setMessage(`已从 v${result.source_version} 恢复为 v${result.new_version}`);
      await onChanged(result.server.id);
    } catch (restoreError) {
      setVersionError(restoreError.message);
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="mcp-registry-layout">
      <section className="settings-card mcp-registry-list">
        <div className="model-section-heading">
          <div><div className="settings-card-title">Workspace MCP</div><span>{servers.length} 个注册服务</span></div>
          <div className="model-section-actions">
            <button className="icon-button secondary" type="button" title="刷新 MCP 运行状态" aria-label="刷新 MCP 运行状态" disabled={runtimeLoading} onClick={onRefreshRuntime}><RefreshIcon /></button>
            <button type="button" onClick={() => { setCreating(true); setSelectedID(""); setDraft(emptyDraft); setError(""); setMessage(""); }}>添加</button>
          </div>
        </div>
        {servers.length ? servers.map((server) => {
          const result = testResults[server.id];
          const runtimeSummary = summarizeMCPRuntimeStates(runtimeByServer[server.id] || []);
          return (
            <button className={`mcp-registry-item ${!creating && selectedID === server.id ? "active" : ""}`} type="button" key={server.id} onClick={() => choose(server)}>
              <span className={`model-provider-status ${server.status !== "active" ? "disabled" : ""}`} />
              <span><strong>{server.name}</strong><small>{server.identifier} · v{server.current_version} · {server.usage_count || 0} 个 Agent</small></span>
              <span className="mcp-registry-status-stack">
                <em className={`health-status ${result?.status || (server.status === "active" ? "unchecked" : "configuration_error")}`}>{result?.status || (server.status === "active" ? "未检查" : "已停用")}</em>
                <em className={`mcp-runtime-state ${runtimeSummary.state}`}>{runtimeSummary.label}</em>
              </span>
            </button>
          );
        }) : <Empty>Workspace 还没有注册 MCP 服务。</Empty>}
      </section>
      <form className="settings-card mcp-registry-editor" onSubmit={save}>
        <div className="model-section-heading">
          <div><div className="settings-card-title">{creating ? "添加 MCP 服务" : "服务配置"}</div><span>{creating ? "创建 Workspace 共享连接" : selected?.id || "未选择"}</span></div>
          {!creating && selected ? <div className="model-section-actions">
            <button className="secondary" type="button" disabled={Boolean(busy)} onClick={() => testServer(selected)}>{busy === `test:${selected.id}` ? "测试中..." : "测试"}</button>
            <button className="secondary" type="button" disabled={Boolean(busy)} onClick={() => toggleServer(selected)}>{selected.status === "active" ? "停用" : "启用"}</button>
            <button className="icon-button danger" type="button" title="归档 MCP 服务" aria-label="归档 MCP 服务" disabled={Boolean(busy) || selected.usage_count > 0} onClick={() => setDeleteCandidate(selected.id)}><DeleteIcon /></button>
          </div> : null}
        </div>
        {error ? <div className="health-error">{error}</div> : null}
        {message ? <div className="environment-message">{message}</div> : null}
        {runtimeError ? <div className="agent-version-error">{runtimeError}</div> : null}
        {deleteCandidate === selected?.id ? <div className="agent-version-confirm"><span>归档后将从 Workspace 目录移除。</span><div><button className="secondary" type="button" onClick={() => setDeleteCandidate("")}>取消</button><button type="button" disabled={Boolean(busy)} onClick={() => archiveServer(selected)}>确认归档</button></div></div> : null}
        {!creating && selected ? (
          <section className="mcp-runtime-panel" aria-label="MCP 运行保护状态">
            <header>
              <div><strong>运行保护</strong><span>{runtimeCheckedAt ? new Date(runtimeCheckedAt).toLocaleTimeString() : "尚未刷新"}</span></div>
              <em className={`mcp-runtime-state ${selectedRuntimeSummary.state}`}>{selectedRuntimeSummary.label}</em>
            </header>
            {selectedRuntimeStates.length ? <div className="mcp-runtime-version-list">{selectedRuntimeStates.map((state) => (
              <div className="mcp-runtime-version" key={`${state.server_id}:${state.version}`}>
                <strong>v{state.version}</strong>
                <span className={`mcp-runtime-state ${state.state}`}>{mcpRuntimeStateLabel(state.state)}</span>
                <span>并发 {state.in_flight || 0}/{state.max_concurrency || 0}</span>
                <span>连续失败 {state.consecutive_failures || 0}/{state.failure_threshold || 0}</span>
                {state.last_failure_class ? <span>{mcpRuntimeFailureLabel(state.last_failure_class)}</span> : null}
                {state.cooldown_remaining_seconds > 0 ? <span>{state.cooldown_remaining_seconds} 秒后探测</span> : null}
              </div>
            ))}</div> : <div className="mcp-runtime-empty">当前进程还没有该服务的调用记录。</div>}
          </section>
        ) : null}
        <div className="mcp-registry-form-grid">
          <label><span>标识</span><input required disabled={!creating} value={draft.identifier} onChange={(event) => setDraft((current) => ({ ...current, identifier: event.target.value }))} placeholder="team_search" /></label>
          <label><span>名称</span><input required value={draft.name} onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} placeholder="团队搜索" /></label>
          <label className="mcp-registry-wide"><span>说明</span><input value={draft.description} onChange={(event) => setDraft((current) => ({ ...current, description: event.target.value }))} /></label>
          <label className="mcp-registry-wide"><span>Server Config JSON</span><textarea rows="13" required value={draft.config} onChange={(event) => setDraft((current) => ({ ...current, config: event.target.value }))} spellCheck="false" /></label>
        </div>
        <div className="model-editor-actions">
          {creating ? <button className="secondary" type="button" onClick={() => { setCreating(false); if (servers[0]) choose(servers[0]); }}>取消</button> : null}
          <button type="submit" disabled={Boolean(busy) || !draft.name.trim() || !draft.identifier.trim()}>{busy === "save" ? "保存中..." : (creating ? "创建服务" : "发布新版本")}</button>
        </div>
      </form>
      {!creating && selected ? (
        <section className="settings-card mcp-registry-history">
          <div className="model-section-heading">
            <div><div className="settings-card-title">版本历史</div><span>{versions.length} 个不可变版本</span></div>
          </div>
          {versionError ? <div className="agent-version-error">{versionError}</div> : null}
          <div className="agent-version-list">
            {versions.map((version) => {
              const current = Number(version.version) === Number(selected.current_version);
              const confirming = restoreCandidate === Number(version.version);
              return (
                <article className={`agent-version-row ${current ? "current" : ""}`} key={version.id || version.version}>
                  <div className="agent-version-main">
                    <div className="agent-version-heading">
                      <strong>版本 #{version.version}</strong>
                      {current ? <span className="agent-version-current">当前</span> : null}
                    </div>
                    <div className="agent-version-meta">
                      <span>{version.created_at ? new Date(version.created_at).toLocaleString() : ""}</span>
                      <span>SHA-256 {String(version.checksum_sha256 || "").slice(0, 12)}</span>
                    </div>
                    <details className="mcp-registry-version-config">
                      <summary>查看配置</summary>
                      <pre>{JSON.stringify(version.config || {}, null, 2)}</pre>
                    </details>
                  </div>
                  {!current && !confirming ? <button className="secondary agent-version-rollback" type="button" disabled={Boolean(busy)} onClick={() => setRestoreCandidate(Number(version.version))}>恢复此版本</button> : null}
                  {confirming ? (
                    <div className="agent-version-confirm">
                      <span>将版本 #{version.version} 复制为新的当前版本。已有 Agent binding 保持不变。</span>
                      <div>
                        <button className="secondary" type="button" disabled={Boolean(busy)} onClick={() => setRestoreCandidate(0)}>取消</button>
                        <button type="button" disabled={Boolean(busy)} onClick={() => restoreVersion(selected, Number(version.version))}>{busy === `restore:${version.version}` ? "恢复中..." : "确认恢复"}</button>
                      </div>
                    </div>
                  ) : null}
                </article>
              );
            })}
            {versionsLoading ? <Empty>正在加载版本...</Empty> : null}
            {!versionsLoading && !versions.length && !versionError ? <Empty>还没有版本记录。</Empty> : null}
          </div>
        </section>
      ) : null}
    </div>
  );
}

function SettingsPage({
  activeSection,
  agents,
  archivedSessions,
  currentSession,
  onClose,
  onCreateAgent,
  onExportAgent,
  onImportAgent,
  onApplySessionConfig,
  onOpenInspector,
  onOpenSession,
  onRestoreSession,
  onRollbackAgent,
  onSaveAgent,
  onSelectAgent,
  onUpdateAgentPermissions,
  recentSessions,
  runtimeConfig,
  search,
  sections,
  selectedAgent,
  restoringSessionID,
  rollingBackVersion,
  savingAgent,
  setActiveSection,
  setSearch,
  skills,
  modelOptions,
  toolingCatalog,
  onSkillsChanged,
  onModelCatalogChanged,
  workspaceID
}) {
  const [archiveQuery, setArchiveQuery] = useState("");
  const [archiveRange, setArchiveRange] = useState("all");
  const [agentPortabilityBusy, setAgentPortabilityBusy] = useState("");
  const [agentPortabilityMessage, setAgentPortabilityMessage] = useState("");
  const [agentCreateOpen, setAgentCreateOpen] = useState(false);
  const [agentCreateName, setAgentCreateName] = useState("");
  const [agentCreateModel, setAgentCreateModel] = useState("");
  const [agentCreateBusy, setAgentCreateBusy] = useState(false);
  const [agentCreateError, setAgentCreateError] = useState("");
  const [agentManagementView, setAgentManagementView] = useState("config");
  const [agentPermissionBusy, setAgentPermissionBusy] = useState("");
  const [agentPermissionError, setAgentPermissionError] = useState("");
  const [healthChecking, setHealthChecking] = useState("");
  const [healthError, setHealthError] = useState("");
  const [healthReport, setHealthReport] = useState({ mcp: [], skills: [] });
  const [mcpRegistryServers, setMCPRegistryServers] = useState([]);
  const [mcpRuntimeStatus, setMCPRuntimeStatus] = useState({ checked_at: "", states: [] });
  const [mcpRuntimeLoading, setMCPRuntimeLoading] = useState(false);
  const [mcpRuntimeError, setMCPRuntimeError] = useState("");
  const importAgentInputRef = useRef(null);
  const enabledSkills = toolingCatalog.sections.find((section) => section.key === "skills")?.items.filter((item) => item.selectable) || [];
  const availableMCP = toolingCatalog.sections.find((section) => section.key === "mcp")?.items || [];
  const availableTools = toolingCatalog.sections.find((section) => section.key === "tools")?.items || [];
  const sessionStats = {
    active: recentSessions.filter((session) => session.status === "running" || session.status === "interrupting").length,
    idle: recentSessions.filter((session) => session.status === "idle").length,
    total: recentSessions.length
  };
  const selectedAgentModel = selectedAgent?.config_version
    ? `${selectedAgent.config_version.llm_provider || ""}::${selectedAgent.config_version.llm_model || ""}`
    : "";
  const defaultCreateModel = modelOptions.some((option) => `${option.llmProvider}::${option.llmModel}` === selectedAgentModel)
    ? selectedAgentModel
    : (modelOptions[0] ? `${modelOptions[0].llmProvider}::${modelOptions[0].llmModel}` : "");
  const agentCreateModelValue = agentCreateModel || defaultCreateModel;
  const healthItems = [...(healthReport.mcp || []), ...(healthReport.skills || [])];
  const healthyCount = healthItems.filter((item) => item.status === "online").length;
  const unhealthyCount = healthItems.filter((item) => item.status !== "online").length;
  const healthByKey = new Map(healthItems.map((item) => [`${item.kind}:${item.identifier}`, item]));
  const mcpWorkspaceID = workspaceID || currentSession?.workspace_id || selectedAgent?.workspace_id || "";

  async function refreshMCPRegistry() {
    const response = await api.mcpServers(mcpWorkspaceID);
    setMCPRegistryServers(response.servers || []);
    await refreshMCPRuntimeStatus();
  }

  async function refreshMCPRuntimeStatus() {
    setMCPRuntimeLoading(true);
    setMCPRuntimeError("");
    try {
      const response = await api.mcpServerRuntimeStatus(mcpWorkspaceID);
      setMCPRuntimeStatus({ checked_at: response.checked_at || "", states: response.states || [] });
    } catch (loadError) {
      setMCPRuntimeError(loadError.message);
    } finally {
      setMCPRuntimeLoading(false);
    }
  }

  useEffect(() => {
    if (activeSection !== "mcp" && activeSection !== "agent") return;
    refreshMCPRegistry().catch((loadError) => setHealthError(loadError.message));
  }, [activeSection, mcpWorkspaceID]);

  async function runToolingHealth(kind = "", identifier = "") {
    if (!selectedAgent?.id) return;
    const key = kind && identifier ? `${kind}:${identifier}` : "all";
    setHealthChecking(key);
    setHealthError("");
    try {
      const report = await api.agentToolingHealth(selectedAgent.id, { kind, identifier });
      if (!kind) {
        setHealthReport(report);
      } else {
        setHealthReport((current) => {
          const field = kind === "mcp" ? "mcp" : "skills";
          const incoming = report[field] || [];
          const incomingKeys = new Set(incoming.map((item) => item.identifier));
          return {
            ...current,
            ...(report.mcp_host ? { mcp_host: report.mcp_host } : {}),
            ...(report.mcp_http_host ? { mcp_http_host: report.mcp_http_host } : {}),
            [field]: [...(current[field] || []).filter((item) => !incomingKeys.has(item.identifier)), ...incoming]
          };
        });
      }
    } catch (error) {
      setHealthError(error.message);
    } finally {
      setHealthChecking("");
    }
  }
  async function updateAgentToolPermission(agent, namespace, enabled) {
    const policy = parseToolPolicy(agent.config_version?.tools);
    const namespaceKeys = new Set(builtinToolNamespaces.map((item) => item.key));
    const customPatterns = policy.enabledToolPatterns.filter((pattern) => !namespaceKeys.has(pattern) && !builtinToolNamespaces.some((item) => pattern.startsWith(`${item.key}.`)));
    const enabledNamespaces = builtinToolNamespaces
      .filter((item) => item.key === namespace ? enabled : toolNamespaceEnabled(item.key, policy))
      .map((item) => item.key);
    const key = `${agent.id}:${namespace}`;
    setAgentPermissionBusy(key);
    setAgentPermissionError("");
    try {
      await onUpdateAgentPermissions(agent.id, {
        enabled_tools: [...enabledNamespaces, ...customPatterns],
        ...(policy.runtime ? { runtime: policy.runtime } : {})
      });
    } catch (error) {
      setAgentPermissionError(error.message);
    } finally {
      setAgentPermissionBusy("");
    }
  }
  const filteredArchivedSessions = useMemo(() => {
    const query = archiveQuery.trim().toLowerCase();
    const now = Date.now();
    return archivedSessions.filter((session) => {
      const archivedTime = new Date(session.archived_at || 0).getTime();
      const ageDays = archivedTime ? (now - archivedTime) / 86400000 : Number.POSITIVE_INFINITY;
      if (archiveRange === "7" && ageDays > 7) return false;
      if (archiveRange === "30" && ageDays > 30) return false;
      if (archiveRange === "older" && ageDays <= 30) return false;
      if (!query) return true;
      const agentName = agents.find((agent) => agent.id === session.agent_id)?.name || "";
      return [session.title, session.id, agentName, session.summary_text, ...(session.tags || [])]
        .some((value) => String(value || "").toLowerCase().includes(query));
    }).sort((left, right) => new Date(right.archived_at || 0).getTime() - new Date(left.archived_at || 0).getTime());
  }, [agents, archiveQuery, archiveRange, archivedSessions]);
  const content = (() => {
    switch (activeSection) {
      case "environment":
        return <EnvironmentVariablesSettings workspaceID={workspaceID} />;
      case "models":
        return <ModelManagementSettings onCatalogChanged={onModelCatalogChanged} onOpenEnvironment={() => setActiveSection("environment")} />;
      case "skills":
        return (
          <SkillsManagement
            agent={selectedAgent}
            onApplyAgentConfig={onApplySessionConfig}
            onSkillsChanged={onSkillsChanged}
            runtimeConfig={runtimeConfig}
            session={currentSession}
            sessionID={currentSession?.id || ""}
            skills={skills}
            workspaceID={currentSession?.workspace_id || selectedAgent?.workspace_id || ""}
          />
        );
      case "mcp":
        return (
          <div className="settings-content-stack">
            <div className="settings-hero-card">
              <div className="health-hero-head">
                <div>
                  <strong>MCP / Skills 健康检查</strong>
                  <div className="subtle">真实连接 MCP 服务并解析 Skills 版本，集中定位离线、配置和权限问题。</div>
                </div>
                <button type="button" disabled={!selectedAgent || Boolean(healthChecking)} onClick={() => runToolingHealth()}>{healthChecking === "all" ? "检查中..." : "全面检查"}</button>
              </div>
            </div>
            <MCPRegistrySettings onChanged={refreshMCPRegistry} onRefreshRuntime={refreshMCPRuntimeStatus} runtimeCheckedAt={mcpRuntimeStatus.checked_at} runtimeError={mcpRuntimeError} runtimeLoading={mcpRuntimeLoading} runtimeStates={mcpRuntimeStatus.states} servers={mcpRegistryServers} workspaceID={mcpWorkspaceID} />
            {healthError ? <div className="health-error">{healthError}</div> : null}
            <div className="settings-grid three">
              <div className="settings-stat-card"><span>已检查</span><strong>{healthItems.length}</strong></div>
              <div className="settings-stat-card"><span>正常</span><strong>{healthyCount}</strong></div>
              <div className="settings-stat-card"><span>需处理</span><strong>{unhealthyCount}</strong></div>
            </div>
            <div className="settings-card">
              <div className="settings-card-title">MCP 服务</div>
              {healthReport.mcp_host ? (
                <div className="settings-row health-result-row">
                  <div>
                    <strong>Server Host</strong>
                    <div className="health-metrics mcp-health-metrics">
                      <span>{toolingHostMetric(healthReport.mcp_host, "sessions")} 个会话</span>
                      <span>{toolingHostMetric(healthReport.mcp_host, "in_use_sessions")} 个使用中</span>
                      <span>上限 {toolingHostMetric(healthReport.mcp_host, "max_sessions")}</span>
                      <span>启动 {toolingHostMetric(healthReport.mcp_host, "starts_total")}</span>
                      <span>回收 {toolingHostMetric(healthReport.mcp_host, "reaped_total")}</span>
                      <span>拒绝 {toolingHostMetric(healthReport.mcp_host, "rejections_total")}</span>
                      <span>目录变更 {toolingHostMetric(healthReport.mcp_host, "tools_list_changed_total") + toolingHostMetric(healthReport.mcp_host, "resources_list_changed_total") + toolingHostMetric(healthReport.mcp_host, "prompts_list_changed_total")}</span>
                      <span>进度 {toolingHostMetric(healthReport.mcp_host, "progress_notifications_total")}</span>
                      <span>日志 {toolingHostMetric(healthReport.mcp_host, "log_messages_total")}</span>
                      <span>非法通知 {toolingHostMetric(healthReport.mcp_host, "invalid_notifications_total")}</span>
                    </div>
                  </div>
                  <span className={`health-status ${toolingHostMetric(healthReport.mcp_host, "rejections_total") > 0 ? "configuration_error" : "online"}`}>
                    {toolingHostMetric(healthReport.mcp_host, "rejections_total") > 0 ? "容量告警" : "运行中"}
                  </span>
                </div>
              ) : null}
              {healthReport.mcp_http_host ? (
                <div className="settings-row health-result-row">
                  <div>
                    <strong>Remote HTTP Host</strong>
                    <div className="health-metrics mcp-health-metrics">
                      <span>{toolingHostMetric(healthReport.mcp_http_host, "sessions")} 个会话</span>
                      <span>{toolingHostMetric(healthReport.mcp_http_host, "in_use_sessions")} 个使用中</span>
                      <span>上限 {toolingHostMetric(healthReport.mcp_http_host, "max_sessions")}</span>
                      <span>启动 {toolingHostMetric(healthReport.mcp_http_host, "starts_total")}</span>
                      <span>回收 {toolingHostMetric(healthReport.mcp_http_host, "reaped_total")}</span>
                      <span>拒绝 {toolingHostMetric(healthReport.mcp_http_host, "rejections_total")}</span>
                      <span>DELETE 失败 {toolingHostMetric(healthReport.mcp_http_host, "delete_errors_total")}</span>
                      <span>目录变更 {toolingHostMetric(healthReport.mcp_http_host, "tools_list_changed_total") + toolingHostMetric(healthReport.mcp_http_host, "resources_list_changed_total") + toolingHostMetric(healthReport.mcp_http_host, "prompts_list_changed_total")}</span>
                      <span>进度 {toolingHostMetric(healthReport.mcp_http_host, "progress_notifications_total")}</span>
                      <span>日志 {toolingHostMetric(healthReport.mcp_http_host, "log_messages_total")}</span>
                      <span>非法通知 {toolingHostMetric(healthReport.mcp_http_host, "invalid_notifications_total")}</span>
                      <span>{toolingHostMetric(healthReport.mcp_http_host, "egress_allow_http") ? "允许 HTTP" : "仅 HTTPS"}</span>
                      <span>{toolingHostMetric(healthReport.mcp_http_host, "egress_allow_private_networks") ? "允许私网" : "阻止私网"}</span>
                      <span>Host 白名单 {toolingHostMetric(healthReport.mcp_http_host, "egress_allowed_host_count")}</span>
                      <span>CIDR 白名单 {toolingHostMetric(healthReport.mcp_http_host, "egress_allowed_cidr_count")}</span>
                      <span>出站阻断 {toolingHostMetric(healthReport.mcp_http_host, "egress_blocked_total")}</span>
                    </div>
                  </div>
                  <span className={`health-status ${toolingHostMetric(healthReport.mcp_http_host, "rejections_total") > 0 || toolingHostMetric(healthReport.mcp_http_host, "delete_errors_total") > 0 || toolingHostMetric(healthReport.mcp_http_host, "egress_blocked_total") > 0 ? "configuration_error" : "online"}`}>
                    {toolingHostMetric(healthReport.mcp_http_host, "rejections_total") > 0 || toolingHostMetric(healthReport.mcp_http_host, "delete_errors_total") > 0 || toolingHostMetric(healthReport.mcp_http_host, "egress_blocked_total") > 0 ? "运行告警" : "运行中"}
                  </span>
                </div>
              ) : null}
              {availableMCP.length ? availableMCP.map((item) => {
                const normalizedIdentifier = String(item.name || "").replace(/[^a-zA-Z0-9_]+/g, "_").replace(/^_+|_+$/g, "").toLowerCase();
                const result = healthByKey.get(`mcp:${item.name}`) || healthByKey.get(`mcp:${normalizedIdentifier}`);
                const capabilities = toolingHealthCapabilities(result);
                return (
                <div className="settings-row health-result-row" key={item.key}>
                  <div>
                    <strong>{item.title}</strong>
                    <div className="subtle">{item.description}</div>
                    {result?.detail ? <div className={`health-detail ${result.status}`}>{result.detail}</div> : (!item.selectable && item.disabledReason ? <div className="settings-row-note">{item.disabledReason}</div> : null)}
                    {result ? (
                      <div className="health-metrics mcp-health-metrics">
                        <span>{result.transport || "stdio"}</span>
                        <span>{result.latency_ms || 0} ms</span>
                        <span>{result.tool_count || 0} 个工具</span>
                        <span>{toolingHealthCatalogMetric(result, "resource_count")} 个资源</span>
                        <span>{toolingHealthCatalogMetric(result, "resource_template_count")} 个资源模板</span>
                        <span>{toolingHealthCatalogMetric(result, "prompt_count")} 个 Prompt</span>
                        {capabilities.length ? <span>能力 {capabilities.join(", ")}</span> : null}
                      </div>
                    ) : null}
                  </div>
                  <div className="settings-row-actions">
                    <span className={`health-status ${result?.status || (item.selectable ? "unchecked" : "configuration_error")}`}>{result?.status || (item.selectable ? "未检查" : "配置错误")}</span>
                    <button className="secondary" type="button" disabled={Boolean(healthChecking)} onClick={() => runToolingHealth("mcp", item.name)}>{healthChecking === `mcp:${item.name}` ? "测试中..." : "测试"}</button>
                  </div>
                </div>
                );
              }) : <Empty>当前智能体还没有配置 MCP 服务。</Empty>}
            </div>
            <div className="settings-card">
              <div className="settings-card-title">已启用 Skills</div>
              {enabledSkills.length ? enabledSkills.map((item) => {
                const result = healthByKey.get(`skill:${item.name}`);
                return (
                  <div className="settings-row health-result-row" key={item.key}>
                    <div>
                      <strong>{item.title}</strong>
                      <div className="subtle">{item.description}</div>
                      {result?.detail ? <div className={`health-detail ${result.status}`}>{result.detail}</div> : null}
                      {result ? <div className="health-metrics">版本 {result.version || "-"} · 约 {result.estimated_tokens || 0} tokens</div> : null}
                    </div>
                    <div className="settings-row-actions">
                      <span className={`health-status ${result?.status || "unchecked"}`}>{result?.status || "未检查"}</span>
                      <button className="secondary" type="button" disabled={Boolean(healthChecking)} onClick={() => runToolingHealth("skill", item.name)}>{healthChecking === `skill:${item.name}` ? "测试中..." : "测试"}</button>
                    </div>
                  </div>
                );
              }) : <Empty>当前 Agent 没有启用 Skills。</Empty>}
            </div>
          </div>
        );
      case "agent":
        return (
          <div className="settings-content-stack">
            <div className="settings-hero-card agent-management-hero">
              <div>
                <strong>Agent 管理</strong>
                <div className="subtle">可视化维护默认模型、system prompt、工具、Skills 与 MCP 服务。</div>
              </div>
              <div className="agent-portability-actions">
                <input
                  ref={importAgentInputRef}
                  type="file"
                  accept="application/json,.json"
                  hidden
                  onChange={async (event) => {
                    const file = event.target.files?.[0];
                    event.target.value = "";
                    if (!file) return;
                    setAgentPortabilityBusy("import");
                    setAgentPortabilityMessage("");
                    try {
                      const imported = await onImportAgent(file);
                      setAgentPortabilityMessage(`已导入 ${imported.name || imported.id}`);
                    } catch (error) {
                      setAgentPortabilityMessage(error.message);
                    } finally {
                      setAgentPortabilityBusy("");
                    }
                  }}
                />
                <button className="secondary" type="button" disabled={agentCreateBusy} onClick={() => {
                  setAgentCreateOpen((current) => !current);
                  setAgentCreateName("");
                  setAgentCreateModel(defaultCreateModel);
                  setAgentCreateError("");
                }}>{agentCreateOpen ? "关闭新建" : "新建 Agent"}</button>
                <button className="secondary" type="button" disabled={Boolean(agentPortabilityBusy)} onClick={() => importAgentInputRef.current?.click()}>{agentPortabilityBusy === "import" ? "导入中..." : "导入 Agent"}</button>
                <button type="button" disabled={!selectedAgent || Boolean(agentPortabilityBusy)} onClick={async () => {
                  setAgentPortabilityBusy("export");
                  setAgentPortabilityMessage("");
                  try {
                    await onExportAgent();
                    setAgentPortabilityMessage(`已导出 ${selectedAgent.name || selectedAgent.id}`);
                  } catch (error) {
                    setAgentPortabilityMessage(error.message);
                  } finally {
                    setAgentPortabilityBusy("");
                  }
                }}>{agentPortabilityBusy === "export" ? "导出中..." : "导出当前 Agent"}</button>
              </div>
              {agentPortabilityMessage ? <div className="agent-portability-message">{agentPortabilityMessage}</div> : null}
            </div>
            {agentCreateOpen ? (
              <form className="settings-card agent-create-panel" onSubmit={async (event) => {
                event.preventDefault();
                const model = modelOptions.find((option) => `${option.llmProvider}::${option.llmModel}` === agentCreateModelValue);
                if (!agentCreateName.trim() || !model) return;
                setAgentCreateBusy(true);
                setAgentCreateError("");
                try {
                  await onCreateAgent({
                    name: agentCreateName.trim(),
                    llm_provider: model.llmProvider,
                    llm_model: model.llmModel
                  });
                  setAgentCreateOpen(false);
                  setAgentCreateName("");
                  setAgentCreateModel("");
                } catch (error) {
                  setAgentCreateError(error.message);
                } finally {
                  setAgentCreateBusy(false);
                }
              }}>
                <div className="settings-card-title">新建 Agent</div>
                {agentCreateError ? <div className="agent-version-error agent-create-error">{agentCreateError}</div> : null}
                <label className="agent-editor-field">
                  <span>Agent 名称</span>
                  <input value={agentCreateName} onChange={(event) => setAgentCreateName(event.target.value)} placeholder="输入 Agent 名称" />
                </label>
                <label className="agent-editor-field">
                  <span>默认模型</span>
                  <select value={agentCreateModelValue} onChange={(event) => setAgentCreateModel(event.target.value)}>
                    {!modelOptions.length ? <option value="">没有可用模型</option> : null}
                    {modelOptions.map((option) => (
                      <option key={`${option.llmProvider}::${option.llmModel}`} value={`${option.llmProvider}::${option.llmModel}`}>{option.label}</option>
                    ))}
                  </select>
                </label>
                <div className="agent-create-actions">
                  <button className="secondary" type="button" disabled={agentCreateBusy} onClick={() => {
                    setAgentCreateOpen(false);
                    setAgentCreateName("");
                    setAgentCreateModel("");
                    setAgentCreateError("");
                  }}>取消</button>
                  <button type="submit" disabled={agentCreateBusy || !agentCreateName.trim() || !agentCreateModelValue}>{agentCreateBusy ? "创建中..." : "创建 Agent"}</button>
                </div>
              </form>
            ) : null}
            <div className="agent-management-tabs" role="tablist" aria-label="Agent 管理视图">
              <button className={agentManagementView === "config" ? "active" : ""} type="button" role="tab" aria-selected={agentManagementView === "config"} onClick={() => setAgentManagementView("config")}>配置编辑</button>
              <button className={agentManagementView === "permissions" ? "active" : ""} type="button" role="tab" aria-selected={agentManagementView === "permissions"} onClick={() => setAgentManagementView("permissions")}>权限矩阵</button>
            </div>
            {agentManagementView === "config" ? <div className="agent-management-layout">
              <div className="settings-card agent-management-list">
                <div className="settings-card-title">工作区智能体 · {agents.length}</div>
                {agents.length ? (
                  <div className="settings-agent-list">
                    {agents.map((agent) => {
                      const isCurrent = agent.id === selectedAgent?.id;
                      return (
                        <div className={`settings-row ${isCurrent ? "current" : ""}`} key={agent.id}>
                          <div>
                            <strong>{agent.name || agent.id}</strong>
                            <div className="subtle">{agent.config_version?.llm_provider || "-"} / {agent.config_version?.llm_model || "-"}</div>
                            <div className="subtle">配置版本 #{agent.current_config_version || agent.config_version?.version || 1}</div>
                          </div>
                          <div className="settings-row-actions">
                            {isCurrent ? <Pill value="idle" /> : <button className="secondary" type="button" onClick={() => onSelectAgent(agent.id)}>选择</button>}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ) : <Empty>工作区里还没有智能体。</Empty>}
              </div>
              <div className="settings-card agent-management-editor">
                <div className="settings-card-title">当前 Agent 配置</div>
                <AgentConfigEditor agent={selectedAgent} mcpRegistryServers={mcpRegistryServers} modelOptions={modelOptions} onRollback={onRollbackAgent} onSave={onSaveAgent} rollingBackVersion={rollingBackVersion} saving={savingAgent} skills={skills} />
              </div>
            </div> : (
              <div className="settings-card agent-permissions-card">
                <div className="settings-card-title">默认工具权限 · {agents.length} 个 Agent</div>
                {agentPermissionError ? <div className="agent-version-error">{agentPermissionError}</div> : null}
                {agents.length ? (
                  <div className="agent-permissions-scroll">
                    <table className="agent-permissions-table">
                      <thead>
                        <tr>
                          <th scope="col">Agent</th>
                          {builtinToolNamespaces.map((item) => <th scope="col" key={item.key}>{item.title}</th>)}
                        </tr>
                      </thead>
                      <tbody>
                        {agents.map((agent) => {
                          const policy = parseToolPolicy(agent.config_version?.tools);
                          return (
                            <tr className={agent.id === selectedAgent?.id ? "current" : ""} key={agent.id}>
                              <th scope="row">
                                <strong>{agent.name || agent.id}</strong>
                                <span>版本 #{agent.current_config_version || 1}</span>
                              </th>
                              {builtinToolNamespaces.map((item) => {
                                const checked = toolNamespaceEnabled(item.key, policy);
                                const busy = agentPermissionBusy === `${agent.id}:${item.key}`;
                                return (
                                  <td key={item.key}>
                                    <input
                                      type="checkbox"
                                      aria-label={`允许 ${agent.name || agent.id} 使用 ${item.title}`}
                                      checked={checked}
                                      disabled={Boolean(agentPermissionBusy)}
                                      onChange={(event) => updateAgentToolPermission(agent, item.key, event.target.checked)}
                                    />
                                    {busy ? <span className="agent-permission-saving">保存中</span> : null}
                                  </td>
                                );
                              })}
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                ) : <Empty>工作区里还没有智能体。</Empty>}
              </div>
            )}
          </div>
        );
      case "work":
        return (
          <div className="settings-content-stack">
            <div className="settings-hero-card">
              <strong>归档任务中心</strong>
              <div className="subtle">集中查找已归档任务，查看历史内容或恢复到主任务列表。</div>
            </div>
            <div className="settings-grid three">
              <div className="settings-stat-card">
                <span>总任务</span>
                <strong>{sessionStats.total}</strong>
              </div>
              <div className="settings-stat-card">
                <span>运行中</span>
                <strong>{sessionStats.active}</strong>
              </div>
              <div className="settings-stat-card">
                <span>空闲</span>
                <strong>{sessionStats.idle}</strong>
              </div>
            </div>
            <div className="settings-card">
              <div className="settings-card-title">当前会话</div>
              {currentSession ? (
                <div className="settings-row">
                  <div>
                    <strong>{currentSession.title || currentSession.id}</strong>
                    <div className="subtle">{currentSession.id}</div>
                  </div>
                  <Pill value={currentSession.status || "unknown"} />
                </div>
              ) : <Empty>当前还没有打开的任务。</Empty>}
            </div>
            <div className="settings-card">
              <div className="archive-toolbar">
                <label className="archive-search-field">
                  <span>搜索归档任务</span>
                  <input value={archiveQuery} onChange={(event) => setArchiveQuery(event.target.value)} placeholder="按标题、任务 ID 或 Agent 搜索" />
                </label>
                <label className="archive-range-field">
                  <span>归档时间</span>
                  <select value={archiveRange} onChange={(event) => setArchiveRange(event.target.value)}>
                    <option value="all">全部时间</option>
                    <option value="7">最近 7 天</option>
                    <option value="30">最近 30 天</option>
                    <option value="older">30 天以前</option>
                  </select>
                </label>
              </div>
              <div className="archive-result-count">{filteredArchivedSessions.length} 个归档任务</div>
              {filteredArchivedSessions.length ? (
                <div className="settings-agent-list">
                  {filteredArchivedSessions.map((session) => {
                    const agentName = agents.find((agent) => agent.id === session.agent_id)?.name || session.agent_id || "未知 Agent";
                    const restoring = restoringSessionID === session.id;
                    return (
                    <div className="settings-row" key={session.id}>
                      <div>
                        <strong>{session.title || session.id}</strong>
                        <div className="subtle">{agentName} · 归档于 {formatTime(session.archived_at || session.created_at)}</div>
                        {session.summary_text ? <div className="archive-session-summary">{sessionSummaryPreview(session.summary_text)}</div> : null}
                        {session.tags?.length ? <div className="task-nav-tags archive-tags">{session.tags.map((tag) => <span key={tag}>{tag}</span>)}</div> : null}
                        <div className="archive-session-id">{session.id}</div>
                      </div>
                      <div className="settings-row-actions">
                        <button className="secondary" type="button" onClick={() => onOpenSession(session)}>查看</button>
                        <button type="button" disabled={restoring} onClick={() => onRestoreSession(session)}>{restoring ? "恢复中..." : "恢复"}</button>
                      </div>
                    </div>
                    );
                  })}
                </div>
              ) : <Empty>{archivedSessions.length ? "没有符合筛选条件的归档任务。" : "还没有已归档任务。"}</Empty>}
            </div>
          </div>
        );
      default:
        return (
          <div className="settings-content-stack">
            <div className="settings-hero-card">
              <strong>工具管理</strong>
              <div className="subtle">当前智能体和会话可见的工具、Skills、MCP 能力会集中显示在这里。</div>
            </div>
            <div className="settings-grid two">
              <div className="settings-card">
                <div className="settings-card-title">当前工具能力</div>
                {availableTools.length ? availableTools.map((item) => (
                  <div className="settings-row" key={item.key}>
                    <div>
                      <strong>{item.title}</strong>
                      <div className="subtle">{item.description}</div>
                    </div>
                    <Pill value={item.selectable ? "idle" : "blocked"} />
                  </div>
                )) : <Empty>当前没有可见工具。</Empty>}
              </div>
              <div className="settings-card">
                <div className="settings-card-title">当前智能体</div>
                {selectedAgent ? (
                  <div className="settings-agent-highlight">
                    <strong>{selectedAgent.name || selectedAgent.id}</strong>
                    <div className="subtle">{selectedAgent.config_version?.llm_provider || "-"} / {selectedAgent.config_version?.llm_model || "-"}</div>
                  </div>
                ) : <Empty>还没有选中智能体。</Empty>}
              </div>
            </div>
          </div>
        );
    }
  })();

  return (
    <div className="settings-page">
      <aside className="settings-sidebar">
        <button className="settings-back-button" type="button" onClick={onClose}>← 返回应用</button>
        <input className="settings-search" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索设置..." />
        <div className="settings-nav">
          {sections.length ? sections.map((section) => (
            <button
              className={`settings-nav-item ${section.key === activeSection ? "active" : ""}`}
              type="button"
              key={section.key}
              onClick={() => {
                if (section.key === "inspector") {
                  onOpenInspector();
                  return;
                }
                setActiveSection(section.key);
              }}
            >
              <div>
                <strong>{section.title}</strong>
                <div>{section.description}</div>
              </div>
            </button>
          )) : <Empty>没有匹配的设置项。</Empty>}
        </div>
      </aside>
      <main className="settings-main">
        <header className="settings-main-header">
          <div>
            <div className="settings-main-label">设置</div>
            <h1>{sections.find((section) => section.key === activeSection)?.title || "设置"}</h1>
          </div>
        </header>
        {content}
      </main>
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

function sessionSummaryPreview(value) {
  let text = String(value || "").trim();
  const assistantMarker = text.lastIndexOf("\nassistant:");
  if (assistantMarker >= 0) text = text.slice(assistantMarker + "\nassistant:".length).trim();
  text = text.replace(/\s+/g, " ");
  return text.length > 180 ? `${text.slice(0, 180)}...` : text;
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
    const isStreamingNoise = event.type === "runtime.llm_delta" || event.type === "runtime.llm_chunk";
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

function llmChunkActivity(data) {
  switch (data.type) {
    case "reasoning":
      return { title: "模型推理", detail: shortText(data.text || "模型正在返回推理内容。", 180), kind: "running" };
    case "tool_call":
      return { title: "模型准备工具", detail: "正在组装工具调用参数。", kind: "running" };
    case "usage":
      return { title: "模型用量已返回", detail: `${Number(data.usage?.total_tokens || 0)} tokens`, kind: "ok" };
    case "stop":
      return { title: "模型流已结束", detail: data.finish_reason || "已完成流式响应。", kind: "ok" };
    case "error": {
      const presentation = providerErrorPresentation(data.error, "流式响应失败。");
      return { title: "模型流错误", detail: shortText(presentation.detail, 260), kind: "failed" };
    }
    default:
      return { title: "生成回复", detail: "正在流式返回内容。", kind: "running" };
  }
}

function activityView(event) {
  const data = eventData(event);
  switch (event?.type) {
    case "agent.message":
      return { title: "智能体已回复", detail: shortText(cleanMessageText(eventText(event)) || "已准备工具动作。", 180), kind: "ok" };
    case "runtime.started":
      return { title: "开始处理", detail: "正在准备本轮任务。", kind: "running" };
    case "runtime.thinking":
      return { title: "正在处理", detail: "正在准备下一步。", kind: "running" };
    case "runtime.llm_request":
      return { title: "请求模型", detail: "正在生成下一步动作或回复。", kind: "running" };
    case "runtime.llm_delta":
      return { title: "生成回复", detail: "正在流式返回内容。", kind: "running" };
    case "runtime.llm_chunk":
      return llmChunkActivity(data);
    case "runtime.llm_response":
      return { title: "模型已返回", detail: "正在判断是否需要工具。", kind: "running" };
    case "runtime.progress_message":
      return { title: "过程更新", detail: shortText(data.text || "智能体正在继续处理。", 180), kind: "running" };
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
        ? data.error || data.message || "工具返回了错误。"
        : artifactCount ? `生成了 ${artifactCount} 个结果文件。` : summary.detail;
      return { title: data.success === false ? `${summary.title} failed` : `${summary.title} finished`, detail: shortText(detail, 180), kind: data.success === false ? "error" : "ok" };
    }
    case "runtime.tool_intervention_required": {
      const summary = toolSummary({
        identifier: data.identifier,
        apiName: data.api_name,
        args: objectValue(data.arguments),
        reason: data.reason
      });
      return { title: `需要审批：${summary.title}`, detail: summary.detail || data.reason || "请先审批再继续。", kind: "warn" };
    }
    case "runtime.tool_intervention_approved":
      return { title: "审批已通过", detail: data.decision_reason || "正在继续任务。", kind: "ok" };
    case "runtime.tool_intervention_rejected":
      return { title: "审批被拒绝", detail: data.decision_reason || "该工具调用未被允许。", kind: "error" };
    case "runtime.completed":
    case "session.status_idle":
      return { title: "任务空闲", detail: payload(event).last_turn_status === "failed" ? payload(event).reason || "上一轮执行失败。" : "等待下一条消息。", kind: payload(event).last_turn_status === "failed" ? "error" : "ok" };
    case "runtime.failed":
    case "session.status_failed": {
      const original = payload(event).reason || payload(event).message || "执行过程中出现失败。";
      const providerError = objectValue(data.provider_error);
      const detail = Object.keys(providerError).length
        ? providerErrorPresentation(providerError, original).detail
        : original;
      return { title: "任务失败", detail: shortText(detail, 260), kind: "error" };
    }
    case "session.status_running":
      return { title: "执行中", detail: "智能体正在处理任务。", kind: "running" };
    case "session.status_interrupting":
      return { title: "正在中断", detail: "正在停止当前轮次。", kind: "warn" };
    case "session.status_terminated":
      return { title: "已归档", detail: "该任务已经归档。", kind: "muted" };
    default:
      return { title: event?.type || "活动", detail: turnActivityLabel(event) || eventText(event), kind: "muted" };
  }
}

function artifactName(artifact) {
  return artifact?.name || artifact?.id || "artifact";
}

function artifactMetadata(artifact) {
  const metadata = artifact?.metadata;
  return metadata && typeof metadata === "object" && !Array.isArray(metadata) ? metadata : {};
}

function isUserFileArtifact(artifact) {
  const metadata = artifactMetadata(artifact);
  return metadata.protocol_version === "tma.tool_export.v1" || String(artifact?.description || "").startsWith("Exported file");
}

function finalFileArtifacts(artifacts) {
  const latestByPath = new Map();
  for (const artifact of artifacts || []) {
    if (!isUserFileArtifact(artifact)) continue;
    const metadata = artifactMetadata(artifact);
    const key = String(metadata.path || artifactName(artifact)).replace(/\\/g, "/");
    latestByPath.set(key, artifact);
  }
  return [...latestByPath.values()];
}

function artifactReferencedByMessage(artifact, text) {
  const normalizedText = String(text || "").replace(/\\/g, "/");
  const metadata = artifactMetadata(artifact);
  const path = String(metadata.path || metadata.file_path || "").replace(/\\/g, "/");
  const name = artifactName(artifact);
  return Boolean((path && normalizedText.includes(path)) || (name && normalizedText.includes(name)));
}

function finalAgentMessageArtifacts(event, artifacts) {
  const turnID = payload(event).turn_id || "";
  const candidates = finalFileArtifacts((artifacts || []).filter((artifact) => artifact.turn_id === turnID));
  const referenced = candidates.filter((artifact) => artifactReferencedByMessage(artifact, eventText(event)));
  return referenced.length ? referenced : candidates;
}

function conversationFinalFileArtifacts(artifacts, events) {
  const candidates = finalFileArtifacts(artifacts);
  const finalMessageByTurn = new Map();
  for (const event of events || []) {
    if (event.type === "agent.message") finalMessageByTurn.set(payload(event).turn_id || "", event);
  }
  const referencedTurns = new Set();
  for (const artifact of candidates) {
    const event = finalMessageByTurn.get(artifact.turn_id || "");
    if (event && artifactReferencedByMessage(artifact, eventText(event))) referencedTurns.add(artifact.turn_id || "");
  }
  return candidates.filter((artifact) => {
    const turnID = artifact.turn_id || "";
    if (!referencedTurns.has(turnID)) return true;
    return artifactReferencedByMessage(artifact, eventText(finalMessageByTurn.get(turnID)));
  });
}

function agentMessageWithArtifactLinks(text, hasArtifacts) {
  if (!hasArtifacts) return text;
  return String(text || "")
    .replace(
      /^\s*(?:\*\*)?文件(?:路径|地址)(?:\*\*)?\s*[：:]\s*`[^`]+`\s*$/gm,
      "文件已保存到任务产物，可从下方预览或下载。"
    )
    .replace(/点击上方\s*artifact\s*中的/gi, "使用下方相关文件中的");
}

function artifactPathParts(artifact) {
  const metadata = artifactMetadata(artifact);
  const explicitPath = metadata.path || metadata.file_path || "";
  const rawPath = String(explicitPath || artifactName(artifact) || "").replace(/\\/g, "/");
  const parts = rawPath.split("/").filter(Boolean);
  if (explicitPath && parts.length) return parts;
  const turnMatch = String(artifact?.turn_id || "").match(/(\d+)$/);
  const turnFolder = turnMatch ? `第 ${Number(turnMatch[1])} 轮` : "其他结果";
  return [turnFolder, parts.at(-1) || artifactName(artifact)];
}

function buildArtifactTree(artifacts) {
  const root = { folders: new Map(), files: [] };
  for (const artifact of artifacts) {
    const parts = artifactPathParts(artifact);
    let node = root;
    for (const folder of parts.slice(0, -1)) {
      if (!node.folders.has(folder)) {
        node.folders.set(folder, { folders: new Map(), files: [] });
      }
      node = node.folders.get(folder);
    }
    node.files.push({ artifact, label: parts.at(-1) || artifactName(artifact) });
  }
  return root;
}

function ArtifactTreeNode({ name, node, depth, selectedArtifactID, onPreview }) {
  const [open, setOpen] = useState(true);
  const folders = Array.from(node.folders.entries()).sort(([left], [right]) => left.localeCompare(right));
  const files = [...node.files].sort((left, right) => left.label.localeCompare(right.label));
  return (
    <div className="artifact-tree-branch">
      {name ? (
        <button className="artifact-tree-folder" type="button" style={{ "--tree-depth": depth }} onClick={() => setOpen((current) => !current)}>
          <span className={`artifact-tree-chevron ${open ? "open" : ""}`}>›</span>
          <FolderIcon open={open} />
          <span>{name}</span>
        </button>
      ) : null}
      {open ? (
        <div>
          {folders.map(([folderName, folderNode]) => (
            <ArtifactTreeNode key={`${depth}-${folderName}`} name={folderName} node={folderNode} depth={depth + 1} selectedArtifactID={selectedArtifactID} onPreview={onPreview} />
          ))}
          {files.map(({ artifact, label }) => {
            return (
              <button
                className={`artifact-tree-file ${artifact.id === selectedArtifactID ? "active" : ""}`}
                key={artifact.id}
                style={{ "--tree-depth": depth + (name ? 1 : 0) }}
                title={`打开 ${artifactName(artifact)}`}
                type="button"
                onClick={() => onPreview(artifact)}
              >
                <FileIcon />
                <span>{label}</span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function ArtifactPreviewContent({ preview, mode = "preview" }) {
  if (!preview) return null;
  if (preview.status === "loading") return <Empty>正在加载预览...</Empty>;
  if (preview.status === "error") return <div className="artifact-preview-error">{preview.error}</div>;
  if (preview.status === "ready" && preview.kind === "image") {
    return <img className="preview-media" src={preview.objectUrl} alt={preview.resource.title} />;
  }
  if (preview.status === "ready" && preview.kind === "text") {
    if (mode === "preview") {
      if (isMarkdownResource(preview.resource, preview.contentType)) {
        return (
          <article className="artifact-preview-markdown">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{preview.text || ""}</ReactMarkdown>
          </article>
        );
      }
      if (isHTMLResource(preview.resource, preview.contentType)) {
        return (
          <iframe
            className="artifact-preview-html"
            sandbox=""
            referrerPolicy="no-referrer"
            srcDoc={htmlPreviewDocument(preview.text || "")}
            title={`${preview.resource.title} HTML 预览`}
          />
        );
      }
    }
    return <pre className="artifact-preview-text">{preview.text || ""}</pre>;
  }
  if (preview.status === "ready" && preview.kind === "download") {
    return <div className="artifact-preview-error">{preview.message || "请下载文件后查看。"}</div>;
  }
  return null;
}

function MessageArtifacts({ artifacts, sessionID, onPreview }) {
  if (!artifacts.length) return null;
  return (
    <div className="message-artifacts">
      <div className="message-artifacts-title">相关文件</div>
      {artifacts.map((artifact) => {
        const href = api.artifactDownloadPath(sessionID, artifact.id);
        return (
          <div className="message-artifact" key={artifact.id}>
            <button className="message-artifact-open" type="button" onClick={() => onPreview(artifact)}>
              <FileIcon />
              <span>{artifactName(artifact)}</span>
            </button>
            <a className="message-artifact-download" href={href} target="_blank" rel="noreferrer">下载</a>
          </div>
        );
      })}
    </div>
  );
}

function uploadedMessageArtifacts(event, artifacts) {
  const attachments = Array.isArray(payload(event).attachments) ? payload(event).attachments : [];
  const byID = new Map((artifacts || []).map((artifact) => [artifact.id, artifact]));
  return attachments.map((attachment) => byID.get(attachment.artifact_id) || {
    id: attachment.artifact_id,
    name: attachment.name,
    artifact_type: "file",
    metadata: {
      content_type: attachment.content_type,
      size_bytes: attachment.size_bytes,
      workspace_path: attachment.workspace_path
    }
  }).filter((artifact) => artifact.id);
}

function turnActivityLabel(event) {
  if (!event) return "";
  switch (event.type) {
    case "runtime.started":
      return "本轮已开始，正在处理...";
    case "runtime.thinking":
      return "正在处理当前请求...";
    case "runtime.llm_request":
      return "正在和模型对话...";
    case "runtime.llm_delta":
      return "正在生成回复...";
    case "runtime.llm_chunk": {
      const type = eventData(event).type;
      if (type === "reasoning") return "模型正在推理...";
      if (type === "tool_call") return "模型正在准备工具调用...";
      if (type === "usage") return "模型已返回用量...";
      if (type === "stop") return "模型流已结束，正在整理...";
      if (type === "error") return "模型流式响应失败。";
      return "正在生成回复...";
    }
    case "runtime.llm_response":
      return "模型已返回结果，正在整理...";
    case "runtime.progress_message":
      return "智能体更新了当前进展。";
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
  const idleEvent = reverseEvents.find((event) => event.type === "session.status_idle");
  const terminatedEvent = reverseEvents.find((event) => event.type === "session.status_terminated");
  const approvalEvent = reverseEvents.find((event) => event.type === "runtime.tool_intervention_required");
  const progressEvent = reverseEvents.find((event) =>
    [
      "runtime.started",
      "runtime.thinking",
      "runtime.llm_request",
      "runtime.llm_chunk",
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
  if (idleEvent || terminatedEvent || sessionStatus === "idle" || sessionStatus === "terminated") {
    return null;
  }
  if (waitingForReply || progressEvent || sessionStatus === "running" || sessionStatus === "interrupting") {
    return {
      kind: "thinking",
      title: "正在处理",
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
            <span className={`source-chip ${summary.source}`}>{summary.sourceLabel}</span>
            <span className={`risk-chip ${summary.risk}`}>{summary.risk} risk</span>
          </Meta>
        </div>
        <span className="tool-action-status">已准备</span>
      </div>
      {summary.detail ? <div className="tool-action-detail">{summary.detail}</div> : null}
      {Object.keys(tool.args || {}).length ? (
        <details className="tool-action-details">
          <summary>详情</summary>
          <pre>{pretty(tool.args)}</pre>
        </details>
      ) : null}
    </div>
  );
}

function ChevronIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="m5 6 3 3 3-3" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" />
    </svg>
  );
}

function ProcessIcon({ type }) {
  if (type === "runtime.failed") {
    return (
      <svg aria-hidden="true" viewBox="0 0 16 16">
        <circle cx="8" cy="8" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.25" />
        <path d="m6 6 4 4m0-4-4 4" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.4" />
      </svg>
    );
  }
  if (type === "runtime.thinking") {
    return (
      <svg aria-hidden="true" viewBox="0 0 16 16">
        <path d="M8 2.25a4.35 4.35 0 0 0-2.72 7.75c.45.36.72.85.72 1.35v.4h4v-.4c0-.5.27-.99.72-1.35A4.35 4.35 0 0 0 8 2.25Z" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.25" />
        <path d="M6.5 13.75h3M8 5v3M6.5 6.5h3" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.25" />
      </svg>
    );
  }
  if (type.includes("intervention")) {
    return (
      <svg aria-hidden="true" viewBox="0 0 16 16">
        <path d="M8 2.25 13 4v3.65c0 2.85-1.9 5.2-5 6.1-3.1-.9-5-3.25-5-6.1V4l5-1.75Z" fill="none" stroke="currentColor" strokeLinejoin="round" strokeWidth="1.25" />
        <path d="M8 5v3.2m0 2.3v.1" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.4" />
      </svg>
    );
  }
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="m9.5 2.5-6 6h4l-1 5 6-7h-4l1-4Z" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.25" />
    </svg>
  );
}

function ProcessStatusIcon({ status }) {
  if (status === "running") return <span aria-hidden="true" className="process-status-spinner" />;
  if (status === "error") {
    return (
      <svg aria-hidden="true" viewBox="0 0 16 16">
        <path d="m5 5 6 6m0-6-6 6" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.5" />
      </svg>
    );
  }
  if (status === "warning") {
    return (
      <svg aria-hidden="true" viewBox="0 0 16 16">
        <path d="M8 2.5 14 13H2L8 2.5Zm0 3.5v3.2m0 1.8v.1" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.25" />
      </svg>
    );
  }
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="m3.5 8 3 3 6-6" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" />
    </svg>
  );
}

function formatClockTime(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function ProcessEventCard({
  event,
  active = false,
  completedAt = "",
  toolLifecycle = null,
  onApplySessionConfig = () => {},
  onRequestSkillEnable = () => {},
  onRequestSkillDisable = () => {},
  activeSkillKeys = new Set(),
  enabledSkillKeys = new Set(),
  latestSkillLifecycle = new Map(),
  sessionConfigApplyBusy = 0,
  sessionConfigVersion = 0,
  skillEnableBusy = "",
  skillDisableBusy = "",
  skillEnableDisabled = false
}) {
  const data = eventData(event);
  const error = objectValue(data.error);
  const args = objectValue(data.arguments);
  const state = objectValue(data.state);
  const artifacts = Array.isArray(data.artifacts) ? data.artifacts : [];
  let title = event.type;
  let metaLabel = event.type;
  let preview = "";
  let detailObject = null;
  let tone = "muted";
  let status = "completed";
  let statusLabel = "完成";
  let defaultExpanded = false;
  const lifecycleResult = toolLifecycle?.result;
  const lifecycleResultData = eventData(lifecycleResult);
  const lifecycleRejected = toolLifecycle?.decision?.type === "runtime.tool_intervention_rejected";
  const lifecycleApproved = toolLifecycle?.decision?.type === "runtime.tool_intervention_approved";

  if (event.type === "runtime.thinking") {
    title = "处理请求";
    metaLabel = "运行状态";
    preview = turnActivityLabel(event) || "正在准备下一步。";
    tone = "tool";
    status = active ? "running" : "completed";
    statusLabel = active ? "进行中" : "完成";
    defaultExpanded = true;
  } else if (isReasoningChunk(event)) {
    title = "推理过程";
    metaLabel = "模型返回";
    preview = String(data.text || "");
    tone = "tool";
    status = active ? "running" : "completed";
    statusLabel = active ? "生成中" : "完成";
    defaultExpanded = false;
  } else if (event.type === "runtime.tool_call") {
    const summary = toolSummary({
      identifier: data.identifier,
      apiName: data.api_name,
      args,
      reason: data.reason,
      source: data.tool_source,
      manifestType: data.manifest_type
    });
    title = summary.title;
    metaLabel = `调用 · ${summary.label}`;
    preview = processPreview(data.identifier, data.api_name, args, {}, summary.source);
    detailObject = {
      source: summary.sourceLabel,
      arguments: Object.keys(args).length ? args : undefined
    };
    tone = summary.risk === "high" ? "warn" : "tool";
    status = summary.risk === "high" ? "warning" : active ? "running" : "completed";
    statusLabel = summary.risk === "high" ? "待确认" : active ? "执行中" : "已调用";
    defaultExpanded = active;
    if (lifecycleResult) {
      tone = lifecycleResultData.success === false ? "error" : "ok";
      status = lifecycleResultData.success === false ? "error" : "completed";
      statusLabel = lifecycleResultData.success === false ? "失败" : "完成";
      defaultExpanded = false;
    } else if (lifecycleRejected) {
      tone = "error";
      status = "error";
      statusLabel = "已拒绝";
    } else if (lifecycleApproved) {
      tone = "ok";
      status = "completed";
      statusLabel = "已通过";
    } else if (toolLifecycle?.required) {
      tone = "warn";
      status = "warning";
      statusLabel = "待审批";
    }
  } else if (event.type === "runtime.tool_result") {
    const summary = toolSummary({
      identifier: data.identifier,
      apiName: data.api_name,
      args,
      reason: data.reason,
      success: data.success,
      source: data.tool_source,
      manifestType: data.manifest_type
    });
    title = summary.title;
    metaLabel = `结果 · ${summary.label}`;
    preview = processPreview(data.identifier, data.api_name, args, {
      content: data.content,
      error,
      state,
      success: data.success
    }, summary.source) || (artifacts.length ? `${artifacts.length} artifact${artifacts.length === 1 ? "" : "s"} generated.` : "Tool finished successfully.");
    detailObject = {
      source: summary.sourceLabel,
      arguments: Object.keys(args).length ? args : undefined,
      content: data.content || undefined,
      state: data.state && Object.keys(objectValue(data.state)).length ? data.state : undefined,
      artifacts: artifacts.length ? artifacts : undefined,
      error: Object.keys(error).length ? error : undefined
    };
    tone = data.success === false ? "error" : "ok";
    status = data.success === false ? "error" : "completed";
    statusLabel = data.success === false ? "失败" : "完成";
    defaultExpanded = data.success !== false && data.identifier === "skills" && ["preview", "install", "enable", "disable"].includes(data.api_name);
  } else if (event.type === "runtime.tool_intervention_required") {
    const summary = toolSummary({
      identifier: data.identifier,
      apiName: data.api_name,
      args,
      reason: data.reason,
      source: data.tool_source,
      manifestType: data.manifest_type
    });
    title = `需要审批：${summary.title}`;
    metaLabel = `审批 · ${summary.label}`;
    preview = summary.detail || data.reason || "请先审批再继续。";
    detailObject = {
      source: summary.sourceLabel,
      arguments: Object.keys(args).length ? args : undefined
    };
    tone = "warn";
    status = "warning";
    statusLabel = "待审批";
    defaultExpanded = true;
    if (lifecycleResult) {
      tone = lifecycleResultData.success === false ? "error" : "ok";
      status = lifecycleResultData.success === false ? "error" : "completed";
      statusLabel = lifecycleResultData.success === false ? "失败" : "已执行";
      defaultExpanded = false;
    } else if (lifecycleRejected) {
      tone = "error";
      status = "error";
      statusLabel = "已拒绝";
      defaultExpanded = false;
    } else if (lifecycleApproved) {
      tone = "ok";
      status = "completed";
      statusLabel = "已通过";
      defaultExpanded = false;
    }
  } else if (event.type === "runtime.tool_intervention_approved") {
    title = "审批已通过";
    metaLabel = "审批";
    preview = data.decision_reason || "该工具调用可以继续执行。";
    tone = "ok";
    statusLabel = "已通过";
  } else if (event.type === "runtime.tool_intervention_rejected") {
    title = "审批被拒绝";
    metaLabel = "审批";
    preview = data.decision_reason || "该工具调用已停止。";
    tone = "error";
    status = "error";
    statusLabel = "已拒绝";
    defaultExpanded = true;
  } else if (event.type === "runtime.failed") {
    title = "任务失败";
    metaLabel = "执行错误";
    const original = payload(event).reason || payload(event).message || eventText(event) || "执行过程中出现错误。";
    const providerError = objectValue(data.provider_error);
    const presentation = Object.keys(providerError).length
      ? providerErrorPresentation(providerError, original)
      : null;
    preview = presentation?.detail || original;
    detailObject = presentation
      ? { description: presentation.description, original_error: presentation.original, provider_error: providerError }
      : Object.keys(error).length ? { error } : null;
    tone = "error";
    status = "error";
    statusLabel = "失败";
    defaultExpanded = true;
  }

  const startedAtMS = new Date(event.created_at || "").getTime();
  const completedAtMS = new Date(completedAt || "").getTime();
  const inferredDurationMS = Number.isFinite(startedAtMS) && Number.isFinite(completedAtMS) && completedAtMS > startedAtMS
    ? completedAtMS - startedAtMS
    : 0;
  const durationMS = Number(firstValue(data, ["duration_ms"]) || firstValue(payload(event), ["duration_ms"]) || inferredDurationMS);
  const eventTime = formatClockTime(event.created_at);
  const installedSkill = objectValue(state.skill);
  const installedVersion = objectValue(state.version);
  const installedSkillKey = `${installedSkill.identifier}:${Number(installedVersion.version || 1)}`;
  const skillInstallAction = event.type === "runtime.tool_result" && data.success !== false && data.identifier === "skills" && data.api_name === "install" && installedSkill.identifier && !enabledSkillKeys.has(installedSkillKey)
    ? {
        identifier: String(installedSkill.identifier),
        title: String(installedSkill.title || installedSkill.identifier),
        version: Number(installedVersion.version || 1),
        key: `${installedSkill.identifier}:${Number(installedVersion.version || 1)}`
      }
    : null;
  const enabledBinding = objectValue(state.binding);
  const enabledSkillKey = `${enabledBinding.skill}:${Number(enabledBinding.version || 1)}`;
  const enabledConfigVersion = Number(state.new_config_version || 0);
  const currentSessionConfigVersion = Number(sessionConfigVersion || 0);
  const enabledLifecycle = latestSkillLifecycle.get(String(enabledBinding.skill || ""));
  const enableIsLatest = !enabledLifecycle || Number(enabledLifecycle.seq || 0) === Number(event.seq || 0);
  const skillEnableAction = event.type === "runtime.tool_result" && data.success !== false && data.identifier === "skills" && data.api_name === "enable" && enabledBinding.skill && enabledConfigVersion > 0
    ? {
        identifier: String(enabledBinding.skill),
        version: Number(enabledBinding.version || 1),
        targetConfigVersion: enabledConfigVersion,
        operation: "enable",
        isLatest: enableIsLatest,
        status: !enableIsLatest
          ? "superseded"
          : activeSkillKeys.has(enabledSkillKey) || currentSessionConfigVersion === enabledConfigVersion
          ? "applied"
          : currentSessionConfigVersion > enabledConfigVersion
            ? "superseded"
            : "pending"
      }
    : null;
  const disabledBinding = objectValue(state.binding);
  const disabledConfigVersion = Number(state.new_config_version || 0);
  const disabledLifecycle = latestSkillLifecycle.get(String(disabledBinding.skill || ""));
  const disableIsLatest = !disabledLifecycle || Number(disabledLifecycle.seq || 0) === Number(event.seq || 0);
  const disabledStillActive = [...activeSkillKeys].some((key) => key.startsWith(`${disabledBinding.skill}:`));
  const skillDisableAction = event.type === "runtime.tool_result" && data.success !== false && data.identifier === "skills" && data.api_name === "disable" && disabledBinding.skill && disabledConfigVersion > 0
    ? {
        identifier: String(disabledBinding.skill),
        version: Number(disabledBinding.version || 0),
        targetConfigVersion: disabledConfigVersion,
        operation: "disable",
        removed: state.removed !== false,
        isLatest: disableIsLatest,
        status: !disableIsLatest
          ? "superseded"
          : currentSessionConfigVersion === disabledConfigVersion || (currentSessionConfigVersion > disabledConfigVersion && !disabledStillActive)
            ? "applied"
            : currentSessionConfigVersion > disabledConfigVersion
              ? "superseded"
              : "pending"
      }
    : null;
  const [expanded, setExpanded] = useState(defaultExpanded);

  return (
    <section className={`process-card ${tone}${expanded ? " expanded" : ""}`}>
      <button className="process-card-head" type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
        <span className="process-card-chevron"><ChevronIcon /></span>
        <span className="process-card-type-icon"><ProcessIcon type={event.type} /></span>
        <span className="process-card-title">
          <strong>{title}</strong>
          <span>{metaLabel}</span>
        </span>
        <span className="process-card-status">
          {eventTime ? <span>{eventTime}</span> : null}
          {durationMS > 0 ? <span>耗时 {formatDuration(durationMS)}</span> : null}
          <strong>{statusLabel}</strong>
          <span className={`process-card-status-icon ${status}`}><ProcessStatusIcon status={status} /></span>
        </span>
      </button>
      {expanded ? (
        <div className="process-card-body">
          <div className="process-card-summary">
            {preview ? <div className="process-card-preview">{preview}</div> : null}
            {data.tool_source ? (
              <div className="process-card-source">
                <span>来源</span>
                <span className={`source-chip ${toolSource(data.identifier, data.tool_source, data.manifest_type)}`}>{toolSourceLabel(toolSource(data.identifier, data.tool_source, data.manifest_type))}</span>
              </div>
            ) : null}
          </div>
          {skillInstallAction ? (
            <div className="skill-install-action">
              <div><strong>{skillInstallAction.title} v{skillInstallAction.version}</strong><span>已安装但尚未写入当前 Agent 配置。</span></div>
              <button type="button" disabled={skillEnableDisabled || skillEnableBusy === skillInstallAction.key} onClick={() => onRequestSkillEnable(skillInstallAction)}>
                {skillEnableBusy === skillInstallAction.key ? "正在发起" : "请求启用"}
              </button>
            </div>
          ) : null}
          {skillEnableAction ? (
            <div className={`skill-install-action session-config-action ${skillEnableAction.status}`}>
              <div>
                <strong>{skillEnableAction.identifier} v{skillEnableAction.version}</strong>
                <span>
                  {skillEnableAction.status === "applied"
                    ? `当前 Session 已使用 Agent 配置 v${currentSessionConfigVersion || skillEnableAction.targetConfigVersion}，Skill 已生效。`
                    : skillEnableAction.status === "superseded"
                      ? skillEnableAction.isLatest
                        ? `当前 Session 已使用更高的 Agent 配置 v${currentSessionConfigVersion}，请在 Agent 设置中确认该 Skill 是否仍启用。`
                        : "该启用结果已被后续 Skill 配置操作取代。"
                      : `当前 Session 仍是 Agent 配置 v${currentSessionConfigVersion || "?"}，应用 v${skillEnableAction.targetConfigVersion} 后生效。`}
                </span>
              </div>
              {skillEnableAction.status === "pending" ? (
                <button
                  type="button"
                  disabled={skillEnableDisabled || sessionConfigApplyBusy === skillEnableAction.targetConfigVersion}
                  onClick={() => onApplySessionConfig(skillEnableAction)}
                >
                  {sessionConfigApplyBusy === skillEnableAction.targetConfigVersion ? "正在应用" : "应用到当前会话"}
                </button>
              ) : skillEnableAction.status === "applied" && skillEnableAction.isLatest ? (
                <button
                  className="secondary danger"
                  type="button"
                  disabled={skillEnableDisabled || skillDisableBusy === skillEnableAction.identifier}
                  onClick={() => onRequestSkillDisable(skillEnableAction)}
                >
                  {skillDisableBusy === skillEnableAction.identifier ? "正在发起" : "请求停用"}
                </button>
              ) : null}
            </div>
          ) : null}
          {skillDisableAction ? (
            <div className={`skill-install-action session-config-action ${skillDisableAction.status}`}>
              <div>
                <strong>{skillDisableAction.identifier}{skillDisableAction.version > 0 ? ` v${skillDisableAction.version}` : ""}</strong>
                <span>
                  {skillDisableAction.status === "applied"
                    ? `当前 Session 已使用 Agent 配置 v${currentSessionConfigVersion || skillDisableAction.targetConfigVersion}，Skill 已停用但仍保留在 Registry。`
                    : skillDisableAction.status === "superseded"
                      ? skillDisableAction.isLatest
                        ? `当前 Session 已使用更高的 Agent 配置 v${currentSessionConfigVersion}，请确认该 Skill 当前是否启用。`
                        : "该停用结果已被后续 Skill 配置操作取代。"
                      : `当前 Session 仍是 Agent 配置 v${currentSessionConfigVersion || "?"}，应用 v${skillDisableAction.targetConfigVersion} 后停用。`}
                </span>
              </div>
              {skillDisableAction.status === "pending" ? (
                <button
                  type="button"
                  disabled={skillEnableDisabled || sessionConfigApplyBusy === skillDisableAction.targetConfigVersion}
                  onClick={() => onApplySessionConfig(skillDisableAction)}
                >
                  {sessionConfigApplyBusy === skillDisableAction.targetConfigVersion ? "正在应用" : "应用到当前会话"}
                </button>
              ) : skillDisableAction.status === "applied" && skillDisableAction.isLatest && skillDisableAction.version > 0 ? (
                <button
                  className="secondary"
                  type="button"
                  disabled={skillEnableDisabled || skillEnableBusy === `${skillDisableAction.identifier}:${skillDisableAction.version}`}
                  onClick={() => onRequestSkillEnable(skillDisableAction)}
                >
                  {skillEnableBusy === `${skillDisableAction.identifier}:${skillDisableAction.version}` ? "正在发起" : "重新启用"}
                </button>
              ) : null}
            </div>
          ) : null}
          {detailObject ? (
            <details className="process-card-details">
              <summary>查看执行详情</summary>
              <pre className="process-card-detail">{pretty(detailObject)}</pre>
            </details>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function MessageBody({ event, artifacts = [], sessionID = "", onPreview = () => {} }) {
  const text = eventText(event);
  if (event.type === "agent.message") {
    return <AgentMessageBody artifacts={artifacts} onPreview={onPreview} sessionID={sessionID} text={text} />;
  }
  const parts = [{ type: "text", text }];
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
      <MessageArtifacts artifacts={artifacts} sessionID={sessionID} onPreview={onPreview} />
    </div>
  );
}

function AgentMessageBody({ text, artifacts = [], sessionID = "", onPreview = () => {}, streaming = false }) {
  const markdown = streaming
    ? String(text || "")
    : agentMessageWithArtifactLinks(cleanMessageText(text), artifacts.length > 0);
  if (!markdown) {
    return artifacts.length
      ? <MessageArtifacts artifacts={artifacts} sessionID={sessionID} onPreview={onPreview} />
      : <div className="message-text">（空消息）</div>;
  }
  return (
    <div className="message-body">
      <MarkdownMessage text={markdown} streaming={streaming} />
      <MessageArtifacts artifacts={artifacts} sessionID={sessionID} onPreview={onPreview} />
    </div>
  );
}

function MarkdownMessage({ text, streaming = false }) {
	return (
		<div className={`message-text message-markdown${streaming ? " is-streaming" : ""}`}>
			<ReactMarkdown
				remarkPlugins={[remarkGfm]}
				skipHtml
				components={{
					a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noreferrer" />
				}}
			>
				{String(text || "")}
			</ReactMarkdown>
			{streaming ? <span aria-hidden="true" className="streaming-cursor" /> : null}
		</div>
	);
}

function hasVisibleAgentText(event) {
  return Boolean(cleanMessageText(eventText(event)));
}

function streamedAgentReply(events) {
  const ordered = [...(events || [])].sort((left, right) => Number(left.seq || 0) - Number(right.seq || 0));
  const lastFinalMessageSeq = ordered.reduce((maximum, event) => (
    event.type === "agent.message" ? Math.max(maximum, Number(event.seq || 0)) : maximum
  ), 0);
  const requestEvent = [...ordered].reverse().find((event) => (
    event.type === "runtime.llm_request" && Number(event.seq || 0) > lastFinalMessageSeq
  ));
  if (!requestEvent) return null;
  const requestSeq = Number(requestEvent.seq || 0);
  const stopped = ordered.some((event) => (
    Number(event.seq || 0) > requestSeq &&
    ["agent.message", "runtime.progress_message", "runtime.tool_call", "runtime.tool_intervention_required", "runtime.failed"].includes(event.type)
  ));
  if (stopped) return null;
  const chunkEvents = ordered.filter((event) => (
    event.type === "runtime.llm_chunk" && eventData(event).type === "text" && Number(event.seq || 0) > requestSeq
  ));
  const deltaEvents = chunkEvents.length ? chunkEvents : ordered.filter((event) => (
    event.type === "runtime.llm_delta" && Number(event.seq || 0) > requestSeq
  ));
  const text = deltaEvents.map((event) => String(eventData(event).text || "")).join("");
  if (!text) return null;
  return {
    createdAt: deltaEvents[0]?.created_at || requestEvent.created_at,
    turnID: String(payload(requestEvent).turn_id || payload(deltaEvents[0]).turn_id || ""),
    text
  };
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

function sessionStatusFromEvent(event) {
  switch (event?.type) {
    case "session.status_provisioning":
    case "session.status_idle":
    case "session.status_running":
    case "session.status_interrupting":
    case "session.status_compacting":
    case "session.status_failed":
    case "session.status_terminated":
      return String(payload(event).status || event.type.replace("session.status_", "") || "");
    default:
      return "";
  }
}

function latestSessionStatus(events, fallback = "") {
  const reverseEvents = [...(events || [])].reverse();
  for (const event of reverseEvents) {
    const status = sessionStatusFromEvent(event);
    if (status) return status;
  }
  return String(fallback || "");
}

function latestIdleTurnStatus(events) {
  const idleEvent = [...(events || [])].reverse().find((event) => event.type === "session.status_idle");
  return String(payload(idleEvent).last_turn_status || "");
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
  const [principal, setPrincipal] = useState(null);
  const [agentID, setAgentID] = useState("");
  const [environmentID, setEnvironmentID] = useState("");
  const [sessionID, setSessionID] = useState("");
  const [task, setTask] = useState("");
  const [composerFiles, setComposerFiles] = useState([]);
  const [composerDragActive, setComposerDragActive] = useState(false);
  const [uploadingFiles, setUploadingFiles] = useState(false);
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
  const [artifactPreviewMode, setArtifactPreviewMode] = useState("preview");
  const [artifactPreviewWidth, setArtifactPreviewWidth] = useState(480);
  const [runtimeConfig, setRuntimeConfig] = useState(null);
  const [runtimeCapabilities, setRuntimeCapabilities] = useState({ default_runtime: "cloud_sandbox", available_runtimes: ["cloud_sandbox"] });
  const [modelOptions, setModelOptions] = useState([]);
  const [defaultAgentConfig, setDefaultAgentConfig] = useState(null);
  const [availableAgents, setAvailableAgents] = useState([]);
  const [taskHoverPreview, setTaskHoverPreview] = useState(null);
  const [settingsDraft, setSettingsDraft] = useState({
    interventionMode: "request_approval",
    llmModel: "",
    llmProvider: "",
    toolRuntime: "cloud_sandbox"
  });
  const [installedSkills, setInstalledSkills] = useState([]);
  const [toolPickerOpen, setToolPickerOpen] = useState(false);
  const [toolingLoading, setToolingLoading] = useState(false);
  const [toolingError, setToolingError] = useState("");
  const [selectedGuidanceKeys, setSelectedGuidanceKeys] = useState([]);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsSection, setSettingsSection] = useState("skills");
  const [settingsSearch, setSettingsSearch] = useState("");
  const [archivedSessions, setArchivedSessions] = useState([]);
  const [savingSettings, setSavingSettings] = useState(false);
  const [savingAgentConfig, setSavingAgentConfig] = useState(false);
  const [rollingBackAgentVersion, setRollingBackAgentVersion] = useState(0);
  const [approvalsOpen, setApprovalsOpen] = useState(false);
  const [visibleTaskCount, setVisibleTaskCount] = useState(10);
  const [rightPanelTab, setRightPanelTab] = useState("results");
  const [taskTemplates, setTaskTemplates] = useState([]);
  const [templatePickerOpen, setTemplatePickerOpen] = useState(false);
  const [selectedTaskTemplateID, setSelectedTaskTemplateID] = useState("");
  const [workflowMode, setWorkflowMode] = useState(false);
  const [workflowRun, setWorkflowRun] = useState(null);
  const [comparisonOpen, setComparisonOpen] = useState(false);
  const [comparisonLeftID, setComparisonLeftID] = useState("");
  const [comparisonRightID, setComparisonRightID] = useState("");
  const [comparisonResult, setComparisonResult] = useState(null);
  const [comparisonLoading, setComparisonLoading] = useState(false);
  const [comparisonVariantModel, setComparisonVariantModel] = useState("");
  const [taskMenuSessionID, setTaskMenuSessionID] = useState("");
  const [taskMenuPosition, setTaskMenuPosition] = useState(null);
  const [metadataSession, setMetadataSession] = useState(null);
  const [metadataTagsDraft, setMetadataTagsDraft] = useState("");
  const [savingMetadata, setSavingMetadata] = useState(false);
  const [requestingSkillEnable, setRequestingSkillEnable] = useState("");
  const [requestingSkillDisable, setRequestingSkillDisable] = useState("");
  const [applyingSessionConfigVersion, setApplyingSessionConfigVersion] = useState(0);
  const [pluginRoutePath, setPluginRoutePath] = useState(pluginPathFromHash);
  const [pluginNavigation, setPluginNavigation] = useState([]);
  const [pluginRoutes, setPluginRoutes] = useState([]);
  const [pluginLoadState, setPluginLoadState] = useState("loading");
  const eventStreamCursorRef = useRef(0);
  const sessionSyncTimerRef = useRef(null);
  const artifactResizeRef = useRef(null);
  const threadRef = useRef(null);
  const shouldAutoScrollRef = useRef(true);
  const pendingApprovalCountRef = useRef(0);
  const approvalDecisionRef = useRef("");
  const scrollFrameRef = useRef(0);
  const workflowAdvancingRef = useRef(false);
  const composerFileInputRef = useRef(null);
  const taskMenuButtonRefs = useRef(new Map());

  useEffect(() => {
    let active = true;
    api.currentPrincipal().then((response) => {
      if (active) setPrincipal(response.principal || null);
    }).catch((error) => {
      if (active) setStatus(error.message);
    });
    return () => {
      active = false;
    };
  }, []);

  async function logout() {
    const response = await fetch("/auth/logout", { method: "POST" });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || `HTTP ${response.status}`);
    window.location.assign(payload.redirect_url || "/app");
  }

  useEffect(() => {
    const syncPluginPath = () => setPluginRoutePath(pluginPathFromHash());
    window.addEventListener("hashchange", syncPluginPath);
    window.addEventListener("popstate", syncPluginPath);
    return () => {
      window.removeEventListener("hashchange", syncPluginPath);
      window.removeEventListener("popstate", syncPluginPath);
    };
  }, []);

  useEffect(() => {
    if (!taskMenuSessionID) return undefined;
    const closeTaskMenu = (event) => {
      const target = event.target;
      if (target instanceof Element && target.closest(".task-action-menu, .task-menu-button")) return;
      setTaskMenuSessionID("");
      setTaskMenuPosition(null);
    };
    const closeTaskMenuOnViewportChange = () => {
      setTaskMenuSessionID("");
      setTaskMenuPosition(null);
    };
    document.addEventListener("pointerdown", closeTaskMenu);
    window.addEventListener("resize", closeTaskMenuOnViewportChange);
    window.addEventListener("scroll", closeTaskMenuOnViewportChange, true);
    return () => {
      document.removeEventListener("pointerdown", closeTaskMenu);
      window.removeEventListener("resize", closeTaskMenuOnViewportChange);
      window.removeEventListener("scroll", closeTaskMenuOnViewportChange, true);
    };
  }, [taskMenuSessionID]);

  useEffect(() => {
    document.title = "TMA Workbench";
    let active = true;
    async function restoreRecentSession() {
      setStatus("loading history");
      const defaultsPromise = loadPreSessionDefaults();
      const response = await api.sessions({ limit: 30 });
      if (!active) return;
      const sessions = response.sessions || [];
      setRecentSessions(sessions);
      const remembered = rememberedSessionID();
      const selected = sessions.find((session) => session.id === remembered) || sessions[0];
      if (!selected) {
        await defaultsPromise;
        setStatus("ready");
        return;
      }
      setSessionID(selected.id);
      setAgentID(selected.agent_id || "");
      setEnvironmentID(selected.environment_id || "");
      rememberSession(selected.id);
      await loadSession(selected.id);
      await defaultsPromise;
      if (active) setStatus("history restored");
    }
    restoreRecentSession().catch((error) => {
      if (active) setStatus(error.message);
    });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    let active = true;
    api.taskTemplates().then((response) => {
      if (active) setTaskTemplates(response.templates || []);
    }).catch((error) => {
      if (active) setStatus(error.message);
    });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!sessionID) {
      setWorkflowRun(null);
      return;
    }
    const restored = readWorkflowRun(sessionID);
    setWorkflowRun(restored?.sessionID === sessionID ? restored : null);
  }, [sessionID]);

  async function loadPreSessionDefaults() {
    const [defaultAgent, agentsResponse, providersResponse] = await Promise.all([
      api.defaultAgent(),
      api.agents(),
      api.llmProviders()
    ]);
    const sortedAgents = [...(agentsResponse.agents || [])].sort((left, right) => {
      if (left.id === defaultAgent.id) return -1;
      if (right.id === defaultAgent.id) return 1;
      return String(left.name || "").localeCompare(String(right.name || ""));
    });
    const enabledProviders = (providersResponse.providers || []).filter((provider) => provider.enabled !== false);
    const modelResponses = await Promise.all(enabledProviders.map((provider) => api.llmModels(provider.id).catch(() => ({ models: [] }))));
    const options = enabledProviders.flatMap((provider, index) => (
      (modelResponses[index].models || []).map((model) => ({
        label: `${provider.id} / ${model.model}`,
        llmModel: model.model,
        llmProvider: provider.id,
        capabilityType: model.capability_type || "text",
        isDefaultVision: Boolean(model.is_default_vision)
      }))
    ));
    setDefaultAgentConfig(defaultAgent);
    setAvailableAgents(sortedAgents);
    setAgentID((current) => current || defaultAgent.id);
    setModelOptions(options);
    const skillsResponse = await api.skills({ workspaceId: defaultAgent.workspace_id }).catch(() => ({ skills: [] }));
    setInstalledSkills(skillsResponse.skills || []);
    setSettingsDraft((current) => ({
      ...current,
      llmModel: current.llmModel || defaultAgent.config_version?.llm_model || "",
      llmProvider: current.llmProvider || defaultAgent.config_version?.llm_provider || "",
      toolRuntime: current.toolRuntime || "cloud_sandbox"
    }));
  }

  const events = eventsResponse.events || [];
  const toolCallLifecycles = useMemo(() => buildToolCallLifecycles(events), [events]);
  const conversationEvents = useMemo(() => events
    .filter((event) => event.type === "user.message" || event.type === "agent.message")
    .sort((left, right) => Number(left.seq || 0) - Number(right.seq || 0)), [events]);
  const chatTimelineEvents = useMemo(() => mergeReasoningChunks([...events]
    .sort((left, right) => Number(left.seq || 0) - Number(right.seq || 0)))
    .filter((event) => {
      if (event.type === "user.message") return true;
      if (event.type === "agent.message") return hasVisibleAgentText(event);
      return [
        "runtime.thinking",
        "runtime.llm_chunk",
        "runtime.progress_message",
        "runtime.tool_call",
        "runtime.tool_result",
        "runtime.tool_intervention_required",
        "runtime.tool_intervention_rejected",
        "runtime.failed"
      ].includes(event.type) && (event.type !== "runtime.llm_chunk" || isReasoningChunk(event));
    })
    , [events]);
  const latestSuccessfulSkillInstallSeq = useMemo(() => {
    const event = [...events].reverse().find((item) => {
      const data = eventData(item);
      return item.type === "runtime.tool_result" && data.success !== false && data.identifier === "skills" && data.api_name === "install";
    });
    return Number(event?.seq || 0);
  }, [events]);
  const enabledSkillKeys = useMemo(() => new Set(events.flatMap((event) => {
    const data = eventData(event);
    const state = objectValue(data.state);
    const binding = objectValue(state.binding);
    if (event.type !== "runtime.tool_result" || data.success === false || data.identifier !== "skills" || data.api_name !== "enable" || !binding.skill) return [];
    return [`${binding.skill}:${Number(binding.version || 1)}`];
  })), [events]);
  const latestSkillLifecycle = useMemo(() => {
    const lifecycle = new Map();
    for (const event of events) {
      const data = eventData(event);
      const state = objectValue(data.state);
      const binding = objectValue(state.binding);
      if (event.type !== "runtime.tool_result" || data.success === false || data.identifier !== "skills" || !["enable", "disable"].includes(data.api_name) || !binding.skill) continue;
      const current = lifecycle.get(String(binding.skill));
      if (!current || Number(event.seq || 0) >= Number(current.seq || 0)) {
        lifecycle.set(String(binding.skill), { apiName: data.api_name, seq: Number(event.seq || 0) });
      }
    }
    return lifecycle;
  }, [events]);
  const activeSkillKeys = useMemo(() => new Set(parseSkillsConfig(runtimeConfig?.skills).enabled.map((binding) => (
    `${binding.skill}:${Number(binding.version || 1)}`
  ))), [runtimeConfig?.skills]);
  const streamingReply = useMemo(() => streamedAgentReply(events), [events]);
	const renderedChatTimelineEvents = useMemo(() => {
		if (!streamingReply) return chatTimelineEvents;
		return [...chatTimelineEvents, {
			type: "agent.streaming",
			seq: `stream-${streamingReply.turnID || "current"}`,
			created_at: streamingReply.createdAt,
			payload: {
				turn_id: streamingReply.turnID,
				content_format: "markdown",
				content: [{ type: "text", text: streamingReply.text }]
			}
		}];
	}, [chatTimelineEvents, streamingReply]);
  const interventions = interventionResponse.interventions || [];
  const artifacts = artifactResponse.artifacts || [];
  const resultFiles = useMemo(() => conversationFinalFileArtifacts(artifacts, conversationEvents), [artifacts, conversationEvents]);
  const artifactTree = useMemo(() => buildArtifactTree(resultFiles), [resultFiles]);
  const lastUserSeq = useMemo(() => {
    return events.reduce((maximum, event) => (
      event.type === "user.message" ? Math.max(maximum, Number(event.seq || 0)) : maximum
    ), 0);
  }, [events]);
  const effectiveSessionStatus = useMemo(() => latestSessionStatus(events, sessionMeta?.status), [events, sessionMeta?.status]);
  const lastIdleTurnStatus = useMemo(() => latestIdleTurnStatus(events), [events]);
  const liveSignal = useMemo(() => turnSignal(events, {
    sinceSeq: lastUserSeq,
    sessionStatus: effectiveSessionStatus,
    waitingForReply,
    interventions,
    includeSuccess: waitingForReply
  }), [events, interventions, lastUserSeq, effectiveSessionStatus, waitingForReply]);
  const runState = useMemo(() => {
    if (interventions.length) return "waiting approval";
    if (effectiveSessionStatus) return effectiveSessionStatus;
    return sessionID ? "active" : "not started";
  }, [interventions.length, effectiveSessionStatus, sessionID]);
  const hasPendingApprovals = interventions.length > 0;
  const activityEvents = useMemo(() => {
    return compactActivityEvents(events);
  }, [events]);
  const filteredTaskSessions = useMemo(() => {
    const query = taskSearch.trim().toLowerCase();
    const activeAgentID = String(agentID || defaultAgentConfig?.id || "").trim();
    const matches = recentSessions.filter((session) => {
      if (activeAgentID && session.agent_id !== activeAgentID) return false;
      if (!query) return true;
      return [session.title, session.id, session.summary_text, ...(session.tags || [])]
        .some((value) => String(value || "").toLowerCase().includes(query));
    });
    return [...matches].sort((left, right) => {
      if (Boolean(left.pinned_at) !== Boolean(right.pinned_at)) return left.pinned_at ? -1 : 1;
      if (left.pinned_at && right.pinned_at) {
        const pinOrder = new Date(right.pinned_at).getTime() - new Date(left.pinned_at).getTime();
        if (pinOrder !== 0) return pinOrder;
      }
      if (left.id === sessionID) return -1;
      if (right.id === sessionID) return 1;
      const statusRank = sessionStatusRank(left.status) - sessionStatusRank(right.status);
      if (statusRank !== 0) return statusRank;
      return new Date(right.created_at || 0).getTime() - new Date(left.created_at || 0).getTime();
    });
  }, [recentSessions, agentID, defaultAgentConfig?.id, sessionID, taskSearch]);
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
  const selectedAgentValue = agentID || defaultAgentConfig?.id || "";
  const selectedAgent = availableAgents.find((agent) => agent.id === selectedAgentValue) || defaultAgentConfig;
  const selectedTaskTemplate = taskTemplates.find((template) => template.id === selectedTaskTemplateID) || null;
  const toolingConfig = sessionID ? runtimeConfig : selectedAgent?.config_version || null;
  const toolingWorkspaceID = String(sessionMeta?.workspace_id || runtimeConfig?.workspace_id || selectedAgent?.workspace_id || defaultAgentConfig?.workspace_id || "").trim();
  const pluginScope = useMemo(() => ({
	organizationId: String(principal?.organization_id || sessionMeta?.organization_id || "").trim() || undefined,
	workspaceId: String(principal?.workspace_id || toolingWorkspaceID || "wksp_default").trim(),
	userId: String(principal?.owner_id || sessionMeta?.owner_id || "system").trim() || "system",
	roles: Array.isArray(principal?.roles) && principal.roles.length ? principal.roles : ["member"]
  }), [principal, sessionMeta?.organization_id, sessionMeta?.owner_id, toolingWorkspaceID]);
  const pluginRuntime = useMemo(() => createStaticPluginRegistry({
    workbenchAPIVersion: "1.0.0",
    designSystemVersion: "1.0.0",
    surface: workbenchSurface(),
    navigationGroups: ["workspace"],
    scope: pluginScope,
    services: {
      permissions: workbenchHostPermissionService,
      dialog: workbenchDialogService,
      notifications: workbenchNotificationService,
      resources: workbenchRelatedResourceService,
      tasks: workbenchTaskService,
      artifacts: workbenchArtifactService,
      http: workbenchScopedHTTPService
    }
  }), [pluginScope]);
  useEffect(() => {
    let active = true;
    setPluginLoadState("loading");
    setPluginNavigation([]);
    setPluginRoutes([]);
    const unsubscribeNavigation = pluginRuntime.navigation.subscribe((items) => {
      if (active) setPluginNavigation(items);
    });
    const unsubscribeRoutes = pluginRuntime.routes.subscribe((items) => {
      if (active) setPluginRoutes(items);
    });
    loadStaticPluginCatalog(pluginRuntime).then((records) => {
      if (!active) return;
      const failed = records.filter((record) => record.status === "failed");
      setPluginLoadState(failed.length ? "partial" : "ready");
      failed.forEach((record) => workbenchNotificationService.show({
        level: "error",
        title: `${record.name} 加载失败`,
        message: record.error,
        dedupeKey: `plugin.load.${record.id}`
      }));
    }).catch((error) => {
      if (!active) return;
      setPluginLoadState("failed");
      workbenchNotificationService.show({
        level: "error",
        title: "扩展加载失败",
        message: error.message || String(error),
        dedupeKey: "plugin.catalog.load"
      });
    });
    return () => {
      active = false;
      unsubscribeNavigation();
      unsubscribeRoutes();
      pluginRuntime.list().forEach((record) => {
        pluginRuntime.unregisterPackage(record.id).catch(() => {});
      });
    };
  }, [pluginRuntime]);
  const activePluginRoute = useMemo(() => (
    pluginRoutes.find((route) => route.path === pluginRoutePath) || null
  ), [pluginRoutePath, pluginRoutes]);
  const toolingCatalog = useMemo(() => buildToolingCatalog({
    config: toolingConfig,
    installedSkills,
    preferredRuntime: settingsDraft.toolRuntime || "cloud_sandbox"
  }), [toolingConfig, installedSkills, settingsDraft.toolRuntime]);
  const selectableToolingItems = useMemo(() => toolingCatalog.items.filter((item) => item.selectable), [toolingCatalog.items]);
  const selectedGuidanceItems = useMemo(() => {
    const byKey = new Map(toolingCatalog.items.map((item) => [item.key, item]));
    return selectedGuidanceKeys.map((key) => byKey.get(key)).filter(Boolean);
  }, [selectedGuidanceKeys, toolingCatalog.items]);
  const settingsSections = useMemo(() => {
    const sections = [
      { key: "environment", title: "环境变量", description: "工具与 Skills 认证配置", keywords: "environment env secret key token credential 环境变量 密钥 认证" },
      { key: "models", title: "模型", description: "Provider、凭证与模型目录", keywords: "model provider llm api 模型 服务商 上下文" },
      { key: "skills", title: "Skills", description: "技能安装、启用与状态", keywords: "skills 技能 install enable disable 停用" },
      { key: "mcp", title: "MCP", description: "MCP 服务与可用性", keywords: "mcp server tool" },
      { key: "agent", title: "Agent", description: "智能体列表与当前配置", keywords: "agent 智能体 model config" },
      { key: "work", title: "Work", description: "任务与会话状态", keywords: "work task session" },
      { key: "inspector", title: "Inspector", description: "调试与观察入口", keywords: "inspector trace debug" }
    ];
    const query = settingsSearch.trim().toLowerCase();
    if (!query) return sections;
    return sections.filter((section) => (
      section.title.toLowerCase().includes(query) ||
      section.description.toLowerCase().includes(query) ||
      section.keywords.toLowerCase().includes(query)
    ));
  }, [settingsSearch]);
  const settingsAgents = useMemo(() => {
    const base = [...availableAgents];
    return base.sort((left, right) => {
      if (left.id === selectedAgent?.id) return -1;
      if (right.id === selectedAgent?.id) return 1;
      return String(left.name || "").localeCompare(String(right.name || ""));
    });
  }, [availableAgents, selectedAgent?.id]);
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
        llmProvider: provider.id,
        capabilityType: model.capability_type || "text",
        isDefaultVision: Boolean(model.is_default_vision)
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

  async function refreshModelOptions() {
    const [providersResponse, modelsResponse] = await Promise.all([api.llmProviders(), api.llmModels()]);
    const enabledProviderIDs = new Set((providersResponse.providers || []).filter((provider) => provider.enabled !== false).map((provider) => provider.id));
    setModelOptions((modelsResponse.models || []).filter((model) => enabledProviderIDs.has(model.provider_id)).map((model) => ({
      label: `${model.provider_id} / ${model.model}`,
      llmModel: model.model,
      llmProvider: model.provider_id,
      capabilityType: model.capability_type || "text",
      isDefaultVision: Boolean(model.is_default_vision)
    })));
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
    const controller = new AbortController();
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
      const streamedStatus = sessionStatusFromEvent(event);
      if (streamedStatus) {
        mergeSessionStatus(currentSessionID, streamedStatus);
      }
      if (sessionSyncEventTypes.has(event.type)) {
        queueSessionSync();
      }
    }

    async function consumeEvents() {
      try {
        for await (const event of api.streamSessionEvents(currentSessionID, {
          afterSeq,
          signal: controller.signal
        })) {
          applyStreamEvent(event);
        }
      } catch (error) {
        if (active && error?.name !== "AbortError") setStatus(error.message);
      }
    }

    consumeEvents();

    return () => {
      active = false;
      controller.abort();
      if (sessionSyncTimerRef.current) {
        window.clearTimeout(sessionSyncTimerRef.current);
        sessionSyncTimerRef.current = null;
      }
    };
  }, [sessionID, sessionMeta?.id]);

  useEffect(() => {
    if (!waitingForReply) return;
    if (effectiveSessionStatus === "interrupting") {
      setStatus("interrupting");
      return;
    }
    if (!liveSignal) {
      if (effectiveSessionStatus === "failed") {
        setStatus("reply failed");
        setWaitingForReply(false);
        return;
      }
      if (effectiveSessionStatus === "terminated") {
        setStatus("archived");
        setWaitingForReply(false);
        return;
      }
      if (effectiveSessionStatus === "idle") {
        if (lastIdleTurnStatus === "failed") {
          setStatus("reply failed");
        } else if (lastIdleTurnStatus === "interrupted") {
          setStatus("interrupted");
        } else {
          setStatus("idle");
        }
        setWaitingForReply(false);
        return;
      }
      setWaitingForReply(false);
      setStatus(effectiveSessionStatus || "ready");
      return;
    }
    if (liveSignal.kind === "thinking") {
      setStatus(effectiveSessionStatus === "interrupting" ? "interrupting" : "thinking");
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
  }, [waitingForReply, liveSignal, effectiveSessionStatus, lastIdleTurnStatus]);

  useEffect(() => {
    loadSessionSettings(sessionID, sessionMeta).catch((error) => setStatus(error.message));
  }, [sessionID, sessionMeta]);

  useEffect(() => {
    if (!toolPickerOpen && !settingsOpen) return;
    let active = true;
    setToolingLoading(true);
    setToolingError("");
    api.skills({ workspaceId: toolingWorkspaceID }).then((response) => {
      if (!active) return;
      setInstalledSkills(response.skills || []);
    }).catch((error) => {
      if (!active) return;
      setInstalledSkills([]);
      setToolingError(error.message);
    }).finally(() => {
      if (active) setToolingLoading(false);
    });
    return () => {
      active = false;
    };
  }, [toolPickerOpen, settingsOpen, toolingWorkspaceID]);

  useEffect(() => {
    if (!settingsOpen) return;
    let active = true;
    api.sessions({ limit: 100, includeArchived: true }).then((response) => {
      if (!active) return;
      const items = (response.sessions || []).filter((session) => Boolean(session.archived_at));
      setArchivedSessions(items);
    }).catch(() => {
      if (!active) return;
      setArchivedSessions([]);
    });
    return () => {
      active = false;
    };
  }, [settingsOpen]);

  useEffect(() => {
    const allowed = new Set(selectableToolingItems.map((item) => item.key));
    setSelectedGuidanceKeys((current) => current.filter((key) => allowed.has(key)));
  }, [selectableToolingItems]);

  useEffect(() => {
    if (!comparisonVariantModel && modelOptions.length) {
      setComparisonVariantModel(`${modelOptions[0].llmProvider}::${modelOptions[0].llmModel}`);
    }
  }, [comparisonVariantModel, modelOptions]);

  useEffect(() => {
    if (!settingsSections.length) return;
    if (!settingsSections.some((section) => section.key === settingsSection)) {
      setSettingsSection(settingsSections[0].key);
    }
  }, [settingsSections, settingsSection]);

  useEffect(() => {
    return () => {
      if (scrollFrameRef.current) {
        window.cancelAnimationFrame(scrollFrameRef.current);
        scrollFrameRef.current = 0;
      }
      workbenchRelatedResourceService.releasePreview();
    };
  }, []);

  useEffect(() => {
    shouldAutoScrollRef.current = true;
  }, [sessionID]);

  useEffect(() => {
    setVisibleTaskCount(10);
  }, [agentID, defaultAgentConfig?.id, taskSearch]);

  useEffect(() => {
    if (!threadRef.current) return;
    if (!sessionID && !chatTimelineEvents.length) {
      window.requestAnimationFrame(() => {
        if (threadRef.current) threadRef.current.scrollTop = 0;
      });
      return;
    }
    if (!shouldAutoScrollRef.current) return;
    window.requestAnimationFrame(() => {
      const node = threadRef.current;
      if (!node) return;
      node.scrollTop = node.scrollHeight;
    });
  }, [chatTimelineEvents.length, hasPendingApprovals, sessionID, streamingReply?.text.length, waitingForReply]);

  useEffect(() => {
    if (hasPendingApprovals && interventions.length > pendingApprovalCountRef.current) {
      setApprovalsOpen(true);
    }
    if (!hasPendingApprovals) {
      setApprovalsOpen(false);
    }
    pendingApprovalCountRef.current = interventions.length;
  }, [hasPendingApprovals, interventions.length]);

  useEffect(() => {
    if (!latestSuccessfulSkillInstallSeq) return;
    const workspaceID = sessionMeta?.workspace_id || defaultAgentConfig?.workspace_id || "";
    api.skills({ workspaceId: workspaceID }).then((response) => {
      setInstalledSkills(response.skills || []);
    }).catch(() => {});
  }, [latestSuccessfulSkillInstallSeq, sessionMeta?.workspace_id, defaultAgentConfig?.workspace_id]);

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

  async function startWorkflow(nextSessionID, template, baseTask) {
    const run = {
      baseTask,
      currentStep: 0,
      sessionID: nextSessionID,
      status: "running",
      templateID: template.id,
      templateTitle: template.title,
      steps: template.workflow_steps.map((step, index) => ({
        ...step,
        sentSeq: 0,
        status: index === 0 ? "running" : "pending"
      }))
    };
    persistWorkflowRun(run);
    setWorkflowRun(run);
    try {
      const sent = await sendTask(nextSessionID, {
        text: workflowStepMessage(template, baseTask, 0)
      });
      const startedRun = {
        ...run,
        steps: run.steps.map((step, index) => index === 0 ? { ...step, sentSeq: Number(sent?.userEvent?.seq || 0) } : step)
      };
      persistWorkflowRun(startedRun);
      setWorkflowRun(startedRun);
    } catch (error) {
      const failedRun = { ...run, status: "failed", steps: run.steps.map((step, index) => index === 0 ? { ...step, status: "failed" } : step) };
      persistWorkflowRun(failedRun);
      setWorkflowRun(failedRun);
      throw error;
    }
  }

  function addComposerFiles(fileList) {
    const incoming = Array.from(fileList || []);
    if (!incoming.length) return;
    setComposerFiles((current) => {
      const known = new Set(current.map((item) => `${item.file.name}:${item.file.size}:${item.file.lastModified}`));
      const next = [...current];
      const rejected = [];
      for (const file of incoming) {
        const fingerprint = `${file.name}:${file.size}:${file.lastModified}`;
        if (known.has(fingerprint)) continue;
        if (next.length >= maxComposerFiles) {
          rejected.push(`最多上传 ${maxComposerFiles} 个文件`);
          break;
        }
        if (file.size > maxComposerFileBytes) {
          rejected.push(`${file.name} 超过 64 MB`);
          continue;
        }
        known.add(fingerprint);
        next.push({ id: composerFileID(file), file, status: "ready", error: "", upload: null });
      }
      if (rejected.length) setStatus(rejected[0]);
      return next;
    });
  }

  function removeComposerFile(id) {
    if (uploadingFiles) return;
    setComposerFiles((current) => current.filter((item) => item.id !== id));
  }

  async function uploadComposerFiles(nextSessionID) {
    if (!composerFiles.length) return [];
    setUploadingFiles(true);
    const completed = [];
    try {
      for (const item of composerFiles) {
        let upload = item.upload;
        if (!upload) {
          setComposerFiles((current) => current.map((entry) => entry.id === item.id ? { ...entry, status: "uploading", error: "" } : entry));
          try {
            upload = await api.uploadSessionArtifact(nextSessionID, item.file);
          } catch (error) {
            setComposerFiles((current) => current.map((entry) => entry.id === item.id ? { ...entry, status: "error", error: error.message } : entry));
            throw new Error(`${item.file.name} 上传失败：${error.message}`);
          }
          setComposerFiles((current) => current.map((entry) => entry.id === item.id ? { ...entry, status: "uploaded", error: "", upload } : entry));
          if (upload.artifact) {
            setArtifactResponse((current) => ({
              ...current,
              artifacts: [...(current.artifacts || []).filter((artifact) => artifact.id !== upload.artifact.id), upload.artifact]
            }));
          }
        }
        completed.push({
          artifact_id: upload.artifact?.id || "",
          object_ref_id: upload.object_ref?.id || "",
          name: upload.artifact?.name || item.file.name,
          content_type: upload.object_ref?.content_type || item.file.type || "application/octet-stream",
          size_bytes: upload.object_ref?.size_bytes || item.file.size,
          workspace_path: upload.workspace_path || ""
        });
      }
      return completed;
    } finally {
      setUploadingFiles(false);
    }
  }

  function handleComposerDrop(event) {
    event.preventDefault();
    setComposerDragActive(false);
    addComposerFiles(event.dataTransfer?.files);
  }

  function handleComposerPaste(event) {
    const clipboard = event.clipboardData;
    if (!clipboard) return;
    const itemImages = Array.from(clipboard.items || [])
      .filter((item) => item.kind === "file" && String(item.type || "").toLowerCase().startsWith("image/"))
      .map((item) => item.getAsFile())
      .filter(Boolean);
    const images = itemImages.length
      ? itemImages
      : Array.from(clipboard.files || []).filter((file) => String(file.type || "").toLowerCase().startsWith("image/"));
    if (!images.length) return;
    event.preventDefault();
    addComposerFiles(images.map((file, index) => clipboardImageFile(file, index)));
    setStatus(images.length === 1 ? "已从剪贴板添加图片" : `已从剪贴板添加 ${images.length} 张图片`);
  }

  function composerVisionError() {
    const imageFiles = composerFiles.filter((item) => String(item.file.type || "").startsWith("image/"));
    if (!imageFiles.length) return "";
    const unsupported = imageFiles.find((item) => !supportedVisionImageTypes.has(String(item.file.type || "").toLowerCase()));
    if (unsupported) return `${unsupported.file.name} 不是支持的视觉图片格式，请使用 PNG、JPEG、GIF 或 WebP。`;
    const current = modelOptions.find((option) => option.llmProvider === settingsDraft.llmProvider && option.llmModel === settingsDraft.llmModel);
    if (current?.capabilityType === "text_image") return "";
    if (modelOptions.some((option) => option.isDefaultVision && option.capabilityType === "text_image")) return "";
    return "当前模型不支持图片解析，且尚未配置统一图片视觉模型。请前往设置 > 模型完成配置。";
  }

  async function startSession() {
    const visionError = composerVisionError();
    if (visionError) {
      setStatus(visionError);
      return;
    }
    setStatus("creating session");
    const agent = agentID.trim() ? { id: agentID.trim() } : (defaultAgentConfig || await api.defaultAgent());
    const environment = environmentID.trim() ? { id: environmentID.trim() } : await api.createEnvironment({
      name: "Workbench Environment",
      config: { type: "cloud" }
    });
    const session = await api.createSession({
      agent_id: agent.id,
      environment_id: environment.id,
      title: task.trim() ? task.trim().slice(0, 80) : (composerFiles[0]?.file.name || "New workbench task")
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
    if (task.trim() || composerFiles.length) {
      if (workflowMode && selectedTaskTemplate?.workflow_steps?.length) {
        await startWorkflow(session.id, selectedTaskTemplate, task.trim());
      } else {
        await sendTask(session.id);
      }
    } else {
      await refresh(session.id);
    }
  }

  async function sendTask(nextSessionID = sessionID, options = {}) {
    const value = String(nextSessionID || "").trim();
    const text = String(options.text ?? task).trim();
    const guidanceItems = options.guidanceItems || selectedGuidanceItems;
    const attachmentItems = options.attachments?.length ? options.attachments : composerFiles;
    const guidedText = buildGuidedTaskMessage(defaultComposerTask(text, attachmentItems), guidanceItems);
    if (!value) {
      setStatus("session required");
      return;
    }
    const visionError = composerVisionError();
    if (visionError && !options.attachments?.length) {
      setStatus(visionError);
      return;
    }
    if (!text && !composerFiles.length && !options.attachments?.length) {
      setStatus("task required");
      return;
    }
    const queued = Boolean(waitingForReply || effectiveSessionStatus === "running" || effectiveSessionStatus === "interrupting");
    setStatus(composerFiles.length ? "uploading files" : (queued ? "queued message" : "sending task"));
    const attachments = options.attachments || await uploadComposerFiles(value);
    setStatus(queued ? "queued message" : "sending task");
    const response = await api.sendSessionMessage(value, guidedText, {
      preferLatest: false,
      attachments,
      queued,
      queueIfBusy: true
    });
    const appendedEvents = response.events || [];
    const userEvent = appendedEvents.find((event) => event.type === "user.message");
    setEventsResponse((current) => ({
      ...current,
      events: mergeEvents(current.events, appendedEvents)
    }));
    const appendedStatus = latestSessionStatus(appendedEvents);
    if (appendedStatus) mergeSessionStatus(value, appendedStatus);
    eventStreamCursorRef.current = Math.max(eventStreamCursorRef.current || 0, maxSeq(appendedEvents));
    if (options.clearTask !== false) {
      setTask("");
      setComposerFiles([]);
    }
    setWaitingForReply(true);
    setStatus("waiting for reply");
    if (userEvent?.seq) {
      eventStreamCursorRef.current = Math.max(eventStreamCursorRef.current || 0, Number(userEvent.seq || 0));
    }
    return { response, userEvent };
  }

  function applyTaskTemplate(template, asWorkflow) {
    const toolNames = new Set(template.tools || []);
    const skillNames = new Set(template.skills || []);
    const matchingKeys = toolingCatalog.items.filter((item) => {
      if (!item.selectable) return false;
      if (item.kind === "tool_namespace") return toolNames.has(item.name);
      if (item.kind === "skill") return skillNames.has(item.name);
      return false;
    }).map((item) => item.key);
    setTask(template.prompt || "");
    setSelectedGuidanceKeys(matchingKeys);
    setSelectedTaskTemplateID(template.id);
    setWorkflowMode(Boolean(asWorkflow));
    setTemplatePickerOpen(false);
    setStatus(asWorkflow ? "工作流已就绪" : "模板已填充");
  }

  function clearTaskTemplate() {
    setSelectedTaskTemplateID("");
    setWorkflowMode(false);
  }

  useEffect(() => {
    if (!workflowRun || workflowRun.status !== "running" || workflowAdvancingRef.current) return;
    const currentStep = workflowRun.steps[workflowRun.currentStep];
    const sentSeq = Number(currentStep?.sentSeq || 0);
    if (!currentStep || currentStep.status !== "running" || sentSeq <= 0) return;
    if (waitingForReply || hasPendingApprovals || effectiveSessionStatus !== "idle") return;
    const laterEvents = events.filter((event) => Number(event.seq || 0) > sentSeq);
    const failed = lastIdleTurnStatus === "failed" || laterEvents.some((event) => event.type === "runtime.failed");
    const replied = laterEvents.some((event) => event.type === "agent.message" && hasVisibleAgentText(event));
    if (!failed && !replied) return;

    workflowAdvancingRef.current = true;
    const advance = async () => {
      if (failed) {
        const failedRun = {
          ...workflowRun,
          status: "failed",
          steps: workflowRun.steps.map((step, index) => index === workflowRun.currentStep ? { ...step, status: "failed" } : step)
        };
        persistWorkflowRun(failedRun);
        setWorkflowRun(failedRun);
        setStatus("工作流步骤失败");
        return;
      }

      const nextIndex = workflowRun.currentStep + 1;
      if (nextIndex >= workflowRun.steps.length) {
        const completedRun = {
          ...workflowRun,
          status: "completed",
          steps: workflowRun.steps.map((step, index) => index === workflowRun.currentStep ? { ...step, status: "completed" } : step)
        };
        persistWorkflowRun(completedRun);
        setWorkflowRun(completedRun);
        setStatus("工作流已完成");
        return;
      }

      const nextRun = {
        ...workflowRun,
        currentStep: nextIndex,
        steps: workflowRun.steps.map((step, index) => {
          if (index === workflowRun.currentStep) return { ...step, status: "completed" };
          if (index === nextIndex) return { ...step, sentSeq: 0, status: "running" };
          return step;
        })
      };
      persistWorkflowRun(nextRun);
      setWorkflowRun(nextRun);
      const template = taskTemplates.find((item) => item.id === workflowRun.templateID) || {
        title: workflowRun.templateTitle,
        workflow_steps: workflowRun.steps
      };
      try {
        const sent = await sendTask(workflowRun.sessionID, {
          text: workflowStepMessage(template, workflowRun.baseTask, nextIndex)
        });
        const sentRun = {
          ...nextRun,
          steps: nextRun.steps.map((step, index) => index === nextIndex ? { ...step, sentSeq: Number(sent?.userEvent?.seq || 0) } : step)
        };
        persistWorkflowRun(sentRun);
        setWorkflowRun(sentRun);
      } catch (error) {
        const failedRun = {
          ...nextRun,
          status: "failed",
          steps: nextRun.steps.map((step, index) => index === nextIndex ? { ...step, status: "failed" } : step)
        };
        persistWorkflowRun(failedRun);
        setWorkflowRun(failedRun);
        setStatus(error.message);
      }
    };
    advance().finally(() => {
      workflowAdvancingRef.current = false;
    });
  }, [effectiveSessionStatus, events, hasPendingApprovals, lastIdleTurnStatus, taskTemplates, waitingForReply, workflowRun]);

  async function interruptTask(nextSessionID = sessionID) {
    const value = String(nextSessionID || "").trim();
    if (!value) {
      setStatus("session required");
      return;
    }
    setStatus("interrupting task");
    setSessionMeta((current) => (current ? { ...current, status: "interrupting" } : current));
    try {
      const response = await api.interruptSession(value);
      const appendedEvents = response.events || [];
      setEventsResponse((current) => ({
        ...current,
        events: mergeEvents(current.events, appendedEvents)
      }));
      eventStreamCursorRef.current = Math.max(eventStreamCursorRef.current || 0, maxSeq(appendedEvents));
      setWaitingForReply(false);
      await syncSession(value);
      setStatus("interrupt requested");
      workbenchNotificationService.show({
        level: "success",
        title: "已请求中断",
        message: sessionMeta?.title || value,
        dedupeKey: `session.interrupt.${value}`
      });
    } catch (error) {
      workbenchNotificationService.show({
        level: "error",
        title: "中断任务失败",
        message: error.message || String(error),
        dedupeKey: `session.interrupt.${value}`
      });
      throw error;
    }
  }

  async function confirmInterruptTask() {
    const confirmed = await workbenchDialogService.confirm({
      title: "中断当前任务？",
      description: "智能体将停止本轮执行，已经生成的消息和文件会保留。",
      detail: sessionMeta?.title || sessionID || "当前任务",
      confirmLabel: "中断任务",
      cancelLabel: "继续运行",
      tone: "warning"
    });
    if (!confirmed) return false;
    await interruptTask();
    return true;
  }

  function deferApprovals() {
    setApprovalsOpen(false);
    workbenchNotificationService.show({
      level: "warning",
      title: "审批已暂时收起",
      message: "任务仍在等待审批，可从顶部提示重新打开或终止任务。",
      dedupeKey: `approval.deferred.${sessionID}`
    });
  }

  async function interruptFromApprovals() {
    const interrupted = await confirmInterruptTask();
    if (interrupted) setApprovalsOpen(false);
  }

  function toggleGuidanceKey(key) {
    setSelectedGuidanceKeys((current) => (
      current.includes(key)
        ? current.filter((item) => item !== key)
        : [...current, key]
    ));
  }

  async function archiveTask(targetSessionID) {
    const nextSessionID = String(targetSessionID || "").trim();
    if (!nextSessionID) return;
    setSessionAction(`archive:${nextSessionID}`);
    setStatus("archiving");
    try {
      const archived = await api.archiveSession(nextSessionID);
      setRecentSessions((current) => current.filter((item) => item.id !== nextSessionID));
      setArchivedSessions((current) => [archived, ...current.filter((item) => item.id !== nextSessionID)]);
      if (sessionID === nextSessionID) {
        setSessionMeta(archived);
        setWaitingForReply(false);
      }
      setStatus("archived");
    } finally {
      setSessionAction("");
    }
  }

  async function restoreTask(session) {
    const nextSessionID = String(session?.id || "").trim();
    if (!nextSessionID) return;
    setSessionAction(`restore:${nextSessionID}`);
    setStatus("restoring");
    try {
      const restored = await api.restoreSession(nextSessionID);
      setArchivedSessions((current) => current.filter((item) => item.id !== nextSessionID));
      setRecentSessions((current) => [restored, ...current.filter((item) => item.id !== nextSessionID)]);
      if (sessionID === nextSessionID) setSessionMeta(restored);
      setStatus("restored");
    } finally {
      setSessionAction("");
    }
  }

  function mergeSessionMetadata(updated) {
    setRecentSessions((current) => current.map((item) => item.id === updated.id ? { ...item, ...updated } : item));
    setArchivedSessions((current) => current.map((item) => item.id === updated.id ? { ...item, ...updated } : item));
    if (sessionID === updated.id) setSessionMeta((current) => ({ ...(current || {}), ...updated }));
    setMetadataSession((current) => current?.id === updated.id ? { ...current, ...updated } : current);
  }

  function mergeSessionStatus(targetSessionID, nextStatus) {
    if (!targetSessionID || !nextStatus) return;
    setSessionMeta((current) => current?.id === targetSessionID ? { ...current, status: nextStatus } : current);
    setRecentSessions((current) => current.map((session) => (
      session.id === targetSessionID ? { ...session, status: nextStatus } : session
    )));
  }

  async function toggleTaskPin(session) {
    if (!session?.id) return;
    const previousPinnedAt = session.pinned_at || null;
    const shouldPin = !previousPinnedAt;
    setSessionAction(`pin:${session.id}`);
    mergeSessionMetadata({
      ...session,
      pinned_at: shouldPin ? new Date().toISOString() : null
    });
    try {
      const updated = await api.updateSessionMetadata(session.id, { pinned: shouldPin });
      mergeSessionMetadata({
        ...updated,
        pinned_at: shouldPin ? (updated.pinned_at || new Date().toISOString()) : null
      });
      setStatus(shouldPin ? "任务已置顶" : "已取消置顶");
    } catch (error) {
      mergeSessionMetadata({ ...session, pinned_at: previousPinnedAt });
      throw error;
    } finally {
      setSessionAction("");
    }
  }

  function openTaskMetadata(session) {
    setMetadataSession(session);
    setMetadataTagsDraft((session.tags || []).join(", "));
    setTaskMenuSessionID("");
  }

  async function saveTaskMetadata() {
    if (!metadataSession?.id) return;
    const tags = metadataTagsDraft.split(/[,，\n]/).map((item) => item.trim()).filter(Boolean);
    setSavingMetadata(true);
    try {
      const updated = await api.updateSessionMetadata(metadataSession.id, { tags });
      mergeSessionMetadata(updated);
      setMetadataTagsDraft((updated.tags || []).join(", "));
      setStatus("任务标签已保存");
    } finally {
      setSavingMetadata(false);
    }
  }

  async function rerunTask(sourceSession, overrides = {}) {
    const sourceSessionID = String(sourceSession?.id || "").trim();
    if (!sourceSessionID) return null;
    setSessionAction(`rerun:${sourceSessionID}`);
    setStatus("正在重跑任务");
    try {
      const response = await api.rerunSession(sourceSessionID, overrides);
      const rerun = response.session;
      setRecentSessions((current) => [rerun, ...current.filter((item) => item.id !== rerun.id)]);
      await openSession(rerun);
      setStatus("任务已按原参数重跑");
      return response;
    } finally {
      setSessionAction("");
    }
  }

  async function loadTaskComparison(leftID = comparisonLeftID, rightID = comparisonRightID) {
    if (!leftID || !rightID || leftID === rightID) return;
    setComparisonLoading(true);
    try {
      const result = await api.compareSessions(leftID, rightID);
      setComparisonResult(result);
    } finally {
      setComparisonLoading(false);
    }
  }

  function openTaskComparison(sourceSession) {
    const sourceID = String(sourceSession?.id || "").trim();
    const baseTitle = String(sourceSession?.title || "").replace(/ \(rerun\)$/i, "");
    const candidates = recentSessions.filter((item) => item.id !== sourceID && item.status !== "terminated");
    const related = candidates.find((item) => String(item.title || "").replace(/ \(rerun\)$/i, "") === baseTitle);
    const rightID = related?.id || candidates[0]?.id || "";
    setComparisonLeftID(sourceID);
    setComparisonRightID(rightID);
    setComparisonResult(null);
    setComparisonOpen(true);
    if (sourceID && rightID) {
      loadTaskComparison(sourceID, rightID).catch((error) => setStatus(error.message));
    }
  }

  async function createComparisonVariant() {
    const source = recentSessions.find((item) => item.id === comparisonLeftID);
    const [llmProvider, llmModel] = comparisonVariantModel.split("::");
    if (!source || !llmProvider || !llmModel) return;
    const response = await rerunTask(source, { llm_provider: llmProvider, llm_model: llmModel });
    if (!response?.session) return;
    setComparisonRightID(response.session.id);
    setComparisonResult(null);
    setComparisonOpen(true);
    loadTaskComparison(source.id, response.session.id).catch((error) => setStatus(error.message));
  }

  async function deleteTask(targetSessionID) {
    const nextSessionID = String(targetSessionID || "").trim();
    if (!nextSessionID) return;
    const targetSession = recentSessions.find((item) => item.id === nextSessionID);
    const confirmed = await workbenchDialogService.confirm({
      title: "删除任务？",
      description: "该任务、事件记录和关联状态将被永久删除，此操作无法撤销。",
      detail: targetSession?.title || nextSessionID,
      confirmLabel: "删除任务",
      cancelLabel: "取消",
      tone: "danger"
    });
    if (!confirmed) {
      window.setTimeout(() => taskMenuButtonRefs.current.get(nextSessionID)?.focus(), 0);
      return;
    }
    setSessionAction(`delete:${nextSessionID}`);
    setStatus("deleting");
    try {
      await api.deleteSession(nextSessionID);
      try {
        window.localStorage.removeItem(workflowStorageKey(nextSessionID));
      } catch {}
      setRecentSessions((current) => current.filter((item) => item.id !== nextSessionID));
      if (sessionID === nextSessionID) {
        startNewTask();
      }
      setStatus("deleted");
      workbenchNotificationService.show({
        level: "success",
        title: "任务已删除",
        message: targetSession?.title || nextSessionID,
        dedupeKey: `session.delete.${nextSessionID}`
      });
    } catch (error) {
      workbenchNotificationService.show({
        level: "error",
        title: "删除任务失败",
        message: error.message || String(error),
        dedupeKey: `session.delete.${nextSessionID}`
      });
      throw error;
    } finally {
      setSessionAction("");
    }
  }

  function clearArtifactPreview() {
    workbenchRelatedResourceService.releasePreview();
    setArtifactPreview(null);
    setArtifactPreviewMode("preview");
  }

  function startArtifactResize(event) {
    if (!artifactPreview) return;
    event.preventDefault();
    artifactResizeRef.current = {
      pointerX: event.clientX,
      width: artifactPreviewWidth
    };
    document.body.classList.add("resizing-artifact-preview");
  }

  useEffect(() => {
    function handlePointerMove(event) {
      const resize = artifactResizeRef.current;
      if (!resize) return;
      const nextWidth = resize.width + resize.pointerX - event.clientX;
      setArtifactPreviewWidth(Math.min(760, Math.max(320, nextWidth)));
    }

    function handlePointerUp() {
      if (!artifactResizeRef.current) return;
      artifactResizeRef.current = null;
      document.body.classList.remove("resizing-artifact-preview");
    }

    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp);
    return () => {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
      document.body.classList.remove("resizing-artifact-preview");
    };
  }, []);

  async function previewArtifact(artifact) {
    if (!sessionID || !artifact?.id) return;
    const resource = artifactToResourceRef(artifact, { sessionID });
    setArtifactPreviewMode("preview");
    setArtifactPreview({ resource, status: "loading" });
    try {
      const preview = await workbenchRelatedResourceService.preview(resource);
      setArtifactPreview({ resource, status: "ready", ...preview });
    } catch (error) {
      if (isPreviewCancelledError(error)) return;
      setArtifactPreview({ resource, status: "error", error: error.message });
    }
  }

  async function downloadArtifactResource(resource) {
    try {
      await workbenchRelatedResourceService.open(resource, { target: "download" });
    } catch (error) {
      setStatus(error.message);
      workbenchNotificationService.show({
        level: "error",
        title: "打开文件失败",
        message: error.message || String(error),
        dedupeKey: `resource.open.${resource.source}.${resource.id}`
      });
    }
  }

  async function approve(intervention) {
    const decisionKey = `${sessionID}:${intervention.turn_id}:${intervention.call_id}`;
    if (approvalDecisionRef.current) return;
    approvalDecisionRef.current = decisionKey;
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
      await syncSession(sessionID);
      setWaitingForReply(true);
      setStatus("waiting for reply");
      workbenchNotificationService.show({
        level: "success",
        title: "审批已通过",
        message: `${intervention.api_name} · ${intervention.call_id}`,
        dedupeKey: `approval.approved.${decisionKey}`
      });
    } catch (error) {
      setWaitingForReply(false);
      workbenchNotificationService.show({
        level: "error",
        title: "审批失败",
        message: error.message || String(error),
        dedupeKey: `approval.failed.${decisionKey}`
      });
      throw error;
    } finally {
      if (approvalDecisionRef.current === decisionKey) approvalDecisionRef.current = "";
      setDecidingApprovalID("");
    }
  }

  async function requestInstalledSkillEnable(skill) {
    const identifier = String(skill?.identifier || "").trim();
    const version = Number(skill?.version || 0);
    if (!sessionID || !identifier || version <= 0) return;
    const key = `${identifier}:${version}`;
    setRequestingSkillEnable(key);
    setStatus("requesting skill enable");
    try {
      await sendTask(sessionID, {
        text: `请调用 skills.enable，将已安装 Skill ${JSON.stringify(identifier)} 的 version ${version} 启用到当前 Agent。该操作需要独立审批；完成后明确说明 requires_session_upgrade，以及当前 Session 是否仍固定在旧 Agent 配置。`,
        attachments: [],
        clearTask: false,
        guidanceItems: []
      });
    } catch (error) {
      setStatus(error.message);
    } finally {
      setRequestingSkillEnable("");
    }
  }

  async function requestInstalledSkillDisable(skill) {
    const identifier = String(skill?.identifier || "").trim();
    if (!sessionID || !identifier) return;
    setRequestingSkillDisable(identifier);
    setStatus("requesting skill disable");
    try {
      await sendTask(sessionID, {
        text: `请调用 skills.disable，将 Skill ${JSON.stringify(identifier)} 从当前 Agent 的最新配置中停用。只移除这个 binding，不要归档或卸载 Skill。该操作需要独立审批；完成后明确说明 requires_session_upgrade，以及当前 Session 是否仍固定在旧 Agent 配置。`,
        attachments: [],
        clearTask: false,
        guidanceItems: []
      });
    } catch (error) {
      setStatus(error.message);
    } finally {
      setRequestingSkillDisable("");
    }
  }

  async function applySkillConfigToSession(action) {
    const targetVersion = Number(action?.targetConfigVersion || 0);
    if (!sessionID || targetVersion <= 0) return;
    if (effectiveSessionStatus !== "idle") {
      const error = new Error("当前任务结束后才能应用 Agent 配置");
      setStatus(error.message);
      if (action?.throwOnError) throw error;
      return null;
    }
    setApplyingSessionConfigVersion(targetVersion);
    setStatus(`正在应用 Agent 配置 v${targetVersion}`);
    try {
      const response = await api.upgradeSessionConfig(sessionID, {
        to_version: targetVersion,
        updated_by: "workbench"
      });
      const nextSession = response.session || await api.session(sessionID);
      setSessionMeta(nextSession);
      setRecentSessions((current) => [nextSession, ...current.filter((item) => item.id !== nextSession.id)]);
      if (response.event?.type) {
        setEventsResponse((current) => ({
          ...current,
          events: mergeEvents(current.events, [response.event])
        }));
        eventStreamCursorRef.current = Math.max(eventStreamCursorRef.current || 0, Number(response.event.seq || 0));
      }
      await loadSessionSettings(sessionID, nextSession);
      const operationLabel = action?.operation === "disable"
        ? "Skill 已在当前会话停用"
        : (action?.operation === "sync" ? "Agent 配置已应用到当前会话" : "Skill 已在当前会话生效");
      setStatus(response.changed ? `${operationLabel} · config v${targetVersion}` : `当前会话已使用 config v${targetVersion}`);
      return response;
    } catch (error) {
      setStatus(error.message);
      if (action?.throwOnError) throw error;
      return null;
    } finally {
      setApplyingSessionConfigVersion(0);
    }
  }

  async function reject(intervention) {
    const decisionKey = `${sessionID}:${intervention.turn_id}:${intervention.call_id}`;
    if (approvalDecisionRef.current) return;
    const reason = window.prompt("拒绝原因", "在页面中拒绝");
    if (reason === null) return;
    approvalDecisionRef.current = decisionKey;
    setDecidingApprovalID(intervention.call_id);
    setStatus("rejecting");
    try {
      await api.rejectIntervention(sessionID, intervention.turn_id, intervention.call_id, { reason });
      const synced = await syncSession(sessionID);
      const resumesTurn = synced?.session?.status === "running";
      setWaitingForReply(resumesTurn);
      setStatus(resumesTurn ? "waiting for reply" : "tool call rejected");
      workbenchNotificationService.show({
        level: "success",
        title: "已拒绝工具调用",
        message: `${intervention.api_name} · ${intervention.call_id}`,
        dedupeKey: `approval.rejected.${decisionKey}`
      });
    } catch (error) {
      workbenchNotificationService.show({
        level: "error",
        title: "拒绝审批失败",
        message: error.message || String(error),
        dedupeKey: `approval.reject-failed.${decisionKey}`
      });
      throw error;
    } finally {
      if (approvalDecisionRef.current === decisionKey) approvalDecisionRef.current = "";
      setDecidingApprovalID("");
    }
  }

  async function openSession(session) {
    setStatus("loading chat");
    clearArtifactPreview();
    setComposerFiles([]);
    setToolPickerOpen(false);
    setSessionID(session.id);
    setAgentID(session.agent_id || "");
    setEnvironmentID(session.environment_id || "");
    rememberSession(session.id);
    await loadSession(session.id);
    await loadSessionSettings(session.id, session);
    setStatus("history restored");
  }

  function startNewTask() {
    clearArtifactPreview();
    setComposerFiles([]);
    setComposerDragActive(false);
    setToolPickerOpen(false);
    setAgentID((current) => current || defaultAgentConfig?.id || "");
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
    setTemplatePickerOpen(false);
    setSelectedTaskTemplateID("");
    setWorkflowMode(false);
    setWorkflowRun(null);
    setTaskHoverPreview(null);
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

  function showTaskHoverPreview(session, event) {
    const rect = event.currentTarget.getBoundingClientRect();
    const previewWidth = 280;
    const gap = 16;
    const maxLeft = window.innerWidth - previewWidth - 16;
    const left = Math.min(rect.right + gap, maxLeft);
    setTaskHoverPreview({
      left,
      time: formatTaskTime(session.created_at),
      title: session.title || "未命名任务",
      top: rect.top + rect.height / 2
    });
  }

  function hideTaskHoverPreview() {
    setTaskHoverPreview(null);
  }

  function resetSessionViewForAgent(agent) {
    clearArtifactPreview();
    setComposerFiles([]);
    setToolPickerOpen(false);
    setSessionID("");
    setSessionMeta(null);
    setEnvironmentID("");
    setEventsResponse({ events: [] });
    setInterventionResponse({ interventions: [] });
    setArtifactResponse({ artifacts: [] });
    setWaitingForReply(false);
    setApprovalsOpen(false);
    setTaskHoverPreview(null);
    setStatus("ready");
    eventStreamCursorRef.current = 0;
    forgetSession();
    if (agent?.config_version) {
      setSettingsDraft((current) => ({
        ...current,
        llmProvider: agent.config_version.llm_provider || current.llmProvider || "",
        llmModel: agent.config_version.llm_model || current.llmModel || ""
      }));
    }
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

  const sessionStatus = String(effectiveSessionStatus || sessionMeta?.status || "");
  const isSessionInterrupting = sessionStatus === "interrupting";
  const isSessionBusy = Boolean(
    sessionID && (
      (waitingForReply && sessionStatus !== "idle" && sessionStatus !== "failed" && sessionStatus !== "terminated") ||
      hasPendingApprovals ||
      liveSignal?.kind === "thinking" ||
      sessionStatus === "running" ||
      sessionStatus === "interrupting"
    )
  );
  const hasComposerContent = Boolean(task.trim() || composerFiles.length);
  const primaryAction = !sessionID
    ? {
        className: "composer-primary-button",
        disabled: uploadingFiles || !hasComposerContent,
        label: "开始",
        mode: "start"
      }
    : isSessionInterrupting
      ? {
          className: "composer-primary-button interrupting",
          disabled: true,
          label: "中断中...",
          mode: "interrupting"
        }
      : isSessionBusy
        ? {
            className: "composer-primary-button interrupt",
            disabled: false,
            label: "运行中",
            mode: "interrupt"
          }
        : {
            className: "composer-primary-button",
            disabled: uploadingFiles || !hasComposerContent,
            label: "发送",
            mode: "send"
          };
  async function stopWorkflowRun() {
    if (!workflowRun || workflowRun.status !== "running") return;
    const canceledRun = {
      ...workflowRun,
      status: "canceled",
      steps: workflowRun.steps.map((step, index) => index === workflowRun.currentStep && step.status === "running" ? { ...step, status: "canceled" } : step)
    };
    persistWorkflowRun(canceledRun);
    setWorkflowRun(canceledRun);
    setStatus("工作流已停止");
    if (isSessionBusy) await interruptTask(workflowRun.sessionID);
  }
  const hasTaskSearch = Boolean(taskSearch.trim());
  function navigatePluginRoute(path) {
    clearArtifactPreview();
    setSettingsOpen(false);
    window.location.hash = encodeURI(path);
  }

  function closePluginRoute() {
    if (!pluginRoutePath && !window.location.hash) return;
    window.history.pushState(null, "", `${window.location.pathname}${window.location.search}`);
    setPluginRoutePath("");
  }

  function openSettingsPage() {
    closePluginRoute();
    setSettingsOpen(true);
  }

  function closeSettingsPage() {
    setSettingsOpen(false);
  }

  function openInspectorPage() {
    window.open("/inspector", "_blank", "noopener,noreferrer");
  }

  function openArchivedSessionFromSettings(session) {
    closeSettingsPage();
    openSession(session).catch((error) => setStatus(error.message));
  }

  function handleTaskComposerKeyDown(event) {
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent?.isComposing) {
      return;
    }
    if ((primaryAction.mode !== "start" && primaryAction.mode !== "send") || primaryAction.disabled) {
      return;
    }
    event.preventDefault();
    if (primaryAction.mode === "start") {
      startSession().catch((error) => setStatus(error.message));
      return;
    }
    if (workflowMode && selectedTaskTemplate?.workflow_steps?.length && task.trim()) {
      startWorkflow(sessionID, selectedTaskTemplate, task.trim()).catch((error) => setStatus(error.message));
      return;
    }
    sendTask().catch((error) => setStatus(error.message));
  }

  function selectAgentFromSettings(nextAgentID) {
    const agent = availableAgents.find((item) => item.id === nextAgentID);
    if (!agent) return;
    setAgentID(nextAgentID);
    if (sessionMeta?.agent_id && sessionMeta.agent_id !== nextAgentID) {
      resetSessionViewForAgent(agent);
      return;
    }
    if (agent.config_version) {
      setSettingsDraft((current) => ({
        ...current,
        llmProvider: agent.config_version.llm_provider || current.llmProvider || "",
        llmModel: agent.config_version.llm_model || current.llmModel || ""
      }));
    }
  }

  async function createAgentFromSettings(body) {
    setStatus("正在创建 Agent");
    const created = await api.createAgent(body);
    setAvailableAgents((current) => {
      const nextAgents = [created, ...current.filter((item) => item.id !== created.id)];
      return nextAgents.sort((left, right) => {
        if (left.id === (defaultAgentConfig?.id || "agt_general")) return -1;
        if (right.id === (defaultAgentConfig?.id || "agt_general")) return 1;
        return String(left.name || "").localeCompare(String(right.name || ""));
      });
    });
    setAgentID(created.id);
    if (sessionMeta?.agent_id && sessionMeta.agent_id !== created.id) {
      resetSessionViewForAgent(created);
    } else if (created.config_version) {
      setSettingsDraft((current) => ({
        ...current,
        llmProvider: created.config_version.llm_provider || current.llmProvider || "",
        llmModel: created.config_version.llm_model || current.llmModel || ""
      }));
    }
    setStatus("Agent 已创建");
    return created;
  }

  async function saveAgentFromSettings(body) {
    if (!selectedAgent?.id) return;
    setSavingAgentConfig(true);
    setStatus("正在保存 Agent 配置");
    try {
      const updated = await api.updateAgent(selectedAgent.id, body);
      setAvailableAgents((current) => current.map((item) => item.id === updated.id ? updated : item));
      setDefaultAgentConfig((current) => current?.id === updated.id ? updated : current);
      if (!sessionID) {
        setSettingsDraft((current) => ({
          ...current,
          llmProvider: updated.config_version?.llm_provider || current.llmProvider,
          llmModel: updated.config_version?.llm_model || current.llmModel
        }));
      }
      setStatus("Agent 配置已保存");
    } finally {
      setSavingAgentConfig(false);
    }
  }

  async function rollbackAgentFromSettings(sourceVersion) {
    if (!selectedAgent?.id) return;
    setRollingBackAgentVersion(sourceVersion);
    setStatus(`正在回滚 Agent 到版本 #${sourceVersion}`);
    try {
      const result = await api.rollbackAgentConfigVersion(selectedAgent.id, sourceVersion);
      const updated = result.agent;
      setAvailableAgents((current) => current.map((item) => item.id === updated.id ? updated : item));
      setDefaultAgentConfig((current) => current?.id === updated.id ? updated : current);
      if (!sessionID) {
        setSettingsDraft((current) => ({
          ...current,
          llmProvider: updated.config_version?.llm_provider || current.llmProvider,
          llmModel: updated.config_version?.llm_model || current.llmModel
        }));
      }
      setStatus(`Agent 已回滚：版本 #${sourceVersion} 已复制为 #${result.new_version}`);
      return result;
    } finally {
      setRollingBackAgentVersion(0);
    }
  }

  async function exportAgentFromSettings() {
    if (!selectedAgent?.id) return;
    setStatus("正在导出 Agent 配置");
    const response = await api.exportAgent(selectedAgent.id);
    const blob = await response.blob();
    const disposition = response.headers.get("Content-Disposition") || "";
    const fileName = disposition.match(/filename="([^"]+)"/)?.[1] || `agent-${selectedAgent.id}.json`;
    const url = window.URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.URL.revokeObjectURL(url);
    setStatus("Agent 配置已导出");
  }

  async function importAgentFromSettings(file) {
    setStatus("正在导入 Agent 配置");
    let importedDocument;
    try {
      importedDocument = JSON.parse(await file.text());
    } catch {
      throw new Error("导入文件不是有效的 JSON");
    }
    const imported = await api.importAgent(importedDocument);
    setAvailableAgents((current) => [...current.filter((item) => item.id !== imported.id), imported]);
    setAgentID(imported.id);
    clearArtifactPreview();
    setSessionID("");
    setSessionMeta(null);
    setEnvironmentID("");
    setEventsResponse({ events: [] });
    setInterventionResponse({ interventions: [] });
    setArtifactResponse({ artifacts: [] });
    eventStreamCursorRef.current = 0;
    forgetSession();
    setSettingsDraft((current) => ({
      ...current,
      llmProvider: imported.config_version?.llm_provider || current.llmProvider,
      llmModel: imported.config_version?.llm_model || current.llmModel
    }));
    setStatus("Agent 配置已导入");
    return imported;
  }

  async function updateAgentPermissionsFromSettings(targetAgentID, tools) {
    setStatus("正在更新 Agent 工具权限");
    const updated = await api.updateAgent(targetAgentID, { tools });
    setAvailableAgents((current) => current.map((item) => item.id === updated.id ? updated : item));
    setDefaultAgentConfig((current) => current?.id === updated.id ? updated : current);
    setStatus(`${updated.name || updated.id} 的工具权限已更新`);
    return updated;
  }

  if (settingsOpen) {
    return (
      <SettingsPage
        activeSection={settingsSection}
        agents={settingsAgents}
        archivedSessions={archivedSessions}
        currentSession={sessionMeta}
        onClose={closeSettingsPage}
        onCreateAgent={createAgentFromSettings}
        onExportAgent={exportAgentFromSettings}
        onImportAgent={importAgentFromSettings}
        onApplySessionConfig={(targetConfigVersion) => applySkillConfigToSession({
          operation: "sync",
          targetConfigVersion,
          throwOnError: true
        })}
        onOpenInspector={openInspectorPage}
        onOpenSession={openArchivedSessionFromSettings}
        onRestoreSession={(session) => restoreTask(session).catch((error) => setStatus(error.message))}
        onRollbackAgent={rollbackAgentFromSettings}
        onSaveAgent={(body) => saveAgentFromSettings(body).catch((error) => setStatus(error.message))}
        onSelectAgent={selectAgentFromSettings}
        onUpdateAgentPermissions={updateAgentPermissionsFromSettings}
        onModelCatalogChanged={refreshModelOptions}
        recentSessions={recentSessions}
        runtimeConfig={runtimeConfig}
        search={settingsSearch}
        sections={settingsSections}
        selectedAgent={selectedAgent}
        restoringSessionID={sessionAction.startsWith("restore:") ? sessionAction.slice("restore:".length) : ""}
        rollingBackVersion={rollingBackAgentVersion}
        savingAgent={savingAgentConfig}
        setActiveSection={setSettingsSection}
        setSearch={setSettingsSearch}
        skills={installedSkills}
        modelOptions={modelOptions}
        toolingCatalog={toolingCatalog}
        workspaceID={toolingWorkspaceID}
        onSkillsChanged={async () => {
          const targetAgentID = sessionMeta?.agent_id || selectedAgent?.id || "";
          const [skillsResponse, refreshedAgent] = await Promise.all([
            api.skills({ workspaceId: toolingWorkspaceID }),
            targetAgentID ? api.agent(targetAgentID) : Promise.resolve(null)
          ]);
          setInstalledSkills(skillsResponse.skills || []);
          if (refreshedAgent) {
            setAvailableAgents((current) => current.map((item) => item.id === refreshedAgent.id ? refreshedAgent : item));
            setDefaultAgentConfig((current) => current?.id === refreshedAgent.id ? refreshedAgent : current);
          }
          return refreshedAgent;
        }}
      />
    );
  }

  return (
    <div className="user-app">
      <header className="user-topbar">
        <div className="topbar-brand">
          <div className="topbar-label">TMA 工作台</div>
          <div className="topbar-context">{activePluginRoute?.title || sessionMeta?.title || sessionID || "通用智能体工作区"}</div>
        </div>
        <div className="topbar-status">
          <span className="status-readout">{status}</span>
          <Pill value={runState} />
          {principal ? (
            <div className="topbar-identity">
              <span title={principal.subject}>{principal.subject}</span>
              <small>{principal.roles?.[principal.roles.length - 1] || "viewer"}</small>
            </div>
          ) : null}
          <button className="secondary topbar-logout" type="button" onClick={() => logout().catch((error) => setStatus(error.message))}>退出</button>
        </div>
      </header>
      <div
        className={`user-layout ${artifactPreview && !pluginRoutePath ? "has-artifact-preview" : ""} ${pluginRoutePath ? "plugin-route-active" : ""}`.trim()}
        style={{ "--artifact-preview-width": `${artifactPreviewWidth}px` }}
      >
        <aside className="user-sidebar">
          <Panel title="工作区" className="workspace-panel">
            <div className="workspace-controls">
              <label className="workspace-field">
                <span>智能体</span>
                <select
                  value={selectedAgentValue}
                  onChange={(event) => {
                    const nextAgentID = event.target.value;
                    setAgentID(nextAgentID);
                    const selectedAgent = availableAgents.find((agent) => agent.id === nextAgentID);
                    if (sessionMeta?.agent_id && sessionMeta.agent_id !== nextAgentID) {
                      resetSessionViewForAgent(selectedAgent);
                    } else if (selectedAgent?.config_version) {
                      setSettingsDraft((current) => ({
                        ...current,
                        llmProvider: selectedAgent.config_version.llm_provider || current.llmProvider || "",
                        llmModel: selectedAgent.config_version.llm_model || current.llmModel || ""
                      }));
                    }
                  }}
                >
                  {availableAgents.map((agent) => (
                    <option key={agent.id} value={agent.id}>{agent.name || agent.id}</option>
                  ))}
                </select>
              </label>
              <button type="button" className="workspace-primary-action" onClick={() => { closePluginRoute(); startNewTask(); }}>新建任务</button>
            </div>
          </Panel>
          <Panel title="任务" className="tasks-panel">
            <div className="stack task-panel-content">
              <input
                value={taskSearch}
                onChange={(event) => setTaskSearch(event.target.value)}
                placeholder="搜索任务..."
              />
              <div className="task-section-scroll">
                <div className="task-section-list">
                  {filteredTaskSessions.length ? (
                    <div className="task-section">
                      <div className="task-section-title">全部任务</div>
                      <div className="turn-list">
                        {visibleTaskSessions.map((session) => (
                        <div
                          className={`turn-item task-nav-item ${session.id === sessionID ? "active" : ""}`}
                          key={session.id}
                          onMouseEnter={(event) => showTaskHoverPreview(session, event)}
                          onMouseLeave={hideTaskHoverPreview}
                        >
                          <button
                            className="task-nav-open"
                            type="button"
                            onClick={() => { closePluginRoute(); openSession(session).catch((error) => setStatus(error.message)); }}
                          >
                              <div className="task-nav-row">
                                <TaskStatusIcon
                                  status={session.id === sessionID && waitingForReply
                                    ? (effectiveSessionStatus === "interrupting" ? "interrupting" : "running")
                                    : session.status}
                                />
                                <strong>{session.title || "Untitled task"}</strong>
                              </div>
                            </button>
                            <div className="task-inline-actions">
                              <button
                                className={`icon-button task-pin-button ${session.pinned_at ? "active" : ""}`}
                                type="button"
                                aria-label={session.pinned_at ? "取消置顶" : "置顶任务"}
                                disabled={sessionAction === `pin:${session.id}`}
                                onClick={() => toggleTaskPin(session).catch((error) => setStatus(error.message))}
                              >
                                <PinIcon filled={Boolean(session.pinned_at)} />
                              </button>
                              <button
                                className={`icon-button task-menu-button ${taskMenuSessionID === session.id ? "active" : ""}`}
                                ref={(element) => {
                                  if (element) taskMenuButtonRefs.current.set(session.id, element);
                                  else taskMenuButtonRefs.current.delete(session.id);
                                }}
                                type="button"
                                aria-label="更多操作"
                                aria-expanded={taskMenuSessionID === session.id}
                                onClick={(event) => {
                                  if (taskMenuSessionID === session.id) {
                                    setTaskMenuSessionID("");
                                    setTaskMenuPosition(null);
                                    return;
                                  }
                                  const rect = event.currentTarget.getBoundingClientRect();
                                  const menuWidth = 180;
                                  const menuHeight = 192;
                                  const gap = 8;
                                  const opensUp = rect.bottom + gap + menuHeight > window.innerHeight;
                                  setTaskMenuPosition({
                                    left: Math.max(gap, Math.min(window.innerWidth - menuWidth - gap, rect.right - menuWidth)),
                                    top: opensUp ? Math.max(gap, rect.top - menuHeight - gap) : rect.bottom + gap
                                  });
                                  setTaskMenuSessionID(session.id);
                                }}
                              >
                                <MoreIcon />
                              </button>
                              {taskMenuSessionID === session.id && taskMenuPosition ? createPortal(
                                <div className="task-action-menu task-action-menu-portal" role="menu" style={taskMenuPosition}>
                                  <button type="button" role="menuitem" onClick={() => openTaskMetadata(session)}>标签与摘要</button>
                                  <button type="button" role="menuitem" onClick={() => { setTaskMenuSessionID(""); openTaskComparison(session); }}>任务对比</button>
                                  <button type="button" role="menuitem" disabled={sessionAction === `rerun:${session.id}`} onClick={() => { setTaskMenuSessionID(""); rerunTask(session).catch((error) => setStatus(error.message)); }}>按原配置重跑</button>
                                  {session.status !== "terminated" ? <button type="button" role="menuitem" disabled={sessionAction === `archive:${session.id}`} onClick={() => { setTaskMenuSessionID(""); archiveTask(session.id).catch((error) => setStatus(error.message)); }}>归档任务</button> : null}
                                  <button className="danger" type="button" role="menuitem" disabled={sessionAction === `delete:${session.id}`} onClick={() => { setTaskMenuSessionID(""); deleteTask(session.id).catch((error) => setStatus(error.message)); }}>删除任务</button>
                                </div>,
                                document.body
                              ) : null}
                            </div>
                        </div>
                      ))}
                      </div>
                      {hasMoreTasks ? (
                        <button className="secondary task-more-button" type="button" onClick={() => setVisibleTaskCount((current) => current + 10)}>
                          显示更多任务
                        </button>
                      ) : null}
                    </div>
                  ) : null}
                  {!filteredTaskSessions.length ? (
                    <Empty>{hasTaskSearch ? "没有匹配的任务。" : "暂无任务。"}</Empty>
                  ) : null}
                </div>
              </div>
            </div>
          </Panel>
          {pluginNavigation.length ? (
            <Panel title="扩展" className="plugin-navigation-panel">
              <nav className="plugin-navigation" aria-label="扩展页面">
                {pluginNavigation.map((item) => (
                  <button
                    className={item.route === pluginRoutePath ? "active" : ""}
                    key={`${item.pluginID}:${item.id}`}
                    type="button"
                    onClick={() => navigatePluginRoute(item.route)}
                  >
                    <span aria-hidden="true" className="plugin-navigation-mark" />
                    <span>{item.title}</span>
                  </button>
                ))}
              </nav>
            </Panel>
          ) : null}
          <div className="sidebar-footer">
            <button className="secondary sidebar-settings-button" type="button" onClick={openSettingsPage}>
              <span>设置</span>
            </button>
          </div>
        </aside>
        <main className={`user-main ${pluginRoutePath ? "plugin-route-main" : ""}`}>
          {pluginRoutePath ? (
            <PluginRouteHost
              loading={pluginLoadState === "loading"}
              onBack={closePluginRoute}
              onError={(error) => workbenchNotificationService.show({
                level: "error",
                title: "插件页面错误",
                message: error.message || String(error),
                dedupeKey: `plugin.route.${pluginRoutePath}`
              })}
              path={pluginRoutePath}
              route={activePluginRoute}
            />
          ) : (
          <>
            <section className="user-thread" onScroll={handleThreadScroll} ref={threadRef}>
                {hasPendingApprovals ? (
                  <div className="approval-alert">
                    <div>
                      <strong>{interventions.length} approval{interventions.length === 1 ? "" : "s"} waiting</strong>
                      <div className="subtle">Review the pending tool call before the agent continues.</div>
                    </div>
                    <div className="approval-alert-actions">
                      <button className="secondary" type="button" onClick={() => interruptFromApprovals().catch((error) => setStatus(error.message))}>终止任务</button>
                      <button type="button" onClick={() => setApprovalsOpen(true)}>查看审批</button>
                    </div>
                  </div>
                ) : null}
                <WorkflowProgress run={workflowRun} onStop={() => stopWorkflowRun().catch((error) => setStatus(error.message))} />
				{renderedChatTimelineEvents.length ? renderedChatTimelineEvents.map((event, eventIndex) => {
                  if (event.type === "runtime.progress_message") {
                    const progressText = String(eventData(event).text || "").trim();
                    return progressText ? (
                      <article aria-label="通用智能体过程更新" className="agent-progress-message" key={`${event.seq}-${event.type}`}>
                        <div>{progressText}</div>
                      </article>
                    ) : null;
                  }
				  if (event.type === "user.message" || event.type === "agent.message" || event.type === "agent.streaming") {
					const streaming = event.type === "agent.streaming";
					const role = event.type === "user.message" ? "user" : "agent";
					const messageKey = role === "agent" && payload(event).turn_id
						? `message-agent-${payload(event).turn_id}`
						: `${event.seq}-${event.type}`;
					const messageArtifacts = streaming ? [] : role === "agent"
                      ? finalAgentMessageArtifacts(event, artifacts)
                      : uploadedMessageArtifacts(event, artifacts);
                    return (
					  <article aria-live={streaming ? "polite" : undefined} className={`message ${role}${streaming ? " streaming" : ""}`} key={messageKey}>
						<Meta><strong>{role === "user" ? "你" : "通用智能体"}</strong><span>{formatTime(event.created_at)}</span>{streaming ? <span>生成中</span> : null}</Meta>
						{role === "agent" ? (
						  <AgentMessageBody
						    artifacts={messageArtifacts}
						    sessionID={sessionID}
						    text={eventText(event)}
						    streaming={streaming}
						    onPreview={(artifact) => previewArtifact(artifact).catch((error) => setStatus(error.message))}
						  />
						) : (
						  <MessageBody
						    event={event}
						    artifacts={messageArtifacts}
						    sessionID={sessionID}
						    onPreview={(artifact) => previewArtifact(artifact).catch((error) => setStatus(error.message))}
						  />
						)}
                      </article>
                    );
                  }
                  const nextEvent = renderedChatTimelineEvents[eventIndex + 1];
                  const toolLifecycle = toolCallLifecycles.get(toolCallID(event));
                  const terminalToolEvent = terminalToolLifecycleEvent(toolLifecycle);
                  const isLatestActiveEvent = eventIndex === renderedChatTimelineEvents.length - 1 && ["running", "interrupting", "provisioning"].includes(effectiveSessionStatus);
                  return (
                    <ProcessEventCard
                      active={isLatestActiveEvent}
                      activeSkillKeys={activeSkillKeys}
                      completedAt={terminalToolEvent?.created_at || nextEvent?.created_at || ""}
                      enabledSkillKeys={enabledSkillKeys}
                      event={event}
                      key={`${event.seq}-${event.type}`}
                      latestSkillLifecycle={latestSkillLifecycle}
                      onApplySessionConfig={applySkillConfigToSession}
                      onRequestSkillEnable={requestInstalledSkillEnable}
                      onRequestSkillDisable={requestInstalledSkillDisable}
                      sessionConfigApplyBusy={applyingSessionConfigVersion}
                      sessionConfigVersion={Number(sessionMeta?.agent_config_version || 0)}
                      skillEnableBusy={requestingSkillEnable}
                      skillDisableBusy={requestingSkillDisable}
                      skillEnableDisabled={Boolean(applyingSessionConfigVersion || waitingForReply || hasPendingApprovals || ["running", "interrupting", "provisioning"].includes(effectiveSessionStatus))}
                      toolLifecycle={toolLifecycle}
                    />
                  );
                }) : (
                  <div className="welcome-state">
                  <div className="welcome-hero">
                      <div className="welcome-eyebrow">托管智能体工作区</div>
                      <h2>开始一个任务，让通用智能体持续推进它。</h2>
                      <p>
                        TMA 可以检查代码、执行校验、编辑文件、调用工具，并持续推进一个会话直到工作完成。
                        你可以从下面选择一个起点，或者直接输入自己的请求。
                      </p>
                      <div className="welcome-tags">
                        <span>代码审查</span>
                        <span>文件修改</span>
                        <span>构建测试</span>
                        <span>工具编排</span>
                      </div>
                    </div>
                    <div className="starter-grid task-template-starter-grid">
                      {taskTemplates.length ? taskTemplates.map((template) => (
                        <article className="starter-card task-template-starter" key={template.id}>
                          <div className="task-template-starter-head"><span>{template.category}</span><strong>{template.title}</strong></div>
                          <div>{template.description}</div>
                          <div className="task-template-starter-actions">
                            <button className="secondary" type="button" onClick={() => applyTaskTemplate(template, false)}>填充</button>
                            <button type="button" onClick={() => applyTaskTemplate(template, true)}>工作流</button>
                          </div>
                        </article>
                      )) : <div className="empty-state compact">正在加载任务模板...</div>}
                    </div>
                    <div className="welcome-note">
                      <strong>提示</strong>
                      <span>你可以在任务开始前预先选择模型、审批模式和运行环境。</span>
                    </div>
                  </div>
                )}
				{!streamingReply && waitingForReply ? (
                  <article className="message agent pending">
                    <Meta><strong>通用智能体</strong></Meta>
                    <div className="message-text">正在处理并生成回复…</div>
                  </article>
				) : null}
              </section>
              <section className="composer">
                <div
                  className={`composer-shell ${composerDragActive ? "drag-active" : ""}`}
                  onDragEnter={(event) => { event.preventDefault(); setComposerDragActive(true); }}
                  onDragOver={(event) => { event.preventDefault(); event.dataTransfer.dropEffect = "copy"; }}
                  onDragLeave={(event) => {
                    if (!event.currentTarget.contains(event.relatedTarget)) setComposerDragActive(false);
                  }}
                  onDrop={handleComposerDrop}
                >
                  <input
                    className="composer-file-input"
                    ref={composerFileInputRef}
                    type="file"
                    multiple
                    onChange={(event) => {
                      addComposerFiles(event.target.files);
                      event.target.value = "";
                    }}
                  />
                  {composerFiles.length ? (
                    <div className="composer-attachments" aria-label="待上传文件">
                      {composerFiles.map((item) => {
                        const extension = item.file.name.includes(".") ? item.file.name.split(".").pop().slice(0, 5).toUpperCase() : "FILE";
                        return (
                          <div className={`composer-attachment ${item.status}`} key={item.id} title={item.error || item.file.name}>
                            <div className="composer-attachment-icon"><FileIcon /><span>{extension}</span></div>
                            <div className="composer-attachment-copy">
                              <strong>{item.file.name}</strong>
                              <span>{item.status === "uploading" ? "上传中..." : item.status === "uploaded" ? "已上传" : item.status === "error" ? "上传失败，发送时重试" : formatFileSize(item.file.size)}</span>
                            </div>
                            <button className="icon-button" type="button" disabled={uploadingFiles} onClick={() => removeComposerFile(item.id)} title="移除文件" aria-label={`移除 ${item.file.name}`}><CloseIcon /></button>
                          </div>
                        );
                      })}
                    </div>
                  ) : null}
                  {composerVisionError() ? (
                    <div className="composer-vision-warning" role="alert">
                      <span>{composerVisionError()}</span>
                      <button type="button" onClick={() => { setSettingsSection("models"); setSettingsOpen(true); }}>配置模型</button>
                    </div>
                  ) : null}
                  <textarea
                    value={task}
                    onChange={(event) => setTask(event.target.value)}
                    onKeyDown={handleTaskComposerKeyDown}
                    onPaste={handleComposerPaste}
                    placeholder="让 TMA 帮你构建、检查、修改或执行某项工作... 回车发送，Shift+Enter 换行。"
                  />
                  <div className="composer-input-actions">
                    <button
                      className="icon-button composer-attach-button"
                      type="button"
                      disabled={uploadingFiles || composerFiles.length >= maxComposerFiles}
                      onClick={() => composerFileInputRef.current?.click()}
                      aria-label="上传文件"
                    >
                      <span aria-hidden="true">+</span>
                    </button>
                  </div>
                  {selectedTaskTemplate ? (
                    <div className="composer-template-selection">
                      <div><span>{workflowMode ? "工作流" : "任务模板"}</span><strong>{selectedTaskTemplate.title}</strong>{workflowMode ? <small>{selectedTaskTemplate.workflow_steps?.length || 0} 步顺序执行</small> : null}</div>
                      <button className="icon-button" type="button" title="清除任务模板" aria-label="清除任务模板" onClick={clearTaskTemplate}>×</button>
                    </div>
                  ) : null}
                  {selectedGuidanceItems.length ? (
                    <div className="composer-guidance-chips">
                      {selectedGuidanceItems.map((item) => (
                        <button className="composer-guidance-chip" type="button" key={item.key} onClick={() => toggleGuidanceKey(item.key)}>
                          <span>{toolingGuidanceLabel(item)}</span>
                          <span>×</span>
                        </button>
                      ))}
                    </div>
                  ) : null}
                  <div className="composer-toolbar">
                    <div className="composer-settings-inline">
                      <label className="composer-setting">
                        <span>审批</span>
                        <select
                          disabled={savingSettings}
                          value={settingsDraft.interventionMode}
                          onChange={(event) => applySessionSettings({ interventionMode: event.target.value }).catch((error) => setStatus(error.message))}
                        >
                          <option value="request_approval">先审批</option>
                          <option value="approve_for_me">替我审批</option>
                          <option value="full_access">完全访问</option>
                        </select>
                      </label>
                      <label className="composer-setting">
                        <span>运行环境</span>
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
                        <span>模型</span>
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
                              {option.label} · {modelCapabilityLabel(option.capabilityType)}
                            </option>
                          ))}
                        </select>
                      </label>
                    </div>
                    <div className="composer-toolbar-end">
                      <button className="secondary composer-tool-button" type="button" onClick={() => setToolPickerOpen(true)}>
                        工具
                        {selectableToolingItems.length ? <span>{selectedGuidanceItems.length}/{selectableToolingItems.length}</span> : null}
                      </button>
                      <button
                        type="button"
                        className={primaryAction.className}
                        disabled={primaryAction.disabled}
                        onClick={() => {
                          if (primaryAction.mode === "start") {
                            startSession().catch((error) => setStatus(error.message));
                            return;
                          }
                          if (primaryAction.mode === "send") {
                            if (workflowMode && selectedTaskTemplate?.workflow_steps?.length && task.trim()) {
                              startWorkflow(sessionID, selectedTaskTemplate, task.trim()).catch((error) => setStatus(error.message));
                            } else {
                              sendTask().catch((error) => setStatus(error.message));
                            }
                            return;
                          }
                          if (primaryAction.mode === "interrupt") {
                            confirmInterruptTask().catch((error) => setStatus(error.message));
                          }
                        }}
                      >
                        {primaryAction.label}
                      </button>
                    </div>
                  </div>
                </div>
              </section>
          </>
          )}
        </main>
        {!pluginRoutePath && artifactPreview ? (
          <section className="artifact-preview-pane" aria-label="结果预览">
            <button className="artifact-resize-handle" type="button" aria-label="拖动调整预览宽度" onPointerDown={startArtifactResize} />
            <header className="artifact-preview-pane-header">
              <div>
                <div className="artifact-preview-pane-label">结果预览</div>
                <strong>{artifactPreview.resource.title}</strong>
                <span>{artifactPreview.contentType || artifactPreview.resource.mimeType || artifactPreview.resource.type || "文件"}</span>
              </div>
              <div className="artifact-preview-pane-actions">
                <button className="secondary icon-button" type="button" onClick={() => downloadArtifactResource(artifactPreview.resource)} title="下载" aria-label="下载">↓</button>
                <button className="icon-button" type="button" onClick={clearArtifactPreview} title="关闭预览" aria-label="关闭预览"><CloseIcon /></button>
              </div>
              <div className="artifact-preview-mode-tabs" role="tablist" aria-label="结果查看方式">
                <button
                  className={artifactPreviewMode === "preview" ? "active" : ""}
                  type="button"
                  role="tab"
                  aria-selected={artifactPreviewMode === "preview"}
                  onClick={() => setArtifactPreviewMode("preview")}
                >
                  预览
                </button>
                <button
                  className={artifactPreviewMode === "source" ? "active" : ""}
                  type="button"
                  role="tab"
                  aria-selected={artifactPreviewMode === "source"}
                  disabled={!isMarkdownResource(artifactPreview.resource, artifactPreview.contentType) && !isHTMLResource(artifactPreview.resource, artifactPreview.contentType)}
                  title={isMarkdownResource(artifactPreview.resource, artifactPreview.contentType) || isHTMLResource(artifactPreview.resource, artifactPreview.contentType) ? "查看文件源码" : "当前文件不支持源码切换"}
                  onClick={() => setArtifactPreviewMode("source")}
                >
                  源码
                </button>
              </div>
            </header>
            <div className="artifact-preview-pane-body">
              <ArtifactPreviewContent preview={artifactPreview} mode={artifactPreviewMode} />
            </div>
          </section>
        ) : null}
        {!pluginRoutePath ? (
        <aside className="user-sidebar right">
          <Panel
            className="right-tab-panel"
            title={(
              <div className="right-panel-tabs" role="tablist" aria-label="右侧面板">
                <button className={rightPanelTab === "results" ? "active" : ""} type="button" role="tab" aria-selected={rightPanelTab === "results"} onClick={() => setRightPanelTab("results")}>结果{resultFiles.length ? ` (${resultFiles.length})` : ""}</button>
                <button className={rightPanelTab === "activity" ? "active" : ""} type="button" role="tab" aria-selected={rightPanelTab === "activity"} onClick={() => setRightPanelTab("activity")}>执行</button>
              </div>
            )}
          >
            {rightPanelTab === "results" ? (
              resultFiles.length ? (
                <div className="artifact-tree" role="tree" aria-label="结果文件目录">
                  <ArtifactTreeNode node={artifactTree} depth={0} selectedArtifactID={artifactPreview?.resource?.id || ""} onPreview={(artifact) => previewArtifact(artifact).catch((error) => setStatus(error.message))} />
                </div>
              ) : <Empty>还没有生成结果文件。</Empty>
            ) : (
              <div className="list activity-list" role="tabpanel">
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
                        {activity.detail || activitySummary(event) || "暂无详情。"}
                        {item.count > 1 ? <span> · {item.count} 次更新</span> : null}
                      </div>
                    </div>
                  );
                }) : <Empty>还没有执行记录。</Empty>}
              </div>
            )}
          </Panel>
        </aside>
        ) : null}
      </div>
      {toolPickerOpen ? (
        <ToolPickerModal
          loading={toolingLoading}
          error={toolingError}
          sections={toolingCatalog.sections}
          selectedKeys={selectedGuidanceKeys}
          onToggle={toggleGuidanceKey}
          onClose={() => setToolPickerOpen(false)}
          onClear={() => setSelectedGuidanceKeys([])}
        />
      ) : null}
      {templatePickerOpen ? (
        <TaskTemplateModal
          templates={taskTemplates}
          onClose={() => setTemplatePickerOpen(false)}
          onSelect={applyTaskTemplate}
        />
      ) : null}
      {comparisonOpen ? (
        <SessionComparisonModal
          sessions={recentSessions}
          leftID={comparisonLeftID}
          rightID={comparisonRightID}
          onLeftChange={(value) => { setComparisonLeftID(value); setComparisonResult(null); }}
          onRightChange={(value) => { setComparisonRightID(value); setComparisonResult(null); }}
          onCompare={() => loadTaskComparison().catch((error) => setStatus(error.message))}
          onClose={() => setComparisonOpen(false)}
          loading={comparisonLoading}
          result={comparisonResult}
          modelOptions={modelOptions}
          variantModel={comparisonVariantModel}
          onVariantModelChange={setComparisonVariantModel}
          onCreateVariant={() => createComparisonVariant().catch((error) => setStatus(error.message))}
          creatingVariant={sessionAction === `rerun:${comparisonLeftID}`}
        />
      ) : null}
      {metadataSession ? (
        <TaskMetadataModal
          session={metadataSession}
          tags={metadataTagsDraft}
          onTagsChange={setMetadataTagsDraft}
          onSave={() => saveTaskMetadata().catch((error) => setStatus(error.message))}
          onClose={() => setMetadataSession(null)}
          saving={savingMetadata || sessionAction === `pin:${metadataSession.id}`}
          onTogglePin={() => toggleTaskPin(metadataSession).catch((error) => setStatus(error.message))}
        />
      ) : null}
      {approvalsOpen ? (
        <div className="approval-modal-backdrop" role="presentation" onClick={deferApprovals}>
          <section className="approval-modal" role="dialog" aria-modal="true" aria-label="Approvals" onClick={(event) => event.stopPropagation()}>
            <div className="approval-modal-header">
              <div>
                <h2>Approvals</h2>
                <div className="subtle">{sessionID ? `Session ${sessionID}` : "No session selected"}</div>
              </div>
              <div className="approval-modal-actions">
                <button className="secondary" type="button" disabled={Boolean(decidingApprovalID)} onClick={() => refresh().catch((error) => setStatus(error.message))}>刷新</button>
                <button className="secondary" type="button" disabled={Boolean(decidingApprovalID)} onClick={() => interruptFromApprovals().catch((error) => setStatus(error.message))}>终止任务</button>
                <button className="secondary" type="button" disabled={Boolean(decidingApprovalID)} onClick={deferApprovals}>稍后处理</button>
              </div>
            </div>
            <div className="approval-list-main">
              {interventions.length ? interventions.map((intervention) => (
                <ApprovalCard
                  key={intervention.call_id}
                  intervention={intervention}
                  busy={Boolean(decidingApprovalID)}
                  active={decidingApprovalID === intervention.call_id}
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
      {taskHoverPreview ? (
        <div
          className="task-hover-floating"
          style={{ left: `${taskHoverPreview.left}px`, top: `${taskHoverPreview.top}px` }}
        >
          <strong>{taskHoverPreview.title}</strong>
          <span>{taskHoverPreview.time}</span>
        </div>
      ) : null}
    </div>
  );
}

function WorkbenchRoot() {
  return (
    <>
      <WorkbenchApp />
      <DialogHost service={workbenchDialogService} />
      <NotificationHost service={workbenchNotificationService} />
    </>
  );
}

createRoot(document.getElementById("root")).render(<WorkbenchRoot />);
