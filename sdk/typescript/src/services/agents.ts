import type {
  Agent,
  AgentConfigRollbackResponse,
  AgentConfigVersion,
  AgentExportDocument,
  AgentImportRequest,
  CreateAgentRequest,
  ToolingHealthRequest,
  ToolingHealthResponse,
  UpdateAgentRequest,
} from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";

export class AgentsService extends ServiceBase {
  create(request: CreateAgentRequest, signal?: AbortSignal): Promise<Agent> {
    return this.transport.requestJSON("POST", "/v2/agents", request, signal ? { signal } : {});
  }

  default(signal?: AbortSignal): Promise<Agent> {
    return this.transport.requestJSON("GET", "/v2/agents/default", undefined, signal ? { signal } : {});
  }

  list(signal?: AbortSignal): Promise<Agent[]> {
    return this.transport.requestJSON<{ agents: Agent[] }>("GET", "/v2/agents", undefined, signal ? { signal } : {}).then((value) => value.agents);
  }

  get(agentId: string, signal?: AbortSignal): Promise<Agent> {
    return this.transport.requestJSON("GET", agentPath(agentId), undefined, signal ? { signal } : {});
  }

  update(agentId: string, request: UpdateAgentRequest, signal?: AbortSignal): Promise<Agent> {
    return this.transport.requestJSON("PATCH", agentPath(agentId), request, signal ? { signal } : {});
  }

  import(request: AgentImportRequest, signal?: AbortSignal): Promise<Agent> {
    return this.transport.requestJSON("POST", "/v2/agents/import", request, signal ? { signal } : {});
  }

  export(agentId: string, signal?: AbortSignal): Promise<AgentExportDocument> {
    return this.transport.requestJSON("GET", `${agentPath(agentId)}/export`, undefined, signal ? { signal } : {});
  }

  listConfigVersions(agentId: string, signal?: AbortSignal): Promise<AgentConfigVersion[]> {
    return this.transport.requestJSON<{ config_versions: AgentConfigVersion[] }>("GET", `${agentPath(agentId)}/config-versions`, undefined, signal ? { signal } : {}).then((value) => value.config_versions);
  }

  createConfigVersion(agentId: string, request: UpdateAgentRequest, signal?: AbortSignal): Promise<Agent> {
    return this.transport.requestJSON("POST", `${agentPath(agentId)}/config-versions`, request, signal ? { signal } : {});
  }

  rollback(agentId: string, version: number, signal?: AbortSignal): Promise<AgentConfigRollbackResponse> {
    return this.transport.requestJSON("POST", resourcePath(`${agentPath(agentId)}/config-versions`, version) + "/rollback", {}, signal ? { signal } : {});
  }

  toolingHealth(agentId: string, request: ToolingHealthRequest = {}, signal?: AbortSignal): Promise<ToolingHealthResponse> {
    return this.transport.requestJSON("POST", `${agentPath(agentId)}/tooling-health`, request, signal ? { signal } : {});
  }
}

function agentPath(agentId: string): string {
  return resourcePath("/v2/agents", agentId);
}
