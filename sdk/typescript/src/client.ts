import type { Client as OpenAPIClient } from "openapi-fetch";
import { createLowLevelClient } from "./low-level.js";
import type { paths } from "./internal/generated/schema.js";
import { Transport, type TransportOptions } from "./transport.js";
import { AgentsService } from "./services/agents.js";
import { AuditService, EnvironmentVariablesService, ObservabilityService } from "./services/administration.js";
import { ArtifactsService } from "./services/artifacts.js";
import { AuthService } from "./services/auth.js";
import { EnvironmentsService } from "./services/environments.js";
import { InterventionsService } from "./services/interventions.js";
import { LLMService } from "./services/llm.js";
import { MCPService } from "./services/mcp.js";
import { MarketplaceService } from "./services/marketplace.js";
import { ObjectRefsService } from "./services/object-refs.js";
import { OrchestrationService } from "./services/orchestration.js";
import { RunsService } from "./services/runs.js";
import { SessionsService } from "./services/sessions.js";
import { TracesService } from "./services/traces.js";
import { SkillsService } from "./services/skills.js";
import { WorkersService, WorkerWorkService } from "./services/workers.js";
import { WorkspaceToolPermissionsService } from "./services/workspace-tool-permissions.js";

export interface TMAClientOptions extends TransportOptions {}

export class TMAClient {
  readonly raw: OpenAPIClient<paths>;
  readonly auth: AuthService;
  readonly agents: AgentsService;
  readonly environments: EnvironmentsService;
  readonly sessions: SessionsService;
  readonly runs: RunsService;
  readonly interventions: InterventionsService;
  readonly artifacts: ArtifactsService;
  readonly traces: TracesService;
  readonly orchestration: OrchestrationService;
  readonly llm: LLMService;
  readonly objectRefs: ObjectRefsService;
  readonly workers: WorkersService;
  readonly workerWork: WorkerWorkService;
  readonly mcp: MCPService;
  readonly observability: ObservabilityService;
  readonly audit: AuditService;
  readonly environmentVariables: EnvironmentVariablesService;
  readonly workspaceToolPermissions: WorkspaceToolPermissionsService;
  readonly skills: SkillsService;
  readonly marketplace: MarketplaceService;

  constructor(baseURL: string, options: TMAClientOptions = {}) {
    const transport = new Transport(baseURL, options);
    this.raw = createLowLevelClient(transport.baseURL, transport.fetch);
    this.auth = new AuthService(transport);
    this.agents = new AgentsService(transport);
    this.environments = new EnvironmentsService(transport);
    this.sessions = new SessionsService(transport);
    this.interventions = new InterventionsService(transport);
    this.runs = new RunsService(transport, this.interventions);
    this.artifacts = new ArtifactsService(transport);
    this.traces = new TracesService(transport);
    this.orchestration = new OrchestrationService(transport);
    this.llm = new LLMService(transport);
    this.objectRefs = new ObjectRefsService(transport);
    this.workers = new WorkersService(transport);
    this.workerWork = new WorkerWorkService(transport);
    this.mcp = new MCPService(transport);
    this.observability = new ObservabilityService(transport);
    this.audit = new AuditService(transport);
    this.environmentVariables = new EnvironmentVariablesService(transport);
    this.workspaceToolPermissions = new WorkspaceToolPermissionsService(transport);
    this.skills = new SkillsService(transport);
    this.marketplace = new MarketplaceService(transport);
  }
}
