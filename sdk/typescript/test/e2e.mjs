import { TMAClient } from "../dist/index.js";

const baseURL = process.env.TMA_TYPESCRIPT_SDK_E2E_BASE_URL;
if (!baseURL) throw new Error("TMA_TYPESCRIPT_SDK_E2E_BASE_URL is required");

const client = new TMAClient(baseURL);
const controller = new AbortController();
const timeout = setTimeout(() => controller.abort(), 10_000);

try {
  const auth = await client.auth.me(controller.signal);
  if (auth.authenticated) throw new Error("test Server should use disabled authentication");

  const agent = await client.agents.create({
    name: "TypeScript SDK E2E",
    llm_provider: "fake",
    llm_model: "fake-demo",
    system: "Complete the test run.",
  }, controller.signal);
  const environment = await client.environments.create({
    name: "TypeScript SDK E2E",
    config: { type: "cloud" },
  }, controller.signal);
  const session = await client.sessions.create({
    agent_id: agent.id,
    environment_id: environment.id,
    title: "TypeScript SDK acceptance",
  }, controller.signal);
  const handle = await client.runs.start(session.id, {
    input: { content: [{ type: "text", text: "complete the TypeScript SDK acceptance run" }] },
    idempotency_key: "typescript-sdk-e2e-1",
  }, controller.signal);
  const result = await handle.wait(controller.signal);
  if (result.run.status !== "completed") throw new Error(`unexpected Run status ${result.run.status}`);

  const trace = await client.traces.getSession(session.id, result.run.id, controller.signal);
  if (!trace.trace_id || trace.turn_id !== result.run.id) throw new Error("trace projection did not identify the completed Run");
  const page = await client.traces.list({ sessionId: session.id, limit: 10 }, controller.signal);
  if (page.items.length !== 1 || page.items[0]?.trace_id !== trace.trace_id) throw new Error("trace catalog did not contain the projected trace");
  const templates = await client.orchestration.taskGroupTemplates(controller.signal);
  if (templates.templates.length === 0) throw new Error("orchestration templates are empty");

  const provider = await client.llm.createProvider({
    id: "typescript-sdk-e2e",
    provider_type: "openai_compatible",
    base_url: "https://llm.example/v1",
    enabled: true,
  }, controller.signal);
  const updatedProvider = await client.llm.updateProvider(provider.id, provider.revision, {
    provider_type: provider.provider_type,
    base_url: "https://llm.example/v2",
    enabled: true,
  }, controller.signal);
  const model = await client.llm.createModel({
    provider_id: provider.id,
    model: "typescript-sdk-model",
    context_window_tokens: 8192,
    capability_type: "text",
  }, controller.signal);
  const models = await client.llm.listModels(provider.id, controller.signal);
  if (updatedProvider.revision !== provider.revision + 1 || !models.some((item) => item.model === model.model)) {
    throw new Error("LLM control-plane lifecycle failed");
  }

  const objectRef = await client.objectRefs.create({
    bucket: "typescript-sdk-e2e",
    object_key: "metadata-only.txt",
    size_bytes: 0,
    metadata: { source: { sdk: "typescript" } },
  }, controller.signal);
  const loadedObjectRef = await client.objectRefs.get(objectRef.id, controller.signal);
  if (loadedObjectRef.metadata?.source?.sdk !== "typescript") throw new Error("ObjectRef metadata was not preserved");
  await client.objectRefs.delete(objectRef.id, controller.signal);

  const workers = await client.workers.list({}, controller.signal);
  if (workers.length !== 0) throw new Error("unexpected Worker fixture state");
  const workerDiagnosis = await client.workers.diagnose({ namespace: "tools", api: "read" }, controller.signal);
  if (workerDiagnosis.matches !== 0) throw new Error("unexpected Worker diagnosis match");
  const work = await client.workerWork.enqueue({
    work_type: "sandbox_command",
    payload: { command: "true" },
  }, controller.signal);
  const workDiagnosis = await client.workerWork.diagnose(work.id, controller.signal);
  if (workDiagnosis.work.id !== work.id) throw new Error("WorkerWork diagnosis did not identify work");
  const canceledWork = await client.workerWork.cancel(work.id, { reason: "E2E complete" }, controller.signal);
  if (canceledWork.status !== "canceled") throw new Error(`unexpected WorkerWork status ${canceledWork.status}`);

  const mcpServer = await client.mcp.create({
    identifier: "typescript-sdk-e2e",
    name: "TypeScript SDK E2E",
    config: { identifier: "typescript-sdk-e2e", command: "true" },
  }, controller.signal);
  const mcpVersions = await client.mcp.versions(mcpServer.id, controller.signal);
  if (mcpVersions.length !== 1 || mcpVersions[0]?.version !== 1) throw new Error("MCP version projection failed");

  const variable = await client.environmentVariables.put("TYPESCRIPT_SDK_E2E_SECRET", { value: "must-not-be-returned" }, {}, controller.signal);
  const variables = await client.environmentVariables.list({}, controller.signal);
  if (!variable.configured || JSON.stringify(variables).includes("must-not-be-returned")) throw new Error("environment variable redaction failed");
  await client.environmentVariables.delete(variable.name, {}, controller.signal);

  const skill = await client.skills.create({ identifier: "typescript-sdk-review", title: "TypeScript SDK Review" }, controller.signal);
  const skillVersion = await client.skills.createVersion(skill.id, {
    content_format: "markdown",
    manifest: { inputs_schema: { type: "object", properties: { strict: { type: "boolean" } }, additionalProperties: false } },
    content_text: "Review carefully.",
  }, controller.signal);
  const resolvedSkills = await client.skills.resolvePreview({
    skills: { enabled: [{ skill: skill.identifier, version: skillVersion.version, inputs: { strict: true } }] },
    max_tokens: 1000,
  }, controller.signal);
  if (resolvedSkills.skills?.length !== 1) throw new Error("Skill resolve preview failed");

  const marketplacePolicy = await client.marketplace.createPolicy({
    scope_type: "workspace",
    workspace_id: skill.workspace_id,
    config: { allowed_owners: ["acme"] },
  }, controller.signal);
  const marketplacePolicies = await client.marketplace.listPolicies({ workspaceId: skill.workspace_id }, controller.signal);
  if (!marketplacePolicies.some((item) => item.id === marketplacePolicy.policy.id)) throw new Error("Marketplace policy list failed");

  const audit = await client.audit.list({ action: "mcp_registry.create", limit: 10 }, controller.signal);
  if (!audit.some((item) => item.resource_id === mcpServer.id)) throw new Error("operator audit did not contain MCP creation");
  const observability = await client.observability.status(controller.signal);
  if (typeof observability !== "object" || observability === null) throw new Error("observability status failed");

  process.stdout.write(JSON.stringify({
    session_id: session.id,
    run_id: result.run.id,
    trace_id: trace.trace_id,
    mcp_server_id: mcpServer.id,
    skill_id: skill.id,
    worker_work_id: work.id,
  }));
} finally {
  clearTimeout(timeout);
}
