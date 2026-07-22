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

export async function getJSON(path) {
  const response = await fetch(path);
  if (!response.ok) throw new Error(await responseErrorMessage(response));
  return response.json();
}

export async function getText(path) {
  const response = await fetch(path);
  if (!response.ok) throw new Error(await response.text());
  return response.text();
}

export function currentPrincipal() {
  return coreSDK.auth.me();
}

export async function postJSON(path, body) {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {})
  });
  if (!response.ok) throw new Error(await responseErrorMessage(response));
  return response.json();
}

export async function patchJSON(path, body) {
  const response = await fetch(path, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {})
  });
  if (!response.ok) throw new Error(await responseErrorMessage(response));
  return response.json();
}

export async function putJSON(path, body) {
  const response = await fetch(path, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {})
  });
  if (!response.ok) throw new Error(await responseErrorMessage(response));
  return response.json();
}

async function responseErrorMessage(response) {
  const text = await response.text();
  try {
    const payload = JSON.parse(text);
    if (typeof payload?.error === "string") return payload.error;
    if (typeof payload?.error?.message === "string") return payload.error.message;
    return text || `HTTP ${response.status}`;
  } catch {
    return text || `HTTP ${response.status}`;
  }
}

export async function deleteRequest(path) {
  const response = await fetch(path, { method: "DELETE" });
  if (!response.ok) throw new Error(await responseErrorMessage(response));
  return true;
}

export async function getBlob(path, options = {}) {
  const response = await fetch(path, options);
  if (!response.ok) throw new Error(await response.text());
  return response;
}

export function trace(sessionId, turnId, format, options = {}) {
  return format
    ? coreSDK.traces.exportSession(sessionId, format, turnId || undefined, options.signal)
    : coreSDK.traces.getSession(sessionId, turnId || undefined, options.signal);
}

export function createAgent(body, options = {}) {
  return coreSDK.agents.create(body, options.signal);
}

export async function agents() {
  return { agents: await coreSDK.agents.list() };
}

export function updateAgent(agentId, body, options = {}) {
  return coreSDK.agents.update(agentId, body || {}, options.signal);
}

export function defaultAgent() {
  return coreSDK.agents.default();
}

export function agent(agentId) {
  return coreSDK.agents.get(agentId);
}

export async function agentConfigVersions(agentId) {
  return { config_versions: await coreSDK.agents.listConfigVersions(agentId) };
}

export async function exportAgent(agentId, options = {}) {
  const document = await coreSDK.agents.export(agentId, options.signal);
  return new Response(JSON.stringify(document, null, 2), {
    headers: {
      "Content-Type": "application/json",
      "Content-Disposition": `attachment; filename="agent-${agentId}.json"`
    }
  });
}

export function importAgent(document, options = {}) {
  return coreSDK.agents.import(document, options.signal);
}

export function rollbackAgentConfigVersion(agentId, version, options = {}) {
  return coreSDK.agents.rollback(agentId, version, options.signal);
}

export function agentToolingHealth(agentId, body = {}, options = {}) {
  return coreSDK.agents.toolingHealth(agentId, body, options.signal);
}

export function workspaceToolPermissions(workspaceId) {
  return coreSDK.workspaceToolPermissions.get(workspaceId);
}

export function updateWorkspaceToolPermissions(workspaceId, permissionRules, expectedRevision) {
  return coreSDK.workspaceToolPermissions.update(workspaceId, expectedRevision, { permission_rules: permissionRules || [] });
}

export function evaluateWorkspaceToolPermission(workspaceId, request) {
  return coreSDK.workspaceToolPermissions.evaluate(workspaceId, request);
}

export function sessionToolPermissionAudit(sessionId, query = {}) {
  return coreSDK.audit.listToolPermissions(sessionId, query);
}

export function agentSchedules(agentId) {
  return getJSON(`/v1/agents/${encodeURIComponent(agentId)}/schedules`);
}

export function createAgentSchedule(agentId, body) {
  return postJSON(`/v1/agents/${encodeURIComponent(agentId)}/schedules`, body);
}

export function updateAgentSchedule(agentId, scheduleId, body) {
  return patchJSON(`/v1/agents/${encodeURIComponent(agentId)}/schedules/${encodeURIComponent(scheduleId)}`, body);
}

export function deleteAgentSchedule(agentId, scheduleId) {
  return deleteRequest(`/v1/agents/${encodeURIComponent(agentId)}/schedules/${encodeURIComponent(scheduleId)}`);
}

export function runAgentSchedule(agentId, scheduleId) {
  return postJSON(`/v1/agents/${encodeURIComponent(agentId)}/schedules/${encodeURIComponent(scheduleId)}/run`, {});
}

export async function mcpServers(workspaceId = "") {
  return { servers: await coreSDK.mcp.list(workspaceId ? { workspaceId } : {}) };
}

export function mcpServerRuntimeStatus(workspaceId = "") {
  return coreSDK.mcp.runtimeStatus(workspaceId ? { workspaceId } : {});
}

export function createMCPServer(body, options = {}) {
  return coreSDK.mcp.create(body, options.signal);
}

export function updateMCPServer(serverId, body, options = {}) {
  return coreSDK.mcp.update(serverId, body, options.signal);
}

export function setMCPServerEnabled(serverId, enabled, options = {}) {
  return coreSDK.mcp.setEnabled(serverId, enabled, options.signal);
}

export function testMCPServer(serverId, options = {}) {
  return coreSDK.mcp.test(serverId, options.signal);
}

export function deleteMCPServer(serverId, options = {}) {
  return coreSDK.mcp.archive(serverId, options.signal);
}

export async function mcpServerVersions(serverId) {
  return { versions: await coreSDK.mcp.versions(serverId) };
}

export function restoreMCPServerVersion(serverId, version, options = {}) {
  return coreSDK.mcp.restoreVersion(serverId, version, options.signal);
}

export function createEnvironment(body, options = {}) {
  return coreSDK.environments.create(body, options.signal);
}

export async function environmentVariables(workspaceId = "") {
  return { variables: await coreSDK.environmentVariables.list(workspaceId ? { workspaceId } : {}) };
}

export function putEnvironmentVariable(name, value, workspaceId = "", options = {}) {
  return coreSDK.environmentVariables.put(name, { value }, workspaceId ? { workspaceId } : {}, options.signal);
}

export function deleteEnvironmentVariable(name, workspaceId = "", options = {}) {
  return coreSDK.environmentVariables.delete(name, workspaceId ? { workspaceId } : {}, options.signal);
}

export function createSession(body, options = {}) {
  return coreSDK.sessions.create(body, options.signal);
}

export async function sessions(filters = {}) {
  const items = await coreSDK.sessions.list({
    limit: filters.limit || 30,
    ...(filters.workspace ? { workspaceId: filters.workspace } : {}),
    ...(filters.status ? { status: filters.status } : {}),
    ...(filters.includeArchived ? { includeArchived: true } : {})
  });
  return { sessions: items };
}

function appendQueuedSessionMessage(sessionId, text, options) {
  return coreSDK.sessions.appendEvents(sessionId, {
    prefer_latest: Boolean(options.preferLatest),
    events: [{
      type: "user.message",
      payload: {
        content: [{ type: "text", text }],
        attachments: options.attachments || []
      }
    }]
  }, options.signal);
}

export async function sendSessionMessage(sessionId, text, options = {}) {
  if (options.queued) return appendQueuedSessionMessage(sessionId, text, options);
  try {
    const handle = await coreSDK.runs.start(sessionId, {
      input: {
        content: [{ type: "text", text }],
        attachments: options.attachments || []
      },
      ...(options.idempotencyKey ? { idempotency_key: options.idempotencyKey } : {})
    }, options.signal);
    return { run: handle.run, events: [...handle.initialEvents], created: handle.created };
  } catch (error) {
    if (options.queueIfBusy && error?.code === "session_busy") {
      return appendQueuedSessionMessage(sessionId, text, options);
    }
    throw error;
  }
}

export function steerSession(sessionId, text, options = {}) {
  return coreSDK.sessions.appendEvents(sessionId, {
    events: [{
      type: "user.steer",
      payload: {
        content: [{ type: "text", text }]
      }
    }]
  }, options.signal);
}

export async function uploadSessionArtifact(sessionId, file, options = {}) {
  return coreSDK.artifacts.upload(sessionId, {
    name: file.name,
    artifact_type: "file",
    description: options.description || "Uploaded by the user",
    metadata: JSON.stringify({ source: "user_upload" })
  }, {
    body: file,
    filename: file.name,
    ...(file.type ? { contentType: file.type } : {})
  }, options.signal);
}

export async function interruptSession(sessionId, options = {}) {
  const runs = await coreSDK.runs.list(sessionId, options.signal);
  const activeRun = [...runs].reverse().find((run) => run.status === "running" || run.status === "waiting_approval");
  if (!activeRun) throw new Error("session has no active Run to interrupt");
  const run = await coreSDK.runs.cancel(sessionId, activeRun.id, options.signal);
  return { run, events: [] };
}

export function archiveSession(sessionId, options = {}) {
  return coreSDK.sessions.archive(sessionId, options.signal);
}

export function restoreSession(sessionId, options = {}) {
  return coreSDK.sessions.restore(sessionId, options.signal);
}

export function rerunSession(sessionId, body = {}, options = {}) {
  return coreSDK.sessions.rerun(sessionId, body, options.signal);
}

export function compareSessions(leftSessionId, rightSessionId, options = {}) {
  return coreSDK.sessions.compare(leftSessionId, rightSessionId, options.signal);
}

export function deleteSession(sessionId, options = {}) {
  return coreSDK.sessions.delete(sessionId, options.signal);
}

export async function traceCatalog(filters = {}, options = {}) {
  const page = await coreSDK.traces.list({
    limit: filters.limit || 20,
    ...(filters.cursor ? { cursor: filters.cursor } : {}),
    ...(filters.workspace ? { workspaceId: filters.workspace } : {}),
    ...(filters.session ? { sessionId: filters.session } : {}),
    ...(filters.turn ? { turnId: filters.turn } : {}),
    ...(filters.sessionStatus ? { sessionStatus: filters.sessionStatus } : {}),
    ...(filters.includeArchived ? { includeArchived: true } : {})
  }, options.signal);
  return { traces: page.items, next_cursor: page.next_cursor, has_more: page.has_more };
}

export function traceByID(traceID, options = {}) {
  return coreSDK.traces.get(traceID, options.signal);
}

export function spanByID(traceID, spanID, options = {}) {
  return coreSDK.traces.getSpan(traceID, spanID, options.signal);
}

export async function spanCatalog(filters = {}, options = {}) {
  const page = await coreSDK.traces.listSpans({
    limit: filters.limit || 20,
    ...(filters.cursor ? { cursor: filters.cursor } : {}),
    ...(filters.workspace ? { workspaceId: filters.workspace } : {}),
    ...(filters.trace ? { traceId: filters.trace } : {}),
    ...(filters.session ? { sessionId: filters.session } : {}),
    ...(filters.turn ? { turnId: filters.turn } : {}),
    ...(filters.query ? { search: filters.query } : {}),
    ...(filters.kind ? { kind: filters.kind } : {}),
    ...(filters.status ? { status: filters.status } : {}),
    ...(typeof filters.critical === "boolean" ? { critical: filters.critical } : {}),
    ...(filters.minDuration ? { minDurationMs: filters.minDuration } : {}),
    ...(filters.maxDuration ? { maxDurationMs: filters.maxDuration } : {}),
    ...(filters.minSelfDuration ? { minSelfDurationMs: filters.minSelfDuration } : {}),
    ...(filters.includeArchived ? { includeArchived: true } : {})
  }, options.signal);
  return { spans: page.items, next_cursor: page.next_cursor, has_more: page.has_more };
}

export function session(sessionId) {
  return coreSDK.sessions.get(sessionId);
}

export function upgradeSessionConfig(sessionId, body, options = {}) {
  return coreSDK.sessions.upgradeConfig(sessionId, body, options.signal);
}

export function updateSessionMetadata(sessionId, body, options = {}) {
  return coreSDK.sessions.updateMetadata(sessionId, body, options.signal);
}

export function sessionRuntimeConfig(sessionId) {
  return coreSDK.sessions.runtimeConfig(sessionId);
}

export function sessionRuntimeCapabilities(sessionId) {
  return coreSDK.sessions.runtimeCapabilities(sessionId);
}

export async function skills(filters = {}) {
  return {
    skills: await coreSDK.skills.list({
      ...(filters.workspaceId ? { workspaceId: filters.workspaceId } : {}),
      ...(filters.includeArchived ? { includeArchived: true } : {})
    })
  };
}

export async function skillVersions(skillId) {
  return { versions: await coreSDK.skills.listVersions(skillId) };
}

export function archiveSkill(skillId, options = {}) {
  return coreSDK.skills.archive(skillId, options.signal);
}

export function discoverSkillsMarketplace(filters = {}, options = {}) {
  return coreSDK.marketplace.discover({
    sessionId: filters.sessionId || "",
    ...(filters.query ? { query: filters.query } : {}),
    ...(filters.repository ? { repository: filters.repository } : {}),
    ...(filters.limit ? { limit: filters.limit } : {})
  }, options.signal);
}

export function previewSkillsMarketplace(body, options = {}) {
  return coreSDK.marketplace.preview(body, options.signal);
}

export function installSkillsMarketplace(body, options = {}) {
  return coreSDK.marketplace.install(body, options.signal);
}

export function discoverInternalSkillsMarketplace(filters = {}, options = {}) {
  return coreSDK.marketplace.browseInternal({
    sessionId: filters.sessionId || "",
    ...(filters.query ? { query: filters.query } : {}),
    ...(filters.category ? { category: filters.category } : {}),
    ...(filters.tags?.length ? { tags: filters.tags } : {}),
    ...(filters.limit ? { limit: filters.limit } : {})
  }, options.signal);
}

export function previewInternalSkillsMarketplace(body, options = {}) {
  return coreSDK.marketplace.previewInternal(body, options.signal);
}

export function installInternalSkillsMarketplace(body, options = {}) {
  return coreSDK.marketplace.installInternal(body, options.signal);
}

export function enableSkill(skillId, body, options = {}) {
  return coreSDK.marketplace.enableInstalled(skillId, body, options.signal);
}

export function disableSkill(skillId, body, options = {}) {
  return coreSDK.marketplace.disableInstalled(skillId, body, options.signal);
}

export async function marketplacePolicies(filters = {}) {
  return {
    policies: await coreSDK.marketplace.listPolicies({
      ...(filters.organizationId ? { organizationId: filters.organizationId } : {}),
      ...(filters.workspaceId ? { workspaceId: filters.workspaceId } : {}),
      ...(filters.includeArchived ? { includeArchived: true } : {})
    })
  };
}

export function marketplacePolicy(policyId) {
  return coreSDK.marketplace.getPolicy(policyId);
}

export function createMarketplacePolicy(body, options = {}) {
  return coreSDK.marketplace.createPolicy(body, options.signal);
}

export function publishMarketplacePolicyVersion(policyId, config, options = {}) {
  return coreSDK.marketplace.publishPolicyVersion(policyId, { config }, options.signal);
}

export function archiveMarketplacePolicy(policyId, options = {}) {
  return coreSDK.marketplace.archivePolicy(policyId, options.signal);
}

export async function marketplaceEntries(filters = {}) {
  return {
    entries: await coreSDK.marketplace.listEntries({
      ...(filters.workspaceId ? { workspaceId: filters.workspaceId } : {}),
      ...(filters.status ? { status: filters.status } : {}),
      ...(filters.includeWithdrawn ? { includeWithdrawn: true } : {})
    })
  };
}

export function marketplaceEntry(entryId, workspaceId = "") {
  return coreSDK.marketplace.getEntry(entryId, workspaceId || undefined);
}

export function createMarketplaceEntry(body, options = {}) {
  return coreSDK.marketplace.createEntry(body, options.signal);
}

export function updateMarketplaceEntry(entryId, body, options = {}) {
  return coreSDK.marketplace.updateEntry(entryId, body, options.signal);
}

export function transitionMarketplaceEntry(entryId, action, body = {}, options = {}) {
  if (action === "submit") return coreSDK.marketplace.submitEntry(entryId, body, options.signal);
  if (action === "publish") return coreSDK.marketplace.publishEntry(entryId, body, options.signal);
  if (action === "withdraw") return coreSDK.marketplace.withdrawEntry(entryId, body, options.signal);
  throw new TypeError(`unsupported Marketplace entry transition: ${action}`);
}

export function skillAssetRetentionEffective(workspaceId, options = {}) {
  return coreSDK.skills.effectiveRetentionPolicy(workspaceId || undefined, options.signal);
}

export async function skillAssetRetentionPolicies(filters = {}, options = {}) {
  return {
    policies: await coreSDK.skills.listRetentionPolicies({
      ...(filters.organizationId ? { organizationId: filters.organizationId } : {}),
      ...(filters.workspaceId ? { workspaceId: filters.workspaceId } : {}),
      ...(filters.includeArchived ? { includeArchived: true } : {})
    }, options.signal)
  };
}

export function createSkillAssetRetentionPolicy(body, options = {}) {
  return coreSDK.skills.createRetentionPolicy(body, options.signal);
}

export function publishSkillAssetRetentionPolicyVersion(policyId, config, options = {}) {
  return coreSDK.skills.publishRetentionPolicyVersion(policyId, { config }, options.signal);
}

export function archiveSkillAssetRetentionPolicy(policyId, options = {}) {
  return coreSDK.skills.archiveRetentionPolicy(policyId, options.signal);
}

export function previewSkillAssetGC(body, options = {}) {
  return coreSDK.skills.previewAssetGC(body, options.signal);
}

export function runSkillAssetGC(body, options = {}) {
  return coreSDK.skills.runAssetGC(body, options.signal);
}

export async function skillAssetGCRuns(workspaceId, limit = 20, options = {}) {
  return { runs: await coreSDK.skills.listAssetGCRuns({ workspaceId: workspaceId || undefined, limit }, options.signal) };
}

export async function skillAssetGCTombstones(workspaceId, limit = 20, options = {}) {
  return { tombstones: await coreSDK.skills.listAssetGCTombstones({ workspaceId: workspaceId || undefined, limit }, options.signal) };
}

export function updateSessionRuntimeSettings(sessionId, expectedRevision, body, options = {}) {
  return coreSDK.sessions.updateRuntimeSettings(sessionId, expectedRevision, body || {}, options.signal);
}

export async function llmProviders() {
  return { providers: await coreSDK.llm.listProviders() };
}

export function createLLMProvider(body, options = {}) {
  return coreSDK.llm.createProvider(body, options.signal);
}

export function updateLLMProvider(providerId, revision, body, options = {}) {
  return coreSDK.llm.updateProvider(providerId, revision, body || {}, options.signal);
}

export function setLLMProviderEnabled(providerId, revision, enabled, options = {}) {
  return coreSDK.llm.setProviderEnabled(providerId, revision, enabled, options.signal);
}

export function deleteLLMProvider(providerId, revision, options = {}) {
  return coreSDK.llm.deleteProvider(providerId, revision, options.signal);
}

export async function testLLMProvider(providerId) {
  return coreSDK.llm.testProvider(providerId);
}

export async function llmModels(providerId) {
  return { models: await coreSDK.llm.listModels(providerId) };
}

export function upsertLLMModel(body, revision, options = {}) {
  return revision == null
    ? coreSDK.llm.createModel(body || {}, options.signal)
    : coreSDK.llm.updateModel(revision, body || {}, options.signal);
}

export function deleteLLMModel(providerId, model, revision, options = {}) {
  return coreSDK.llm.deleteModel(providerId, model, revision, options.signal);
}

export async function testLLMModel(providerId, model) {
  return coreSDK.llm.testModel(providerId, model);
}

export function usage(sessionId, options = {}) {
  return coreSDK.sessions.usage(sessionId, options.signal);
}

export function summary(sessionId, options = {}) {
  return coreSDK.sessions.summary(sessionId, options.signal);
}

export async function taskPlan(sessionId, options = {}) {
  try {
    return { plan: await coreSDK.sessions.taskPlan(sessionId, options.signal) };
  } catch (error) {
    if (error?.status === 404) return { plan: null };
    throw error;
  }
}

export async function artifacts(sessionId) {
  return { artifacts: await coreSDK.artifacts.list(sessionId) };
}

export function downloadArtifact(sessionId, artifactId, options = {}) {
  return coreSDK.artifacts.download(sessionId, artifactId, options.signal);
}

export function artifactDownloadPath(sessionId, artifactId) {
  return `/v2/sessions/${encodeURIComponent(sessionId)}/artifacts/${encodeURIComponent(artifactId)}/download`;
}

export function achievementLibrary(workspaceId) {
  const query = workspaceId ? `?workspace_id=${encodeURIComponent(workspaceId)}` : "";
  return getJSON(`/v2/achievement-library${query}`);
}

export function includeArtifactInAchievementLibrary(sessionId, artifactId, body) {
  return postJSON(`/v2/sessions/${encodeURIComponent(sessionId)}/artifacts/${encodeURIComponent(artifactId)}/achievement-library`, body);
}

export function updateAchievementLibraryItem(itemId, body) {
  return patchJSON(`/v2/achievement-library/${encodeURIComponent(itemId)}`, body);
}

export function deleteAchievementLibraryItem(itemId, workspaceId) {
  const query = workspaceId ? `?workspace_id=${encodeURIComponent(workspaceId)}` : "";
  return deleteRequest(`/v2/achievement-library/${encodeURIComponent(itemId)}${query}`);
}

export function referenceAchievementLibraryItem(itemId, sessionId) {
  return postJSON(`/v2/achievement-library/${encodeURIComponent(itemId)}/reference`, { session_id: sessionId });
}

export function achievementLibraryDownloadPath(itemId, workspaceId) {
  const query = workspaceId ? `?workspace_id=${encodeURIComponent(workspaceId)}` : "";
  return `/v2/achievement-library/${encodeURIComponent(itemId)}/download${query}`;
}

export function downloadAchievementLibraryItem(itemId, workspaceId, options = {}) {
  return getBlob(achievementLibraryDownloadPath(itemId, workspaceId), { signal: options.signal });
}

export function objectRefDownloadPath(objectRefId, sessionId) {
  const query = sessionId ? `?session_id=${encodeURIComponent(sessionId)}` : "";
  return `/v2/object-refs/${encodeURIComponent(objectRefId)}/download${query}`;
}

export function skillPackageDownloadPath(skillId, version) {
  return `/v2/skills/${encodeURIComponent(skillId)}/versions/${encodeURIComponent(version)}/package`;
}

export async function events(sessionId, afterSeq = 0) {
  return { events: await coreSDK.sessions.listEvents(sessionId, afterSeq) };
}

export function streamSessionEvents(sessionId, options = {}) {
  return coreSDK.sessions.events(sessionId, options);
}

export function streamSessionLiveEvents(sessionId, options = {}) {
  return coreSDK.sessions.liveEvents(sessionId, options);
}

export async function interventions(sessionId, status) {
  return { interventions: await coreSDK.interventions.list(sessionId, status) };
}

export function metrics(sessionId, turnId) {
  return getText(metricsPath(sessionId, turnId));
}

export function observabilityStatus() {
  return coreSDK.observability.status();
}

export function retryObservability(options = {}) {
  return coreSDK.observability.retry(options.signal);
}

export function taskGroupTemplates(options = {}) {
  return coreSDK.orchestration.taskGroupTemplates(options.signal);
}

export function taskTemplates() {
  return getJSON("/v1/task-templates");
}

export async function sessionTaskGroups(sessionId, options = {}) {
  return { task_groups: await coreSDK.orchestration.listTaskGroups(sessionId, options.signal) };
}

export function sessionTaskGroup(sessionId, groupId, options = {}) {
  return coreSDK.orchestration.getTaskGroup(sessionId, groupId, options.signal);
}

export function approveIntervention(sessionId, turnId, callId, body = {}, options = {}) {
  return coreSDK.interventions.approve(sessionId, turnId, callId, body.reason || "", options.signal, body.response);
}

export function rejectIntervention(sessionId, turnId, callId, body = {}, options = {}) {
  return coreSDK.interventions.reject(sessionId, turnId, callId, body.reason || "", options.signal);
}

export function respondIntervention(sessionId, turnId, callId, body = {}, options = {}) {
  return coreSDK.interventions.respond(sessionId, turnId, callId, body.response, options.signal);
}

export function skipIntervention(sessionId, turnId, callId, body = {}, options = {}) {
  return coreSDK.interventions.skip(sessionId, turnId, callId, body.reason || "", options.signal);
}

export function cancelIntervention(sessionId, turnId, callId, body = {}, options = {}) {
  return coreSDK.interventions.cancel(sessionId, turnId, callId, body.reason || "", options.signal);
}
