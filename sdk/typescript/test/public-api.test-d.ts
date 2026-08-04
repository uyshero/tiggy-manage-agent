import {
  AgentsService,
  ArtifactsService,
  ArtifactExchangesService,
  AuditService,
  AuthService,
  EnvironmentVariablesService,
  EnvironmentsService,
  InterventionsService,
  LLMService,
  MarketplaceService,
  MCPService,
  ObjectRefsService,
  ObjectCleanupService,
  ObservabilityService,
  OrchestrationService,
  RunHandle,
  RunsService,
  SessionsService,
  SkillsService,
  TMAClient,
  TracesService,
  WorkersService,
  WorkerWorkService,
  WorkspaceToolPermissionsService,
  type CreateSkillRequest,
  type LLMDiagnosticResult,
  type LLMRealtimeCapabilities,
  type MultimodalMediaFrame,
  type MultimodalSessionStart,
  MultimodalRealtimeSession,
  type ModelInvocation,
  type ObjectReconciliationReport,
  type ObjectReconciliationArtifactExport,
  type RunAttempt,
  type SessionListQuery,
  type TMAClientOptions,
  type ToolPermissionAuditPage,
} from "../src/index.js";
import { createLowLevelClient, type paths } from "../src/low-level.js";

declare const client: TMAClient;
declare const options: TMAClientOptions;
declare const skillRequest: CreateSkillRequest;
declare const sessionQuery: SessionListQuery;
declare const realtimeCapabilities: LLMRealtimeCapabilities;
declare const multimodalInvocation: ModelInvocation;
declare const multimodalStart: MultimodalSessionStart;
declare const multimodalFrame: MultimodalMediaFrame;

realtimeCapabilities.input_formats[0]!.codec satisfies string;
realtimeCapabilities.max_frame_bytes satisfies number;
multimodalInvocation.capability satisfies "generate" | "embedding" | "rerank" | "speech_to_text" | "text_to_speech" | "multimodal_realtime";
multimodalInvocation.input_video_frames satisfies number;
multimodalInvocation.output_video_ms satisfies number;
const multimodalSession: MultimodalRealtimeSession = client.modelRuntime.connectMultimodalRealtime();
const startedMultimodal = multimodalSession.start(multimodalStart);
const sentMultimodal = multimodalSession.sendMedia(multimodalFrame);

const services: [
  AuthService,
  AgentsService,
  EnvironmentsService,
  SessionsService,
  RunsService,
  InterventionsService,
  ArtifactsService,
  ArtifactExchangesService,
  ObjectRefsService,
  ObjectCleanupService,
  LLMService,
  WorkersService,
  WorkerWorkService,
  MCPService,
  SkillsService,
  MarketplaceService,
  OrchestrationService,
  TracesService,
  ObservabilityService,
  AuditService,
  EnvironmentVariablesService,
  WorkspaceToolPermissionsService,
] = [
  client.auth,
  client.agents,
  client.environments,
  client.sessions,
  client.runs,
  client.interventions,
  client.artifacts,
  client.artifactExchanges,
  client.objectRefs,
  client.objectCleanup,
  client.llm,
  client.workers,
  client.workerWork,
  client.mcp,
  client.skills,
  client.marketplace,
  client.orchestration,
  client.traces,
  client.observability,
  client.audit,
  client.environmentVariables,
  client.workspaceToolPermissions,
];

new TMAClient("https://tma.example.com", options);
createLowLevelClient("https://tma.example.com");
client.sessions.list(sessionQuery);
client.sessions.summary("session/1");
client.sessions.taskPlan("session/1");
client.sessions.taskPlans("session/1");
client.sessions.usage("session/1");
client.sessions.upgradeConfig("session/1", { to_current: true, updated_by: "type-contract" });
client.sessions.updateRuntimeSettings("session/1", 1, {
  permission_rules: [{
    id: "session-src", tool: "default_edit_file", argument: "path",
    pattern: "/workspace/src/**", behavior: "allow",
  }],
});
client.sessions.appendEvents("session/1", { events: [{ type: "custom.event", payload: { extension: true } }] });
const providerDiagnostic: Promise<LLMDiagnosticResult> = client.llm.testProvider("provider/1");
const modelDiagnostic: Promise<LLMDiagnosticResult> = client.llm.testModel("provider/1", "model/1");
client.skills.create(skillRequest);
client.workspaceToolPermissions.evaluate("workspace/1", { tool: "default_read_file", path: "/workspace/README.md" });
const permissionAudit: Promise<ToolPermissionAuditPage> = client.audit.listToolPermissions("session/1", { decision: "ask", limit: 20, cursor: "next" });
const reconciliation: Promise<ObjectReconciliationReport> = client.objectCleanup.previewReconciliation({ workspace_id: "workspace/1", limit: 50 });
const reconciliationArtifact: Promise<ObjectReconciliationArtifactExport> = client.objectCleanup.exportReconciliationArtifact({ session_id: "session/1", workspace_id: "workspace/1", limit: 50 });
const handle: Promise<RunHandle> = client.runs.start("session/1", { input: { text: "run" } });
const attempts: Promise<RunAttempt[]> = client.runs.listAttempts("session/1", "run/1");
const attempt: Promise<RunAttempt> = client.runs.getAttempt("session/1", "run/1", "attempt/1");
const rawPaths: paths | undefined = undefined;

// These boundaries must remain absent from the Core SDK.
// @ts-expect-error Worker registration belongs to the Worker machine protocol.
client.workers.register({});
// @ts-expect-error Worker polling belongs to the Worker machine protocol.
client.workerWork.poll("worker/1");
// @ts-expect-error Legacy task templates are intentionally excluded from v2 SDKs.
client.templates.list();

void services;
void handle;
void attempts;
void attempt;
void rawPaths;
void providerDiagnostic;
void modelDiagnostic;
void permissionAudit;
void reconciliation;
void multimodalSession;
void startedMultimodal;
void sentMultimodal;
