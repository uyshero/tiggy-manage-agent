import type {
  CreateMCPServerRequest,
  MCPRestoreResult,
  MCPRuntimeStatus,
  MCPServer,
  MCPServerQuery,
  MCPServerTestResult,
  MCPServerVersion,
  UpdateMCPServerRequest,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export class MCPService extends ServiceBase {
  list(query: MCPServerQuery = {}, signal?: AbortSignal): Promise<MCPServer[]> {
    return this.transport.requestJSON<{ servers: MCPServer[] }>("GET", serversPath(query), undefined, signal ? { signal } : {}).then((value) => value.servers);
  }

  runtimeStatus(query: MCPServerQuery = {}, signal?: AbortSignal): Promise<MCPRuntimeStatus> {
    const path = withQuery("/v2/mcp-servers/runtime-status", { workspace_id: query.workspaceId });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }

  create(request: CreateMCPServerRequest, signal?: AbortSignal): Promise<MCPServer> {
    return this.transport.requestJSON("POST", "/v2/mcp-servers", request, signal ? { signal } : {});
  }

  get(serverId: string, signal?: AbortSignal): Promise<MCPServer> {
    return this.transport.requestJSON("GET", serverPath(serverId), undefined, signal ? { signal } : {});
  }

  update(serverId: string, request: UpdateMCPServerRequest, signal?: AbortSignal): Promise<MCPServer> {
    return this.transport.requestJSON("PATCH", serverPath(serverId), request, signal ? { signal } : {});
  }

  setEnabled(serverId: string, enabled: boolean, signal?: AbortSignal): Promise<MCPServer> {
    return this.transport.requestJSON("POST", `${serverPath(serverId)}/${enabled ? "enable" : "disable"}`, {}, signal ? { signal } : {});
  }

  archive(serverId: string, signal?: AbortSignal): Promise<MCPServer> {
    return this.transport.requestJSON("DELETE", serverPath(serverId), undefined, signal ? { signal } : {});
  }

  test(serverId: string, signal?: AbortSignal): Promise<MCPServerTestResult> {
    return this.transport.requestJSON("POST", `${serverPath(serverId)}/test`, {}, signal ? { signal } : {});
  }

  versions(serverId: string, signal?: AbortSignal): Promise<MCPServerVersion[]> {
    return this.transport.requestJSON<{ versions: MCPServerVersion[] }>("GET", `${serverPath(serverId)}/versions`, undefined, signal ? { signal } : {}).then((value) => value.versions);
  }

  restoreVersion(serverId: string, version: number, signal?: AbortSignal): Promise<MCPRestoreResult> {
    const path = resourcePath(`${serverPath(serverId)}/versions`, version) + "/restore";
    return this.transport.requestJSON("POST", path, undefined, signal ? { signal } : {});
  }
}

function serversPath(query: MCPServerQuery): string {
  return withQuery("/v2/mcp-servers", { workspace_id: query.workspaceId });
}

function serverPath(serverId: string): string {
  return resourcePath("/v2/mcp-servers", serverId);
}
