import type {
  EnvironmentVariable,
  EnvironmentVariableQuery,
  ObservabilityRetryResult,
  ObservabilityStatus,
  OperatorAuditQuery,
  OperatorAuditRecord,
  PutEnvironmentVariableRequest,
  SecurityAuditIntegrityKeyStatus,
  SecurityAuditReplayResult,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";
import { sessionPath } from "./sessions.js";

export class ObservabilityService extends ServiceBase {
  status(signal?: AbortSignal): Promise<ObservabilityStatus> {
    return this.transport.requestJSON("GET", "/v2/observability/status", undefined, signal ? { signal } : {});
  }

  retry(signal?: AbortSignal): Promise<ObservabilityRetryResult> {
    return this.transport.requestJSON("POST", "/v2/observability/retry", undefined, signal ? { signal } : {});
  }

  integrityKeys(signal?: AbortSignal): Promise<SecurityAuditIntegrityKeyStatus> {
    return this.transport.requestJSON("GET", "/v2/observability/security-audit/integrity-keys", undefined, signal ? { signal } : {});
  }
}

export class AuditService extends ServiceBase {
  list(query: OperatorAuditQuery = {}, signal?: AbortSignal): Promise<OperatorAuditRecord[]> {
    const path = withQuery("/v2/operator-audit", {
      workspace_id: query.workspaceId,
      session_id: query.sessionId,
      principal_id: query.principalId,
      action: query.action,
      limit: query.limit,
    });
    return this.listPath(path, signal);
  }

  listSession(sessionId: string, signal?: AbortSignal): Promise<OperatorAuditRecord[]> {
    return this.listPath(`${sessionPath(sessionId)}/operator-audit`, signal);
  }

  integrityKeys(signal?: AbortSignal): Promise<SecurityAuditIntegrityKeyStatus> {
    return this.transport.requestJSON("GET", "/v2/observability/security-audit/integrity-keys", undefined, signal ? { signal } : {});
  }

  replayDeadLetters(limit?: number, signal?: AbortSignal): Promise<SecurityAuditReplayResult> {
    const path = withQuery("/v2/observability/security-audit/replay", { limit });
    return this.transport.requestJSON("POST", path, undefined, signal ? { signal } : {});
  }

  private listPath(path: string, signal?: AbortSignal): Promise<OperatorAuditRecord[]> {
    return this.transport.requestJSON<{ audit_records: OperatorAuditRecord[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.audit_records);
  }
}

export class EnvironmentVariablesService extends ServiceBase {
  list(query: EnvironmentVariableQuery = {}, signal?: AbortSignal): Promise<EnvironmentVariable[]> {
    return this.transport.requestJSON<{ variables: EnvironmentVariable[] }>("GET", variablesPath(query), undefined, signal ? { signal } : {}).then((value) => value.variables);
  }

  put(name: string, request: PutEnvironmentVariableRequest, query: EnvironmentVariableQuery = {}, signal?: AbortSignal): Promise<EnvironmentVariable> {
    return this.transport.requestJSON("PUT", variablePath(name, query), request, signal ? { signal } : {});
  }

  delete(name: string, query: EnvironmentVariableQuery = {}, signal?: AbortSignal): Promise<void> {
    return this.transport.requestJSON("DELETE", variablePath(name, query), undefined, signal ? { signal } : {});
  }
}

function variablesPath(query: EnvironmentVariableQuery): string {
  return withQuery("/v2/environment-variables", { workspace_id: query.workspaceId });
}

function variablePath(name: string, query: EnvironmentVariableQuery): string {
  return withQuery(resourcePath("/v2/environment-variables", name), { workspace_id: query.workspaceId });
}
