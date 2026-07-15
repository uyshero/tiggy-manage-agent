import {
  AgentsService,
  ArtifactsService,
  AuditService,
  AuthService,
  EnvironmentVariablesService,
  EnvironmentsService,
  InterventionsService,
  LLMService,
  MarketplaceService,
  MCPService,
  ObjectRefsService,
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
  type CreateSkillRequest,
  type LLMDiagnosticResult,
  type SessionListQuery,
  type TMAClientOptions,
} from "../src/index.js";
import { createLowLevelClient, type paths } from "../src/low-level.js";

declare const client: TMAClient;
declare const options: TMAClientOptions;
declare const skillRequest: CreateSkillRequest;
declare const sessionQuery: SessionListQuery;

const services: [
  AuthService,
  AgentsService,
  EnvironmentsService,
  SessionsService,
  RunsService,
  InterventionsService,
  ArtifactsService,
  ObjectRefsService,
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
] = [
  client.auth,
  client.agents,
  client.environments,
  client.sessions,
  client.runs,
  client.interventions,
  client.artifacts,
  client.objectRefs,
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
];

new TMAClient("https://tma.example.com", options);
createLowLevelClient("https://tma.example.com");
client.sessions.list(sessionQuery);
client.sessions.summary("session/1");
client.sessions.usage("session/1");
client.sessions.upgradeConfig("session/1", { to_current: true, updated_by: "type-contract" });
client.sessions.appendEvents("session/1", { events: [{ type: "custom.event", payload: { extension: true } }] });
const providerDiagnostic: Promise<LLMDiagnosticResult> = client.llm.testProvider("provider/1");
const modelDiagnostic: Promise<LLMDiagnosticResult> = client.llm.testModel("provider/1", "model/1");
client.skills.create(skillRequest);
const handle: Promise<RunHandle> = client.runs.start("session/1", { input: { text: "run" } });
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
void rawPaths;
void providerDiagnostic;
void modelDiagnostic;
